package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web
var webFS embed.FS

// Config holds runtime configuration sourced from environment variables.
type Config struct {
	Port        string
	DataDir     string
	Password    string
	UserName    string
	MaxItems    int
	MaxAgeDays  int
	MaxUploadMB int64
}

func loadConfig() Config {
	return Config{
		Port:        env("PORT", "7827"),
		DataDir:     env("DATA_DIR", "/data"),
		Password:    os.Getenv("APP_PASSWORD"),
		UserName:    env("APP_USER", "user"),
		MaxItems:    envInt("MAX_ITEMS", 200),
		MaxAgeDays:  envInt("MAX_AGE_DAYS", 30),
		MaxUploadMB: int64(envInt("MAX_UPLOAD_MB", 100)),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("warning: %s=%q is not an integer, using default %d", key, v, def)
	}
	return def
}

// App bundles the shared dependencies passed to HTTP handlers.
type App struct {
	cfg    Config
	store  *Store
	auth   *Auth
	hub    *Hub
	logins *loginLimiter
}

func main() {
	cfg := loadConfig()

	if cfg.Password == "" {
		log.Fatal("APP_PASSWORD is not set — refusing to start an unprotected instance. " +
			"Set APP_PASSWORD to a shared secret.")
	}

	store, err := NewStore(cfg)
	if err != nil {
		log.Fatalf("init store: %v", err)
	}
	defer store.Close()

	app := &App{
		cfg:    cfg,
		store:  store,
		auth:   NewAuth(cfg.Password),
		hub:    NewHub(),
		logins: newLoginLimiter(10, 15*time.Minute),
	}

	// Background retention sweep.
	stopPrune := make(chan struct{})
	go app.pruneLoop(stopPrune)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           app.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		log.Printf("stash listening on :%s (data dir: %s)", cfg.Port, cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	// Graceful shutdown.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("shutting down…")
	close(stopPrune)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func (a *App) pruneLoop(stop <-chan struct{}) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			if n, err := a.store.Prune(); err != nil {
				log.Printf("prune: %v", err)
			} else if n > 0 {
				log.Printf("pruned %d expired item(s)", n)
			}
		}
	}
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	// Static assets (CSS/JS) — served from the embedded web/ dir, no auth needed.
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed sub: %v", err)
	}
	assets := staticCache(sub, http.FileServer(http.FS(sub)))
	mux.Handle("GET /static/", http.StripPrefix("/static/", assets))

	// Auth endpoints.
	mux.HandleFunc("GET /login", a.handleLoginPage)
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("POST /api/logout", a.handleLogout)

	// UI.
	mux.HandleFunc("GET /", a.requireAuthPage(a.handleIndex))

	// API (cookie-protected).
	mux.HandleFunc("GET /api/items", a.requireAuthAPI(a.handleListItems))
	mux.HandleFunc("POST /api/text", a.requireAuthAPI(a.handleAddText))
	mux.HandleFunc("POST /api/files", a.requireAuthAPI(a.handleUploadFiles))
	mux.HandleFunc("GET /api/files/{id}", a.requireAuthAPI(a.handleDownload))
	mux.HandleFunc("DELETE /api/items/{id}", a.requireAuthAPI(a.handleDelete))
	mux.HandleFunc("GET /api/events", a.requireAuthAPI(a.handleEvents))

	return securityHeaders(mux)
}

// staticCache adds revalidation-friendly caching to the asset file server.
// Embedded files carry no mod time, so http.FileServer emits no validators and
// any cache in front (browser heuristics, a CDN on the reverse-proxy path)
// holds stale CSS/JS across releases. A content-hash ETag plus
// Cache-Control: no-cache makes every load a cheap 304 revalidation while
// guaranteeing new builds are picked up immediately.
func staticCache(sub fs.FS, next http.Handler) http.Handler {
	etags := map[string]string{}
	err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(sub, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		etags[p] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	if err != nil {
		log.Fatalf("hash static assets: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag, ok := etags[strings.TrimPrefix(r.URL.Path, "/")]
		if ok {
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "no-cache")
			if strings.Contains(r.Header.Get("If-None-Match"), tag) {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders adds defense-in-depth headers to every response. The strict
// CSP works because all assets are embedded and served from this origin —
// keep scripts/styles in external files under web/, never inline.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; "+
				"connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
