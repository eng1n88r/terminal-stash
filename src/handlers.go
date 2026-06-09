package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

// --- Pages -----------------------------------------------------------------

func (app *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Personalize the prompt: "you@stash" → "<APP_USER>@stash". Escaped because
	// the value comes from the environment, not from the embedded page.
	if u := app.cfg.UserName; u != "" {
		b = bytes.ReplaceAll(b, []byte("you@stash"), []byte(html.EscapeString(u)+"@stash"))
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (app *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// Already authenticated → straight to the app.
	if app.auth.authed(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	app.servePage(w, "web/login.html")
}

func (app *App) servePage(w http.ResponseWriter, path string) {
	b, err := webFS.ReadFile(path)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

// --- Auth ------------------------------------------------------------------

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)

	ip := clientIP(r)
	if !app.logins.allow(ip) {
		log.Printf("login rate-limited: %s", ip)
		http.Error(w, "too many attempts, try again later", http.StatusTooManyRequests)
		return
	}

	var pw string
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		pw = body.Password
	} else {
		pw = r.FormValue("password")
	}

	if !app.auth.Check(pw) {
		app.logins.fail(ip)
		log.Printf("login failed: %s", ip)
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}
	app.logins.success(ip)
	app.auth.issueCookie(w, secureRequest(r))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (app *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Items -----------------------------------------------------------------

func (app *App) handleListItems(w http.ResponseWriter, r *http.Request) {
	items, err := app.store.List()
	if err != nil {
		http.Error(w, "list failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (app *App) handleAddText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Content) == "" {
		http.Error(w, "empty content", http.StatusBadRequest)
		return
	}
	it, err := app.store.AddText(body.Content)
	if err != nil {
		http.Error(w, "save failed", http.StatusInternalServerError)
		return
	}
	app.hub.Broadcast(Event{Type: "created", Item: &it})
	writeJSON(w, http.StatusCreated, it)
}

func (app *App) handleUploadFiles(w http.ResponseWriter, r *http.Request) {
	maxBytes := app.cfg.MaxUploadMB << 20
	// Cap the whole request body; allow a little overhead for multipart framing.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1<<20)

	reader, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected multipart/form-data", http.StatusBadRequest)
		return
	}

	created := []Item{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "read upload: "+err.Error(), http.StatusBadRequest)
			return
		}
		if part.FormName() != "files" || part.FileName() == "" {
			continue
		}

		id, f, err := app.store.CreateBlob()
		if err != nil {
			http.Error(w, "storage error", http.StatusInternalServerError)
			return
		}

		size, err := io.Copy(f, io.LimitReader(part, maxBytes+1))
		if cerr := f.Close(); err == nil {
			err = cerr // a failed close can mean unflushed data — treat as a write failure
		}
		if err != nil || size > maxBytes {
			app.store.removeBlob(id)
			if size > maxBytes {
				http.Error(w, fmt.Sprintf("file exceeds %d MB limit", app.cfg.MaxUploadMB), http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "write failed", http.StatusInternalServerError)
			}
			return
		}

		mime := part.Header.Get("Content-Type")
		if mime == "" {
			mime = "application/octet-stream"
		}
		it, err := app.store.AddFile(id, sanitizeName(part.FileName()), mime, size)
		if err != nil {
			app.store.removeBlob(id)
			http.Error(w, "save failed", http.StatusInternalServerError)
			return
		}
		app.hub.Broadcast(Event{Type: "created", Item: &it})
		created = append(created, it)
	}

	if len(created) == 0 {
		http.Error(w, "no files in request", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (app *App) handleDownload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(id) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	it, err := app.store.Get(id)
	if err != nil || it.Kind != "file" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", it.Mime)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename*=UTF-8''%s", urlEncode(it.Filename)))
	http.ServeFile(w, r, app.store.blobPath(id))
}

func (app *App) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validID(id) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := app.store.Delete(id); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	app.hub.Broadcast(Event{Type: "deleted", ID: id})
	w.WriteHeader(http.StatusNoContent)
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// sanitizeName strips any directory components from an uploaded filename.
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	return name
}

// validID reports whether id has the shape of our 16-byte hex ids. PathValue
// can yield values with encoded slashes, so anything else stays away from the
// filesystem even though the DB lookup would already miss.
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// clientIP returns the originating client address for logging and rate
// limiting, preferring X-Forwarded-For (set by the reverse proxy) over the
// socket peer.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip, _, _ := strings.Cut(xff, ",")
		return strings.TrimSpace(ip)
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// secureRequest reports whether the request arrived over HTTPS, either
// directly or via a TLS-terminating reverse proxy.
func secureRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// urlEncode percent-encodes a filename for the Content-Disposition header.
func urlEncode(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteByte(r)
		} else {
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}
