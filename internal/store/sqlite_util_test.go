package store

import (
	"context"
	"testing"

	"github.com/rcliao/ghost/internal/embedding"
)

// unitEmbedder is a tiny deterministic embedder for plumbing tests.
type unitEmbedder struct{}

func (unitEmbedder) Embed(ctx context.Context, text string) (embedding.Vector, error) {
	v := make(embedding.Vector, 8)
	for i, r := range text {
		v[i%8] += float32(r%13) / 13
	}
	return v, nil
}
func (unitEmbedder) Dims() int { return 8 }

// ReembedAll must clear and regenerate every chunk embedding — the
// model-migration path (found 2026-07-28: raw sqlite cannot clear chunks
// because the FTS triggers need ghost's cjk_segment function).
func TestReembedAll(t *testing.T) {
	s := newTestStore(t)
	s.SetEmbedder(unitEmbedder{})
	ctx := context.Background()
	for _, k := range []string{"a", "b"} {
		if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: k, Content: "the quick brown fox jumps over " + k}); err != nil {
			t.Fatal(err)
		}
	}
	var withEmb int
	s.db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE embedding IS NOT NULL`).Scan(&withEmb)
	if withEmb == 0 {
		t.Fatalf("puts did not embed")
	}
	n, err := s.ReembedAll(ctx, nil)
	if err != nil {
		t.Fatalf("reembed: %v", err)
	}
	if n == 0 {
		t.Fatalf("reembed processed 0 chunks")
	}
	var nulls int
	s.db.QueryRow(`SELECT COUNT(*) FROM chunks WHERE embedding IS NULL`).Scan(&nulls)
	if nulls != 0 {
		t.Fatalf("%d chunks left without embeddings", nulls)
	}
}
