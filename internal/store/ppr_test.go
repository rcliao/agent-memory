package store

import (
	"context"
	"testing"
)

// TestPersonalizedPageRankMultiHop: on a line graph A—B—C seeded at A, mass must
// reach the 2-hop node C (single-hop can't), and the closer node B outranks C.
func TestPersonalizedPageRankMultiHop(t *testing.T) {
	adj := map[string][]adjEdge{
		"A": {{to: "B", w: 1}},
		"B": {{to: "A", w: 1}, {to: "C", w: 1}},
		"C": {{to: "B", w: 1}},
	}
	mass := personalizedPageRank(map[string]float64{"A": 1}, adj, 0.5, 30)
	if mass["C"] <= 0 {
		t.Fatalf("PPR should reach 2-hop node C, got %v", mass)
	}
	if mass["B"] <= mass["C"] {
		t.Errorf("closer node B should outrank C: B=%.4f C=%.4f", mass["B"], mass["C"])
	}
}

// buildChain seeds A→B→C as a 2-hop chain where only A matches the query; the
// answer is C. Returns the store.
func buildChain(t *testing.T) *SQLiteStore {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	put := func(k, c string) {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: k, Content: c, Kind: "semantic", Tier: "ltm", Importance: 0.6, SkipAutoLink: true}); err != nil {
			t.Fatal(err)
		}
	}
	put("a", "Project Phoenix runs on the main cluster.")       // query matches here
	put("b", "The main cluster is owned by the platform crew.") // bridge (1 hop)
	put("c", "The platform crew carries pager 5150.")           // answer (2 hops, no query overlap)
	edge := func(from, to string) {
		if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "t", FromKey: from, ToNS: "t", ToKey: to, Rel: "relates_to", Weight: 0.6}); err != nil {
			t.Fatal(err)
		}
	}
	edge("a", "b")
	edge("b", "c")
	return s
}

// TestPPRReachesTwoHop: with GHOST_PPR on, the 2-hop answer surfaces.
func TestPPRReachesTwoHop(t *testing.T) {
	t.Setenv("GHOST_PPR", "1")
	s := buildChain(t)
	res, err := s.Context(context.Background(), ContextParams{NS: "t", Query: "who is on call for Project Phoenix", Budget: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if !contextHasKey(res, "c") {
		t.Fatalf("PPR should surface the 2-hop answer 'c'; keys=%v", contextKeys(res))
	}
}

// TestPPRDefaultOffSingleHop: default (no GHOST_PPR) is single-hop and does NOT
// reach the 2-hop node — confirms PPR is opt-in and the default path is unchanged.
func TestPPRDefaultOffSingleHop(t *testing.T) {
	s := buildChain(t)
	res, err := s.Context(context.Background(), ContextParams{NS: "t", Query: "who is on call for Project Phoenix", Budget: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if contextHasKey(res, "c") {
		t.Errorf("single-hop default should NOT reach the 2-hop node 'c'; keys=%v", contextKeys(res))
	}
}

// TestPPRPreservesContradiction: with PPR on, a contradicting memory is still
// force-included even when it isn't a strong PPR-mass node.
func TestPPRPreservesContradiction(t *testing.T) {
	t.Setenv("GHOST_PPR", "1")
	s := newTestStore(t)
	ctx := context.Background()
	put := func(k, c string) {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: k, Content: c, Kind: "semantic", Tier: "ltm", Importance: 0.6, SkipAutoLink: true}); err != nil {
			t.Fatal(err)
		}
	}
	put("new", "The API now uses OAuth2 for authentication.")
	put("old", "The API uses static keys for authentication.")
	if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "t", FromKey: "new", ToNS: "t", ToKey: "old", Rel: "contradicts"}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Context(ctx, ContextParams{NS: "t", Query: "how does the API do authentication", Budget: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if !contextHasKey(res, "new") || !contextHasKey(res, "old") {
		t.Errorf("contradiction should force-include both under PPR; keys=%v", contextKeys(res))
	}
}
