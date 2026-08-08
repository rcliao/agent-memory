package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// ── Stress tests for the relation-handling theory ────────────────────
//
// The theory arrived at in #93/#98/#100 is: direction is a property of the
// relation, reservation (not score) is what carries a neighbour past a
// near-duplicate wall, and only relations whose absence makes the agent assert
// something FALSE may claim reserved budget.
//
// The pull-through corpus was built to demonstrate that theory, so it is a poor
// judge of it. These cases are built to break it, using the kind of content a
// household assistant actually accumulates rather than one-line probes.
// Synthetic throughout — this ships in a public repo.
//
// Four failure modes, each of which would matter in production:
//
//	A. BACKWARDS AUTHORING. An agent writes the edge the wrong way round.
//	   Nothing in the ghost_edge tool description says which end is `from`, so
//	   this is a coin flip today. Measures what it costs.
//	B. FAN-OUT. One memory carrying many reserve-class edges. If the reserve is
//	   unbounded this is a denial of service on the rest of context.
//	C. CHAIN DEPTH. A depends_on B depends_on C. Expansion is single-hop, so C
//	   is unreachable — measures whether the theory silently assumes transitivity.
//	D. MISLABEL COST. An agent tags ordinary similarity as `depends_on`. Junk
//	   then claims reserved budget. Measures the blast radius of a wrong label,
//	   which is the real risk of asking agents to curate edges.

// householdMemory is one fact in a stress scenario.
type householdMemory struct {
	key, content string
	pinned       bool
}

// stressStore builds a store with a household corpus plus the edges a scenario
// needs. Edges are given as {fromKey, toKey, rel}.
func stressStore(t *testing.T, mems []householdMemory, edges [][3]string) *SQLiteStore {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	for i, m := range mems {
		s.SetClock(func() time.Time { return base.AddDate(0, 0, i) })
		if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: m.key, Content: m.content,
			Kind: "semantic", Tier: "ltm", Pinned: m.pinned, Importance: 0.6}); err != nil {
			t.Fatal(err)
		}
	}
	s.SetClock(func() time.Time { return base.AddDate(0, 0, len(mems)+5) })
	for _, e := range edges {
		if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "agent:home", FromKey: e[0],
			ToNS: "agent:home", ToKey: e[1], Rel: e[2]}); err != nil {
			t.Fatalf("edge %v: %v", e, err)
		}
	}
	return s
}

