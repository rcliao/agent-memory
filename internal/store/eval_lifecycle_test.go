package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Longitudinal lifecycle eval (FAMA-style).
//
// Every other suite measures a frozen store. This one measures the thing that
// makes ghost a MEMORY system rather than a search index: the store rewrites
// itself between queries (reflect, supersede, decay, GC), and that rewriting
// should IMPROVE retrieval over time, not erode it. Inspired by Memora's
// Forgetting-Aware Memory Accuracy (arXiv 2604.20006): retrieving a
// superseded memory above its successor counts against the system.
//
// Simulation: 30 virtual days on the injectable store clock. Facts evolve
// (bundler Webpack→Vite→esbuild), noise accumulates daily, reflect+GC run
// each epoch. Measured per epoch:
//   - stable recall: facts that never changed stay retrievable — including
//     facts NOT probed along the way (no practice effect keeping them alive)
//   - FAMA: for evolved facts, the CURRENT version must outrank all
//     superseded versions
// Run with reflect ON vs OFF to attribute trajectory changes to lifecycle.

const lcNS = "agent:lifecycle"

type lcEvolvingFact struct {
	topic    string
	query    string
	versions []string // content per version; index i goes in at day i*10
	keys     []string
}

func lcCorpus() ([]SeedMemory, []lcEvolvingFact) {
	stable := []SeedMemory{
		{NS: lcNS, Key: "api-auth", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "The public API authenticates with OAuth2 bearer tokens issued by Keycloak."},
		{NS: lcNS, Key: "backup-policy", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "Database backups run nightly at 2am with 30-day retention in S3 Glacier."},
		{NS: lcNS, Key: "oncall-rotation", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "On-call rotation hands off every Monday at 10am via PagerDuty schedule."},
		{NS: lcNS, Key: "style-guide", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "Code style: gofmt plus golangci-lint with the errcheck and staticcheck linters."},
		// The "survival set": never probed until the final epoch, so no
		// practice effect keeps them alive through reflect demotion.
		{NS: lcNS, Key: "license-choice", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "The project is licensed under Apache 2.0 for patent-grant reasons."},
		{NS: lcNS, Key: "vendor-contact", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "The Datadog account manager is Priya Raman, renewals happen in September."},
		{NS: lcNS, Key: "meeting-cadence", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "Architecture review meets biweekly on Thursdays in the Redwood room."},
		{NS: lcNS, Key: "error-budget", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.6,
			Content: "SLO error budget is 0.1% monthly downtime for the ingestion pipeline."},
	}

	evolving := []lcEvolvingFact{
		{
			topic: "bundler", query: "what do we use to bundle the frontend",
			versions: []string{
				"We use Webpack to bundle the frontend assets.",
				"We switched from Webpack to Vite for bundling the frontend.",
				"We switched from Vite to esbuild for bundling the frontend.",
			},
		},
		{
			topic: "database", query: "which database does the app use",
			versions: []string{
				"The app stores data in MySQL 8 on RDS.",
				"We migrated the app database from MySQL to PostgreSQL 15.",
			},
		},
		{
			topic: "deploy-tool", query: "how do we deploy to production",
			versions: []string{
				"Production deploys go through Jenkins pipelines.",
				"We moved production deploys from Jenkins to ArgoCD.",
			},
		},
	}
	for i := range evolving {
		for v := range evolving[i].versions {
			evolving[i].keys = append(evolving[i].keys,
				fmt.Sprintf("%s-v%d", evolving[i].topic, v+1))
		}
	}
	return stable, evolving
}

// lcEpochMetrics is one measurement point in the trajectory.
type lcEpochMetrics struct {
	day             int
	stableRecall    float64 // probed stable facts in top-5
	famaFreshWins   float64 // evolving topics where current version outranks all superseded
	famaPenalized   float64 // MRR of current version, zeroed when a superseded version outranks it
	activeMemories  int
	dormantOrOlder  int
	survivalPenalty bool
}

