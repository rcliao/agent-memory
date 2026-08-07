package store

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ── Graph utilisation eval ───────────────────────────────────────
//
// Ghost has nine relation types with distinct weights, force-include for
// `contradicts`, parent substitution for `contains`, and spreading activation
// over the lot. This measures PULL-THROUGH: given a query that retrieves one
// endpoint of an edge, does the other endpoint arrive in the assembled
// context, and is it distinguishable from a passenger that arrived by plain
// similarity?
//
// The control matters. An edge "worked" only if the neighbour arrives when the
// edge exists AND does not arrive when it doesn't. Without the no-edge arm,
// topical similarity alone would score as graph utilisation — the neighbour is
// about the same subject by construction.
//
// FINDINGS, in the order they were established:
//
//  1. EXPANSION WAS OUTGOING-ONLY. getEdgesForExpansion filtered `WHERE from_id
//     = ?`, so only edges pointing AWAY from a seed were traversed. A correction
//     is naturally written `correction --contradicts--> stale`, which is
//     INCOMING at the stale memory — so when the stale fact was the seed, the
//     correction was unreachable. Fixed in #93 by the per-relation policy table
//     in edge_policy.go.
//
//  2. PINNED SEEDS WERE GRAPH-INERT. Pinned memories are appended straight to
//     the result in Phase 1 and never entered scoreMap, which is the seed set
//     for expansion — so the one memory guaranteed to be present every turn
//     could pull in nothing. Fixed in #94.
//
//  3. THIS CORPUS WROTE THREE RELATIONS BACKWARDS, and that masked finding 4.
//     Every edge was authored neighbour->seed regardless of relation. That is
//     correct for `contradicts` and `refines` (the NEW memory refutes or
//     improves the OLD one) and backwards for `depends_on`, `caused_by` and
//     `prevents`, whose subject is the memory the query finds. Those three
//     therefore read as "inert" when they were merely being traversed against
//     their own semantics. Direction is now derived per relation by
//     naturalEdgeDirection below, so a case can only be authored the way an
//     agent would actually write it.
//
//  4. PROPAGATED SCORE CANNOT BEAT A NEAR-DUPLICATE WALL. With directions
//     corrected, every relation traverses — and every non-contradicts neighbour
//     still lands LAST. Measured candidate scores at budget 700: neighbours
//     arrive at 0.10-0.32 against a filler wall at 0.40-0.49, because
//     propagated = seed.score * weight * damping(0.3) is structurally capped at
//     under a third of the seed's own score, and the seed itself sits mid-pack.
//     Raising the 0.3 edge-only cap does nothing (it never binds); adding a
//     score floor does nothing (the cap clamps it back); doing both still loses
//     until the floor exceeds ~0.9x seed. `contradicts` does not clear the wall
//     either — it scores 0.3912 against fillers at 0.4373 and reaches rank 0
//     only because the #97 refutation reserve hoists it out of score order.
//     So the one mechanism that has ever made an edge change an assembly is
//     that hoist, and it applies to exactly one relation.
//
// Findings 1-3 are structural and none was a tuning problem. Finding 4 says the
// remaining work is upstream of scoring: the near-duplicate wall is the thing
// out-competing every typed edge.

// edgeDir is which way a case authors its edge, expressed relative to the seed
// (the memory the query finds).
type edgeDir int

const (
	// dirOut: seed --rel--> neighbour. The seed is the relation's subject.
	dirOut edgeDir = iota
	// dirIn: neighbour --rel--> seed. The neighbour is the relation's subject,
	// which is how corrections and refinements are written — the new memory
	// points back at what it supersedes.
	dirIn
)

func (d edgeDir) String() string {
	if d == dirIn {
		return "in"
	}
	return "out"
}

