package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLatencyBreakdown(t *testing.T) {
	dbPath := os.Getenv("GHOST_BENCH_LATENCY_DB")
	if dbPath == "" {
		t.Skip("GHOST_BENCH_LATENCY_DB not set")
	}
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	ns := os.Getenv("GHOST_NS")

	queries := []string{
		"what did we decide about the deploy pipeline",
		"user preferences for communication style",
		"grocery list and meal planning",
		"schedule for next week",
	}
	p := SearchParams{NS: ns, Limit: 10}
	for pass := 1; pass <= 2; pass++ {
		t.Logf("--- pass %d (%s cache)", pass, map[int]string{1: "cold", 2: "warm"}[pass])
		for _, q := range queries {
			p.Query = q
			t0 := time.Now()
			fts := s.searchFTS(ctx, p, 20)
			tFTS := time.Since(t0)

			t0 = time.Now()
			like, _ := s.searchLike(ctx, p, nil, 20)
			tLike := time.Since(t0)

			tVec := time.Duration(0)
			nvec := 0
			if s.embedder != nil {
				t0 = time.Now()
				vec, _ := s.searchVector(ctx, p, nil, 20)
				tVec = time.Since(t0)
				nvec = len(vec)
			}
			t.Logf("%-45q fts=%6s(%d) like=%6s(%d) vec=%6s(%d)",
				q, tFTS.Round(time.Millisecond), len(fts),
				tLike.Round(time.Millisecond), len(like),
				tVec.Round(time.Millisecond), nvec)
		}
	}
}
