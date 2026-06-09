package main

import (
	"net/http/httptest"
	"testing"
)

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"report.pdf":           "report.pdf",
		`..\..\evil.exe`:       "evil.exe",
		"/etc/passwd":          "passwd",
		"dir/sub/file.txt":     "file.txt",
		`C:\Users\x\notes.txt`: "notes.txt",
		"":                     "file",
		"   ":                  "file",
		"  spaced.txt  ":       "spaced.txt",
		"weird/":               "file",
	}
	for in, want := range cases {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestURLEncode(t *testing.T) {
	cases := map[string]string{
		"plain.txt":    "plain.txt",
		"with space":   "with%20space",
		"héllo.txt":    "h%C3%A9llo.txt",
		`q"uote%.txt`:  "q%22uote%25.txt",
		"safe-_.~name": "safe-_.~name",
	}
	for in, want := range cases {
		if got := urlEncode(in); got != want {
			t.Errorf("urlEncode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidID(t *testing.T) {
	if !validID("0123456789abcdef0123456789abcdef") {
		t.Error("canonical id rejected")
	}
	bad := []string{
		"",
		"0123456789abcdef0123456789abcde",   // 31 chars
		"0123456789abcdef0123456789abcdef0", // 33 chars
		"0123456789ABCDEF0123456789ABCDEF",  // uppercase
		"../../stash.db",
		"0123456789abcdef0123456789abcdeg", // non-hex
		"..%2f..%2fstash.db",
	}
	for _, id := range bad {
		if validID(id) {
			t.Errorf("validID(%q) = true", id)
		}
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.1.50:54321"
	if ip := clientIP(r); ip != "192.168.1.50" {
		t.Errorf("clientIP without XFF = %q", ip)
	}

	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if ip := clientIP(r); ip != "203.0.113.7" {
		t.Errorf("clientIP with single XFF = %q", ip)
	}

	r.Header.Set("X-Forwarded-For", " 203.0.113.9 , 10.0.0.1, 172.16.0.1")
	if ip := clientIP(r); ip != "203.0.113.9" {
		t.Errorf("clientIP with XFF chain = %q", ip)
	}
}

func TestSecureRequest(t *testing.T) {
	r := httptest.NewRequest("GET", "http://example.com/", nil)
	if secureRequest(r) {
		t.Error("plain request reported secure")
	}

	r.Header.Set("X-Forwarded-Proto", "https")
	if !secureRequest(r) {
		t.Error("X-Forwarded-Proto: https not honored")
	}

	r.Header.Set("X-Forwarded-Proto", "http")
	if secureRequest(r) {
		t.Error("X-Forwarded-Proto: http reported secure")
	}

	tls := httptest.NewRequest("GET", "https://example.com/", nil)
	if !secureRequest(tls) {
		t.Error("direct TLS request not reported secure")
	}
}
