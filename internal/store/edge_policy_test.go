package store

import "testing"

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
