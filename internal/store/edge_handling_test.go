package store

import (
	"testing"
)

// ── How should each relation be HANDLED, not just traversed? ─────────
//
// #93 and #98 settled direction: every relation now traverses the way an agent
// would author it, 9/10 on the pull-through corpus. Handling is the open half,
// and measurement says ghost currently has two classes rather than nine:
//
//	budget 250 / 350   only `contradicts` survives          2/10
//	budget 500         the rest arrive, all at rank 11      last, first dropped
//
// So every relation except `contradicts` is best-effort, and under the budget
// pressure a real agent turn actually runs at, best-effort means absent.
//
// The mechanism matters as much as the taxonomy. Three separate attempts to lift
// a neighbour by SCORE all failed (recorded as finding 4 in eval_graph_test.go):
// raising the edge-only cap does nothing because it never binds, adding a score
// floor does nothing because the cap clamps it back, and doing both still loses
// until the floor exceeds ~0.9x the seed. `contradicts` does not win on score
// either — it wins because #97 hoists it out of score order into reserved
// budget. Reservation is the only lever that has ever worked, so handling
// classes are expressed as reservation tiers rather than score adjustments.
//
// The classes, defined by WHAT BREAKS when the neighbour is missing:
//
//	handleReserve    — the agent asserts something false or unsafe.
//	                   contradicts: states a corrected fact as current.
//	                   prevents:    recommends the thing that is ruled out. This
//	                                is the allergy shape, and ghost has already
//	                                lost a correct allergy fact from view once.
//	                   depends_on:  recommends an action that will fail for want
//	                                of its prerequisite.
//	handleAccompany  — the answer is incomplete but not wrong: caused_by,
//	                   implies, refines. Nice to have, not worth taking budget
//	                   from a direct hit.
//	handleSubstitute — contains, which already has its own packing path.
//	handleBackground — relates_to, the auto-linked bulk relation.
//	handleNever      — merged_into, an audit trail.

// forceClass is what this test asserts must survive budget pressure.
var forceClass = map[string]bool{
	"contradicts": true,
	"prevents":    true,
	"depends_on":  true,
}

// tightBudget is small enough that the near-duplicate filler wall consumes it,
// which is exactly the regime where a missing prerequisite or a missing
// contradiction changes what the agent tells someone.
const tightBudget = 250

// TestReserveClassSurvivesTightBudget is the gate for handling. A relation in
// the reserve class must reach context even when the budget is fully contested;
// one outside it may legitimately lose.
func TestReserveClassSurvivesTightBudget(t *testing.T) {
	for _, c := range graphCorpus() {
		if !forceClass[c.rel] || !c.wantTraversal {
			continue
		}
		name := c.rel
		if c.seedPinned {
			name += "/pinned"
		}
		t.Run(name, func(t *testing.T) {
			with := buildGraphCase(t, c, true, c.dir(), tightBudget)
			if !hasMarker(with, c.neighbourMarker) {
				t.Errorf("%s is in the reserve class but its neighbour did not reach "+
					"context at budget %d — reservation is the only mechanism that "+
					"survives a contested budget, and this relation is not using it",
					c.rel, tightBudget)
			}
			// The control keeps this honest: without the edge the neighbour must
			// be absent, or the relation is not what put it there.
			without := buildGraphCase(t, c, false, c.dir(), tightBudget)
			if hasMarker(without, c.neighbourMarker) {
				t.Errorf("%s: neighbour arrived without the edge at budget %d — "+
					"this case proves nothing", c.rel, tightBudget)
			}
		})
	}
}

// TestNonReserveClassIsNotPromoted guards the other side. Reservation spends
// budget that a direct search hit would otherwise get, so it must stay scarce:
// relations outside the reserve class must NOT start claiming reserved budget.
// Without this, "make the graph work better" quietly becomes "reserve
// everything", and the reserve stops meaning anything.
func TestNonReserveClassIsNotPromoted(t *testing.T) {
	for _, c := range graphCorpus() {
		if forceClass[c.rel] || !c.wantTraversal {
			continue
		}
		with := buildGraphCase(t, c, true, c.dir(), tightBudget)
		if hasMarker(with, c.neighbourMarker) {
			t.Errorf("%s is not in the reserve class but reached context at budget %d; "+
				"if this is deliberate, move it into forceClass and say why",
				c.rel, tightBudget)
		}
	}
}
