package store

import (
	"fmt"
	"testing"
)

// ── Should expansion go beyond one hop? ──────────────────────────────
//
// Spreading activation has always stopped at one hop: seeds are snapshotted
// once, their neighbours are added as candidates, and those candidates never
// expand in turn. TestStressChainDepthIsOne pinned that limit; this measures
// whether lifting it is worth doing.
//
// The case FOR going deeper is that a chain is the whole point of having a
// graph. "Book the dentist" depends_on "the card lapsed" depends_on "the
// replacement form is unsigned" — the third memory is the only ACTIONABLE one,
// and at one hop the agent is told the card lapsed with no idea what to do
// about it. A depth limit of one means ghost stores chains it cannot follow.
//
// The case AGAINST is fan-out. `relates_to` is 76,738 of ~80,000 live edges,
// auto-linked in bulk, with measured degree up to 39 in a single near-duplicate
// component. Two hops over that is a second-order neighbourhood in the
// thousands, and every one of those is a candidate competing for the budget.
//
// So the question is not "one hop or two" but "which relations earn a second
// hop". This eval measures both the benefit and the cost, separately, because
// a single aggregate number would let the chain win pay for the fan-out damage.

// chainCase is a two-link chain: query finds seed, seed -> hop1 -> hop2.
type chainCase struct {
	name     string
	rel      string
	query    string
	mems     []householdMemory
	hop1Mark string
	hop2Mark string
	// wantHop2 is whether the far end SHOULD be reached once multi-hop is on.
	wantHop2 bool
}

func chainCorpus() []chainCase {
	// Filler shares the query's vocabulary so the budget binds on plausible
	// content and the chain has to earn its slots. Synthetic throughout.
	dentistFiller := func() []householdMemory {
		var out []householdMemory
		for i := 0; i < 8; i++ {
			out = append(out, householdMemory{key: fmt.Sprintf("appt-%d", i),
				content: fmt.Sprintf("Appointment note %d: still need to book the twice-yearly dentist visit for the kids.", i)})
		}
		return out
	}

	return []chainCase{
		{
			// The motivating case. Only the far end tells the agent what to DO.
			name:  "prerequisite-chain",
			rel:   "depends_on",
			query: "book the dentist appointment",
			mems: append([]householdMemory{
				{key: "dentist", content: "Need to book the twice-yearly dentist appointment for the kids."},
				{key: "insurance", content: "The dental card on file lapsed at the end of last quarter and has not been replaced."},
				{key: "signature", content: "That replacement paperwork sits unsigned in the folder by the front door."},
			}, dentistFiller()...),
			hop1Mark: "lapsed at the end of last quarter",
			hop2Mark: "sits unsigned in the folder",
			wantHop2: true,
		},
		{
			// The fan-out risk, in miniature. Generic similarity should NOT
			// earn a second hop: two topically-adjacent steps is a different
			// subject, and at production degree this is where thousands of
			// candidates would come from.
			name:  "similarity-chain",
			rel:   "relates_to",
			query: "when does the recycling go out",
			mems: append([]householdMemory{
				{key: "recycling", content: "Recycling goes out on alternate Wednesdays, the blue bin only."},
				{key: "bins", content: "The bins live down the side passage behind the gate."},
				{key: "gate", content: "The side gate latch sticks in wet weather and needs lifting to close properly."},
			}, func() []householdMemory {
				var out []householdMemory
				for i := 0; i < 8; i++ {
					out = append(out, householdMemory{key: fmt.Sprintf("bin-note-%d", i),
						content: fmt.Sprintf("Bin note %d: recycling goes out on alternate Wednesdays as usual, blue bin.", i)})
				}
				return out
			}()...),
			hop1Mark: "down the side passage",
			hop2Mark: "latch sticks in wet weather",
			wantHop2: false,
		},
	}
}

func buildHopChain(t *testing.T, c chainCase, budget int) *ContextResult {
	t.Helper()
	keys := []string{c.mems[0].key, c.mems[1].key, c.mems[2].key}
	s := stressStore(t, c.mems, [][3]string{
		{keys[0], keys[1], c.rel},
		{keys[1], keys[2], c.rel},
	})
	return stressContext(t, s, c.query, budget)
}

// TestEvalMultiHopReach reports, per chain, how far expansion travels. It is a
// reporting instrument plus a gate on the two properties that must hold:
// a reserve-class chain reaches its far end, and a similarity chain does not.
func TestEvalMultiHopReach(t *testing.T) {
	const budget = 500
	for _, c := range chainCorpus() {
		t.Run(c.name, func(t *testing.T) {
			res := buildHopChain(t, c, budget)
			h1, h2 := hasMarker(res, c.hop1Mark), hasMarker(res, c.hop2Mark)
			t.Logf("%-18s rel=%-10s hop1=%-5v hop2=%-5v (want hop2=%v) context=%d memories",
				c.name, c.rel, h1, h2, c.wantHop2, len(res.Memories))

			if !h1 {
				t.Errorf("%s: the FIRST hop did not arrive — this case cannot say "+
					"anything about depth", c.name)
			}
			if h2 != c.wantHop2 {
				t.Errorf("%s: two hops away reached=%v, want %v", c.name, h2, c.wantHop2)
			}
		})
	}
}

// TestEvalMultiHopCost measures what a second hop costs when it is allowed:
// how many extra candidates enter, and whether ordinary search hits are
// displaced. Benefit and cost are reported separately on purpose — a single
// score would let the chain win pay for the fan-out damage.
func TestEvalMultiHopCost(t *testing.T) {
	const budget = 500
	for _, c := range chainCorpus() {
		res := buildHopChain(t, c, budget)
		fillers := 0
		for _, m := range res.Memories {
			if len(m.Key) >= 5 && (m.Key[:5] == "appt-" || m.Key[:4] == "bin-") {
				fillers++
			}
		}
		t.Logf("%-18s context=%2d memories, %2d of them ordinary search hits",
			c.name, len(res.Memories), fillers)
	}
}
