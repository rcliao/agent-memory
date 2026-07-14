package store

import (
	"context"
	"testing"
)

// Found by the first lifecycle review (2026-07-14): the tier-less
// sys-prune-low-utility rule matched ~2.7k already-dormant memories every
// cycle, re-"demoting" them to dormant — thousands of no-op writes per
// reflect and a demoted counter that measured churn, not transitions. A
// tier-change to the memory's current tier must not fire.
func TestReflectNoOpTierChangeNotCounted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "stale-dormant",
		Content: "an old low-utility memory", Tier: "dormant", Kind: "semantic"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	// Make it a low-utility candidate: accessed 25 times, never useful.
	if _, err := s.db.Exec(
		`UPDATE memories SET access_count = 25, utility_count = 1 WHERE key = 'stale-dormant'`); err != nil {
		t.Fatalf("set counters: %v", err)
	}

	res, err := s.Reflect(ctx, ReflectParams{NS: "agent:t"})
	if err != nil {
		t.Fatalf("reflect: %v", err)
	}
	if res.Demoted != 0 {
		t.Fatalf("already-dormant memory re-demoted: Demoted = %d, want 0 (no-op tier changes must not fire)", res.Demoted)
	}

	// DryRun must agree with the real pass.
	res, err = s.Reflect(ctx, ReflectParams{NS: "agent:t", DryRun: true})
	if err != nil {
		t.Fatalf("dry-run reflect: %v", err)
	}
	if res.Demoted != 0 {
		t.Fatalf("dry-run counts no-op demotion: Demoted = %d, want 0", res.Demoted)
	}
}
