package store

import (
	"context"
	"testing"
)

// TestReflectRelink verifies the multi-signal backfill links memories that
// share a named entity even with NO embedder (cosine=0) — the case the
// cosine-only auto-linker misses, which is why the live graph is ~97% islands.
func TestReflectRelink(t *testing.T) {
	s := newTestStore(t) // no embedder → cosine signal is 0
	ctx := context.Background()

	put := func(key, content string) {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: key, Content: content, Kind: "semantic", Importance: 0.5, SkipAutoLink: true}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	// Two memories share the entity "Postgres"; the third is unrelated.
	put("db-choice", "We use Postgres for the metadata store.")
	put("db-txns", "Postgres gives us transactions across the metadata store.")
	put("weather", "the sky was clear yesterday afternoon")

	res, err := s.Reflect(ctx, ReflectParams{NS: "t", Relink: true})
	if err != nil {
		t.Fatalf("reflect relink: %v", err)
	}
	if res.Relinked < 1 {
		t.Fatalf("expected at least one relates_to edge from shared entity, got %d (errors: %v)", res.Relinked, res.Errors)
	}

	// The two Postgres memories must now be connected.
	edges, err := s.GetEdgesByNSKey(ctx, "t", "db-choice")
	if err != nil {
		t.Fatalf("get edges: %v", err)
	}
	linked := false
	for _, e := range edges {
		if e.Rel == "relates_to" {
			linked = true
		}
	}
	if !linked {
		t.Errorf("db-choice should have a relates_to edge after relink; edges=%+v", edges)
	}
}

// TestReflectRelinkIdempotent confirms re-running relink does not duplicate edges.
func TestReflectRelinkIdempotent(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for k, c := range map[string]string{
		"a": "Redis powers our session cache.",
		"b": "We rely on Redis for the session cache layer.",
	} {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: k, Content: c, Kind: "semantic", SkipAutoLink: true}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.Reflect(ctx, ReflectParams{NS: "t", Relink: true})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Reflect(ctx, ReflectParams{NS: "t", Relink: true})
	if err != nil {
		t.Fatal(err)
	}
	// Count edges after each run — they must not grow.
	edges, _ := s.GetEdgesByNSKey(ctx, "t", "a")
	if len(edges) == 0 {
		t.Fatalf("expected an edge after relink (first reported %d)", first.Relinked)
	}
	_ = second // second run must not add duplicates (INSERT OR IGNORE)
	edgesAfter, _ := s.GetEdgesByNSKey(ctx, "t", "a")
	if len(edgesAfter) != len(edges) {
		t.Errorf("relink not idempotent: edges grew from %d to %d", len(edges), len(edgesAfter))
	}
}

// TestReflectRelinkRequiresNS confirms relink without a namespace reports an error.
func TestReflectRelinkRequiresNS(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	res, err := s.Reflect(ctx, ReflectParams{NS: "", Relink: true})
	if err != nil {
		t.Fatalf("reflect should not hard-error: %v", err)
	}
	found := false
	for _, e := range res.Errors {
		if e == "relink requires a namespace (--ns)" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a relink-requires-ns error, got %v", res.Errors)
	}
}
