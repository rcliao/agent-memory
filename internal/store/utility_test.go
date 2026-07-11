package store

import (
	"context"
	"testing"
	"time"

	"github.com/rcliao/ghost/internal/model"
)

// utilMem builds a minimal semantic ltm memory with the given access/utility
// counts. Callers pass a shared created time so recency is identical across
// instances (otherwise sub-tick differences make "equal scores" flaky).
func utilMem(created time.Time, access, utility int) model.Memory {
	return model.Memory{
		Kind: "semantic", Tier: "ltm", Importance: 0.5,
		CreatedAt: created, AccessCount: access, UtilityCount: utility,
	}
}

// TestUtilityWeightOffByDefault confirms utility does not affect scoring unless
// GHOST_UTILITY_WEIGHT is set — two memories identical except utility_count
// score the same.
func TestUtilityWeightOffByDefault(t *testing.T) {
	now := time.Now().UTC()
	hi := utilMem(now, 25, 20)
	lo := utilMem(now, 25, 0)
	shi := computeContextScore(hi, 0.5, now, nil)
	slo := computeContextScore(lo, 0.5, now, nil)
	if shi != slo {
		t.Errorf("with knob off, utility must not change score: hi=%.4f lo=%.4f", shi, slo)
	}
}

// TestUtilityWeightOn confirms that with the knob set, a memory that proved
// useful outranks an equally-relevant one that did not.
func TestUtilityWeightOn(t *testing.T) {
	t.Setenv("GHOST_UTILITY_WEIGHT", "0.5")
	now := time.Now().UTC()
	hi := utilMem(now, 25, 20)
	lo := utilMem(now, 25, 0)
	if computeContextScore(hi, 0.5, now, nil) <= computeContextScore(lo, 0.5, now, nil) {
		t.Error("with knob on, higher-utility memory should score above lower-utility")
	}
}

// TestEvalPersonalUtilityKnob runs the utility-recall scenario with the opt-in
// utility weight on and asserts the proven-useful memory now ranks above the
// equally-relevant but never-useful one.
func TestEvalPersonalUtilityKnob(t *testing.T) {
	t.Setenv("GHOST_UTILITY_WEIGHT", "0.6")
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := SeedStore(ctx, s, PersonalSeedCorpus()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := s.Context(ctx, ContextParams{NS: PersonalNS, Query: "how do deploys use the staging pipeline", Budget: 1000})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	keys := contextKeys(res)
	pHi, pLo := rankOf(keys, "util-high"), rankOf(keys, "util-low")
	if pHi < 0 {
		t.Fatalf("util-high not retrieved; keys=%v", keys)
	}
	if !(pLo < 0 || pHi < pLo) {
		t.Errorf("with utility weight on, util-high (rank %d) should outrank util-low (rank %d); keys=%v", pHi, pLo, keys)
	}
}

// TestGroundedUtilityCreditsOnlyDirectHits verifies utility_count is credited to
// the memory that DIRECTLY matched the query, not to a memory pulled in only via
// edge expansion — keeping utility a grounded "answered a query" signal.
func TestGroundedUtilityCreditsOnlyDirectHits(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	mustPut := func(key, content string) {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: key, Content: content, Kind: "semantic", Importance: 0.6, SkipAutoLink: true}); err != nil {
			t.Fatal(err)
		}
	}
	mustPut("db", "Postgres is our primary database for storage.")
	mustPut("moon", "The moon is made of green cheese apparently.")
	// Link them so 'moon' can be pulled in via edge expansion from 'db'.
	if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "t", FromKey: "db", ToNS: "t", ToKey: "moon", Rel: "relates_to"}); err != nil {
		t.Fatal(err)
	}

	res, err := s.Context(ctx, ContextParams{NS: "t", Query: "what database do we use", Budget: 4000})
	if err != nil {
		t.Fatal(err)
	}
	if !contextHasKey(res, "db") {
		t.Fatalf("db (direct hit) should be returned; keys=%v", contextKeys(res))
	}
	if !contextHasKey(res, "moon") {
		t.Skip("moon not pulled via edge expansion in this config; nothing to distinguish")
	}

	util := func(key string) int {
		var u int
		s.db.QueryRow(`SELECT utility_count FROM memories WHERE ns='t' AND key=? AND deleted_at IS NULL ORDER BY version DESC LIMIT 1`, key).Scan(&u)
		return u
	}
	if util("db") != 1 {
		t.Errorf("direct-hit 'db' should earn utility_count 1, got %d", util("db"))
	}
	if util("moon") != 0 {
		t.Errorf("edge-passenger 'moon' must NOT earn utility credit, got %d", util("moon"))
	}
}