// naturalEdgeDirection returns the direction an agent would actually author a
// relation in, given that the seed is the memory a query finds. It follows the
// semantics documented on validEdgeRels in edge.go, where an edge reads
// `from --rel--> to`:
//
//	contradicts  A conflicts with B      — the correction (new) points at the stale fact
//	refines      A improves B            — the refinement (later) points at what it improves
//	depends_on   A requires B            — the dependent task points at its prerequisite
//	caused_by    B is the cause of A     — the symptom points at its cause
//	prevents     B prevents A            — the prevented outcome points at the preventer
//	implies      A implies B             — the premise points at its consequence
//	contains     A is a summary over B   — the parent points at its children
//	merged_into  A was merged into B     — audit trail, never traversed
//	relates_to   symmetric               — either end
//
// For the first two the seed is the OLD memory, so the edge points at it
// (dirIn). For the rest the seed is the relation's subject, so the edge points
// away from it (dirOut).
func naturalEdgeDirection(rel string) edgeDir {
	switch rel {
	case "contradicts", "refines":
		return dirIn
	default:
		return dirOut
	}
}

type graphCase struct {
	rel string
	// seed is what the query finds; neighbour is what the edge should pull in.
	seedContent, neighbourContent string
	query                         string
	neighbourMarker               string
	// seedPinned reproduces the production shape that broke rescue: the seed
	// is chronically present via Phase 1 rather than found by search.
	seedPinned bool
	// wantTraversal is false for relations that must NOT be followed.
	wantTraversal bool
	// reverseHandledElsewhere marks a relation whose reverse direction is
	// deliberately served by a DIFFERENT mechanism than the policy table, so
	// the "policy forbids it, therefore it must not happen" probe does not
	// apply. Only `contains` qualifies: child->parent is the parent-boosting
	// path in expandEdges (getContainsParents), kept out of the policy table on
	// purpose so the two cannot collide. Measured: authoring contains backwards
	// still pulls the parent in, via that path and not via expansion.
	reverseHandledElsewhere bool
}

// dir is the direction this case authors its edge in.
func (c graphCase) dir() edgeDir { return naturalEdgeDirection(c.rel) }

