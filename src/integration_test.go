package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testPassword = "integration-test-pw"

// newTestServer spins up the full application (real store in a temp dir, real
// routes and middleware) behind an httptest server.
func newTestServer(t *testing.T, mut func(*App)) (*App, *httptest.Server) {
	t.Helper()
	cfg := Config{
		DataDir:     t.TempDir(),
		Password:    testPassword,
		MaxItems:    100,
		MaxAgeDays:  30,
		MaxUploadMB: 1,
	}
	store, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	app := &App{
		cfg:    cfg,
		store:  store,
		auth:   NewAuth(cfg.Password),
		hub:    NewHub(),
		logins: newLoginLimiter(10, 15*time.Minute),
	}
	if mut != nil {
		mut(app)
	}
	srv := httptest.NewServer(app.routes())
	t.Cleanup(func() {
		srv.Close()
		store.Close()
	})
	return app, srv
}

// noRedirect returns a client that surfaces redirects instead of following them.
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func login(t *testing.T, srv *httptest.Server) *http.Cookie {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/login", "application/json",
		strings.NewReader(`{"password":"`+testPassword+`"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c
		}
	}
	t.Fatal("login response did not set a session cookie")
	return nil
}

func authedReq(t *testing.T, c *http.Cookie, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(c)
	return req
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	_, srv := newTestServer(t, nil)
	resp, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	checks := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for h, want := range checks {
		if got := resp.Header.Get(h); got != want {
			t.Errorf("%s = %q, want %q", h, got, want)
		}
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
}

func TestStaticAssetCaching(t *testing.T) {
	_, srv := newTestServer(t, nil)

	resp, err := http.Get(srv.URL + "/static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on /static/app.js")
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want %q", cc, "no-cache")
	}

	// Revalidation with the same tag must produce a 304.
	req, _ := http.NewRequest("GET", srv.URL+"/static/app.js", nil)
	req.Header.Set("If-None-Match", etag)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match revalidation -> %d, want 304", resp.StatusCode)
	}

	// HTML pages must not be stored at all.
	resp, err = http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("/login Cache-Control = %q, want %q", cc, "no-store")
	}
}

func TestPageRedirects(t *testing.T) {
	_, srv := newTestServer(t, nil)
	client := noRedirect()

	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Errorf("unauthenticated / -> %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp.Body.Close()

	c := login(t, srv)
	req := authedReq(t, c, "GET", srv.URL+"/login", nil)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/" {
		t.Errorf("authenticated /login -> %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp.Body.Close()
}

func TestLoginRejectsWrongPassword(t *testing.T) {
	_, srv := newTestServer(t, nil)
	resp, err := http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(`{"password":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong password -> %d", resp.StatusCode)
	}
	if len(resp.Cookies()) != 0 {
		t.Error("wrong password still set a cookie")
	}
}

func TestLoginAcceptsFormEncoding(t *testing.T) {
	_, srv := newTestServer(t, nil)
	resp, err := http.Post(srv.URL+"/api/login", "application/x-www-form-urlencoded",
		strings.NewReader("password="+testPassword))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("form login -> %d", resp.StatusCode)
	}
}

func TestLoginBodyTooLarge(t *testing.T) {
	_, srv := newTestServer(t, nil)
	huge := `{"password":"` + strings.Repeat("x", 8<<10) + `"}`
	resp, err := http.Post(srv.URL+"/api/login", "application/json", strings.NewReader(huge))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized login body -> %d, want 400", resp.StatusCode)
	}
}

