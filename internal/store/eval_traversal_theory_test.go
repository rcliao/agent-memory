package store

import (
	"context"
	"testing"
)

// ── Does curated traversal fix a real multi-hop failure? ─────────────
//
// TestEvalMultiHop has scored 0.33/0.33/0.00/0.50 recall since it was written:
// questions whose answers span several memories, where search surfaces one
// piece and the rest lose their top-K slots to unrelated content. That
// benchmark tests multi-hop REASONING — no edges exist in its corpus, so it
// measures search breadth alone.
//
// The whole graph campaign (#93..#102) rests on one claim: if an agent HAD
// written the natural edges, traversal would carry the missing pieces in.
// This test closes the loop by adding exactly the edges a knowledgeable agent
// would write to that same corpus and measuring the same questions through
// ghost_context — the API agents are told to start with — instead of bare
// search.
//
// A premise correction this test forced: the deploy case was expected to be
// the honest negative, because Search(top-5) retrieves NEITHER of its pieces.
// Through Context the premise is false — Context searches an internal pool of
// 50 before packing, deploy-yesterday IS in that pool, and the edge then
// carries ship-process in (1/2 -> 2/2 measured). Traversal still cannot create
// a seed from nothing, but "search cannot find it" claims made from a top-5
// benchmark do not transfer to the API agents actually use.

// theoryEdges is what a knowledgeable agent would plausibly curate for the
// project:alpha corpus, written in each relation's natural direction:
// the dependent points at its prerequisite, the ruled-out thing points at the
// constraint that rules it out.
var theoryEdges = [][3]string{
	// The gateway's routes depend on the services they route to; each service
	// depends on the data layer under it. Two chains sharing a head:
	//   atlas-routing -> user-service-arch   -> data-layer
	//   atlas-routing -> search-service-arch -> data-layer
	{"atlas-routing", "user-service-arch", "depends_on"},
	{"atlas-routing", "search-service-arch", "depends_on"},
	{"user-service-arch", "data-layer", "depends_on"},
	{"search-service-arch", "data-layer", "depends_on"},
	// Yesterday's deploy went through the standard ship process.
	{"deploy-yesterday", "ship-process", "depends_on"},
	// The architecture's safeguards are what prevent the incident recurring:
	// from = the thing being ruled out, to = the constraint.
	{"perf-incident-mar", "search-service-arch", "prevents"},
}

