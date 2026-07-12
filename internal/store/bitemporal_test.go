package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func newBitemporalTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func putBitemporalPair(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	// Old fact, then a change announcement sharing the "Webpack" entity so
	// detectSupersede fires (change cue + entity overlap).
	if _, err := s.Put(ctx, PutParams{
		NS: "agent:test", Key: "build-tool",
		Content: "The project builds with Webpack for all bundling.",
	}); err != nil {
		t.Fatalf("put old: %v", err)
	}
	if _, err := s.Put(ctx, PutParams{
		NS: "agent:test", Key: "build-tool-update",
		Content: "We switched from Webpack to Vite for bundling.",
	}); err != nil {
		t.Fatalf("put new: %v", err)
	}
}

func searchKeys(t *testing.T, s *SQLiteStore, p SearchParams) map[string]bool {
	t.Helper()
	results, err := s.Search(context.Background(), p)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	keys := map[string]bool{}
	for _, r := range results {
		keys[r.Key] = true
	}
	return keys
}

func TestBitemporalSupersedeRetiresOldFact(t *testing.T) {
	t.Setenv("GHOST_BITEMPORAL", "1")
	s := newBitemporalTestStore(t)
	ctx := context.Background()

	// Backdate the old fact so a real event-time interval exists between its
	// creation and the supersede (RFC3339 has 1s granularity, so same-second
	// put/supersede/AsOf would all collapse to one instant).
	if _, err := s.Put(ctx, PutParams{
		NS: "agent:test", Key: "build-tool",
		Content: "The project builds with Webpack for all bundling.",
	}); err != nil {
		t.Fatalf("put old: %v", err)
	}
	backdated := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if _, err := s.db.Exec(
		`UPDATE memories SET created_at = ? WHERE key = 'build-tool'`, backdated); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	before := time.Now().UTC().Add(-1 * time.Hour)

	if _, err := s.Put(ctx, PutParams{
		NS: "agent:test", Key: "build-tool-update",
		Content: "We switched from Webpack to Vite for bundling.",
	}); err != nil {
		t.Fatalf("put new: %v", err)
	}

	// Default search must hard-retire the superseded fact.
	keys := searchKeys(t, s, SearchParams{NS: "agent:test", Query: "Webpack bundling"})
	if keys["build-tool"] {
		t.Errorf("superseded fact still returned by default search")
	}
	if !keys["build-tool-update"] {
		t.Errorf("current fact missing from search")
	}

	// AsOf before the supersede must still see the old fact.
	keys = searchKeys(t, s, SearchParams{NS: "agent:test", Query: "Webpack bundling", AsOf: before})
	if !keys["build-tool"] {
		t.Errorf("AsOf recall lost the old fact")
	}
	// ... and must not see the fact created after the AsOf instant.
	if keys["build-tool-update"] {
		t.Errorf("AsOf recall returned a fact from the future (valid_from not stamped or filter inverted)")
	}
}

func TestBitemporalOffIsNoop(t *testing.T) {
	t.Setenv("GHOST_BITEMPORAL", "0")
	s := newBitemporalTestStore(t)
	putBitemporalPair(t, s)

	// With the knob off, freshness only demotes importance; both facts remain
	// searchable and valid_to stays NULL.
	keys := searchKeys(t, s, SearchParams{NS: "agent:test", Query: "Webpack bundling"})
	if !keys["build-tool"] || !keys["build-tool-update"] {
		t.Errorf("knob-off retrieval changed: got %v, want both facts", keys)
	}

	var stamped int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM memories WHERE valid_to IS NOT NULL`).Scan(&stamped); err != nil {
		t.Fatalf("count: %v", err)
	}
	if stamped != 0 {
		t.Errorf("valid_to stamped with knob off: %d rows", stamped)
	}
}

func TestBitemporalAsOfWorksWithoutKnob(t *testing.T) {
	// AsOf is honored even with the knob off — on an unstamped DB the validity
	// columns are NULL, so only the valid_from/created-in-future cut applies.
	s := newBitemporalTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{
		NS: "agent:test", Key: "fact",
		Content: "The deploy pipeline uses blue-green rollouts.",
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	keys := searchKeys(t, s, SearchParams{NS: "agent:test", Query: "deploy pipeline", AsOf: time.Now().UTC()})
	if !keys["fact"] {
		t.Errorf("AsOf now should return live facts")
	}
}
