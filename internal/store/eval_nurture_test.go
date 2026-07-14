package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rcliao/ghost/internal/capture"
)

// growNurture runs a kit against a fresh store: onboarding, then day-by-day
// exchanges (raw sensory write + LLM-free capture distillation, mirroring
// shell's LogExchange), probes (context assembly → access logging), and a
// nightly reflect. Returns cumulative reflect totals.
func growNurture(t *testing.T, s *SQLiteStore, kit NurtureKit) (promoted, deleted int) {
	t.Helper()
	ctx := context.Background()
	base := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	clockAt := func(ts time.Time) { s.SetClock(func() time.Time { return ts }) }

	clockAt(base)
	for _, p := range kit.Onboarding {
		p.NS = kit.NS
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatalf("onboarding %s: %v", p.Key, err)
		}
	}

	for _, day := range kit.Days {
		dayStart := base.Add(time.Duration(day.Day) * 24 * time.Hour)
		for i, ex := range day.Exchanges {
			clockAt(dayStart.Add(time.Duration(i) * time.Hour))
			// Raw exchange at the sensory tier, like shell's LogExchange.
			if _, err := s.Put(ctx, PutParams{NS: kit.NS,
				Key:     fmt.Sprintf("exchange-d%d-%d", day.Day, i),
				Content: "User: " + ex.Text, Kind: "episodic", Tier: "sensory",
				Tags: []string{"chat:1"}, TTL: "7d", Importance: 0.3}); err != nil {
				t.Fatalf("exchange put: %v", err)
			}
			// LLM-free distillation into searchable stm (B1 semantics).
			cands := capture.Extract(ex.Text, capture.Options{Tags: []string{"chat:1", "same-day"}})
			if ex.Salient && len(cands) == 0 {
				t.Fatalf("day %d: capture distilled nothing from salient turn %q", day.Day, ex.Text)
			}
			for _, c := range cands {
				if _, err := s.Put(ctx, PutParams{NS: kit.NS, Key: c.Key, Content: c.Content,
					Kind: c.Kind, Tags: c.Tags, Tier: "stm", Priority: c.Priority,
					Importance: c.Importance, Dedup: true}); err != nil {
					t.Fatalf("distill put: %v", err)
				}
			}
		}
		for _, probe := range day.Probes {
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
					t.Fatalf("day %d probe %q: %v", day.Day, probe.Query, err)
				}
				var b strings.Builder
				for _, m := range res.Memories {
					b.WriteString(m.Content)
					b.WriteString("\n")
				}
				joined = b.String()
			}
			for _, want := range probe.WantContent {
				if !strings.Contains(joined, want) {
					t.Errorf("day %d probe %q: context missing %q\ncontext:\n%s", day.Day, probe.Query, want, joined)
				}
			}
			for _, avoid := range probe.AvoidContent {
				if strings.Contains(joined, avoid) {
					t.Errorf("day %d probe %q: context should not contain %q", day.Day, probe.Query, avoid)
				}
			}
		}
		// Nightly reflect, like the daemon.
		clockAt(dayStart.Add(23 * time.Hour))
		res, err := s.Reflect(ctx, ReflectParams{NS: kit.NS})
		if err != nil {
			t.Fatalf("day %d reflect: %v", day.Day, err)
		}
		promoted += res.Promoted
		deleted += res.Deleted
	}

	clockAt(base.Add(time.Duration(kit.FinalDay) * 24 * time.Hour))
	res, err := s.Reflect(ctx, ReflectParams{NS: kit.NS})
	if err != nil {
		t.Fatalf("final reflect: %v", err)
	}
	promoted += res.Promoted
	deleted += res.Deleted
	return promoted, deleted
}

