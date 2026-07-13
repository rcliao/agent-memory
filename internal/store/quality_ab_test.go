package store

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestRetrievalQualityAB prints the injected memory set for each query under
// the current env config — run twice (reranker off/on) and diff the sets to
// judge relevance agentically. Gated; measurement tool, not a pass/fail test.
func TestRetrievalQualityAB(t *testing.T) {
	dbPath := os.Getenv("GHOST_BENCH_LATENCY_DB")
	if dbPath == "" {
		t.Skip("GHOST_BENCH_LATENCY_DB not set")
	}
	ns := os.Getenv("GHOST_NS")
	chatTag := os.Getenv("GHOST_BENCH_CHAT_TAG")

	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	queries := strings.Split(os.Getenv("GHOST_BENCH_QUERIES"), "\n")
	for _, q := range queries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		var tags []string
		if chatTag != "" {
			tags = []string{chatTag}
		}
		res, err := s.Context(ctx, ContextParams{NS: ns, Query: q, Tags: tags, Budget: 2000})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("QUERY: %s", q)
		for i, m := range res.Memories {
			c := strings.ReplaceAll(m.Content, "\n", " ")
			if len(c) > 90 {
				c = c[:90]
			}
			t.Logf("  %2d. %s :: %s", i+1, m.Key, c)
		}
	}
}
