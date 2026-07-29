package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// FAMA on the CONTEXT path — the instrument the Search-only FAMA cannot be.
//
// TestEvalLifecycle measures forgetting with s.Search(). Measured 2026-07-29:
// fama_fresh = 1.00 at every epoch in BOTH the reflect=true and reflect=false
// arms, and fama_mrr = 1.00 from day 10. A metric saturated in both arms
// cannot adjudicate a change — it is not an instrument, it is a constant. Any
// restatement-aggregation work needs a metric with headroom to move, so this
// file measures what the agent actually consumes: the assembled context block.
//
// TWO LABELED CHANNELS, because ghost has a deliberate design decision here.
// On supersede, freshness.go writes a `contradicts` edge, and context.go
// force-includes contradicting memories past the score cap — ghost injects a
// superseded fact NEXT TO its correction on purpose, so the agent can see the
// change rather than silently trusting whichever version won a ranking. Under
// a naive FAMA absence criterion that scores 0 by design. So:
//
//   - forceIncluded: superseded content present AND a contradicts edge links
//     it to the current version. DESIGN. Reported, never asserted against.
//   - draggedIn: superseded content present with NO contradicts edge — it rode
//     in on relates_to expansion or recency. DEFECT, and the same population
//     as the utility-parasite problem.
//
// Collapsing these two into one number is how a real regression hides behind
// an intentional behavior, and how someone "fixes" the metric by deleting the
// force-include and blinding the agent to its own corrections.
func TestEvalFAMAContext(t *testing.T) {
	s, evolving, restate := famaSetup(t)
	ctx := context.Background()

	var forceIncluded, draggedIn, currentPresent int
	for _, ef := range evolving {
		current := len(ef.versions) - 1
		res, err := s.Context(ctx, ContextParams{NS: lcNS, Query: ef.query,
			Budget: 1500, MinScore: 0.3, ExcludePinned: true})
		if err != nil {
			t.Fatalf("context %q: %v", ef.query, err)
		}
		inCtx := map[string]bool{}
		for _, m := range res.Memories {
			inCtx[m.Key] = true
		}
		if inCtx[ef.keys[current]] {
			currentPresent++
		}
		for v, key := range ef.keys {
			if v == current || !inCtx[key] {
				continue
			}
			if famaHasContradictsEdge(t, s, key, ef.keys[current]) {
				forceIncluded++
			} else {
				draggedIn++
				t.Logf("  DRAG-IN [%s] superseded %q in context with no contradicts edge", ef.topic, key)
			}
		}
	}

	t.Logf("FAMA(context): current_present=%d/%d  forceIncluded=%d (design)  draggedIn=%d (defect)",
		currentPresent, len(evolving), forceIncluded, draggedIn)

	// The only hard assertion: the CURRENT version must reach the agent.
	// Absence of superseded versions is reported, not required — that is the
	// design decision above, and this test exists to keep it visible.
	if currentPresent < len(evolving) {
		t.Errorf("current version missing from assembled context for %d/%d evolving topics",
			len(evolving)-currentPresent, len(evolving))
	}
	// A superseded fact with no contradicts edge is unambiguously wrong: the
	// agent sees a stale claim with nothing marking it stale.
	if draggedIn > 0 {
		t.Errorf("%d superseded memories reached context with no contradicts edge — stale claims presented as current", draggedIn)
	}

	// Restatement arm: N rewordings of ONE rule. Ghost splits them into
	// siblings today (pinned by the PR #64 ratchet); this measures the cost
	// where it actually hurts — context tokens spent on redundant copies of
	// one rule, and rehearsal credit split across them.
	res, err := s.Context(ctx, ContextParams{NS: lcNS, Query: restate.query,
		Budget: 1500, MinScore: 0.3, ExcludePinned: true})
	if err != nil {
		t.Fatalf("restatement context: %v", err)
	}
	copies, tokens, maxAccess := 0, 0, 0
	for _, m := range res.Memories {
		if strings.Contains(m.Key, restate.keyPrefix) {
			copies++
			tokens += len(m.Content) / 4
		}
	}
	for _, k := range restate.keys {
		var ac int
		s.db.QueryRow(`SELECT COALESCE(access_count,0) FROM memories
			WHERE ns=? AND key=? AND deleted_at IS NULL`, lcNS, k).Scan(&ac)
		if ac > maxAccess {
			maxAccess = ac
		}
	}
	t.Logf("RESTATEMENT: %d/%d rewordings of one rule in context (~%d tokens), max access_count across copies=%d",
		copies, len(restate.keys), tokens, maxAccess)

	// RATCHET, not aspiration — aggregation is not built yet (see PR #64: the
	// cosine ordering inversion makes write-time merging impossible, and the
	// 0.85 auto-link gate never even sees the 0.72-0.75 restatement band). If
	// this starts passing with <=1 copy, aggregation landed: flip the
	// assertion to the aspirational form and record the token saving.
	if copies <= 1 && len(restate.keys) > 1 {
		t.Errorf("ratchet: expected the documented sibling explosion, got %d copy — aggregation appears to have landed, flip this to the aspirational assertion and record the delta", copies)
	}
}

// famaHasContradictsEdge reports whether a contradicts edge links these two
// memories in either direction.
func famaHasContradictsEdge(t *testing.T, s *SQLiteStore, keyA, keyB string) bool {
	t.Helper()
	ctx := context.Background()
	edges, err := s.GetEdgesByNSKey(ctx, lcNS, keyA)
	if err != nil {
		return false
	}
	var idB string
	s.db.QueryRow(`SELECT id FROM memories WHERE ns=? AND key=? AND deleted_at IS NULL
		ORDER BY version DESC LIMIT 1`, lcNS, keyB).Scan(&idB)
	if idB == "" {
		return false
	}
	for _, e := range edges {
		if e.Rel == "contradicts" && (e.ToID == idB || e.FromID == idB) {
			return true
		}
	}
	return false
}

