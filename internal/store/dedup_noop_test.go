package store

import (
	"context"
	"testing"
)

// Companion to TestReflectNoOpTierChangeNotCounted, for the similarity pass:
// dedup archives duplicates to dormant with a contains edge, but the cluster
// still exists next cycle, so applyDedup re-archived the same already-dormant
// members every reflect (deduped=47/171 per cycle in production — churn, not
// work). Already-dormant members must be skipped and not counted.
func TestDedupSkipsAlreadyDormantMembers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for _, k := range []string{"canon", "dupe-1", "dupe-2"} {
		if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: k,
			Content: "the same duplicated note about watering plants", Tier: "stm", Kind: "semantic"}); err != nil {
			t.Fatalf("put %s: %v", k, err)
		}
	}
	// Simulate a prior dedup cycle: dupes already archived to dormant.
	if _, err := s.db.Exec(`UPDATE memories SET tier='dormant' WHERE key IN ('dupe-1','dupe-2')`); err != nil {
		t.Fatalf("archive dupes: %v", err)
	}

	// Run applyDedup directly on the cluster (the surrounding similarity
	// machinery needs embeddings; the counting contract is what we test).
	grp, err := s.List(ctx, ListParams{NS: "agent:t"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(grp) != 3 {
		t.Fatalf("want 3 memories, got %d", len(grp))
	}
	archived, err := s.applyDedup(ctx, grp)
	if err != nil {
		t.Fatalf("applyDedup: %v", err)
	}
	if archived != 0 {
		t.Fatalf("already-dormant duplicates re-archived: %d, want 0 (churn, not work)", archived)
	}
}
