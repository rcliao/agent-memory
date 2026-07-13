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
		// Mirror the daemon's full injection: chat-scoped fetch + cross-chat
		// fetch (both ExcludePinned — pinned live in the system prompt),
		// cross results deduped against chat results, chat first.
		var tags []string
		if chatTag != "" {
			tags = []string{chatTag}
		}
		chatRes, err := s.Context(ctx, ContextParams{
			NS: ns, Query: q, Tags: tags, Budget: 2000, ExcludePinned: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		crossRes, err := s.Context(ctx, ContextParams{
			NS: ns, Query: q, Budget: 2000, ExcludePinned: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		var merged []ContextMemory
		for _, m := range chatRes.Memories {
			seen[m.Key] = true
			merged = append(merged, m)
		}
		for _, m := range crossRes.Memories {
			if !seen[m.Key] {
				merged = append(merged, m)
			}
		}
		t.Logf("QUERY: %s", q)
		for i, m := range merged {
			c := strings.ReplaceAll(m.Content, "\n", " ")
			if len(c) > 90 {
				c = c[:90]
			}
			src := "chat"
			if i >= len(chatRes.Memories) {
				src = "cross"
			}
			t.Logf("  %2d. [%s] %s :: %s", i+1, src, m.Key, c)
		}
	}
}
