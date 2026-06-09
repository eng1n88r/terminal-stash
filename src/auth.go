package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	cookieName = "hc_session"
	// Kept short-ish: tokens are stateless, so logout can't revoke them
	// server-side — expiry and process restarts are the only invalidation.
	sessionMaxAge = 7 * 24 * time.Hour
)

// Auth implements a single-shared-password gate using a signed session cookie.
// The signing secret is generated fresh on each process start, so restarting the
// server invalidates existing sessions (acceptable for a LAN tool).
type Auth struct {
	password string
	secret   []byte
}

func NewAuth(password string) *Auth {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &Auth{password: password, secret: secret}
}

// Check reports whether the supplied password matches, in constant time.
func (a *Auth) Check(pw string) bool {
	return subtle.ConstantTimeCompare([]byte(pw), []byte(a.password)) == 1
}

// sign produces "<expiry>.<hmac>" for the given expiry unix timestamp.
func (a *Auth) sign(expiry int64) string {
	mac := hmac.New(sha256.New, a.secret)
	fmt.Fprintf(mac, "%d", expiry)
	return fmt.Sprintf("%d.%s", expiry, hex.EncodeToString(mac.Sum(nil)))
}

func (a *Auth) valid(token string) bool {
	exp, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	expiry, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() > expiry {
		return false
	}
	want := a.sign(expiry)
	// want is "<expiry>.<hmac>"; compare the hmac part in constant time.
	_, wantSig, _ := strings.Cut(want, ".")
	return subtle.ConstantTimeCompare([]byte(sig), []byte(wantSig)) == 1
}

func (a *Auth) issueCookie(w http.ResponseWriter, secure bool) {
	expiry := time.Now().Add(sessionMaxAge).Unix()
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    a.sign(expiry),
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(expiry, 0),
		MaxAge:   int(sessionMaxAge.Seconds()),
	})
}

func clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

func (a *Auth) authed(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	return a.valid(c.Value)
}

// requireAuthPage guards an HTML page handler, redirecting to /login when unauthenticated.
func (app *App) requireAuthPage(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !app.auth.authed(r) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// requireAuthAPI guards an API handler, returning 401 when unauthenticated.
func (app *App) requireAuthAPI(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !app.auth.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}
