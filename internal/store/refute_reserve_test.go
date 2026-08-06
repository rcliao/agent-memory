package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// A refutation must survive PACKING, not merely become a candidate.
//
// `contradicts` already bypassed the propagated-score cap, which made a
// refutation score well — and it still lost its token slot to the same
// near-duplicate siblings that buried it. Measured before this reservation:
// pull-through worked at a 700-token budget and vanished at 400 and below,
// which is precisely where a correction matters most.
func TestRefutationSurvivesTightBudget(t *testing.T) {
	ctx := context.Background()
	build := func(link bool, budget int) *ContextResult {
		s := newTestStore(t)
		if _, err := s.Put(ctx, PutParams{NS: "agent:ref", Key: "stale",
			Content: "The side door code is 4417 and has not changed since the spring.",
			Kind:    "semantic", Tier: "ltm", Importance: 0.7}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Put(ctx, PutParams{NS: "agent:ref", Key: "correction",
			Content: "Building services reissued every entry credential in July after the audit.",
			Kind:    "semantic", Tier: "ltm", Importance: 0.5}); err != nil {
			t.Fatal(err)
		}
		// Siblings closer to the query than the correction is: without them the
		// budget never binds and the test proves nothing.
		for i := 0; i < 10; i++ {
			_, _ = s.Put(ctx, PutParams{NS: "agent:ref", Key: fmt.Sprintf("door-note-%d", i),
				Content: fmt.Sprintf("Note %d: the side door code was used again today, nothing unusual to report.", i),
				Kind:    "semantic", Tier: "ltm", Importance: 0.6})
		}
		if link {
			if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "agent:ref", FromKey: "correction",
				ToNS: "agent:ref", ToKey: "stale", Rel: "contradicts"}); err != nil {
				t.Fatal(err)
			}
		}
		res, err := s.Context(ctx, ContextParams{NS: "agent:ref", Query: "what is the side door code", Budget: budget})
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

	// Tight budgets are the point: this is the regime where it used to fail.
	for _, budget := range []int{250, 400, 700} {
		if !has(build(true, budget), "correction") {
			t.Errorf("budget=%d: refutation did not survive packing", budget)
		}
	}
	// Control: without the edge the correction must NOT arrive at the tight
	// budget, or the test is measuring similarity rather than the reservation.
	if has(build(false, 250), "correction") {
		t.Error("control is broken: the correction arrives without an edge, so this proves nothing")
	}
}

// The reservation must not become its own denial of service: many refutations
// cannot crowd out everything else.
func TestRefutationReserveIsBounded(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.Put(ctx, PutParams{NS: "agent:ref2", Key: "seed",
		Content: "The quarterly inventory count happens in the main warehouse.",
		Kind:    "semantic", Tier: "ltm", Importance: 0.8}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		key := fmt.Sprintf("refuter-%d", i)
		if _, err := s.Put(ctx, PutParams{NS: "agent:ref2", Key: key,
			Content: fmt.Sprintf("Correction %d: an entirely separate assertion about scheduling and staffing arrangements.", i),
			Kind:    "semantic", Tier: "ltm", Importance: 0.5}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "agent:ref2", FromKey: key,
			ToNS: "agent:ref2", ToKey: "seed", Rel: "contradicts"}); err != nil {
			t.Fatal(err)
		}
	}
	res, err := s.Context(ctx, ContextParams{NS: "agent:ref2", Query: "where is the quarterly inventory count", Budget: 800})
	if err != nil {
		t.Fatal(err)
	}
	var refuters, tokens int
	for _, m := range res.Memories {
		if strings.HasPrefix(m.Key, "refuter-") {
			refuters++
			tokens += (len(m.Content) / 4) + 20
		}
	}
	if tokens > 800/2 {
		t.Errorf("refuters claimed %d of an 800-token budget (%d memories) — reservation is unbounded", tokens, refuters)
	}
	t.Logf("12 refuters present, %d admitted holding %d tokens of 800", refuters, tokens)
}
