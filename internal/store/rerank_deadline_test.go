package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rcliao/ghost/internal/embedding"
)

// slowReranker scores chunks deterministically (longer content = higher) with
// a fixed per-call delay, so tests can exercise deadline behavior.
type slowReranker struct {
	delay time.Duration
	calls int
}

func (r *slowReranker) Rerank(ctx context.Context, query string, docs []string) ([]embedding.RerankResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	time.Sleep(r.delay)
	r.calls++
	out := make([]embedding.RerankResult, len(docs))
	for i, d := range docs {
		out[i] = embedding.RerankResult{Index: i, Score: float32(len(d))}
	}
	return out, nil
}

func rerankTestStore(t *testing.T, delay time.Duration) (*SQLiteStore, *slowReranker) {
	t.Helper()
	s, err := NewSQLiteStore(filepath.Join(t.TempDir(), "r.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	rr := &slowReranker{delay: delay}
	s.SetReranker(rr)
	return s, rr
}

func rerankInput(n int) []SearchResult {
	var results []SearchResult
	for i := 0; i < n; i++ {
		content := "memory content number "
		for j := 0; j <= i; j++ {
			content += "x" // longer content for later docs → fake scores ascend
		}
		results = append(results, SearchResult{})
		results[i].ID = string(rune('a' + i))
		results[i].Key = string(rune('a' + i))
		results[i].Content = content
	}
	return results
}

func TestRerankMaxPCompletesWithoutDeadline(t *testing.T) {
	s, rr := rerankTestStore(t, 0)
	results := rerankInput(6)
	out := s.rerankMaxP(context.Background(), "query", results)
	if len(out) != 6 {
		t.Fatalf("lost results: %d", len(out))
	}
	// Fake scores ascend with doc index → full rerank reverses the order.
	if out[0].Key != "f" || out[5].Key != "a" {
		t.Errorf("full rerank order wrong: first=%s last=%s", out[0].Key, out[5].Key)
	}
	if rr.calls != 6 {
		t.Errorf("expected 6 per-doc calls, got %d", rr.calls)
	}
}

func TestRerankMaxPHonorsDeadline(t *testing.T) {
	// 40ms per doc, ~100ms budget → ~2-3 docs scored, rest untouched.
	s, rr := rerankTestStore(t, 40*time.Millisecond)
	results := rerankInput(6)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	out := s.rerankMaxP(ctx, "query", results)
	elapsed := time.Since(start)

	if len(out) != 6 {
		t.Fatalf("deadline dropped results: %d", len(out))
	}
	if elapsed > 400*time.Millisecond {
		t.Errorf("deadline not honored: rerank ran %s", elapsed)
	}
	if rr.calls >= 6 {
		t.Errorf("all docs scored despite deadline (calls=%d)", rr.calls)
	}
	// The unscored tail must keep original relative order at the end.
	scored := rr.calls
	for i := scored; i < 6; i++ {
		want := string(rune('a' + i))
		if out[i].Key != want {
			t.Errorf("unscored tail reordered at %d: got %s want %s", i, out[i].Key, want)
		}
	}
}

func TestRerankMaxPZeroBudgetFallsBack(t *testing.T) {
	s, _ := rerankTestStore(t, 10*time.Millisecond)
	results := rerankInput(4)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already expired
	out := s.rerankMaxP(ctx, "query", results)
	for i, r := range out {
		if r.Key != string(rune('a'+i)) {
			t.Fatalf("expired ctx should return fused order unchanged")
		}
	}
}
