package store

import (
	"testing"
	"time"

	"github.com/rcliao/ghost/internal/model"
)

func TestACTRActivationOrdering(t *testing.T) {
	now := time.Now()
	mem := func(ageHours float64, accesses int, lastAccessHours float64) model.Memory {
		m := model.Memory{
			CreatedAt:   now.Add(-time.Duration(ageHours * float64(time.Hour))),
			AccessCount: accesses,
		}
		if lastAccessHours >= 0 {
			la := now.Add(-time.Duration(lastAccessHours * float64(time.Hour)))
			m.LastAccessedAt = &la
		}
		return m
	}

	fresh := actrActivation(mem(1, 0, -1), now)
	oldUnused := actrActivation(mem(24*30, 0, -1), now)
	if fresh <= oldUnused {
		t.Errorf("fresh unaccessed (%.3f) should out-activate month-old unaccessed (%.3f)", fresh, oldUnused)
	}

	oldHot := actrActivation(mem(24*30, 50, 2), now)
	if oldHot <= oldUnused {
		t.Errorf("frequently-accessed old memory (%.3f) should out-activate unused old memory (%.3f)", oldHot, oldUnused)
	}

	recentAccess := actrActivation(mem(24*30, 5, 1), now)
	staleAccess := actrActivation(mem(24*30, 5, 24*20), now)
	if recentAccess <= staleAccess {
		t.Errorf("recently-touched (%.3f) should out-activate long-untouched (%.3f) at equal frequency", recentAccess, staleAccess)
	}

	// More practice at equal recency wins (frequency effect).
	practiced := actrActivation(mem(24*30, 40, 5), now)
	rare := actrActivation(mem(24*30, 2, 5), now)
	if practiced <= rare {
		t.Errorf("practiced (%.3f) should out-activate rare (%.3f)", practiced, rare)
	}
}

func TestACTRActivationBounds(t *testing.T) {
	now := time.Now()
	cases := []model.Memory{
		{CreatedAt: now},                                       // zero-age
		{CreatedAt: now.Add(-100000 * time.Hour)},              // very old, never accessed
		{CreatedAt: now.Add(-time.Hour), AccessCount: 1000000}, // pathological hot
		{},                                                     // zero-value memory
	}
	for i, m := range cases {
		p := actrActivation(m, now)
		if p < 0 || p > 1 || p != p {
			t.Errorf("case %d: activation out of bounds or NaN: %v", i, p)
		}
	}
}
