package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// Repro for the shell-reported bug (2026-07-14): DBs created before the
// standalone-bigram FTS rebuild kept their original triggers — CREATE TRIGGER
// IF NOT EXISTS never replaces. The stale bodies (a) use the external-content
// 'delete' command, an SQL error on the standalone table that silently broke
// every chunk DELETE/UPDATE (= expired-chunk GC), and (b) index new text
// without cjk_segment, so new CJK content was invisible to FTS. Reopening the
// DB must detect the stale DDL, recreate the triggers, and reindex.
func TestStaleFTSTriggerMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")

	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "old-note", Content: "an old note about deployment"}); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Simulate the legacy trigger set exactly as found in production agent DBs.
	for _, stmt := range []string{
		`DROP TRIGGER chunks_ai`, `DROP TRIGGER chunks_ad`, `DROP TRIGGER chunks_au`,
		`CREATE TRIGGER chunks_ai AFTER INSERT ON chunks BEGIN
			INSERT INTO chunks_fts(rowid, text) VALUES (new.rowid, new.text);
		END`,
		`CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
			INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES('delete', old.rowid, old.text);
		END`,
		`CREATE TRIGGER chunks_au AFTER UPDATE ON chunks BEGIN
			INSERT INTO chunks_fts(chunks_fts, rowid, text) VALUES('delete', old.rowid, old.text);
			INSERT INTO chunks_fts(rowid, text) VALUES (new.rowid, new.text);
		END`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("install legacy trigger (%s): %v", stmt[:20], err)
		}
	}
	// A CJK memory written under the stale insert trigger: indexed WITHOUT
	// bigram segmentation.
	if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "cjk-note", Content: "今天買了排骨飯當午餐"}); err != nil {
		t.Fatalf("cjk put: %v", err)
	}
	// The stale delete trigger breaks chunk deletes.
	if _, err := s.db.Exec(`DELETE FROM chunks WHERE memory_id IN (SELECT id FROM memories WHERE key = 'old-note')`); err == nil {
		t.Fatal("expected stale trigger to break chunk delete (bug precondition) — did the fixture change?")
	}
	s.Close()

	// Reopen: migration must repair triggers and reindex.
	s2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	var adSQL string
	if err := s2.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='trigger' AND name='chunks_ad'`).Scan(&adSQL); err != nil {
		t.Fatalf("read trigger ddl: %v", err)
	}
	if strings.Contains(adSQL, "'delete'") {
		t.Fatalf("chunks_ad still uses external-content 'delete' syntax after reopen: %s", adSQL)
	}
	// Chunk deletes work again (the GC path).
	if _, err := s2.db.Exec(`DELETE FROM chunks WHERE memory_id IN (SELECT id FROM memories WHERE key = 'old-note')`); err != nil {
		t.Fatalf("chunk delete still failing after migration: %v", err)
	}
	// The CJK memory indexed under the stale trigger is searchable after reindex.
	res, err := s2.Search(ctx, SearchParams{NS: "agent:t", Query: "排骨飯", Limit: 5})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := false
	for _, r := range res {
		if r.Key == "cjk-note" {
			found = true
		}
	}
	if !found {
		t.Fatalf("CJK content written under stale trigger should be FTS-searchable after reindex, got %+v", res)
	}
}
