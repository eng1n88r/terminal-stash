package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// --- Pages -----------------------------------------------------------------

func (app *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	app.servePage(w, "web/index.html")
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
	w.Write(b)
}

// --- Auth ------------------------------------------------------------------

func (app *App) handleLogin(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "invalid password", http.StatusUnauthorized)
		return
	}
	app.auth.issueCookie(w, r.TLS != nil)
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
		f.Close()
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
	json.NewEncoder(w).Encode(v)
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