func runLifecycle(t *testing.T, reflectOn bool) []lcEpochMetrics {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer s.Close()

	epoch := evalClockEpoch
	clock := epoch
	s.SetClock(func() time.Time { return clock })
	ctx := context.Background()

	stable, evolving := lcCorpus()
	put := func(key, content string) {
		if _, err := s.Put(ctx, PutParams{NS: lcNS, Key: key, Content: content}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}
	putAs := func(key, content, kind, tier string) {
		if _, err := s.Put(ctx, PutParams{NS: lcNS, Key: key, Content: content, Kind: kind, Tier: tier}); err != nil {
			t.Fatalf("put %s: %v", key, err)
		}
	}

	// Day 0: stable corpus + first versions.
	for _, m := range stable {
		put(m.Key, m.Content)
	}
	for _, ef := range evolving {
		put(ef.keys[0], ef.versions[0])
	}

	probedStable := stable[:4]
	survivalSet := stable[4:]

	var trajectory []lcEpochMetrics
	const days = 30
	for day := 0; day <= days; day++ {
		clock = epoch.Add(time.Duration(day) * 24 * time.Hour)

		// Daily noise: episodic chatter that must not crowd out real facts.
		// Sensory-tier ephemera exercises the sys-delete-sensory rule; stm
		// episodic exercises decay + archive-old-episodic.
		putAs(fmt.Sprintf("chatter-%02d-a", day),
			fmt.Sprintf("Standup note day %d: reviewed PRs, minor test flake in CI, lunch ran long.", day),
			"episodic", "sensory")
		putAs(fmt.Sprintf("chatter-%02d-b", day),
			fmt.Sprintf("Random observation day %d: office coffee machine acting up again.", day),
			"episodic", "stm")

		// Evolving facts change at day 10 and day 20.
		if day == 10 || day == 20 {
			v := day / 10
			for _, ef := range evolving {
				if v < len(ef.versions) {
					put(ef.keys[v], ef.versions[v])
				}
			}
		}

		// Epoch boundary every 5 days: lifecycle runs, then measurement.
		if day%5 != 0 {
			continue
		}
		if reflectOn {
			if _, err := s.Reflect(ctx, ReflectParams{NS: lcNS}); err != nil {
				t.Fatalf("reflect day %d: %v", day, err)
			}
			if _, err := s.GC(ctx); err != nil {
				t.Fatalf("gc day %d: %v", day, err)
			}
		}

		m := lcEpochMetrics{day: day}

		// Stable recall over the probed set (probing = realistic practice).
		hits := 0
		for _, sm := range probedStable {
			res, err := s.Search(ctx, SearchParams{NS: lcNS, Query: sm.Content[:40], Limit: 5})
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			for _, r := range res {
				if r.Key == sm.Key {
					hits++
					break
				}
			}
		}
		m.stableRecall = float64(hits) / float64(len(probedStable))

		// FAMA over evolving topics.
		fresh, penalized := 0, 0.0
		for _, ef := range evolving {
			current := 0
			for v := range ef.versions {
				if day >= v*10 {
					current = v
				}
			}
			res, err := s.Search(ctx, SearchParams{NS: lcNS, Query: ef.query, Limit: 10})
			if err != nil {
				t.Fatalf("fama probe: %v", err)
			}
			currentRank, supersededRank := -1, -1
			for i, r := range res {
				for v, key := range ef.keys {
					if r.Key != key {
						continue
					}
					if v == current && currentRank == -1 {
						currentRank = i
					}
					if v != current && supersededRank == -1 {
						supersededRank = i
					}
				}
			}
			if currentRank >= 0 && (supersededRank == -1 || currentRank < supersededRank) {
				fresh++
			}
			if currentRank >= 0 {
				score := 1.0 / float64(currentRank+1)
				if supersededRank >= 0 && supersededRank < currentRank {
					score = 0 // FAMA penalty: obsolete memory outranked the truth
				}
				penalized += score
			}
		}
		m.famaFreshWins = float64(fresh) / float64(len(evolving))
		m.famaPenalized = penalized / float64(len(evolving))

		count, _ := s.countActiveMemories(ctx, lcNS)
		m.activeMemories = int(count)
		trajectory = append(trajectory, m)
	}

	// Final: the survival set — facts never probed in 30 days. Did the
	// lifecycle keep them reachable, or did decay/demotion bury them?
	final := &trajectory[len(trajectory)-1]
	surviving := 0
	for _, sm := range survivalSet {
		res, err := s.Search(ctx, SearchParams{NS: lcNS, Query: sm.Content[:40], Limit: 5})
		if err != nil {
			t.Fatalf("survival probe: %v", err)
		}
		for _, r := range res {
			if r.Key == sm.Key {
				surviving++
				break
			}
		}
	}
	if surviving < len(survivalSet) {
		final.survivalPenalty = true
	}
	t.Logf("[reflect=%v] survival set: %d/%d unprobed stable facts retrievable at day 30",
		reflectOn, surviving, len(survivalSet))
	return trajectory
}

func TestEvalLifecycle(t *testing.T) {
	for _, reflectOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("reflect=%v", reflectOn), func(t *testing.T) {
			traj := runLifecycle(t, reflectOn)
			for _, m := range traj {
				t.Logf("day %2d: stable_recall=%.2f fama_fresh=%.2f fama_mrr=%.2f active=%d",
					m.day, m.stableRecall, m.famaFreshWins, m.famaPenalized, m.activeMemories)
			}
			final := traj[len(traj)-1]

			// Hard gates: the lifecycle must not destroy the store.
			if final.stableRecall < 0.75 {
				t.Errorf("stable recall collapsed by day 30: %.2f (want >= 0.75)", final.stableRecall)
			}
			if final.famaFreshWins < 0.66 {
				t.Errorf("FAMA fresh-wins at day 30: %.2f — superseded facts outranking current ones", final.famaFreshWins)
			}
			// Benchmark signals (soft): trajectory shape.
			first := traj[0]
			if final.famaPenalized < first.famaPenalized {
				t.Logf("BENCHMARK: FAMA penalized MRR declined day 0 %.2f -> day 30 %.2f",
					first.famaPenalized, final.famaPenalized)
			}
			if final.survivalPenalty {
				t.Logf("BENCHMARK: unprobed stable facts lost by day 30 (decay buried facts nobody practiced)")
			}
		})
	}
}
