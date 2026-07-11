package store

import (
	"context"
	"testing"
)

// TestDetectSupersedeDisabled confirms the feature is inert when GHOST_FRESHNESS=0.
func TestDetectSupersedeDisabled(t *testing.T) {
	t.Setenv("GHOST_FRESHNESS", "0")
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "old", Content: "We use Webpack to bundle the frontend.", Kind: "semantic", Importance: 0.6}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "new", Content: "We now use Vite instead of Webpack to bundle.", Kind: "semantic", Importance: 0.5}); err != nil {
		t.Fatal(err)
	}
	// No knob → no contradicts edge, old importance unchanged.
	edges, _ := s.GetEdgesByNSKey(ctx, "t", "new")
	for _, e := range edges {
		if e.Rel == "contradicts" {
			t.Errorf("supersede edge created with knob off: %+v", e)
		}
	}
	old, _ := s.Get(ctx, GetParams{NS: "t", Key: "old"})
	if len(old) == 0 || old[0].Importance != 0.6 {
		t.Errorf("old importance should be unchanged (0.6), got %v", old)
	}
}

// TestDetectSupersedeOn confirms that with the knob set, a change announcement
// sharing an entity creates a contradicts edge and demotes the old fact.
func TestDetectSupersedeOn(t *testing.T) {
	t.Setenv("GHOST_FRESHNESS", "1")
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "old", Content: "We use Webpack to bundle the frontend.", Kind: "semantic", Importance: 0.6}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "new", Content: "We now use Vite instead of Webpack to bundle.", Kind: "semantic", Importance: 0.5}); err != nil {
		t.Fatal(err)
	}

	edges, _ := s.GetEdgesByNSKey(ctx, "t", "new")
	found := false
	for _, e := range edges {
		if e.Rel == "contradicts" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a contradicts edge from new, got %+v", edges)
	}

	old, _ := s.Get(ctx, GetParams{NS: "t", Key: "old"})
	if len(old) == 0 || old[0].Importance >= 0.6 {
		t.Errorf("old importance should be demoted below 0.6, got %v", old)
	}
}

// TestNoSupersedeWithoutEntityOverlap confirms an unrelated change announcement
// does not clobber an unrelated memory.
func TestNoSupersedeWithoutEntityOverlap(t *testing.T) {
	t.Setenv("GHOST_FRESHNESS", "1")
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "unrelated", Content: "The office is in Portland.", Kind: "semantic", Importance: 0.6}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "change", Content: "We now use Vite instead of Webpack.", Kind: "semantic", Importance: 0.5}); err != nil {
		t.Fatal(err)
	}
	un, _ := s.Get(ctx, GetParams{NS: "t", Key: "unrelated"})
	if len(un) == 0 || un[0].Importance != 0.6 {
		t.Errorf("unrelated memory must not be demoted, got %v", un)
	}
}

// TestEvalPersonalFreshnessKnob runs the freshness-update scenario with the
// opt-in supersede detection ON and asserts the current-truth memory now ranks
// above the stale one — the gap the baseline (knob off) records as unmet.
func TestEvalPersonalFreshnessKnob(t *testing.T) {
	t.Setenv("GHOST_FRESHNESS", "1")
	s := newTestStore(t)
	ctx := context.Background()
	// Seed via the shared corpus so detectSupersede runs during Put.
	if _, err := SeedStore(ctx, s, PersonalSeedCorpus()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	res, err := s.Context(ctx, ContextParams{NS: PersonalNS, Query: "what do we use to bundle the frontend", Budget: 1000})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	keys := contextKeys(res)
	pFresh, pStale := rankOf(keys, "new-bundler"), rankOf(keys, "old-bundler")

	if pFresh < 0 {
		t.Fatalf("current-truth memory new-bundler not retrieved; keys=%v", keys)
	}
	if !(pStale < 0 || pFresh < pStale) {
		t.Errorf("with freshness ON, new-bundler (rank %d) should outrank old-bundler (rank %d); keys=%v", pFresh, pStale, keys)
	}
	// The stale fact should still be visible for transparency (force-include).
	if pStale < 0 {
		t.Logf("note: old-bundler not surfaced (acceptable); fresh ranked at %d", pFresh)
	}
}