func TestLoginSecureCookieBehindProxy(t *testing.T) {
	_, srv := newTestServer(t, nil)
	req, _ := http.NewRequest("POST", srv.URL+"/api/login",
		strings.NewReader(`{"password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-Proto", "https")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	cs := resp.Cookies()
	if len(cs) != 1 || !cs[0].Secure {
		t.Errorf("cookie behind https proxy not Secure: %+v", cs)
	}
}

func TestLoginRateLimit(t *testing.T) {
	_, srv := newTestServer(t, func(a *App) {
		a.logins = newLoginLimiter(3, time.Minute)
	})

	attempt := func(pw, xff string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/api/login",
			strings.NewReader(`{"password":"`+pw+`"}`))
		req.Header.Set("Content-Type", "application/json")
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	for i := 0; i < 3; i++ {
		if code := attempt("wrong", ""); code != http.StatusUnauthorized {
			t.Fatalf("failure %d -> %d, want 401", i+1, code)
		}
	}
	if code := attempt("wrong", ""); code != http.StatusTooManyRequests {
		t.Errorf("4th failure -> %d, want 429", code)
	}
	// Even the correct password is blocked from the limited IP.
	if code := attempt(testPassword, ""); code != http.StatusTooManyRequests {
		t.Errorf("correct password from limited IP -> %d, want 429", code)
	}
	// A different client IP (via X-Forwarded-For) is unaffected.
	if code := attempt(testPassword, "203.0.113.50"); code != http.StatusOK {
		t.Errorf("login from different IP -> %d, want 200", code)
	}
}

func TestAPIRequiresAuth(t *testing.T) {
	_, srv := newTestServer(t, nil)
	endpoints := []struct{ method, path string }{
		{"GET", "/api/items"},
		{"POST", "/api/text"},
		{"POST", "/api/files"},
		{"GET", "/api/files/0123456789abcdef0123456789abcdef"},
		{"DELETE", "/api/items/0123456789abcdef0123456789abcdef"},
		{"GET", "/api/events"},
	}
	for _, e := range endpoints {
		req, _ := http.NewRequest(e.method, srv.URL+e.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s without cookie -> %d, want 401", e.method, e.path, resp.StatusCode)
		}
	}
}

func TestTextLifecycle(t *testing.T) {
	_, srv := newTestServer(t, nil)
	c := login(t, srv)

	// Create.
	req := authedReq(t, c, "POST", srv.URL+"/api/text", strings.NewReader(`{"content":"snippet"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create -> %d", resp.StatusCode)
	}
	var it Item
	json.NewDecoder(resp.Body).Decode(&it)
	resp.Body.Close()

	// List contains it.
	resp, _ = http.DefaultClient.Do(authedReq(t, c, "GET", srv.URL+"/api/items", nil))
	var items []Item
	json.NewDecoder(resp.Body).Decode(&items)
	resp.Body.Close()
	if len(items) != 1 || items[0].ID != it.ID || items[0].Content != "snippet" {
		t.Fatalf("list = %+v", items)
	}

	// Delete.
	resp, _ = http.DefaultClient.Do(authedReq(t, c, "DELETE", srv.URL+"/api/items/"+it.ID, nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete -> %d", resp.StatusCode)
	}
	resp, _ = http.DefaultClient.Do(authedReq(t, c, "GET", srv.URL+"/api/items", nil))
	items = nil
	json.NewDecoder(resp.Body).Decode(&items)
	resp.Body.Close()
	if len(items) != 0 {
		t.Errorf("item still listed after delete")
	}
}

func TestAddTextRejectsEmpty(t *testing.T) {
	_, srv := newTestServer(t, nil)
	c := login(t, srv)
	for _, body := range []string{`{"content":""}`, `{"content":"  \n "}`, `not json`} {
		req := authedReq(t, c, "POST", srv.URL+"/api/text", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, _ := http.DefaultClient.Do(req)
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("body %q -> %d, want 400", body, resp.StatusCode)
		}
	}
}

func multipartBody(t *testing.T, field, filename string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(content)
	mw.Close()
	return &buf, mw.FormDataContentType()
}

func TestFileUploadDownloadDelete(t *testing.T) {
	app, srv := newTestServer(t, nil)
	c := login(t, srv)

	body, ctype := multipartBody(t, "files", `..\dir\héllo file.txt`, []byte("file payload"))
	req := authedReq(t, c, "POST", srv.URL+"/api/files", body)
	req.Header.Set("Content-Type", ctype)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload -> %d", resp.StatusCode)
	}
	var created []Item
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if len(created) != 1 {
		t.Fatalf("created %d items", len(created))
	}
	it := created[0]
	if it.Filename != "héllo file.txt" {
		t.Errorf("path components not stripped from filename: %q", it.Filename)
	}

	// Download round-trips the bytes with safe headers.
	resp, _ = http.DefaultClient.Do(authedReq(t, c, "GET", srv.URL+"/api/files/"+it.ID, nil))
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(got) != "file payload" {
		t.Errorf("download = %d %q", resp.StatusCode, got)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, "h%C3%A9llo%20file.txt") {
		t.Errorf("Content-Disposition = %q", cd)
	}

	// Delete removes the blob from disk too.
	resp, _ = http.DefaultClient.Do(authedReq(t, c, "DELETE", srv.URL+"/api/items/"+it.ID, nil))
	resp.Body.Close()
	if _, err := os.Stat(app.store.blobPath(it.ID)); !os.IsNotExist(err) {
		t.Error("blob still on disk after delete")
	}
}

func TestUploadTooLargeLeavesNoOrphan(t *testing.T) {
	app, srv := newTestServer(t, nil) // MaxUploadMB = 1
	c := login(t, srv)

	body, ctype := multipartBody(t, "files", "big.bin", bytes.Repeat([]byte("x"), 1<<20+10))
	req := authedReq(t, c, "POST", srv.URL+"/api/files", body)
	req.Header.Set("Content-Type", ctype)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized upload -> %d, want 413", resp.StatusCode)
	}

	entries, err := os.ReadDir(filepath.Join(app.cfg.DataDir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("%d orphan blob(s) left after rejected upload", len(entries))
	}
}

func TestUploadWithoutFiles(t *testing.T) {
	_, srv := newTestServer(t, nil)
	c := login(t, srv)
	body, ctype := multipartBody(t, "wrong-field", "x.txt", []byte("hi"))
	req := authedReq(t, c, "POST", srv.URL+"/api/files", body)
	req.Header.Set("Content-Type", ctype)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("upload without files field -> %d, want 400", resp.StatusCode)
	}
}

func TestDownloadRejectsBadIDs(t *testing.T) {
	_, srv := newTestServer(t, nil)
	c := login(t, srv)
	for _, raw := range []string{
		"/api/files/..%2f..%2fstash.db",
		"/api/files/0123456789abcdef0123456789abcdef", // valid shape, not in DB
	} {
		resp, _ := http.DefaultClient.Do(authedReq(t, c, "GET", srv.URL+raw, nil))
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s -> %d, want 404", raw, resp.StatusCode)
		}
	}
	resp, _ := http.DefaultClient.Do(authedReq(t, c, "DELETE", srv.URL+"/api/items/..%2f..%2fstash.db", nil))
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("DELETE traversal id -> %d, want 404", resp.StatusCode)
	}
}

