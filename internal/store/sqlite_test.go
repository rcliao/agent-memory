package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSQLiteStore(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutAndGet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	mem, err := s.Put(ctx, PutParams{
		NS: "test", Key: "hello", Content: "world", Kind: "semantic", Priority: "normal",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if mem.Version != 1 {
		t.Errorf("expected version 1, got %d", mem.Version)
	}
	if mem.ID == "" {
		t.Error("expected non-empty ID")
	}

	got, err := s.Get(ctx, GetParams{NS: "test", Key: "hello"})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].Content != "world" {
		t.Errorf("expected 'world', got %q", got[0].Content)
	}
	// Access count incremented after read, verify with a second get
	got2, _ := s.Get(ctx, GetParams{NS: "test", Key: "hello"})
	if got2[0].AccessCount != 1 {
		t.Errorf("expected access_count 1 after second get, got %d", got2[0].AccessCount)
	}
}

func TestVersioning(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "v1"})
	m2, _ := s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "v2"})

	if m2.Version != 2 {
		t.Errorf("expected version 2, got %d", m2.Version)
	}
	if m2.Supersedes == "" {
		t.Error("expected supersedes to be set")
	}

	// Get latest
	got, _ := s.Get(ctx, GetParams{NS: "ns", Key: "k"})
	if got[0].Content != "v2" {
		t.Errorf("expected 'v2', got %q", got[0].Content)
	}

	// Get history
	hist, _ := s.Get(ctx, GetParams{NS: "ns", Key: "k", History: true})
	if len(hist) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(hist))
	}

	// Get specific version
	v1, _ := s.Get(ctx, GetParams{NS: "ns", Key: "k", Version: 1})
	if v1[0].Content != "v1" {
		t.Errorf("expected 'v1', got %q", v1[0].Content)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Put(ctx, PutParams{NS: "ns", Key: "a", Content: "alpha"})
	s.Put(ctx, PutParams{NS: "ns", Key: "b", Content: "beta"})
	s.Put(ctx, PutParams{NS: "other", Key: "c", Content: "gamma"})

	// List all
	all, _ := s.List(ctx, ListParams{})
	if len(all) != 3 {
		t.Errorf("expected 3, got %d", len(all))
	}

	// List by namespace
	nsOnly, _ := s.List(ctx, ListParams{NS: "ns"})
	if len(nsOnly) != 2 {
		t.Errorf("expected 2, got %d", len(nsOnly))
	}
}

func TestListShowsLatestVersion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "v1"})
	s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "v2"})

	list, _ := s.List(ctx, ListParams{NS: "ns"})
	if len(list) != 1 {
		t.Fatalf("expected 1 (latest only), got %d", len(list))
	}
	if list[0].Content != "v2" {
		t.Errorf("expected latest 'v2', got %q", list[0].Content)
	}
}

func TestSoftDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "data"})
	err := s.Rm(ctx, RmParams{NS: "ns", Key: "k"})
	if err != nil {
		t.Fatalf("rm: %v", err)
	}

	_, err = s.Get(ctx, GetParams{NS: "ns", Key: "k"})
	if err == nil {
		t.Error("expected error after soft delete")
	}
}

func TestHardDelete(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "data"})
	err := s.Rm(ctx, RmParams{NS: "ns", Key: "k", Hard: true})
	if err != nil {
		t.Fatalf("rm hard: %v", err)
	}

	_, err = s.Get(ctx, GetParams{NS: "ns", Key: "k"})
	if err == nil {
		t.Error("expected error after hard delete")
	}
}

func TestDeleteAllVersions(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "v1"})
	s.Put(ctx, PutParams{NS: "ns", Key: "k", Content: "v2"})

	s.Rm(ctx, RmParams{NS: "ns", Key: "k", AllVersions: true})

	_, err := s.Get(ctx, GetParams{NS: "ns", Key: "k", History: true})
	if err == nil {
		t.Error("expected error after deleting all versions")
	}
}

func TestTags(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	s.Put(ctx, PutParams{NS: "ns", Key: "a", Content: "x", Tags: []string{"deploy", "infra"}})
	s.Put(ctx, PutParams{NS: "ns", Key: "b", Content: "y", Tags: []string{"deploy"}})
	s.Put(ctx, PutParams{NS: "ns", Key: "c", Content: "z"})

	list, _ := s.List(ctx, ListParams{NS: "ns", Tags: []string{"deploy"}})
	if len(list) != 2 {
		t.Errorf("expected 2 with 'deploy' tag, got %d", len(list))
	}

	list, _ = s.List(ctx, ListParams{NS: "ns", Tags: []string{"infra"}})
	if len(list) != 1 {
		t.Errorf("expected 1 with 'infra' tag, got %d", len(list))
	}
}

func TestDBPathCreation(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sub", "dir", "test.db")
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	s.Close()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("expected db file to be created")
	}
}

func TestPutWithPriorityAndKind(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	mem, _ := s.Put(ctx, PutParams{
		NS: "ns", Key: "k", Content: "data",
		Kind: "procedural", Priority: "critical",
	})
	if mem.Kind != "procedural" {
		t.Errorf("expected kind 'procedural', got %q", mem.Kind)
	}
	if mem.Priority != "critical" {
		t.Errorf("expected priority 'critical', got %q", mem.Priority)
	}

	got, _ := s.Get(ctx, GetParams{NS: "ns", Key: "k"})
	if got[0].Kind != "procedural" || got[0].Priority != "critical" {
		t.Error("kind/priority not persisted correctly")
	}
}
