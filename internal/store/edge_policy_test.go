package store

import (
	"context"
	"fmt"
	"testing"
)

// The policy table is the whole reason expansion rules live in Go rather than
// SQL: it can be asserted directly, without a database.
func TestNeighborForSeedDirection(t *testing.T) {
	const seed, other = "seed-id", "other-id"
	cases := []struct {
		name   string
		edge   Edge
		wantID string
		wantOK bool
	}{
		// The case that was broken: a correction points AT the stale memory,
		// so from the stale seed the edge is incoming.
		{"contradicts incoming is followed", Edge{FromID: other, ToID: seed, Rel: "contradicts"}, other, true},
		{"contradicts outgoing is followed", Edge{FromID: seed, ToID: other, Rel: "contradicts"}, other, true},
		{"relates_to is symmetric", Edge{FromID: other, ToID: seed, Rel: "relates_to"}, other, true},
		{"refines incoming is followed", Edge{FromID: other, ToID: seed, Rel: "refines"}, other, true},
		// Asymmetric relations keep today's behaviour.
		{"depends_on outgoing only", Edge{FromID: seed, ToID: other, Rel: "depends_on"}, other, true},
		{"depends_on incoming is not followed", Edge{FromID: other, ToID: seed, Rel: "depends_on"}, other, false},
		{"contains stays parent->child", Edge{FromID: other, ToID: seed, Rel: "contains"}, other, false},
		{"implies incoming is not followed", Edge{FromID: other, ToID: seed, Rel: "implies"}, other, false},
		// merged_into is an audit trail, never traversed.
		{"merged_into never travels", Edge{FromID: seed, ToID: other, Rel: "merged_into"}, other, false},
		// Guards.
		{"self loop", Edge{FromID: seed, ToID: seed, Rel: "contradicts"}, "", false},
		{"unrelated edge", Edge{FromID: "a", ToID: "b", Rel: "contradicts"}, "", false},
		// An unknown relation must degrade to the old behaviour, not silently
		// gain reverse traversal.
		{"unknown rel outgoing", Edge{FromID: seed, ToID: other, Rel: "invented"}, other, true},
		{"unknown rel incoming", Edge{FromID: other, ToID: seed, Rel: "invented"}, other, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotOK := neighborForSeed(tc.edge, seed)
			if gotOK != tc.wantOK {
				t.Fatalf("followable = %v, want %v", gotOK, tc.wantOK)
			}
			if gotOK && gotID != tc.wantID {
				t.Errorf("neighbour = %q, want %q", gotID, tc.wantID)
			}
		})
	}
}

// Typed relations must outrank generic similarity, or auto-linked relates_to
// edges (cosine-weighted, routinely >0.9) take every expansion slot.
func TestExpansionPriorityBeatsSimilarityWeight(t *testing.T) {
	if got, want := expansionDirectionsFor("contradicts").Priority, expansionDirectionsFor("relates_to").Priority; got <= want {
		t.Errorf("contradicts priority %d must exceed relates_to %d", got, want)
	}
	for _, rel := range []string{"refines", "depends_on", "caused_by", "prevents", "implies", "contains"} {
		if expansionDirectionsFor(rel).Priority <= expansionDirectionsFor("relates_to").Priority {
			t.Errorf("%s must outrank relates_to for an expansion slot", rel)
		}
	}
	if expansionDirectionsFor("merged_into").Outgoing || expansionDirectionsFor("merged_into").Incoming {
		t.Error("merged_into is an audit trail and must never be traversed")
	}
}

// A pinned memory is in context every turn, so it is the most reliable seed
// the graph has — but Phase 1 appends pinned memories straight to the result
// and marks them seen, so they never entered scoreMap and could pull in
// nothing. This is the live shape that mattered: a household safety fact is
// pinned, a later correction refutes it, and the correction has to arrive.
func TestPinnedMemorySeedsExpansion(t *testing.T) {
	ctx := context.Background()
	build := func(link bool) *ContextResult {
		s := newTestStore(t)
		if _, err := s.Put(ctx, PutParams{NS: "agent:pin", Key: "stale",
			Content: "The garden gate latch sticks, so it is left unlocked overnight.",
			Kind:    "semantic", Tier: "ltm", Pinned: true, Importance: 0.9}); err != nil {
			t.Fatal(err)
		}
		// Phrased so search cannot find it from the query — only the edge can.
		if _, err := s.Put(ctx, PutParams{NS: "agent:pin", Key: "correction",
			Content: "The June work order from the hardware contractor was completed and signed off on the 14th, and nothing on that list is outstanding.",
			Kind:    "semantic", Tier: "ltm", Importance: 0.5}); err != nil {
			t.Fatal(err)
		}
		// Filler closer to the query than the correction is, so the correction
		// cannot win a slot on similarity. Without this the control arm passes
		// on its own and the test proves nothing — which is exactly what the
		// first version of it did.
		for i := 0; i < 8; i++ {
			_, _ = s.Put(ctx, PutParams{NS: "agent:pin", Key: fmt.Sprintf("gate-note-%d", i),
				Content: fmt.Sprintf("Note %d: the garden gate was checked and left as it was, no change to the latch or the lock.", i),
				Kind:    "semantic", Tier: "ltm", Importance: 0.6})
		}
		if link {
			if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "agent:pin", FromKey: "correction",
				ToNS: "agent:pin", ToKey: "stale", Rel: "contradicts"}); err != nil {
				t.Fatal(err)
			}
		}
		res, err := s.Context(ctx, ContextParams{NS: "agent:pin", Query: "is the garden gate left unlocked", Budget: 600})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	has := func(res *ContextResult, key string) bool {
		for _, m := range res.Memories {
			if m.Key == key {
				return true
			}
		}
		return false
	}

	linked, unlinked := build(true), build(false)
	if !has(linked, "correction") {
		t.Error("a contradicts edge from a pinned memory did not pull the correction into context")
	}
	if has(unlinked, "correction") {
		t.Error("control arm is broken: the correction arrives without an edge, so this test proves nothing")
	}
	// Seeding must not turn the pinned memory into a candidate as well.
	var count int
	for _, m := range linked.Memories {
		if m.Key == "stale" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("pinned memory appears %d times in context, want exactly 1", count)
	}
}
