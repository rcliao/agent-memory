package store

import (
	"context"
	"testing"
)

// ── Subsystem liveness canary ─────────────────────────────────────────
//
// Four production incidents share one shape: a retrieval stage died
// silently while every eval stayed green, because nothing asserted the
// stage still CONTRIBUTES TO OUTPUT under the config production actually
// runs. (#103 MinScore floor killed edge candidates; dormancy hid
// refuters; a scan-site miss disabled reflect's prune; #113 broke
// getMemoryByID and link expansion errored on every call for five days.)
//
// This eval builds one corpus that exercises every stage, then asserts —
// under BOTH the default and the PRODUCTION profile — that every stage
// contributed at least once and no stage swallowed an error. A stage
// born dead, or killed by a config interaction, fails here within one
// test run instead of five days of production.

func livenessCorpus(t *testing.T) *SQLiteStore {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()

	puts := []PutParams{
		{Key: "pin-conventions", Content: "House rule: confirm before booking anything nonrefundable.",
			Pinned: true, Importance: 0.9},
		{Key: "trip-plan", Content: "The coast trip plan books the seaside hotel for the long weekend.",
			Importance: 0.6},
		// Zero query-term overlap: reachable ONLY through its edge (the
		// finding-4 shape — reservation, not score, is the lever).
		{Key: "trip-constraint", Content: "Deposit clearance takes five business days. Sizable purchases pause until funds settle.",
			Importance: 0.5},
		{Key: "trip-guess", Content: "Mami seems happy to book the seaside hotel immediately.",
			SourceUser: "mami", SourceKind: "observed", Importance: 0.8},
		{Key: "trip-stated", Content: "Mami said to hold hotel booking until she checks her schedule.",
			SourceUser: "mami", SourceKind: "stated", Importance: 0.4},
		// One weak query term: enough overlap for link expansion, ranked
		// below the search limit by the filler block.
		{Key: "trip-note", Content: "Hotel parking passes live in the glovebox since the lobby renovation.",
			Importance: 0.3},
	}
	for i := 0; i < 10; i++ {
		puts = append(puts, PutParams{Key: "filler-" + string(rune('a'+i)),
			Content: "The seaside hotel trip booking checklist item " + string(rune('a'+i)) + " covers packing and weekend logistics.",
			Importance: 0.5})
	}
	for _, p := range puts {
		p.NS, p.Kind, p.Tier = "agent:live", "semantic", "ltm"
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	// A typed reserve-class edge: the constraint must accompany the plan.
	if _, err := s.CreateEdge(ctx, EdgeParams{
		FromNS: "agent:live", FromKey: "trip-constraint",
		ToNS: "agent:live", ToKey: "trip-plan",
		Rel: "prevents", Weight: 0.9}); err != nil {
		t.Fatal(err)
	}
	// An association for search-side link expansion.
	if _, err := s.CreateEdge(ctx, EdgeParams{
		FromNS: "agent:live", FromKey: "trip-note",
		ToNS: "agent:live", ToKey: "trip-plan",
		Rel: "relates_to", Weight: 0.8}); err != nil {
		t.Fatal(err)
	}
	return s
}

func assertStagesAlive(t *testing.T, s *SQLiteStore, label string) {
	t.Helper()
	ctx := context.Background()
	_, errsBefore := DiagLinkExpansion()

	res, err := s.Context(ctx, ContextParams{
		NS: "agent:live", Query: "book the seaside hotel for the trip", Budget: 900})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{
		"packed_pinned", "search_results", "packed_search",
		"edge_marked_reserved", "authority_reserved", "packed_reserved",
	} {
		if res.Stages[stage] == 0 {
			t.Errorf("[%s] stage %q contributed nothing — dead subsystem or "+
				"config interaction (stages: %v)", label, stage, res.Stages)
		}
	}

	// Search-side link expansion (opt-in: only ExpandEdges callers — today
	// the benchmark harnesses — reach it; the #113 broken scan hid here).
	addedBefore, _ := DiagLinkExpansion()
	if _, err := s.Search(ctx, SearchParams{
		NS: "agent:live", Query: "seaside hotel trip", Limit: 10, ExpandEdges: true}); err != nil {
		t.Fatal(err)
	}
	addedAfter, errsAfter := DiagLinkExpansion()
	if addedAfter == addedBefore {
		t.Errorf("[%s] search link expansion added nothing on an edge-rich corpus", label)
	}
	if errsAfter != errsBefore {
		t.Errorf("[%s] link expansion swallowed %d error(s) — the #113 shape", label, errsAfter-errsBefore)
	}
}

func TestSubsystemLivenessDefaultProfile(t *testing.T) {
	s := livenessCorpus(t)
	assertStagesAlive(t, s, "default")
}

// The production profile is the config every incident actually ran under —
// and the config the eval suite historically never set (#103's lesson,
// institutionalized).
func TestSubsystemLivenessProductionProfile(t *testing.T) {
	t.Setenv("GHOST_MIN_SCORE", "0.3")
	t.Setenv("GHOST_RERANK_TOP_N", "10")
	t.Setenv("GHOST_UTILITY_WEIGHT", "0.5")
	s := livenessCorpus(t)
	res, err := s.Context(context.Background(), ContextParams{
		NS: "agent:live", Query: "book the seaside hotel for the trip", Budget: 900,
		MinScore: 0.3})
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"packed_pinned", "packed_search", "edge_marked_reserved", "authority_reserved", "packed_reserved"} {
		if res.Stages[stage] == 0 {
			t.Errorf("[production] stage %q contributed nothing under GHOST_MIN_SCORE=0.3 — "+
				"this is exactly how #103 hid (stages: %v)", stage, res.Stages)
		}
	}
}