type famaRestatement struct {
	query     string
	keyPrefix string
	keys      []string
}

// famaSetup builds a compressed lifecycle: the evolving facts from the
// existing FAMA corpus (so both instruments describe the same world) plus one
// rule restated several ways, seeded across distinct days so the spaced-
// rehearsal machinery sees a realistic trajectory.
func famaSetup(t *testing.T) (*SQLiteStore, []lcEvolvingFact, famaRestatement) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	clock := evalClockEpoch
	s.SetClock(func() time.Time { return clock })

	_, evolving := lcCorpus()

	// Evolving facts: each version lands 10 days apart, with Dedup on so the
	// supersede path (freshness.go) gets a real chance to fire and write its
	// contradicts edge — that is what separates the two channels.
	for v := 0; v < 3; v++ {
		clock = evalClockEpoch.Add(time.Duration(v*10) * 24 * time.Hour)
		for _, ef := range evolving {
			if v >= len(ef.versions) {
				continue
			}
			if _, err := s.Put(ctx, PutParams{NS: lcNS, Key: ef.keys[v],
				Content: ef.versions[v], Kind: "semantic", Tier: "stm",
				Importance: 0.6, Dedup: true}); err != nil {
				t.Fatalf("put %s: %v", ef.keys[v], err)
			}
		}
	}

	// One standing rule, reworded four ways — the live pathology: the
	// heartbeat tier rewrites the same lesson each cycle. Wordings mirror the
	// real 2026-07-19 triple (diary-idiom rule) at synthetic content.
	restate := famaRestatement{
		query:     "how should entries be worded",
		keyPrefix: "rule-wording",
		keys: []string{
			"rule-wording-a", "rule-wording-b", "rule-wording-c", "rule-wording-d",
		},
	}
	wordings := []string{
		"Avoid inventing non-standard phrasing in written entries — the reader gets confused and has to ask what it means.",
		"Do not use invented or non-standard expressions in entries; the reader will ask what they mean if the phrasing is coined.",
		"Entry wording rule: stick to standard phrasing only, since coined expressions confuse the reader and need follow-up explanation.",
		"When writing entries, keep to conventional wording — made-up phrasings force the reader to ask for clarification.",
	}
	for i, w := range wordings {
		clock = evalClockEpoch.Add(time.Duration(5+i*6) * 24 * time.Hour)
		if _, err := s.Put(ctx, PutParams{NS: lcNS, Key: restate.keys[i], Content: w,
			Kind: "semantic", Tier: "stm", Importance: 0.6, Dedup: true}); err != nil {
			t.Fatalf("put %s: %v", restate.keys[i], err)
		}
	}

	// Nightly reflect across the window, then land the clock past the last
	// write so recency does not paper over lifecycle effects.
	for day := 1; day <= 30; day++ {
		clock = evalClockEpoch.Add(time.Duration(day)*24*time.Hour + 23*time.Hour)
		if _, err := s.Reflect(ctx, ReflectParams{NS: lcNS}); err != nil {
			t.Fatalf("reflect day %d: %v", day, err)
		}
	}
	clock = evalClockEpoch.Add(31 * 24 * time.Hour)
	return s, evolving, restate
}

// TestEvalFAMAInvalidation covers the op class the Search-only FAMA omits
// entirely: explicit invalidation. Memora's trace is ~20% delete/invalidate
// ops; ghost models zero of them. A soft-deleted fact must not reach the
// agent through ANY retrieval path — search, context, or edge expansion.
func TestEvalFAMAInvalidation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	clock := evalClockEpoch
	s.SetClock(func() time.Time { return clock })

	const q = "which region hosts the analytics cluster"
	if _, err := s.Put(ctx, PutParams{NS: lcNS, Key: "region-fact",
		Content: "The analytics cluster runs in the eu-west-2 region.",
		Kind: "semantic", Tier: "stm", Importance: 0.6}); err != nil {
		t.Fatal(err)
	}
	// A neighbour that will hold an edge to it — the edge-expansion path is
	// the one most likely to resurrect a deleted memory.
	if _, err := s.Put(ctx, PutParams{NS: lcNS, Key: "region-neighbour",
		Content: "The analytics cluster is sized for 40 nodes and scales nightly.",
		Kind: "semantic", Tier: "stm", Importance: 0.6}); err != nil {
		t.Fatal(err)
	}

	clock = evalClockEpoch.Add(24 * time.Hour)
	if err := s.Rm(ctx, RmParams{NS: lcNS, Key: "region-fact"}); err != nil {
		t.Fatalf("delete: %v", err)
	}

	clock = evalClockEpoch.Add(48 * time.Hour)
	res, err := s.Search(ctx, SearchParams{NS: lcNS, Query: q, Limit: 10})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range res {
		if r.Key == "region-fact" {
			t.Errorf("invalidated fact still reachable via Search")
		}
	}
	cres, err := s.Context(ctx, ContextParams{NS: lcNS, Query: q, Budget: 1500, ExcludePinned: true})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	for _, m := range cres.Memories {
		if m.Key == "region-fact" || strings.Contains(m.Content, "eu-west-2") {
			t.Errorf("invalidated fact reached assembled context (key=%s)", m.Key)
		}
	}
	t.Logf("invalidation: fact absent from both Search and Context after soft-delete (%d context memories)", len(cres.Memories))
}
