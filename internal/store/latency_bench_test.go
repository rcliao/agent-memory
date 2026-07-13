package store

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

// TestLatencyBench measures end-to-end Search latency on a real DB across
// retrieval configurations — the "speed" axis of the quality/speed balance.
//
// Usage:
//
//	GHOST_BENCH_LATENCY_DB=/path/to/memory.db GHOST_NS='agent:pikamini' \
//	  go test ./internal/store/ -run TestLatencyBench -v -timeout 30m
//
// Configs are controlled by the usual env knobs (GHOST_EMBED_PROVIDER,
// GHOST_RERANKER); run it once per config and compare.
func TestLatencyBench(t *testing.T) {
	dbPath := os.Getenv("GHOST_BENCH_LATENCY_DB")
	if dbPath == "" {
		t.Skip("GHOST_BENCH_LATENCY_DB not set")
	}
	ns := os.Getenv("GHOST_NS")

	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()
	ctx := context.Background()

	queries := []string{
		"what did we decide about the deploy pipeline",
		"user preferences for communication style",
		"how does the memory system work",
		"what happened with the performance incident",
		"grocery list and meal planning",
		"schedule for next week",
		"which model are we using for classification",
		"how do I configure the telegram bot",
		"what went wrong with the database migration",
		"birthday plans and gift ideas",
	}

	measure := func(label string, fn func(q string) error) {
		// Warmup (model load, page cache).
		_ = fn(queries[0])
		var times []time.Duration
		for _, q := range queries {
			start := time.Now()
			if err := fn(q); err != nil {
				t.Fatalf("%s %q: %v", label, q, err)
			}
			times = append(times, time.Since(start))
		}
		sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
		var total time.Duration
		for _, d := range times {
			total += d
		}
		t.Logf("%-24s p50=%8s p95=%8s mean=%8s", label,
			times[len(times)/2].Round(time.Millisecond),
			times[len(times)-1].Round(time.Millisecond),
			(total / time.Duration(len(times))).Round(time.Millisecond))
	}

	measure("Search(limit=10)", func(q string) error {
		_, err := s.Search(ctx, SearchParams{NS: ns, Query: q, Limit: 10})
		return err
	})
	measure("Context(budget=2000)", func(q string) error {
		_, err := s.Context(ctx, ContextParams{NS: ns, Query: q, Budget: 2000})
		return err
	})

	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE embedding IS NOT NULL`).Scan(&n)
	t.Logf("db: %s  embedded_chunks=%d  embedder=%v  reranker=%v",
		fmt.Sprintf("%dMB", dbSize(dbPath)/1e6), n, s.embedder != nil, s.reranker != nil)
}

func dbSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}
