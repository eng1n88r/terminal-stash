package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthCheck(t *testing.T) {
	a := NewAuth("correct horse")
	if !a.Check("correct horse") {
		t.Error("correct password rejected")
	}
	for _, pw := range []string{"", "wrong", "correct horse ", "Correct horse"} {
		if a.Check(pw) {
			t.Errorf("password %q accepted", pw)
		}
	}
}

func TestTokenRoundTrip(t *testing.T) {
	a := NewAuth("pw")
	tok := a.sign(time.Now().Add(time.Hour).Unix())
	if !a.valid(tok) {
		t.Error("freshly signed token rejected")
	}
}

func TestTokenExpired(t *testing.T) {
	a := NewAuth("pw")
	tok := a.sign(time.Now().Add(-time.Minute).Unix())
	if a.valid(tok) {
		t.Error("expired token accepted")
	}
}

func TestTokenTampered(t *testing.T) {
	a := NewAuth("pw")
	tok := a.sign(time.Now().Add(time.Hour).Unix())

	// Flip the last hex digit of the signature.
	flipped := tok[:len(tok)-1]
	if strings.HasSuffix(tok, "0") {
		flipped += "1"
	} else {
		flipped += "0"
	}
	if a.valid(flipped) {
		t.Error("token with tampered signature accepted")
	}

	// Extend the expiry without re-signing.
	_, sig, _ := strings.Cut(tok, ".")
	future := time.Now().Add(100 * time.Hour).Unix()
	if a.valid(fmt.Sprintf("%d.%s", future, sig)) {
		t.Error("token with tampered expiry accepted")
	}
}

func TestTokenMalformed(t *testing.T) {
	a := NewAuth("pw")
	for _, tok := range []string{"", "abc", "123", ".", "123.", ".abc", "notanumber.ff", "123.zz"} {
		if a.valid(tok) {
			t.Errorf("malformed token %q accepted", tok)
		}
	}
}

func TestTokenFromOtherProcess(t *testing.T) {
	// Each Auth gets a fresh random secret, so tokens never survive a restart.
	a1 := NewAuth("pw")
	a2 := NewAuth("pw")
	tok := a1.sign(time.Now().Add(time.Hour).Unix())
	if a2.valid(tok) {
		t.Error("token signed with a different secret accepted")
	}
}

func TestIssueCookie(t *testing.T) {
	a := NewAuth("pw")

	for _, secure := range []bool{true, false} {
		rec := httptest.NewRecorder()
		a.issueCookie(rec, secure)
		cs := rec.Result().Cookies()
		if len(cs) != 1 {
			t.Fatalf("expected 1 cookie, got %d", len(cs))
		}
		c := cs[0]
		if c.Name != cookieName {
			t.Errorf("cookie name = %q", c.Name)
		}
		if !c.HttpOnly {
			t.Error("cookie not HttpOnly")
		}
		if c.Secure != secure {
			t.Errorf("cookie Secure = %v, want %v", c.Secure, secure)
		}
		if c.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie SameSite = %v, want Lax", c.SameSite)
		}
		if c.MaxAge != int(sessionMaxAge.Seconds()) {
			t.Errorf("cookie MaxAge = %d", c.MaxAge)
		}
		if !a.valid(c.Value) {
			t.Error("issued cookie value does not validate")
		}
	}
}

func TestClearCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	clearCookie(rec)
	cs := rec.Result().Cookies()
	if len(cs) != 1 || cs[0].MaxAge >= 0 || cs[0].Value != "" {
		t.Errorf("clearCookie did not expire the cookie: %+v", cs)
	}
}

func TestAuthedRequest(t *testing.T) {
	a := NewAuth("pw")

	r := httptest.NewRequest("GET", "/", nil)
	if a.authed(r) {
		t.Error("request without cookie authed")
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: a.sign(time.Now().Add(time.Hour).Unix())})
	if !a.authed(r) {
		t.Error("request with valid cookie not authed")
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.AddCookie(&http.Cookie{Name: cookieName, Value: a.sign(time.Now().Add(-time.Hour).Unix())})
	if a.authed(r) {
		t.Error("request with expired cookie authed")
	}
}
