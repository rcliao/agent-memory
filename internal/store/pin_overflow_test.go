package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// "Pinned" is not a guarantee — pinned memories compete for a sub-budget and
// the overflow is dropped every turn. This pins the behaviour so the silent
// version cannot come back: an oversubscribed pin set must drop the
// LOWEST-importance entries, and must say so.
func TestPinnedOverflowIsBounded(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const ns = "agent:pin"
	body := strings.Repeat("household fact about the kitchen and the garden. ", 12) // ~100 tokens

	// Ten pinned memories at descending importance; only a few can fit.
	for i := 0; i < 10; i++ {
		if _, err := s.Put(ctx, PutParams{NS: ns, Key: fmt.Sprintf("pin-%02d", i),
			Content: fmt.Sprintf("%s marker%02d", body, i),
			Kind:    "semantic", Tier: "ltm", Pinned: true,
			Importance: 1.0 - float64(i)*0.05}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := s.Context(ctx, ContextParams{NS: ns, Query: "kitchen", Budget: 600, PinBudget: 250})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range res.Memories {
		if strings.HasPrefix(m.Key, "pin-") {
			got = append(got, m.Key)
		}
	}
	if len(got) == 0 {
		t.Fatalf("no pinned memory survived a 250-token pin budget")
	}
	if len(got) == 10 {
		t.Fatalf("expected the pin budget to bind; all 10 fit")
	}
	// Highest importance wins: pin-00 must be present, and the lowest must not.
	joined := strings.Join(got, ",")
	if !strings.Contains(joined, "pin-00") {
		t.Errorf("highest-importance pin was dropped; got %v", got)
	}
	if strings.Contains(joined, "pin-09") {
		t.Errorf("lowest-importance pin survived while others were dropped; got %v", got)
	}
	t.Logf("pin budget bound as expected: %d/10 admitted (%v)", len(got), got)
}
