package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Summary-redesign eval: summaries are packing artifacts, not memories.
//
//  1. Budget-shrink curve: with room, specifics pack individually; under
//     pressure, the contains-parent substitutes for the group WITH drill-down
//     keys — topic coverage survives compression (the lossless property).
//  2. Liveness-scaled retrieval rights: a summary whose children are alive
//     defers to them in direct search; once the children age to dormant, the
//     summary inherits their searchability.
func summarySeededStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sum.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := context.Background()

	put := func(key, content string) {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: key, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	// Four specifics about one project epoch + a distilled parent.
	put("mig-1", "database migration step one: exported the legacy postgres schema and verified row counts")
	put("mig-2", "database migration step two: rewrote the schema for the new postgres cluster with partitioning")
	put("mig-3", "database migration step three: ran the dual-write phase for the postgres migration over two weeks")
	put("mig-4", "database migration step four: cut over reads to the new postgres cluster and archived the legacy one")
	put("mig-summary", "Database migration epoch summary: four-step postgres migration (export, schema rewrite, dual-write, cutover) completed successfully with zero data loss")
	for _, child := range []string{"mig-1", "mig-2", "mig-3", "mig-4"} {
		if _, err := s.CreateEdge(ctx, EdgeParams{
			FromNS: "t", FromKey: "mig-summary", ToNS: "t", ToKey: child, Rel: "contains",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Unrelated memory to keep retrieval honest.
	put("other", "team lunch is on thursdays at the taco place")
	return s
}

func TestEvalPackingSubstitution(t *testing.T) {
	s := summarySeededStore(t)
	ctx := context.Background()
	query := "how did the postgres database migration go"

	// Roomy budget: specifics pack individually, no substitution.
	roomy, err := s.Context(ctx, ContextParams{NS: "t", Query: query, Budget: 2000})
	if err != nil {
		t.Fatal(err)
	}
	childCount := 0
	for _, m := range roomy.Memories {
		if len(m.SummaryOf) > 0 {
			t.Errorf("roomy budget must not substitute (got summary_of on %s)", m.Key)
		}
		if m.Key == "mig-1" || m.Key == "mig-2" || m.Key == "mig-3" || m.Key == "mig-4" {
			childCount++
		}
	}
	if childCount < 3 {
		t.Fatalf("roomy budget should pack the specifics (got %d children)", childCount)
	}

	// Tight budget: the parent substitutes, carrying drill-down keys.
	tight, err := s.Context(ctx, ContextParams{NS: "t", Query: query, Budget: 120})
	if err != nil {
		t.Fatal(err)
	}
	var sub *ContextMemory
	for i := range tight.Memories {
		if tight.Memories[i].Key == "mig-summary" {
			sub = &tight.Memories[i]
		}
	}
	if sub == nil {
		t.Fatalf("tight budget should inject the summary; got %v", keysOfContext(tight))
	}
	if len(sub.SummaryOf) < 3 {
		t.Errorf("substituted summary missing drill-down keys: %v", sub.SummaryOf)
	}
}

func TestEvalSummaryLivenessRights(t *testing.T) {
	s := summarySeededStore(t)
	ctx := context.Background()
	query := "postgres migration schema dual-write cutover epoch summary"

	// Children alive: direct search must prefer specifics over the summary.
	res, err := s.Search(ctx, SearchParams{NS: "t", Query: query, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) > 0 && res[0].Key == "mig-summary" {
		t.Errorf("living children: summary must not outrank specifics (top=%s)", res[0].Key)
	}

	// Age the children out: the summary inherits retrieval rights.
	if _, err := s.db.Exec(
		`UPDATE memories SET tier='dormant' WHERE key LIKE 'mig-_'`); err != nil {
		t.Fatal(err)
	}
	res2, err := s.Search(ctx, SearchParams{NS: "t", Query: query, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range res2 {
		if r.Key == "mig-summary" {
			found = true
		}
	}
	if !found {
		t.Errorf("dormant children: summary should be retrievable (got %v)", keysOfSearch(res2))
	}
}

func keysOfContext(r *ContextResult) []string {
	var out []string
	for _, m := range r.Memories {
		out = append(out, m.Key)
	}
	return out
}

func keysOfSearch(rs []SearchResult) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Key)
	}
	return out
}
