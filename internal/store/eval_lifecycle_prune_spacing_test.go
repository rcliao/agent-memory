package store

import (
	"context"
	"testing"
	"time"
)

// Companion to TestEvalSpacedRehearsalBeatsAmbientPrune. That test fixed the
// promotion side (>=3 distinct access days = real rehearsal) and used the same
// bar to spare memories from sys-prune-low-utility. This one covers the gap
// between the two bars: a memory retrieved across TWO days is not rehearsed
// enough to promote, but it is not massed ambient noise either, and pruning it
// is far more destructive than declining to promote it — dormant memories are
// excluded from search and context entirely (search.go ExcludeTiers,
// context.go dormant skip).
//
// Observed on a live store 2026-07-28: ort-cli (284 accesses, utility 1, 2
// distinct access days) was pruned to dormant and became unreachable — an
// exact phrase from its own content returned nothing until it was promoted
// back. 1,865 memories with 100+ accesses had collected in dormant the same
// way, and none of them had >=3 access days.
func TestEvalPruneSparesTwoDaySpacing(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	day0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return day0 })

	for _, key := range []string{"two-day-memory", "single-day-ambient"} {
		if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: key,
			Content: "content of " + key, Tier: "stm", Kind: "semantic"}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	// Identical counters — heavily accessed, one lone utility mark. The raw
	// ratio cannot tell them apart; only the access-log spacing can.
	if _, err := s.db.Exec(
		`UPDATE memories SET access_count = 284, utility_count = 1
		 WHERE key IN ('two-day-memory','single-day-ambient')`); err != nil {
		t.Fatalf("set counters: %v", err)
	}
	logAccess := func(key string, ts time.Time) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO memory_accesses (memory_id, accessed_at)
			SELECT id, ? FROM memories WHERE key = ? AND deleted_at IS NULL`,
			ts.Format(time.RFC3339), key); err != nil {
			t.Fatalf("log access: %v", err)
		}
	}
	// Two distinct days: returned to on a later day, like ort-cli.
	logAccess("two-day-memory", day0)
	logAccess("two-day-memory", day0.Add(24*time.Hour))
	// One day, many times: massed ambient injection.
	for i := 0; i < 5; i++ {
		logAccess("single-day-ambient", day0.Add(time.Duration(i)*time.Hour))
	}

	// Past the 72h grace period so the prune rule is live for both.
	s.SetClock(func() time.Time { return day0.Add(6 * 24 * time.Hour) })
	if _, err := s.Reflect(ctx, ReflectParams{NS: "agent:t"}); err != nil {
		t.Fatalf("reflect: %v", err)
	}

	tierOf := func(key string) string {
		t.Helper()
		ms, err := s.Get(ctx, GetParams{NS: "agent:t", Key: key})
		if err != nil || len(ms) == 0 {
			t.Fatalf("get %s: %v", key, err)
		}
		return ms[0].Tier
	}
	if got := tierOf("two-day-memory"); got == "dormant" {
		t.Fatalf("memory returned to across two distinct days was pruned to dormant, "+
			"which removes it from search and context entirely; got tier %q", got)
	}
	if got := tierOf("single-day-ambient"); got != "dormant" {
		t.Fatalf("massed single-day memory should still be pruned to dormant, got tier %q", got)
	}
}