func TestEvalNurture(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	kit := DefaultNurtureKit()
	promoted, deleted := growNurture(t, s, kit)

	// Growth report.
	tiers := map[string]int{}
	rows, _ := s.db.Query(`SELECT tier, COUNT(*) FROM memories WHERE deleted_at IS NULL AND ns = ? GROUP BY tier`, kit.NS)
	for rows.Next() {
		var tier string
		var n int
		rows.Scan(&tier, &n)
		tiers[tier] = n
	}
	rows.Close()
	t.Logf("after %d days: tiers=%v promoted=%d deleted=%d", kit.FinalDay, tiers, promoted, deleted)

	contentTier := func(substr string) (string, string) {
		t.Helper()
		var key, tier string
		s.db.QueryRow(`SELECT key, tier FROM memories WHERE deleted_at IS NULL AND ns = ?
			AND key NOT LIKE 'exchange-%' AND content LIKE ? ORDER BY created_at LIMIT 1`,
			kit.NS, "%"+substr+"%").Scan(&key, &tier)
		return key, tier
	}

	t.Run("IdentityStability", func(t *testing.T) {
		for _, seed := range kit.Onboarding {
			ms, err := s.Get(ctx, GetParams{NS: kit.NS, Key: seed.Key})
			if err != nil || len(ms) == 0 {
				t.Fatalf("identity key %q gone after growth: %v", seed.Key, err)
			}
			if ms[0].Content != seed.Content {
				t.Errorf("%s content drifted:\n was: %s\n now: %s", seed.Key, seed.Content, ms[0].Content)
			}
			if !ms[0].Pinned {
				t.Errorf("%s lost pinned", seed.Key)
			}
			if ms[0].Version != 1 {
				t.Errorf("%s version %d — something rewrote identity", seed.Key, ms[0].Version)
			}
		}
	})

	t.Run("EarnedPermanence", func(t *testing.T) {
		key, tier := contentTier("7am")
		if key == "" {
			t.Fatal("rehearsed watering memory not found")
		}
		if tier != "ltm" {
			t.Errorf("memory rehearsed across 4+ spaced days should be ltm, got %q (key %s)", tier, key)
		}
		if promoted == 0 {
			t.Error("no promotions in six weeks of rehearsed life — starvation")
		}
	})

	t.Run("GracefulForgetting", func(t *testing.T) {
		for _, noise := range []string{"parcel from the bookstore", "blue umbrella"} {
			key, tier := contentTier(noise)
			if key == "" {
				continue // fully forgotten (sensory decay) — acceptable
			}
			if tier == "ltm" {
				t.Errorf("one-off noise %q reached ltm (key %s)", noise, key)
			}
		}
		// Raw sensory exchanges must not survive six weeks in live tiers.
		var liveSensory int
		s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND ns = ?
			AND tier = 'sensory'`, kit.NS).Scan(&liveSensory)
		if liveSensory > 0 {
			t.Errorf("%d sensory exchanges still live after six weeks", liveSensory)
		}
	})

	t.Run("CorrectionWins", func(t *testing.T) {
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
			t.Errorf("stale location (front yard, rank %d) outranks correction (back patio, rank %d)", frontRank, patioRank)
		}
	})
}

// TestEvalNurtureSupersedeFired documents whether the day-14 correction
// triggered freshness supersede against the stale fact. Currently a known
// gap: detectSupersede requires shared NAMED entities, and common-noun facts
// ("roses", "front yard") have none — so the stale memory keeps full
// importance and no contradicts edge exists. Logged, not asserted, until the
// term-overlap fallback ships (BACKLOG #3 correction-ranking).
func TestEvalNurtureSupersedeFired(t *testing.T) {
	s := newTestStore(t)
	growNurture(t, s, DefaultNurtureKit())
	var edges int
	s.db.QueryRow(`SELECT COUNT(*) FROM memory_edges WHERE rel = 'contradicts'`).Scan(&edges)
	var staleImp float64
	s.db.QueryRow(`SELECT importance FROM memories WHERE deleted_at IS NULL AND content LIKE '%7am%' LIMIT 1`).Scan(&staleImp)
	t.Logf("contradicts edges=%d stale-fact importance=%.2f (supersede %s)", edges, staleImp,
		map[bool]string{true: "FIRED", false: "did NOT fire — entity-overlap gap"}[edges > 0])
}