func graphCorpus() []graphCase {
	// Every neighbour here is UNREACHABLE from the query by similarity — that
	// is the only condition under which an edge is observable. A neighbour that
	// a plain search would find scores the same in both arms and tells us
	// nothing, which is what happened on the first two versions of this corpus:
	// four of six relations read "arrived anyway" and the eval could not
	// distinguish a working relation from a broken one.
	//
	// Consequence worth stating: these neighbours are not textbook instances of
	// their relation. A refinement of "backups run nightly" would naturally be
	// about timing, and timing is what the query asks for — so it would be
	// found anyway. What is under test is whether the RELATION TRAVERSES, not
	// whether the content is a canonical example of it. The relation label is
	// the variable; the content is chosen to make the label measurable.
	//
	// All content is synthetic office/ops material by design — this corpus ships
	// in a public repo and must never carry real personal or family data.
	return []graphCase{
		{
			rel:              "contradicts",
			seedContent:      "The office badge reader on the third floor accepts the old blue cards.",
			neighbourContent: "Facilities swapped that unit out in July and anything issued before then is refused now.",
			query:            "which badge works on the third floor reader",
			neighbourMarker:  "swapped that unit out",
			wantTraversal:    true,
		},
		{
			rel:              "refines",
			seedContent:      "Backups run nightly.",
			neighbourContent: "Restores have to be requested through the ops desk and typically take an hour to prepare.",
			query:            "when do the backups run",
			neighbourMarker:  "requested through the ops desk",
			wantTraversal:    true,
		},
		{
			rel:              "depends_on",
			seedContent:      "Deploying the reporting service requires the migration step to have completed.",
			// Careful with vocabulary: an earlier version said "migrate-reports
			// task", whose "reports" made this a plain lexical hit on the query
			// and put the neighbour in BOTH arms. The edge then looked inert
			// when it was simply never needed.
			neighbourContent: "Nobody has automated the migration step; someone still triggers it by hand each time.",
			query:            "how do I deploy the reporting service",
			neighbourMarker:  "triggers it by hand",
			wantTraversal:    true,
		},
		{
			rel:              "caused_by",
			seedContent:      "The dashboard was slow all Tuesday afternoon.",
			neighbourContent: "Finance moved their quarterly export to noon and it holds most of the pool while it works.",
			query:            "why was the dashboard slow on Tuesday",
			neighbourMarker:  "moved their quarterly export",
			wantTraversal:    true,
		},
		{
			rel:              "prevents",
			seedContent:      "Never ship a release on a Friday afternoon.",
			neighbourContent: "Handover happens at 17:00 and whoever picks up the pager has no idea what went out that day.",
			query:            "can we release on Friday",
			neighbourMarker:  "picks up the pager",
			wantTraversal:    true,
		},
		{
			rel:              "implies",
			seedContent:      "The build server runs on the ARM fleet.",
			neighbourContent: "Anything produced there needs the cross-toolchain flag or it silently emits x86 artifacts.",
			query:            "where does the build server run",
			neighbourMarker:  "cross-toolchain flag",
			wantTraversal:    true,
		},
		{
			rel:              "contains",
			seedContent:      "The Q3 onboarding review covered tooling, access requests and mentoring.",
			neighbourContent: "Pairing was dropped after the second week because nobody had time to sit with anyone.",
			query:                   "what did the Q3 onboarding review cover",
			neighbourMarker:         "dropped after the second week",
			wantTraversal:           true,
			reverseHandledElsewhere: true,
		},
		{
			rel:              "relates_to",
			seedContent:      "The staging environment resets every night at 02:00.",
			neighbourContent: "Anyone testing overnight should copy their fixtures somewhere durable first.",
			query:            "when does staging reset",
			neighbourMarker:  "copy their fixtures",
			wantTraversal:    true,
		},
		{
			// Negative case: merged_into is an audit trail. Traversing it would
			// resurrect memories that dedup deliberately retired.
			rel:              "merged_into",
			seedContent:      "The printer on the second floor is out of toner.",
			neighbourContent: "Someone already filed a ticket about consumables for that whole corridor last month.",
			query:            "is the second floor printer working",
			neighbourMarker:  "filed a ticket about consumables",
			wantTraversal:    false,
		},
		{
			// The production shape: the seed is pinned, so before #94 it never
			// entered the search result set and could not seed expansion.
			rel:              "contradicts",
			seedContent:      "The kitchen coffee machine takes the tall paper cups from the cupboard.",
			neighbourContent: "That unit was replaced in the spring and the new one has much less clearance underneath.",
			query:            "which cups fit the kitchen coffee machine",
			neighbourMarker:  "much less clearance",
			seedPinned:       true,
			wantTraversal:    true,
		},
	}
}

