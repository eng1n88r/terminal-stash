# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`stash` is a self-hosted LAN clipboard & file drop: a single static Go binary (`net/http`) that serves an embedded vanilla HTML/CSS/JS frontend and a small JSON API. Text snippets and file metadata live in SQLite; file blobs live on disk next to the DB. Connected browsers stay in sync via a Server-Sent Events stream.

LAN-only by design — no TLS, no per-user accounts, a single shared password. Never add features that assume internet exposure without the explicit reverse-proxy/VPN caveat.

## Commands

```bash
# Run locally (APP_PASSWORD is required or the server refuses to start)
APP_PASSWORD=test DATA_DIR=./data go run ./src   # → http://localhost:7827

go build ./...          # compile
go vet ./...            # vet
go test ./...           # unit + integration tests (src/*_test.go; integration_test.go runs the full HTTP stack via httptest)

# End-to-end browser tests (Playwright; builds the binary and starts it on :7832 with a clean data dir)
cd e2e && npm install && npx playwright test

# Docker (production path; data persists in the stash-data named volume)
echo 'APP_PASSWORD=...' > .env   # git-ignored
docker compose up --build -d
```

The binary is built with `CGO_ENABLED=0` (see Dockerfile) — this is mandatory: it relies on the pure-Go `modernc.org/sqlite` driver so no cgo/libsqlite is needed. Don't introduce cgo dependencies.

## Architecture

All Go code lives in `src/` as `package main` (the module root with `go.mod` stays at the repo root; build/run with `./src`), wired together through the `App` struct (`src/main.go`), which bundles the four collaborators:

- **`src/store.go` — `Store`**: SQLite (`items` table) + on-disk blob directory (`DATA_DIR/files/<id>`). One `Item` represents either a `text` snippet or a `file`. `db.SetMaxOpenConns(1)` serializes writes to avoid "database is locked". File uploads use `CreateBlob()` to get an id+handle first, then `AddFile()` records metadata once the blob is written. Every insert calls `Prune()` (also runs hourly via `pruneLoop` in `main.go`), enforcing `MAX_ITEMS` and `MAX_AGE_DAYS`.
- **`src/auth.go` — `Auth`**: single-shared-password gate. The HMAC signing secret is generated fresh on every process start, so **restarting the server invalidates all sessions** — this is intentional, not a bug. Constant-time comparison for both password and cookie signature. `requireAuthPage` (redirects to `/login`) wraps HTML routes; `requireAuthAPI` (returns 401) wraps API routes.
- **`src/events.go` — `Hub`**: in-memory pub/sub fanning `Event{created|deleted}` out to SSE subscribers. `Broadcast` drops events for slow consumers rather than blocking. Mutations in handlers must call `app.hub.Broadcast(...)` after a successful store change so other browsers update live.
- **`src/handlers.go`**: the HTTP handlers. Routes are registered in `App.routes()` in `src/main.go` using Go 1.22+ method-prefixed patterns (e.g. `"GET /api/files/{id}"`, read via `r.PathValue("id")`).

The frontend (`src/web/`) is embedded into the binary via `//go:embed web` (`src/main.go`) — embed paths are relative to the source file, so `web/` **must** stay inside `src/`. HTML pages are served by reading from the embed FS; CSS/JS are served under `/static/`. It's a no-build vanilla ES module (`src/web/app.js`) — edit and rebuild the Go binary; there is no bundler or npm step.

## Conventions

- Config comes only from environment variables, parsed once in `loadConfig()` (`src/main.go`). To add an option, extend `Config`, add an `env`/`envInt` line, and document it in README's config table.
- Uploaded filenames are run through `sanitizeName()` (strips path components) before storage, and percent-encoded via `urlEncode()` for the `Content-Disposition` header on download.
- Upload size is enforced twice: `http.MaxBytesReader` caps the request body, and `io.Copy` with a `LimitReader` rejects any single file over `MAX_UPLOAD_MB`; failed uploads must `removeBlob()` to avoid orphans.
