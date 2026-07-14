package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// The promotion-starvation bug (lifecycle review 2026-07-14): every context
// injection increments access_count, so useful memories hit the low-utility
// prune's access>20 threshold within hours (utility is rarely marked → ratio
// ≈ 0) and are demoted to dormant at priority 90 before the promotion rule at
// priority 50 ever sees them. 2,497 of pika's 5,218 dormant memories were such
// casualties; promoted=0 in nearly every production cycle.
//
// Fix under test: the two system lifecycle rules distinguish SPACED access
// (distinct days in the access log — real rehearsal) from MASSED ambient
// access (injected constantly within a day or two):
//   - sys-promote-to-ltm requires >=3 distinct access days
//   - sys-prune-low-utility spares memories with >=3 distinct access days,
//     and grants a 72h grace period so young memories can earn spacing
//   - memories with NO access-log rows keep legacy behavior (pre-log rows,
//     benchmark stores)
func TestEvalSpacedRehearsalBeatsAmbientPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	day0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return day0 })

	for _, key := range []string{"useful-fact", "ambient-noise"} {
		if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: key,
			Content: "content of " + key, Tier: "stm", Kind: "semantic"}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	// Both look identical to the old rules: heavily accessed, utility-starved
	// (the perverse real pattern: one utility mark in thousands of accesses).
	if _, err := s.db.Exec(
		`UPDATE memories SET access_count = 120, utility_count = 1 WHERE key IN ('useful-fact','ambient-noise')`); err != nil {
		t.Fatalf("set counters: %v", err)
	}
	// The difference lives in the access log: useful-fact is retrieved across
	// five distinct days (spaced rehearsal); ambient-noise got all its
	// accesses in one day (massed injection).
	logAccess := func(key string, ts time.Time) {
		t.Helper()
		if _, err := s.db.Exec(`INSERT INTO memory_accesses (memory_id, accessed_at)
			SELECT id, ? FROM memories WHERE key = ? AND deleted_at IS NULL`,
			ts.Format(time.RFC3339), key); err != nil {
			t.Fatalf("log access: %v", err)
		}
	}
	for d := 0; d < 5; d++ {
		logAccess("useful-fact", day0.Add(time.Duration(d)*24*time.Hour))
	}
	for i := 0; i < 5; i++ {
		logAccess("ambient-noise", day0.Add(time.Duration(i)*time.Hour))
	}

	// Day 6: both are >24h old and >72h past the grace period.
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
	if got := tierOf("useful-fact"); got != "ltm" {
		t.Fatalf("spaced-rehearsal memory should be PROMOTED to ltm, got tier %q (pruned before promotion = the starvation bug)", got)
	}
	if got := tierOf("ambient-noise"); got != "dormant" {
		t.Fatalf("massed-ambient memory should still be pruned to dormant, got tier %q", got)
	}
}

// Young memories must get a grace window to earn spacing before the prune
// rule may execute them — the shampoo case: written at 03:31, access>20 by
// evening of day one, would be dormant by day two under the old rules.
func TestEvalPruneGracePeriodForYoungMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	day0 := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return day0 })

	if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "fresh-fact",
		Content: "user switched to Vanicream shampoo", Tier: "stm", Kind: "semantic"}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := s.db.Exec(
		`UPDATE memories SET access_count = 40, utility_count = 1 WHERE key = 'fresh-fact'`); err != nil {
		t.Fatalf("set counters: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO memory_accesses (memory_id, accessed_at)
		SELECT id, ? FROM memories WHERE key = 'fresh-fact'`,
		day0.Add(2*time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatalf("log access: %v", err)
	}

	// 30h old: past the promote age (24h) but inside the 72h prune grace.
	s.SetClock(func() time.Time { return day0.Add(30 * time.Hour) })
	if _, err := s.Reflect(ctx, ReflectParams{NS: "agent:t"}); err != nil {
		t.Fatalf("reflect: %v", err)
	}
	ms, err := s.Get(ctx, GetParams{NS: "agent:t", Key: "fresh-fact"})
	if err != nil || len(ms) == 0 {
		t.Fatalf("get: %v", err)
	}
	if ms[0].Tier == "dormant" {
		t.Fatal(fmt.Sprintf("young memory pruned at %s old — grace period not honored", "30h"))
	}
}