// buildGraphCase seeds a store for one case. linked controls whether the edge
// exists — the control arm that separates graph pull-through from plain
// topical similarity. dir overrides the case's natural authoring direction,
// which is what the direction-matrix test uses to probe the wrong way round.
func buildGraphCase(t *testing.T, c graphCase, linked bool, dir edgeDir, budget int) *ContextResult {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return base })

	if _, err := s.Put(ctx, PutParams{NS: "agent:graph", Key: "seed", Content: c.seedContent,
		Kind: "semantic", Tier: "ltm", Pinned: c.seedPinned, Importance: 0.7}); err != nil {
		t.Fatal(err)
	}
	// The neighbour is written LATER and deliberately phrased so it is not the
	// obvious lexical match for the query — if it arrives, the edge is why.
	s.SetClock(func() time.Time { return base.AddDate(0, 0, 10) })
	if _, err := s.Put(ctx, PutParams{NS: "agent:graph", Key: "neighbour", Content: c.neighbourContent,
		Kind: "semantic", Tier: "ltm", Importance: 0.5}); err != nil {
		t.Fatal(err)
	}
	// Filler that is TOPICALLY CLOSER to the query than the neighbour is, so the
	// neighbour cannot win a slot on similarity and only an edge can carry it
	// in. Without this the budget binds on unrelated text and the neighbour
	// rides in for free, which is what made the first version unable to measure
	// anything. It also models production: this near-duplicate wall is the same
	// shape a real store grows, and finding 4 above is about exactly this.
	for i := 0; i < 10; i++ {
		_, _ = s.Put(ctx, PutParams{NS: "agent:graph", Key: fmt.Sprintf("filler-%d", i),
			Content: fmt.Sprintf("%s (note %d, no change reported this week).", c.seedContent, i),
			Kind:    "semantic", Tier: "ltm", Importance: 0.45})
	}
	if linked {
		from, to := "seed", "neighbour"
		if dir == dirIn {
			from, to = "neighbour", "seed"
		}
		if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "agent:graph", FromKey: from,
			ToNS: "agent:graph", ToKey: to, Rel: c.rel}); err != nil {
			t.Fatal(err)
		}
	}
	s.SetClock(func() time.Time { return base.AddDate(0, 0, 20) })

	res, err := s.Context(ctx, ContextParams{NS: "agent:graph", Query: c.query, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// evalBudget is the reporting budget, overridable for the manual sweeps that
// produced finding 4.
func evalBudget() int {
	if v := os.Getenv("GRAPH_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return 700
}

// generousBudget fits the whole corpus with room to spare, so the direction
// matrix measures TRAVERSAL alone and never trips over packing. Finding 4 is
// about packing; keeping the two apart is what makes each measurable.
const generousBudget = 4000

func renderKeys(res *ContextResult) string {
	var ks []string
	for _, m := range res.Memories {
		ks = append(ks, m.Key)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func hasMarker(res *ContextResult, marker string) bool {
	return markerRank(res, marker) >= 0
}

// markerRank returns the neighbour's 0-based position in the assembled context,
// or -1 if absent.
//
// Presence alone turned out to be too blunt a metric. For several relations the
// neighbour is a DIRECT search hit however it is worded, so both arms reported
// "present" and the edge could be neither credited nor blamed. Rank still moves
// in those cases: an edge that contributes propagated score should lift the
// neighbour, and one that contributes nothing should not.
func markerRank(res *ContextResult, marker string) int {
	for i, m := range res.Memories {
		if strings.Contains(m.Content, marker) {
			return i
		}
	}
	return -1
}

// traverses reports whether the edge — and only the edge — put the neighbour in
// context, at a budget generous enough that packing cannot be the reason it
// didn't.
func traverses(t *testing.T, c graphCase, dir edgeDir) bool {
	t.Helper()
	with := buildGraphCase(t, c, true, dir, generousBudget)
	without := buildGraphCase(t, c, false, dir, generousBudget)
	return hasMarker(with, c.neighbourMarker) && !hasMarker(without, c.neighbourMarker)
}

// TestGraphExpansionDirectionMatrix is the assertive suite behind the reporting
// eval: for every relation, expansion must follow the directions the policy
// table declares and must not follow the ones it doesn't.
//
// This is what the old corpus could not check. It authored every edge one way
// round, so a relation that traversed correctly in its own direction and a
// relation that was genuinely broken produced the same output.
func TestGraphExpansionDirectionMatrix(t *testing.T) {
	for _, c := range graphCorpus() {
		name := c.rel
		if c.seedPinned {
			name += "/pinned"
		}
		t.Run(name, func(t *testing.T) {
			pol := expansionDirectionsFor(c.rel)
			natural := c.dir()

			got := traverses(t, c, natural)
			if got != c.wantTraversal {
				t.Errorf("%s authored %s (its natural direction): traversal=%v, want %v\n"+
					"policy says Outgoing=%v Incoming=%v",
					c.rel, natural, got, c.wantTraversal, pol.Outgoing, pol.Incoming)
			}

			// Probe the opposite direction. Where the policy forbids it,
			// traversal must not happen — that is the guard keeping incoming
			// expansion from quietly becoming universal and flooding context
			// with reverse-direction passengers.
			opposite := dirOut
			if natural == dirOut {
				opposite = dirIn
			}
			allowed := pol.Outgoing
			if opposite == dirIn {
				allowed = pol.Incoming
			}
			if !allowed && !c.reverseHandledElsewhere {
				if traverses(t, c, opposite) {
					t.Errorf("%s authored %s traversed, but policy forbids that direction "+
						"(Outgoing=%v Incoming=%v)", c.rel, opposite, pol.Outgoing, pol.Incoming)
				}
			}
		})
	}
}

// TestMergedIntoIsNeverTraversed pins the one relation that must stay inert.
// merged_into is an audit trail written when dedup retires a memory; following
// it would resurrect exactly the content a merge decided to remove.
func TestMergedIntoIsNeverTraversed(t *testing.T) {
	for _, c := range graphCorpus() {
		if c.rel != "merged_into" {
			continue
		}
		for _, dir := range []edgeDir{dirOut, dirIn} {
			if traverses(t, c, dir) {
				t.Errorf("merged_into authored %s pulled its neighbour into context; "+
					"a retired memory must not come back through the graph", dir)
			}
		}
		return
	}
	t.Fatal("corpus lost its merged_into case — the negative control is not optional")
}

// TestEvalGraphPullThrough reports, per relation type, whether the edge
// actually changes what reaches the agent at a REALISTIC budget, where packing
// competes with traversal. Reporting instrument plus a floor: relations that
// pull through must keep pulling through, so the measured baseline cannot
// silently rot. The direction matrix above is the strict correctness gate.
func TestEvalGraphPullThrough(t *testing.T) {
	// Locked in at budget 700 with directions corrected: every relation pulls
	// through except merged_into, which must not. Before the direction fix this
	// read 3/6, and the three that changed were exactly the three the corpus had
	// authored backwards.
	//
	// Pull-through is not the same as usefulness: note that every relation
	// except `contradicts` arrives at rank 11 — dead last, first to be dropped
	// the moment the budget tightens. That is finding 4, and it is now visible
	// across all eight relations rather than inferred from two.
	const wantPullThroughAtDefaultBudget = 9

	budget := evalBudget()
	var effective int

	for _, c := range graphCorpus() {
		with := buildGraphCase(t, c, true, c.dir(), budget)
		without := buildGraphCase(t, c, false, c.dir(), budget)
		hasWith, hasWithout := hasMarker(with, c.neighbourMarker), hasMarker(without, c.neighbourMarker)
		rankWith, rankWithout := markerRank(with, c.neighbourMarker), markerRank(without, c.neighbourMarker)

		verdict := "NO EFFECT"
		switch {
		case hasWith && !hasWithout:
			verdict = "PULLED THROUGH (edge is why)"
			effective++
		case !hasWith && hasWithout:
			verdict = "REGRESSION (edge pushed it out)"
		case hasWith && hasWithout:
			switch {
			case rankWith < rankWithout:
				verdict = fmt.Sprintf("RANK LIFTED %d -> %d (edge helped)", rankWithout, rankWith)
			case rankWith > rankWithout:
				verdict = fmt.Sprintf("RANK DROPPED %d -> %d", rankWithout, rankWith)
			default:
				verdict = fmt.Sprintf("present in both at rank %d, edge changed nothing", rankWith)
			}
		}
		t.Logf("%-12s dir=%-3s pinned=%-5v want=%-5v present(with/without)=%v/%v rank=%d/%d -> %s",
			c.rel, c.dir(), c.seedPinned, c.wantTraversal, hasWith, hasWithout, rankWith, rankWithout, verdict)
		t.Logf("      keys(with)=%s", renderKeys(with))
	}

	t.Logf("\nSUMMARY: %d/%d relations made an otherwise-unreachable neighbour reachable at budget %d",
		effective, len(graphCorpus()), budget)

	if budget != 700 {
		return // sweep mode: report only
	}
	if effective < wantPullThroughAtDefaultBudget {
		t.Errorf("graph pull-through regressed: %d relations pulled through, baseline is %d",
			effective, wantPullThroughAtDefaultBudget)
	}
}
