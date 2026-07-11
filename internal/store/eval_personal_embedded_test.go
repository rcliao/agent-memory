package store

import (
	"context"
	"os"
	"testing"

	"github.com/rcliao/ghost/internal/embedding"
)

// TestEvalPersonalEmbedded exercises the semantic/vector path that the FTS-only
// personal eval cannot: queries with ZERO term overlap to their evidence, found
// only by embedding similarity. It runs each scenario FTS-only and then with the
// local embedder and asserts vectors improve semantic recall.
//
// Gated behind GHOST_PERSONAL_EMBED=1 because it loads the local model (slow).
//
//	GHOST_PERSONAL_EMBED=1 go test ./internal/store/ -run TestEvalPersonalEmbedded -v
func TestEvalPersonalEmbedded(t *testing.T) {
	if os.Getenv("GHOST_PERSONAL_EMBED") == "" {
		t.Skip("set GHOST_PERSONAL_EMBED=1 to run (loads the local embedding model)")
	}
	ctx := context.Background()
	emb := embedding.NewLocalEmbedder()
	if v, err := emb.Embed(ctx, "probe embedding availability"); err != nil || len(v) == 0 {
		t.Skipf("local embedder unavailable: %v", err)
	}

	// Semantic-recall scenarios: the query shares NO tokens with the target's
	// content, so FTS/LIKE cannot find it — only vector similarity can.
	sem := []struct{ name, query, relevant string }{
		{"indent-go", "how should I indent my Golang source files", "pref-go-format"},
		{"ship-release", "how do I ship a release to production", "proc-deploy"},
		{"datastore", "which datastore did we choose", "decision-db"},
		{"editor", "what do I write my code in day to day", "pref-editor"},
	}

	run := func(withEmbed bool) map[string]float64 {
		s := newTestStore(t)
		if withEmbed {
			s.SetEmbedder(emb)
		} else {
			s.SetEmbedder(nil) // FTS/LIKE only
		}
		seedPersonalInto(t, ctx, s)
		out := map[string]float64{}
		for _, sc := range sem {
			res, err := s.Context(ctx, ContextParams{NS: PersonalNS, Query: sc.query, Budget: 1000})
			if err != nil {
				t.Fatalf("%s: context: %v", sc.name, err)
			}
			out[sc.name] = MRR(contextKeys(res), map[string]bool{sc.relevant: true})
		}
		return out
	}

	fts := run(false)
	vec := run(true)

	var ftsSum, vecSum float64
	t.Logf("── Embedded personal eval: semantic recall (no term overlap) ──")
	t.Logf("  %-14s %-16s FTS-MRR  vec-MRR", "scenario", "target")
	for _, sc := range sem {
		t.Logf("  %-14s %-16s %6.2f   %6.2f", sc.name, sc.relevant, fts[sc.name], vec[sc.name])
		ftsSum += fts[sc.name]
		vecSum += vec[sc.name]
	}
	n := float64(len(sem))
	t.Logf("  MEAN: FTS %.3f → vec %.3f", ftsSum/n, vecSum/n)

	if vecSum <= ftsSum {
		t.Errorf("embeddings did not improve semantic recall: FTS mean %.3f, vec mean %.3f", ftsSum/n, vecSum/n)
	}
}
