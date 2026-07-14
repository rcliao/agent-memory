package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rcliao/ghost/internal/capture"
)

// nurtureConditions ablate pieces of ghost's machinery so the ablation eval
// can measure what each contributes to memory evolution.
type nurtureConditions struct {
	Reflect bool // nightly reflect (lifecycle: decay/promote/prune/archive)
	Distill bool // LLM-free capture distillation of salient turns into stm
}

// nurtureReport is what a childhood produces: the numbers that say whether
// the memory grew up well.
type nurtureReport struct {
	ProbeHits, ProbeTotal int
	ProbeMisses           []string // "day 10 q=... missing=..."
	Promoted, Deleted     int
	LiveTotal             int
	LiveSensory           int
	NoiseLive             int // live non-exchange memories about noise things
	NoiseInLTM            int
	SalientTier           map[string]string // marker substring -> tier ("" = gone)
	IdentityIntact        bool
	WeeklyLive            []int // live population at each 7-day boundary
}

func isNurtureNoise(content string) bool {
	lc := strings.ToLower(content)
	for _, w := range nurtureNoiseThings {
		if strings.Contains(lc, w) {
			return true
		}
	}
	return false
}

// growNurture raises the kit's agent under the given conditions: onboarding,
// then day-by-day exchanges (raw sensory write + optional capture
// distillation, mirroring shell's LogExchange), deterministic ambient noise,
// probes (context assembly → access logging = rehearsal), and an optional
// nightly reflect. Probe misses are recorded, not asserted — ablated runs are
// EXPECTED to fail probes; that's the measurement.
func growNurture(t *testing.T, s *SQLiteStore, kit NurtureKit, cond nurtureConditions) nurtureReport {
	t.Helper()
	ctx := context.Background()
	rep := nurtureReport{SalientTier: map[string]string{}}
	base := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	clockAt := func(ts time.Time) { s.SetClock(func() time.Time { return ts }) }

	clockAt(base)
	for _, p := range kit.Onboarding {
		p.NS = kit.NS
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatalf("onboarding %s: %v", p.Key, err)
		}
	}

	scripted := map[int]NurtureDay{}
	for _, d := range kit.Days {
		scripted[d.Day] = d
	}

	logExchange := func(day int, seq int, text string, salient bool) {
		key := fmt.Sprintf("exchange-d%d-%d", day, seq)
		if _, err := s.Put(ctx, PutParams{NS: kit.NS, Key: key,
			Content: "User: " + text, Kind: "episodic", Tier: "sensory",
			Tags: []string{"chat:1"}, TTL: "7d", Importance: 0.3}); err != nil {
			t.Fatalf("exchange put: %v", err)
		}
		if !cond.Distill {
			return
		}
		cands := capture.Extract(text, capture.Options{Tags: []string{"chat:1", "same-day"}})
		if salient && len(cands) == 0 {
			t.Fatalf("day %d: capture distilled nothing from salient turn %q", day, text)
		}
		for _, c := range cands {
			if _, err := s.Put(ctx, PutParams{NS: kit.NS, Key: c.Key, Content: c.Content,
				Kind: c.Kind, Tags: c.Tags, Tier: "stm", Priority: c.Priority,
				Importance: c.Importance, Dedup: true}); err != nil {
				t.Fatalf("distill put: %v", err)
			}
		}
	}

	countLive := func() int {
		var n int
		s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND ns = ?
			AND tier NOT IN ('dormant','archived')`, kit.NS).Scan(&n)
		return n
	}

	for day := 1; day <= kit.FinalDay; day++ {
		dayStart := base.Add(time.Duration(day) * 24 * time.Hour)
		seq := 0
		if d, ok := scripted[day]; ok {
			for _, ex := range d.Exchanges {
				clockAt(dayStart.Add(time.Duration(seq) * time.Hour))
				logExchange(day, seq, ex.Text, ex.Salient)
				seq++
			}
		}
		// Ambient noise: the background chatter of a real life.
		if kit.NoisePerDay > 0 && day <= kit.FinalDay-7 {
			for n := 0; n < kit.NoisePerDay; n++ {
				thing := nurtureNoiseThings[(day*kit.NoisePerDay+n)%len(nurtureNoiseThings)]
				other := nurtureNoiseThings[(day+n*5+3)%len(nurtureNoiseThings)]
				clockAt(dayStart.Add(time.Duration(seq) * time.Hour))
				logExchange(day, seq, fmt.Sprintf(
					"This morning the %s ended up next to the %s again, day %d of the mystery.",
					thing, other, day), false)
				seq++
			}
		}
		if d, ok := scripted[day]; ok {
			for _, probe := range d.Probes {
				clockAt(dayStart.Add(8 * time.Hour))
				repeats := probe.Repeats
				if repeats == 0 {
					repeats = 1
				}
				var joined string
				for r := 0; r < repeats; r++ {
					res, err := s.Context(ctx, ContextParams{NS: kit.NS, Query: probe.Query,
						Budget: 2000, MinScore: 0.3, ExcludePinned: true})
					if err != nil {
						t.Fatalf("day %d probe %q: %v", day, probe.Query, err)
					}
					var b strings.Builder
					for _, m := range res.Memories {
						b.WriteString(m.Content)
						b.WriteString("\n")
					}
					joined = b.String()
				}
				for _, want := range probe.WantContent {
					rep.ProbeTotal++
					if strings.Contains(joined, want) {
						rep.ProbeHits++
					} else {
						rep.ProbeMisses = append(rep.ProbeMisses,
							fmt.Sprintf("day %d q=%q missing=%q", day, probe.Query, want))
					}
				}
			}
		}
		if cond.Reflect {
			clockAt(dayStart.Add(23 * time.Hour))
			res, err := s.Reflect(ctx, ReflectParams{NS: kit.NS})
			if err != nil {
				t.Fatalf("day %d reflect: %v", day, err)
			}
			rep.Promoted += res.Promoted
			rep.Deleted += res.Deleted
		}
		if day%7 == 0 {
			rep.WeeklyLive = append(rep.WeeklyLive, countLive())
		}
	}

	// Final census.
	clockAt(base.Add(time.Duration(kit.FinalDay)*24*time.Hour + time.Hour))
	rep.LiveTotal = countLive()
	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND ns = ? AND tier = 'sensory'`, kit.NS).Scan(&rep.LiveSensory)
	rows, _ := s.db.Query(`SELECT content, tier FROM memories WHERE deleted_at IS NULL AND ns = ?
		AND key NOT LIKE 'exchange-%'`, kit.NS)
	for rows.Next() {
		var content, tier string
		rows.Scan(&content, &tier)
		if isNurtureNoise(content) {
			if tier != "dormant" && tier != "archived" {
				rep.NoiseLive++
			}
			if tier == "ltm" {
				rep.NoiseInLTM++
			}
		}
	}
	rows.Close()
	for _, marker := range []string{"7am", "seaweed", "back patio"} {
		var tier string
		s.db.QueryRow(`SELECT tier FROM memories WHERE deleted_at IS NULL AND ns = ?
			AND key NOT LIKE 'exchange-%' AND content LIKE ? LIMIT 1`, kit.NS, "%"+marker+"%").Scan(&tier)
		rep.SalientTier[marker] = tier
	}
	rep.IdentityIntact = true
	for _, seed := range kit.Onboarding {
		ms, err := s.Get(ctx, GetParams{NS: kit.NS, Key: seed.Key})
		if err != nil || len(ms) == 0 || ms[0].Content != seed.Content || !ms[0].Pinned || ms[0].Version != 1 {
			rep.IdentityIntact = false
		}
	}
	return rep
}