func stressContext(t *testing.T, s *SQLiteStore, query string, budget int) *ContextResult {
	t.Helper()
	res, err := s.Context(context.Background(), ContextParams{NS: "agent:home", Query: query, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// dinnerCorpus is the shared background: a household planning meals, with
// enough near-identical meal notes to create the filler wall that finding 4
// says is the real adversary.
func dinnerCorpus() []householdMemory {
	mems := []householdMemory{
		{key: "dinner-plan", content: "Thinking about what to cook for dinner this week; satay and noodles are on the shortlist."},
		// The safety fact, worded so it shares almost no vocabulary with a
		// dinner query — exactly the case where only an edge can carry it.
		{key: "allergy", content: "Anaphylaxis risk in this house: an EpiPen lives in the hall drawer and the school has one too."},
	}
	for i := 0; i < 10; i++ {
		mems = append(mems, householdMemory{
			key:     fmt.Sprintf("meal-note-%d", i),
			content: fmt.Sprintf("Dinner idea %d: satay and noodles again this week, nothing new to report on the shortlist.", i),
		})
	}
	return mems
}

const dinnerQuery = "what should we cook for dinner"

// A. Backwards authoring.
//
// The `prevents` relation reads `A --prevents--> B` where B is the preventer,
// so the natural authoring is dinner-plan --prevents--> allergy. An agent with
// no direction guidance has even odds of writing it the other way. This
// measures the cost of that, and the cost is total: a backwards safety edge is
// silently inert, which is the worst possible failure for this relation class.
func TestStressBackwardsAuthoring(t *testing.T) {
	for _, tc := range []struct {
		name       string
		from, to   string
		wantArrive bool
	}{
		{"correct: plan -> allergy", "dinner-plan", "allergy", true},
		{"backwards: allergy -> plan", "allergy", "dinner-plan", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := stressStore(t, dinnerCorpus(), [][3]string{{tc.from, tc.to, "prevents"}})
			res := stressContext(t, s, dinnerQuery, 300)
			got := hasMarker(res, "EpiPen lives in the hall drawer")
			if got != tc.wantArrive {
				t.Errorf("safety fact reached context = %v, want %v", got, tc.wantArrive)
			}
			if !got && tc.wantArrive {
				t.Log("the reserve class did not save it — check handling wiring")
			}
			if !tc.wantArrive && !got {
				t.Log("CONFIRMED: a backwards safety edge is silently inert. Nothing warns " +
					"the author, and the allergy fact simply never arrives.")
			}
		})
	}
}

// B. Fan-out: does the reserve become a denial of service?
//
// One seed carrying six reserve-class edges. The bound is a third of the
// remaining budget, so some neighbours must be admitted and the rest must fall
// back to score order — and ordinary search results must still get in.
func TestStressReserveFanOut(t *testing.T) {
	// Sized so the reserve bound actually binds: 12 constraints of ~45 tokens
	// each is ~540 tokens competing for a reserve of budget/3 = 200. An earlier
	// version used 6 short ones, which fit inside the third and so proved
	// nothing about the bound.
	const fanOut = 12
	const budget = 600

	// The constraints are written FIRST so they are the OLDEST memories here.
	// An earlier version appended them, which made them the newest, and recency
	// alone put 8 of 12 into context with no edges at all — the test measured
	// the clock, not the reserve. Same trap the graph corpus documents: a
	// neighbour reachable without its edge proves nothing.
	run := func(rel string, linked bool) (constraints, other int) {
		var mems []householdMemory
		for i := 0; i < fanOut; i++ {
			mems = append(mems, householdMemory{key: fmt.Sprintf("constraint-%d", i),
				content: fmt.Sprintf("Storage caveat %d: the upstairs appliance has been unreliable ever since the move, "+
					"so item %d left in it overnight has to be checked before anyone uses it.", i, i)})
		}
		mems = append(mems, dinnerCorpus()...)
		var edges [][3]string
		if linked {
			for i := 0; i < fanOut; i++ {
				edges = append(edges, [3]string{"dinner-plan", fmt.Sprintf("constraint-%d", i), rel})
			}
		}
		res := stressContext(t, stressStore(t, mems, edges), dinnerQuery, budget)
		for _, m := range res.Memories {
			if len(m.Key) > 11 && m.Key[:11] == "constraint-" {
				constraints++
			} else {
				other++
			}
		}
		return
	}

	// Control 1: no edges at all. Must admit zero constraints, or the corpus is
	// measuring reachability-by-search and nothing else.
	nc, no := run("prevents", false)
	// Control 2: identical fan-out labelled as ordinary similarity. Whatever
	// difference remains between this and the reserve arm is the reserve's cost.
	bc, bo := run("relates_to", true)
	rc, ro := run("prevents", true)
	t.Logf("budget=%d, %d neighbours offered", budget, fanOut)
	t.Logf("  no edges     (control): %2d admitted + %2d ordinary", nc, no)
	t.Logf("  as relates_to(control): %2d admitted + %2d ordinary", bc, bo)
	t.Logf("  as prevents  (reserve): %2d admitted + %2d ordinary", rc, ro)
	t.Logf("  reserve class costs %+d ordinary memories vs relates_to", ro-bo)

	if nc != 0 {
		t.Fatalf("%d constraints reached context with NO edge — they are reachable by "+
			"search, so this corpus cannot measure the reserve at all", nc)
	}
	if ro == 0 {
		t.Errorf("reserve class crowded out EVERY ordinary memory — the one-third bound "+
			"limits the hoist but not total admission, so a fan-out of %d is a denial "+
			"of service on the rest of context", fanOut)
	}
	if rc == 0 {
		t.Errorf("no reserve-class neighbour admitted at all — reservation is not firing")
	}
	if rc == fanOut {
		t.Errorf("all %d reserve-class neighbours were admitted, so the bound never "+
			"engaged and this test proves nothing about denial of service", fanOut)
	}
}

// C. Chain depth: the theory says nothing about transitivity, and expansion is
// single-hop. This pins that limit so nobody assumes otherwise.
//
// "Book the dentist" depends_on "insurance card expired" depends_on "the renewal
// form needs a signature". Querying the first should reach the second and NOT
// the third.
func TestStressChainDepthIsOne(t *testing.T) {
	mems := []householdMemory{
		{key: "dentist", content: "Need to book the twice-yearly dentist appointment for the kids."},
		{key: "insurance", content: "The dental card on file lapsed at the end of last quarter and has not been replaced."},
		{key: "signature", content: "That replacement paperwork sits unsigned in the folder by the front door."},
	}
	for i := 0; i < 8; i++ {
		mems = append(mems, householdMemory{key: fmt.Sprintf("appt-%d", i),
			content: fmt.Sprintf("Appointment note %d: still need to book the twice-yearly dentist visit for the kids.", i)})
	}
	s := stressStore(t, mems, [][3]string{
		{"dentist", "insurance", "depends_on"},
		{"insurance", "signature", "depends_on"},
	})
	res := stressContext(t, s, "book the dentist appointment", 400)

	hop1 := hasMarker(res, "lapsed at the end of last quarter")
	hop2 := hasMarker(res, "sits unsigned in the folder")
	t.Logf("one hop away reached=%v, two hops away reached=%v", hop1, hop2)
	if !hop1 {
		t.Errorf("the direct prerequisite did not arrive; reserve handling is not working for depends_on")
	}
	if hop2 {
		t.Log("NOTE: two hops arrived — expansion is no longer single-hop, update the theory")
	} else {
		t.Log("CONFIRMED: expansion is single-hop. A prerequisite OF a prerequisite is " +
			"invisible, so an agent curating a chain must link the query-side memory " +
			"to every blocker directly, not transitively.")
	}
}

// D. Mislabel cost: what does one wrong label buy an agent?
//
// The realistic curation error is calling ordinary similarity `depends_on`.
// Those neighbours then claim reserved budget ahead of genuine search hits.
// This measures the displacement, which is the honest cost of asking agents to
// curate edges at all.
func TestStressMislabelCost(t *testing.T) {
	build := func(rel string) *ContextResult {
		mems := dinnerCorpus()
		var edges [][3]string
		for i := 0; i < 5; i++ {
			k := fmt.Sprintf("chatter-%d", i)
			mems = append(mems, householdMemory{key: k,
				content: fmt.Sprintf("Passing remark %d about groceries, unrelated to any actual constraint or prerequisite whatsoever.", i)})
			edges = append(edges, [3]string{"dinner-plan", k, rel})
		}
		s := stressStore(t, mems, edges)
		return stressContext(t, s, dinnerQuery, 500)
	}

	honest := build("relates_to")
	mislabelled := build("depends_on")

	count := func(res *ContextResult, prefix string) int {
		n := 0
		for _, m := range res.Memories {
			if len(m.Key) >= len(prefix) && m.Key[:len(prefix)] == prefix {
				n++
			}
		}
		return n
	}
	hj, hm := count(honest, "chatter-"), count(honest, "meal-note-")
	mj, mm := count(mislabelled, "chatter-"), count(mislabelled, "meal-note-")
	t.Logf("labelled relates_to : %d junk + %d real notes in context (total %d)", hj, hm, len(honest.Memories))
	t.Logf("labelled depends_on : %d junk + %d real notes in context (total %d)", mj, mm, len(mislabelled.Memories))
	t.Logf("cost of the wrong label: %+d junk memories, %+d real notes displaced", mj-hj, mm-hm)
	if mj > hj && mm >= hm {
		t.Log("NOTE: junk got in without displacing anything — the budget had slack, " +
			"so this understates the cost at a tighter budget")
	}
}
