package store

import (
	"context"
	"path/filepath"
	"testing"
)

// TestRerankExcludesSummaryParents: contains-parents (consolidation
// summaries) must not be promoted by the cross-encoder.
func TestRerankExcludesSummaryParents(t *testing.T) {
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "sb.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// Fake reranker scores longer content higher — the summary has the
	// longest content, so without the exclusion it would win rank 1.
	s.SetReranker(&slowReranker{})
	ctx := context.Background()

	put := func(key, content string) {
		if _, err := s.Put(ctx, PutParams{NS: "t", Key: key, Content: content}); err != nil {
			t.Fatal(err)
		}
	}
	put("specific-a", "the deploy pipeline uses ArgoCD for production rollouts")
	put("specific-b", "deploy notifications go to the ops channel")
	put("mega-summary", "Consolidated summary of everything: deploys, meals, health, travel, games, lights, coffee, vitamins, restaurants, and every other topic that ever came up in any conversation across months")
	if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "t", FromKey: "mega-summary", ToNS: "t", ToKey: "specific-a", Rel: "contains"}); err != nil {
		t.Fatal(err)
	}

	res, err := s.Search(ctx, SearchParams{NS: "t", Query: "deploy pipeline production", Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) == 0 {
		t.Fatal("no results")
	}
	if res[0].Key == "mega-summary" {
		t.Errorf("summary parent was CE-promoted to rank 1 despite exclusion")
	}

	// With the escape hatch, the summary may win again (longest content).
	t.Setenv("GHOST_RERANK_ALLOW_SUMMARIES", "1")
	res2, _ := s.Search(ctx, SearchParams{NS: "t", Query: "deploy pipeline production", Limit: 3})
	if len(res2) > 0 && res2[0].Key != "mega-summary" {
		t.Logf("note: escape hatch did not restore summary to rank 1 (fused order may dominate) — acceptable")
	}
}
