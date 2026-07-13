package store

import (
	"context"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

// TestLatencyBenchLargestChat measures the per-TURN memory cost the way the
// shell daemon actually incurs it: two concurrent Context calls (chat-scoped
// + cross-chat) under a fetch budget, against a real DB and a real chat tag.
//
// This exists because broad-namespace averages hid the user who matters
// most: the densest chat pool (months of meal/health memories) fills every
// rerank window and ran 8s where a sparse DM ran 0.6s on identical config.
// The reranker re-entry criterion is <2s p50 ON THIS BENCHMARK, not on
// namespace averages.
//
// Usage:
//
//	GHOST_BENCH_LATENCY_DB=copy-of-live.db GHOST_NS='agent:pikamini' \
//	GHOST_BENCH_CHAT_TAG='chat:<your-densest-chat-id>' [GHOST_BENCH_TURN_BUDGET=3s] \
//	  go test ./internal/store/ -run TestLatencyBenchLargestChat -v
func TestLatencyBenchLargestChat(t *testing.T) {
	dbPath := os.Getenv("GHOST_BENCH_LATENCY_DB")
	chatTag := os.Getenv("GHOST_BENCH_CHAT_TAG")
	if dbPath == "" || chatTag == "" {
		t.Skip("GHOST_BENCH_LATENCY_DB / GHOST_BENCH_CHAT_TAG not set")
	}
	ns := os.Getenv("GHOST_NS")
	budget := 3 * time.Second
	if v := os.Getenv("GHOST_BENCH_TURN_BUDGET"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			budget = d
		}
	}

	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Mock dense-pool queries: generic daily-life themes (meals, health,
	// logistics) chosen to maximally overlap a personal-agent chat pool.
	// Deliberately synthetic — no real names, dates, or specifics.
	queries := []string{
		"what did I have for breakfast today",
		"weekly meal log summary",
		"when do I take the vitamins and supplements",
		"which restaurant did we visit recently",
		"recent sleep and exercise trends",
		"food recommendations and travel plans",
		"how do I use this skincare product",
		"troubleshooting the smart light sensor",
	}

	var turns []time.Duration
	degraded := 0
	for _, q := range queries {
		fetchCtx, cancel := context.WithTimeout(context.Background(), budget)
		start := time.Now()
		var wg sync.WaitGroup
		wg.Add(2)
		var chatErr, crossErr error
		go func() {
			defer wg.Done()
			_, chatErr = s.Context(fetchCtx, ContextParams{
				NS: ns, Query: q, Tags: []string{chatTag}, Budget: 2000,
			})
		}()
		go func() {
			defer wg.Done()
			_, crossErr = s.Context(fetchCtx, ContextParams{
				NS: ns, Query: q, Budget: 2000, ExcludePinned: true,
			})
		}()
		wg.Wait()
		cancel()
		turns = append(turns, time.Since(start))
		if chatErr != nil || crossErr != nil {
			degraded++
		}
	}

	sort.Slice(turns, func(i, j int) bool { return turns[i] < turns[j] })
	var total time.Duration
	for _, d := range turns {
		total += d
	}
	p50 := turns[len(turns)/2]
	max := turns[len(turns)-1]
	t.Logf("largest-chat turn: p50=%s max=%s mean=%s degraded=%d/%d budget=%s",
		p50.Round(time.Millisecond), max.Round(time.Millisecond),
		(total / time.Duration(len(turns))).Round(time.Millisecond),
		degraded, len(queries), budget)

	// The budget must be enforceable: overshoot is bounded by one in-flight
	// inference call per concurrent fetch (the model call cannot be
	// interrupted mid-forward) plus contention — measured ≤800ms on the
	// production copy. Anything past 1s grace means the deadline checks
	// are not firing.
	if max > budget+time.Second {
		t.Errorf("turn exceeded budget+grace: %s > %s — deadline not honored", max, budget)
	}
}
