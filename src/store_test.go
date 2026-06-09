package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStore(t *testing.T, mut func(*Config)) *Store {
	t.Helper()
	cfg := Config{DataDir: t.TempDir(), MaxItems: 0, MaxAgeDays: 0, MaxUploadMB: 100}
	if mut != nil {
		mut(&cfg)
	}
	s, err := NewStore(cfg)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// setCreatedAt backdates an item so ordering and pruning are deterministic.
func setCreatedAt(t *testing.T, s *Store, id string, ts int64) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE items SET created_at = ? WHERE id = ?`, ts, id); err != nil {
		t.Fatalf("backdate %s: %v", id, err)
	}
}

func TestAddTextAndGet(t *testing.T) {
	s := newTestStore(t, nil)
	it, err := s.AddText("hello world")
	if err != nil {
		t.Fatalf("AddText: %v", err)
	}
	if !validID(it.ID) {
		t.Errorf("AddText id %q is not a 32-char hex id", it.ID)
	}
	got, err := s.Get(it.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Kind != "text" || got.Content != "hello world" || got.Size != 11 {
		t.Errorf("Get returned %+v", got)
	}
}

func TestListNewestFirst(t *testing.T) {
	s := newTestStore(t, nil)
	old, _ := s.AddText("old")
	fresh, _ := s.AddText("fresh")
	setCreatedAt(t, s, old.ID, 1000)
	setCreatedAt(t, s, fresh.ID, 2000)

	items, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 || items[0].ID != fresh.ID || items[1].ID != old.ID {
		t.Errorf("List order wrong: %+v", items)
	}
}

func TestFileBlobLifecycle(t *testing.T) {
	s := newTestStore(t, nil)

	id, f, err := s.CreateBlob()
	if err != nil {
		t.Fatalf("CreateBlob: %v", err)
	}
	if _, err := f.WriteString("blob bytes"); err != nil {
		t.Fatalf("write blob: %v", err)
	}
	f.Close()

	it, err := s.AddFile(id, "report.pdf", "application/pdf", 10)
	if err != nil {
		t.Fatalf("AddFile: %v", err)
	}
	if it.Kind != "file" || it.Filename != "report.pdf" || it.Mime != "application/pdf" {
		t.Errorf("AddFile returned %+v", it)
	}
	if b, err := os.ReadFile(s.blobPath(id)); err != nil || string(b) != "blob bytes" {
		t.Errorf("blob on disk = %q, err %v", b, err)
	}

	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(id); err == nil {
		t.Error("item still in DB after Delete")
	}
	if _, err := os.Stat(s.blobPath(id)); !os.IsNotExist(err) {
		t.Error("blob still on disk after Delete")
	}
}

func TestDeleteMissing(t *testing.T) {
	s := newTestStore(t, nil)
	if err := s.Delete("00000000000000000000000000000000"); err == nil {
		t.Error("deleting a missing item did not error")
	}
}

func TestRemoveBlob(t *testing.T) {
	s := newTestStore(t, nil)
	id, f, _ := s.CreateBlob()
	f.Close()
	s.removeBlob(id)
	if _, err := os.Stat(s.blobPath(id)); !os.IsNotExist(err) {
		t.Error("removeBlob left the file behind")
	}
}

func TestPruneMaxItems(t *testing.T) {
	// Seed with pruning disabled (AddText prunes on every insert), then enable.
	s := newTestStore(t, nil)
	ids := make([]string, 5)
	for i := range ids {
		it, _ := s.AddText("item")
		ids[i] = it.ID
		setCreatedAt(t, s, it.ID, int64(1000+i))
	}
	s.cfg.MaxItems = 3

	n, err := s.Prune()
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if n != 2 {
		t.Errorf("Prune removed %d items, want 2", n)
	}
	items, _ := s.List()
	if len(items) != 3 {
		t.Fatalf("%d items left, want 3", len(items))
	}
	// The three newest (highest created_at) survive.
	for _, it := range items {
		if it.ID == ids[0] || it.ID == ids[1] {
			t.Errorf("old item %s survived pruning", it.ID)
		}
	}
}

func TestPruneMaxAge(t *testing.T) {
	s := newTestStore(t, func(c *Config) { c.MaxAgeDays = 7 })

	fresh, _ := s.AddText("fresh")

	// An expired file item: both the row and the blob must go.
	id, f, _ := s.CreateBlob()
	f.WriteString("x")
	f.Close()
	stale, _ := s.AddFile(id, "old.txt", "text/plain", 1)
	setCreatedAt(t, s, stale.ID, time.Now().Add(-8*24*time.Hour).Unix())

	if n, err := s.Prune(); err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1, nil", n, err)
	}
	items, _ := s.List()
	if len(items) != 1 || items[0].ID != fresh.ID {
		t.Errorf("wrong survivor: %+v", items)
	}
	if _, err := os.Stat(s.blobPath(id)); !os.IsNotExist(err) {
		t.Error("expired file's blob not removed")
	}
}

func TestPruneDisabled(t *testing.T) {
	s := newTestStore(t, nil) // MaxItems=0, MaxAgeDays=0
	for i := 0; i < 5; i++ {
		it, _ := s.AddText("keep")
		setCreatedAt(t, s, it.ID, time.Now().Add(-365*24*time.Hour).Unix())
	}
	if n, _ := s.Prune(); n != 0 {
		t.Errorf("Prune removed %d items with limits disabled", n)
	}
}

func TestBlobPathStaysInFilesDir(t *testing.T) {
	s := newTestStore(t, nil)
	p := s.blobPath("deadbeefdeadbeefdeadbeefdeadbeef")
	if filepath.Dir(p) != s.filesDir {
		t.Errorf("blobPath escaped files dir: %s", p)
	}
}