func TestLogoutClearsCookie(t *testing.T) {
	_, srv := newTestServer(t, nil)
	c := login(t, srv)
	resp, err := http.DefaultClient.Do(authedReq(t, c, "POST", srv.URL+"/api/logout", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	cs := resp.Cookies()
	if len(cs) != 1 || cs[0].MaxAge >= 0 || cs[0].Value != "" {
		t.Errorf("logout did not clear the cookie: %+v", cs)
	}
}

func TestSSEDeliversEvents(t *testing.T) {
	_, srv := newTestServer(t, nil)
	c := login(t, srv)

	req := authedReq(t, c, "GET", srv.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q", ct)
	}

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	waitFor := func(want string) string {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case line, ok := <-lines:
				if !ok {
					t.Fatalf("stream closed waiting for %q", want)
				}
				if strings.Contains(line, want) {
					return line
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %q", want)
			}
		}
	}

	waitFor(": connected")

	// A mutation through the API must show up on the stream.
	body := `{"content":"sse payload"}`
	postReq := authedReq(t, c, "POST", srv.URL+"/api/text", strings.NewReader(body))
	postReq.Header.Set("Content-Type", "application/json")
	postResp, _ := http.DefaultClient.Do(postReq)
	var it Item
	json.NewDecoder(postResp.Body).Decode(&it)
	postResp.Body.Close()

	created := waitFor("data: ")
	var ev Event
	if err := json.Unmarshal([]byte(strings.TrimPrefix(created, "data: ")), &ev); err != nil {
		t.Fatalf("bad event payload %q: %v", created, err)
	}
	if ev.Type != "created" || ev.Item == nil || ev.Item.Content != "sse payload" {
		t.Errorf("created event = %+v", ev)
	}

	// Deletion is broadcast too.
	delResp, _ := http.DefaultClient.Do(authedReq(t, c, "DELETE", srv.URL+"/api/items/"+it.ID, nil))
	delResp.Body.Close()
	deleted := waitFor("data: ")
	if err := json.Unmarshal([]byte(strings.TrimPrefix(deleted, "data: ")), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Type != "deleted" || ev.ID != it.ID {
		t.Errorf("deleted event = %+v", ev)
	}
}

func TestMultiFileUpload(t *testing.T) {
	_, srv := newTestServer(t, nil)
	c := login(t, srv)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for i := 0; i < 3; i++ {
		fw, _ := mw.CreateFormFile("files", fmt.Sprintf("f%d.txt", i))
		fmt.Fprintf(fw, "content %d", i)
	}
	mw.Close()

	req := authedReq(t, c, "POST", srv.URL+"/api/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var created []Item
	json.NewDecoder(resp.Body).Decode(&created)
	if resp.StatusCode != http.StatusCreated || len(created) != 3 {
		t.Errorf("multi upload -> %d, %d items", resp.StatusCode, len(created))
	}
}

func TestIndexPromptPersonalization(t *testing.T) {
	fetchIndex := func(t *testing.T, mut func(*App)) string {
		t.Helper()
		_, srv := newTestServer(t, mut)
		c := login(t, srv)
		resp, err := http.DefaultClient.Do(authedReq(t, c, "GET", srv.URL+"/", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	t.Run("default prompt", func(t *testing.T) {
		body := fetchIndex(t, nil)
		if !strings.Contains(body, "user@stash") {
			t.Error("index without APP_USER should keep the user@stash prompt")
		}
	})

	t.Run("configured user", func(t *testing.T) {
		body := fetchIndex(t, func(a *App) { a.cfg.UserName = "exbarboss" })
		if !strings.Contains(body, "exbarboss@stash") {
			t.Error("index should render the configured APP_USER in the prompt")
		}
		if strings.Contains(body, "user@stash") {
			t.Error("default prompt should be replaced when APP_USER is set")
		}
	})

	t.Run("html in user name is escaped", func(t *testing.T) {
		body := fetchIndex(t, func(a *App) { a.cfg.UserName = `<img src=x>` })
		if strings.Contains(body, "<img src=x>@stash") {
			t.Error("APP_USER must be HTML-escaped in the page")
		}
		if !strings.Contains(body, "&lt;img src=x&gt;@stash") {
			t.Error("escaped APP_USER should appear in the prompt")
		}
	})
}
