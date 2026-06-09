package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Item is a single stash entry: either a text snippet or an uploaded file.
type Item struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"` // "text" | "file"
	Content   string `json:"content"`
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size"`
	Mime      string `json:"mime,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// Store wraps the SQLite database and the on-disk blob directory.
type Store struct {
	db       *sql.DB
	filesDir string
	cfg      Config
}

func NewStore(cfg Config) (*Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	filesDir := filepath.Join(cfg.DataDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, fmt.Errorf("create files dir: %w", err)
	}

	dbPath := filepath.Join(cfg.DataDir, "stash.db")
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: serialize writes, avoids "database is locked".

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS items (
			id         TEXT PRIMARY KEY,
			kind       TEXT NOT NULL,
			content    TEXT NOT NULL,
			filename   TEXT,
			size       INTEGER NOT NULL DEFAULT 0,
			mime       TEXT,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &Store{db: db, filesDir: filesDir, cfg: cfg}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// blobPath returns the on-disk path for a file item's blob.
func (s *Store) blobPath(id string) string { return filepath.Join(s.filesDir, id) }

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// List returns items newest-first.
func (s *Store) List() ([]Item, error) {
	rows, err := s.db.Query(`
		SELECT id, kind, content, COALESCE(filename,''), size, COALESCE(mime,''), created_at
		FROM items ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.ID, &it.Kind, &it.Content, &it.Filename, &it.Size, &it.Mime, &it.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

func (s *Store) Get(id string) (Item, error) {
	var it Item
	err := s.db.QueryRow(`
		SELECT id, kind, content, COALESCE(filename,''), size, COALESCE(mime,''), created_at
		FROM items WHERE id = ?`, id).
		Scan(&it.ID, &it.Kind, &it.Content, &it.Filename, &it.Size, &it.Mime, &it.CreatedAt)
	return it, err
}

// AddText inserts a text snippet and prunes.
func (s *Store) AddText(content string) (Item, error) {
	it := Item{
		ID:        newID(),
		Kind:      "text",
		Content:   content,
		Size:      int64(len(content)),
		CreatedAt: time.Now().Unix(),
	}
	if _, err := s.db.Exec(
		`INSERT INTO items (id, kind, content, size, created_at) VALUES (?,?,?,?,?)`,
		it.ID, it.Kind, it.Content, it.Size, it.CreatedAt,
	); err != nil {
		return Item{}, err
	}
	_, _ = s.Prune()
	return it, nil
}

// AddFile records file metadata. The blob must already be written to blobPath(id);
// callers use CreateBlob to obtain the destination handle with the right id.
func (s *Store) AddFile(id, originalName, mime string, size int64) (Item, error) {
	it := Item{
		ID:        id,
		Kind:      "file",
		Content:   originalName,
		Filename:  originalName,
		Size:      size,
		Mime:      mime,
		CreatedAt: time.Now().Unix(),
	}
	if _, err := s.db.Exec(
		`INSERT INTO items (id, kind, content, filename, size, mime, created_at) VALUES (?,?,?,?,?,?,?)`,
		it.ID, it.Kind, it.Content, it.Filename, it.Size, it.Mime, it.CreatedAt,
	); err != nil {
		return Item{}, err
	}
	_, _ = s.Prune()
	return it, nil
}

// CreateBlob creates the destination file for a new file item and returns its id + handle.
func (s *Store) CreateBlob() (string, *os.File, error) {
	id := newID()
	f, err := os.Create(s.blobPath(id))
	if err != nil {
		return "", nil, err
	}
	return id, f, nil
}

// removeBlob deletes an orphaned blob (used to clean up after a failed upload).
func (s *Store) removeBlob(id string) { _ = os.Remove(s.blobPath(id)) }

// Delete removes an item and its blob (if any).
func (s *Store) Delete(id string) error {
	it, err := s.Get(id)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM items WHERE id = ?`, id); err != nil {
		return err
	}
	if it.Kind == "file" {
		_ = os.Remove(s.blobPath(id))
	}
	return nil
}

// Prune enforces MaxItems and MaxAgeDays. Returns the number of items removed.
func (s *Store) Prune() (int, error) {
	ids := map[string]struct{}{}

	if s.cfg.MaxAgeDays > 0 {
		cutoff := time.Now().Add(-time.Duration(s.cfg.MaxAgeDays) * 24 * time.Hour).Unix()
		rows, err := s.db.Query(`SELECT id FROM items WHERE created_at < ?`, cutoff)
		if err != nil {
			return 0, err
		}
		collectIDs(rows, ids)
	}

	if s.cfg.MaxItems > 0 {
		rows, err := s.db.Query(
			`SELECT id FROM items ORDER BY created_at DESC, id DESC LIMIT -1 OFFSET ?`,
			s.cfg.MaxItems)
		if err != nil {
			return 0, err
		}
		collectIDs(rows, ids)
	}

	n := 0
	for id := range ids {
		if err := s.Delete(id); err == nil {
			n++
		}
	}
	return n, nil
}

func collectIDs(rows *sql.Rows, out map[string]struct{}) {
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			out[id] = struct{}{}
		}
	}
}