func theoryRecall(t *testing.T, s *SQLiteStore, query string, wantKeys []string, budget int) (int, []string) {
	t.Helper()
	res, err := s.Context(context.Background(), ContextParams{Query: query, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	var keys []string
	for _, m := range res.Memories {
		got[m.Key] = true
		keys = append(keys, m.Key)
	}
	found := 0
	for _, k := range wantKeys {
		if got[k] {
			found++
		}
	}
	return found, keys
}

func TestEvalTraversalTheory(t *testing.T) {
	const budget = 1200
	cases := []struct {
		name     string
		query    string
		wantKeys []string
		// seedRetrievable is whether search finds at least one piece — the
		// precondition for traversal to help at all.
		seedRetrievable bool
	}{
		{"gateway_to_database", "what database does the API gateway route user requests to",
			[]string{"atlas-routing", "user-service-arch", "data-layer"}, true},
		{"search_end_to_end", "how does the search feature work from the API layer to the database",
			[]string{"atlas-routing", "search-service-arch", "data-layer"}, true},
		// seedRetrievable=true even though Search(top-5) finds neither piece:
		// Context's 50-wide internal pool does retrieve deploy-yesterday, and
		// the edge does the rest. See the premise-correction note above.
		{"deploy_what_and_how", "what was in the latest deployment and how does our deploy pipeline work",
			[]string{"deploy-yesterday", "ship-process"}, true},
		{"incident_plus_architecture", "what caused the search latency incident and what safeguards exist",
			[]string{"perf-incident-mar", "search-service-arch"}, true},
	}

	// Two stores, identical corpus, one difference: the curated edges.
	bare, _ := seedEvalStore(t)
	linked, _ := seedEvalStore(t)
	for _, e := range theoryEdges {
		if _, err := linked.CreateEdge(context.Background(), EdgeParams{
			FromNS: "project:alpha", FromKey: e[0],
			ToNS: "project:alpha", ToKey: e[1], Rel: e[2]}); err != nil {
			t.Fatalf("edge %v: %v", e, err)
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			without, _ := theoryRecall(t, bare, tc.query, tc.wantKeys, budget)
			with, keys := theoryRecall(t, linked, tc.query, tc.wantKeys, budget)
			n := len(tc.wantKeys)
			t.Logf("%-28s context recall without edges %d/%d -> with edges %d/%d",
				tc.name, without, n, with, n)
			t.Logf("  keys(with)=%v", keys)

			if with < without {
				t.Errorf("%s: adding edges REDUCED recall %d -> %d", tc.name, without, with)
			}
			if tc.seedRetrievable && with < n {
				t.Errorf("%s: a retrievable seed plus curated edges still left %d/%d pieces "+
					"missing — the traversal theory does not hold on this case",
					tc.name, with, n)
			}
			if !tc.seedRetrievable && with > without {
				t.Errorf("%s: no piece is search-retrievable, yet edges changed recall "+
					"%d -> %d — traversal cannot create a seed, so the corpus premise "+
					"is stale; re-measure", tc.name, without, with)
			}
		})
	}
}

// ── The production killer: the MinScore floor vs structural candidates ──
//
// Both live agents run GHOST_MIN_SCORE=0.3. The floor was built for SEARCH
// noise: topically-unmatched filler that rides recency over weak matches, with
// a relevance-based rescue (relevance >= 0.35) for genuine topical hits.
//
// Edge-expanded candidates fail both prongs BY CONSTRUCTION: their relevance
// is 0 (they were never scored against the query — that is what the edge is
// for), and their propagated score is capped at 0.3 and typically ~0.2x the
// seed. So under the production floor, every non-contradicts edge neighbour
// was filtered out before the reserve hoist could see it, and a contradicts
// neighbour survived only when 0.8 x seed cleared 0.3. The graph work shipped
// in #100/#102 was dead on arrival in production, and the eval missed it
// because tests never set MinScore.
func TestEdgeCandidatesSurviveMinScoreFloor(t *testing.T) {
	mems := []householdMemory{
		{key: "dentist", content: "Need to book the twice-yearly dentist appointment for the kids."},
		{key: "insurance", content: "The dental card on file lapsed at the end of last quarter and has not been replaced."},
		{key: "signature", content: "That replacement paperwork sits unsigned in the folder by the front door."},
	}
	for i := 0; i < 8; i++ {
		mems = append(mems, householdMemory{key: "appt-" + string(rune('0'+i)),
			content: "Appointment note: still need to book the twice-yearly dentist visit for the kids."})
	}
	s := stressStore(t, mems, [][3]string{
		{"dentist", "insurance", "depends_on"},
		{"insurance", "signature", "depends_on"},
	})

	res, err := s.Context(context.Background(), ContextParams{
		NS: "agent:home", Query: "book the dentist appointment",
		Budget: 800, MinScore: 0.3, // production configuration
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasMarker(res, "lapsed at the end of last quarter") {
		t.Errorf("depends_on neighbour dropped by the MinScore floor — the reserve class " +
			"does not exist in production while GHOST_MIN_SCORE=0.3 filters structural " +
			"candidates on relevance they were never meant to have")
	}
	if !hasMarker(res, "sits unsigned in the folder") {
		t.Errorf("hop-2 prerequisite dropped by the MinScore floor — multi-hop traversal " +
			"is inert under the production configuration")
	}
}

// TestRelatesToStaysUnderMinScoreFloor guards the boundary of the exemption.
// relates_to is itself a similarity claim, so the relevance floor is a fair
// judge of it — and on the live stores it is 76,738 auto-linked edges deep in
// near-duplicate territory, so exempting it would re-admit the sibling glut
// the floor happens to suppress. If this test starts failing, the exemption
// has silently widened from "typed relations" to "any edge", which is how a
// safety valve becomes a floodgate.
func TestRelatesToStaysUnderMinScoreFloor(t *testing.T) {
	mems := []householdMemory{
		{key: "dentist", content: "Need to book the twice-yearly dentist appointment for the kids."},
		{key: "tangent", content: "The garden hose nozzle cracked over the winter and drips at the join."},
	}
	for i := 0; i < 8; i++ {
		mems = append(mems, householdMemory{key: "appt-" + string(rune('0'+i)),
			content: "Appointment note: still need to book the twice-yearly dentist visit for the kids."})
	}
	s := stressStore(t, mems, [][3]string{{"dentist", "tangent", "relates_to"}})

	res, err := s.Context(context.Background(), ContextParams{
		NS: "agent:home", Query: "book the dentist appointment",
		Budget: 800, MinScore: 0.3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if hasMarker(res, "garden hose nozzle cracked") {
		t.Errorf("a relates_to passenger bypassed the MinScore floor — the structural " +
			"exemption has widened beyond typed relations")
	}
}

// ── The second production killer: decay buries the refuter ───────────
//
// The MinScore fix turned out not to move umbreon at all (0/12 -> 0/12), and
// the census explains why: 78 of its 144 contradicts refuters are DORMANT, and
// expandEdges skips dormant neighbours unconditionally. So a stale fact can
// stay alive in ltm while the very memory that corrects it decays out of
// reach — stale-without-fresh, manufactured by the lifecycle. Ghost has
// already lost a correct allergy fact to exactly this shape.
//
// The rule this pins: a RESERVE-CLASS edge may pull its neighbour out of
// dormancy. Dormant is "not worth surfacing on its own", and a reserve-class
// neighbour is never surfacing on its own — something already in context is
// false without it. Background and accompany relations still respect
// dormancy; the same false-vs-incomplete line drawn everywhere else.
func TestReserveEdgeReachesDormantNeighbour(t *testing.T) {
	ctx := context.Background()
	s := stressStore(t, []householdMemory{
		{key: "reader", content: "The office badge reader on the third floor accepts the old blue cards."},
		{key: "correction", content: "Facilities swapped that unit out in July and anything issued before then is refused now."},
	}, [][3]string{{"correction", "reader", "contradicts"}})

	// The correction has decayed to dormant; the stale fact is still live.
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memories SET tier='dormant' WHERE ns='agent:home' AND key='correction'`); err != nil {
		t.Fatal(err)
	}

	res, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "which badge works on the third floor reader", Budget: 800})
	if err != nil {
		t.Fatal(err)
	}
	if !hasMarker(res, "swapped that unit out") {
		t.Errorf("dormant refuter did not arrive: a live stale fact is being served with " +
			"its correction decayed out of reach — stale-without-fresh via the lifecycle")
	}
}

// TestBackgroundEdgeRespectsDormancy is the boundary: dormancy still means
// something for non-reserve relations. If this fails, dormant memories are
// leaking back through ordinary similarity edges and archives stop being
// archives.
func TestBackgroundEdgeRespectsDormancy(t *testing.T) {
	ctx := context.Background()
	s := stressStore(t, []householdMemory{
		{key: "reader", content: "The office badge reader on the third floor accepts the old blue cards."},
		{key: "trivia", content: "The lobby fish tank was refilled with a different gravel colour in spring."},
	}, [][3]string{{"reader", "trivia", "relates_to"}})
	if _, err := s.db.ExecContext(ctx,
		`UPDATE memories SET tier='dormant' WHERE ns='agent:home' AND key='trivia'`); err != nil {
		t.Fatal(err)
	}
	res, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "which badge works on the third floor reader", Budget: 800})
	if err != nil {
		t.Fatal(err)
	}
	if hasMarker(res, "different gravel colour") {
		t.Errorf("a dormant memory resurfaced through a relates_to edge — dormancy no " +
			"longer means anything for the bulk relation")
	}
}
