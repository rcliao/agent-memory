package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestParasiteBench measures utility-parasite share in assembled contexts on
// a real DB snapshot — the aggregate failure the per-query evals cannot see.
//
// A parasite is a memory with high access_count and near-zero utility_count:
// it rides into contexts (edge expansion, big budgets) without ever directly
// matching a query, and Hebbian co-retrieval strengthening compounds the
// ride. Measured live 2026-07-23: 150 such memories on one production DB,
// 64 still riding within 3 days.
//
// Usage (A/B the utility knob by running twice):
//
//	GHOST_BENCH_PARASITE_DB=/path/to/snapshot.db GHOST_NS='agent:x' \
//	  go test ./internal/store/ -run TestParasiteBench -v
//	GHOST_UTILITY_WEIGHT=0.5 GHOST_BENCH_PARASITE_DB=... go test ...
//
// Reports, per budget tier (per-turn 2000 / system 10000):
//   - parasite share of injected tokens (memories with acc>threshold and
//     utility/acc < 1%)
//   - zero-utility share (acc>100, utility==0) — the broader tail
//   - recall canary: fraction of queries whose top-3 contains at least one
//     memory with utilityRatio > 10% (suppressing parasites must not also
//     suppress the useful population)
func TestParasiteBench(t *testing.T) {
	dbPath := os.Getenv("GHOST_BENCH_PARASITE_DB")
	if dbPath == "" {
		t.Skip("GHOST_BENCH_PARASITE_DB not set")
	}
	ns := os.Getenv("GHOST_NS")

	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	// Query shapes mirror the daemon's real callers: short conversational
	// turns (per-turn inject) and broad/system-ish prompts (heartbeats,
	// session assembly) — the large-budget calls are where parasites ride.
	queries := []string{
		"does the plant need watering today",
		"what did we decide about the recent purchase",
		"any updates I should know about",
		"good morning, what's the plan",
		"remind me about the family's preferences",
		"summarize recent activity and check for anything needing attention",
		"what happened yesterday evening",
		"health notes from this week",
	}

	type row struct {
		acc, util, tokens int
	}
	lookup := func(ns, key string) (row, bool) {
		var r row
		err := s.db.QueryRow(`SELECT access_count, utility_count, COALESCE(est_tokens,0)
			FROM memories WHERE ns=? AND key=? AND deleted_at IS NULL
			ORDER BY version DESC LIMIT 1`, ns, key).Scan(&r.acc, &r.util, &r.tokens)
		return r, err == nil
	}

	for _, budget := range []int{2000, 10000} {
		var totTok, parTok, zeroTok int
		var canaryHits, canaryTotal int
		for _, q := range queries {
			res, err := s.Context(ctx, ContextParams{NS: ns, Query: q,
				Budget: budget, MinScore: 0.3, ExcludePinned: true})
			if err != nil {
				t.Fatalf("context %q: %v", q, err)
			}
			canaryTotal++
			topUseful := false
			for i, m := range res.Memories {
				r, ok := lookup(m.NS, m.Key)
				if !ok {
					continue
				}
				totTok += r.tokens
				if r.acc > 1000 && r.util*100 < r.acc {
					parTok += r.tokens
				}
				if r.acc > 100 && r.util == 0 {
					zeroTok += r.tokens
				}
				if i < 3 && r.acc > 0 && float64(r.util)/float64(r.acc) > 0.10 {
					topUseful = true
				}
			}
			if topUseful {
				canaryHits++
			}
		}
		share := func(n int) string {
			if totTok == 0 {
				return "n/a"
			}
			return fmt.Sprintf("%d%%", n*100/totTok)
		}
		t.Logf("budget=%-5d totalTokens=%-6d parasiteShare=%-4s zeroUtilShare=%-4s recallCanary=%d/%d (utilWeight=%s)",
			budget, totTok, share(parTok), share(zeroTok), canaryHits, canaryTotal,
			strings.TrimSpace(os.Getenv("GHOST_UTILITY_WEIGHT")))
	}
}
