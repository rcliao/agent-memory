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