// TestEvalNurture — the baseline upbringing: full machinery, four properties.
func TestEvalNurture(t *testing.T) {
	s := newTestStore(t)
	kit := DefaultNurtureKit()
	rep := growNurture(t, s, kit, nurtureConditions{Reflect: true, Distill: true})
	t.Logf("baseline: probes %d/%d promoted=%d deleted=%d live=%d noiseLive=%d weeklyLive=%v",
		rep.ProbeHits, rep.ProbeTotal, rep.Promoted, rep.Deleted, rep.LiveTotal, rep.NoiseLive, rep.WeeklyLive)

	t.Run("IdentityStability", func(t *testing.T) {
		if !rep.IdentityIntact {
			t.Fatal("identity mutated during growth")
		}
	})
	t.Run("EarnedPermanence", func(t *testing.T) {
		if tier := rep.SalientTier["7am"]; tier != "ltm" {
			t.Errorf("memory rehearsed across 4+ spaced days should be ltm, got %q", tier)
		}
		if rep.Promoted == 0 {
			t.Error("no promotions in six weeks of rehearsed life — starvation")
		}
	})
	t.Run("GracefulForgetting", func(t *testing.T) {
		if rep.LiveSensory > 0 {
			t.Errorf("%d sensory exchanges still live after six weeks", rep.LiveSensory)
		}
		if rep.NoiseInLTM > 0 {
			t.Errorf("%d noise memories reached ltm", rep.NoiseInLTM)
		}
	})
	t.Run("RecallThroughLife", func(t *testing.T) {
		if len(rep.ProbeMisses) > 0 {
			t.Errorf("probe misses under full machinery:\n%s", strings.Join(rep.ProbeMisses, "\n"))
		}
	})
	t.Run("CorrectionWins", func(t *testing.T) {
		ctx := context.Background()
		res, err := s.Context(ctx, ContextParams{NS: kit.NS, Query: "where did we move the roses",
			Budget: 2000, MinScore: 0.3, ExcludePinned: true})
		if err != nil {
			t.Fatalf("context: %v", err)
		}
		patioRank, frontRank := -1, -1
		for i, m := range res.Memories {
			if patioRank == -1 && strings.Contains(m.Content, "back patio") {
				patioRank = i
			}
			if frontRank == -1 && strings.Contains(m.Content, "front yard") && !strings.Contains(m.Content, "back patio") {
				frontRank = i
			}
		}
		if patioRank == -1 {
			t.Fatalf("corrected location (back patio) absent from recall; got %d memories", len(res.Memories))
		}
		if frontRank != -1 && frontRank < patioRank {
			t.Errorf("stale location (rank %d) outranks correction (rank %d)", frontRank, patioRank)
		}
	})
}

// TestEvalNurtureAblation — the "does ghost help" table: raise the same agent
// with pieces of the machinery removed and measure what each contributes.
func TestEvalNurtureAblation(t *testing.T) {
	kit := DefaultNurtureKit()
	conds := []struct {
		name string
		c    nurtureConditions
	}{
		{"full", nurtureConditions{Reflect: true, Distill: true}},
		{"no-reflect", nurtureConditions{Reflect: false, Distill: true}},
		{"no-distill", nurtureConditions{Reflect: true, Distill: false}},
	}
	reps := map[string]nurtureReport{}
	for _, cd := range conds {
		s := newTestStore(t)
		reps[cd.name] = growNurture(t, s, kit, cd.c)
	}

	t.Log("condition    probes      promoted  live  sensory  noiseLive  identity  weeklyLive")
	for _, cd := range conds {
		r := reps[cd.name]
		t.Logf("%-12s %2d/%-8d %-9d %-5d %-8d %-10d %-9v %v",
			cd.name, r.ProbeHits, r.ProbeTotal, r.Promoted, r.LiveTotal, r.LiveSensory, r.NoiseLive, r.IdentityIntact, r.WeeklyLive)
	}

	full, noReflect, noDistill := reps["full"], reps["no-reflect"], reps["no-distill"]

	// Identity protection holds regardless of which machinery runs.
	for name, r := range reps {
		if !r.IdentityIntact {
			t.Errorf("%s: identity mutated", name)
		}
	}
	// Distillation is the write path: without it nothing said is recallable
	// (raw exchanges live in the search-excluded sensory tier).
	if noDistill.ProbeHits >= full.ProbeHits {
		t.Errorf("distillation should be what makes life recallable: full %d hits vs no-distill %d",
			full.ProbeHits, noDistill.ProbeHits)
	}
	// Reflect is evolution: promotion only happens with it, and it keeps the
	// live population bounded under noise pressure.
	if noReflect.Promoted != 0 {
		t.Errorf("no-reflect promoted %d — promotion without reflect?", noReflect.Promoted)
	}
	if full.Promoted == 0 {
		t.Error("full machinery produced no promotions")
	}
	if noReflect.LiveTotal <= full.LiveTotal {
		t.Errorf("reflect should bound the live population under noise: full %d vs no-reflect %d",
			full.LiveTotal, noReflect.LiveTotal)
	}
	if noReflect.LiveSensory == 0 {
		t.Error("no-reflect should leave sensory exchanges alive (nothing decays them)")
	}
	// Reflect must not cost recall: forgetting the right things means the
	// probes still hit everything the full condition hits.
	if full.ProbeHits < noReflect.ProbeHits {
		t.Errorf("reflect lost recall: full %d hits vs no-reflect %d — forgetting the wrong things",
			full.ProbeHits, noReflect.ProbeHits)
	}
}
