package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/rcliao/ghost/internal/embedding"
)

// Embedded nurture variant — the same childhoods raised with the vector
// channel on (local all-MiniLM embeddings), measuring what FTS-only mode
// structurally cannot:
//
//  1. WRITE-TIME AGGREGATION: cosine triage should collapse restated lessons
//     into one strengthening memory (the parenting kit's sibling-splitting
//     finding) — repetition aggregates without probe tuning.
//  2. PARAPHRASE RECALL: a correction asked about in different words ("where
//     are the roses planted now" vs "moved to the back patio") should be
//     reachable through the vector channel — the fade-below-floor diagnostic
//     from the FTS-only kit.
//
// Gated on GHOST_NURTURE_EMBED=1 (needs the local model on disk; slower —
// every distilled put embeds). Follows the eval_personal_embedded pattern.
func newEmbeddedNurtureStore(t *testing.T) *SQLiteStore {
	t.Helper()
	if os.Getenv("GHOST_NURTURE_EMBED") != "1" {
		t.Skip("set GHOST_NURTURE_EMBED=1 to run the embedded nurture variant")
	}
	s := newTestStore(t)
	emb := embedding.NewLocalEmbedder()
	if emb == nil {
		t.Skip("local embedder unavailable")
	}
	s.SetEmbedder(emb)
	return s
}

func TestEvalNurtureEmbeddedAggregation(t *testing.T) {
	s := newEmbeddedNurtureStore(t)
	kit := ParentingNurtureKit()
	rep := growNurture(t, s, kit, nurtureConditions{Reflect: true, Distill: true})
	t.Logf("embedded parenting: probes %d/%d promoted=%d live=%d noiseLive=%d contamination=%d",
		rep.ProbeHits, rep.ProbeTotal, rep.Promoted, rep.LiveTotal, rep.NoiseLive, rep.ProbeContamination)

	if len(rep.ProbeMisses) > 0 {
		t.Errorf("probe misses under embeddings:\n%s", strings.Join(rep.ProbeMisses, "\n"))
	}
	// The aggregation claim: the conduct rule was restated 6 times in varied
	// words. Cosine triage should have collapsed them — at most 2 live
	// memories carry the rule (FTS-only mode produced ~6 siblings).
	var siblings int
	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND ns = ?
		AND key NOT LIKE 'exchange-%' AND content LIKE '%short and gentle%'`, kit.NS).Scan(&siblings)
	if siblings > 2 {
		t.Errorf("restated rule fragmented into %d siblings — cosine triage not aggregating", siblings)
	}
	// And the aggregated memory should carry reinforcement standing: multiple
	// rehearsal accesses from the restatements alone.
	var maxAccess int
	s.db.QueryRow(`SELECT COALESCE(MAX(access_count),0) FROM memories WHERE deleted_at IS NULL AND ns = ?
		AND key NOT LIKE 'exchange-%' AND content LIKE '%short and gentle%'`, kit.NS).Scan(&maxAccess)
	if maxAccess < 3 {
		t.Errorf("aggregated rule has access_count=%d — restatements did not reinforce", maxAccess)
	}
	if rep.NoiseInLTM > 0 || rep.ProbeContamination > 0 {
		t.Errorf("noise leaked under embeddings: ltm=%d contamination=%d", rep.NoiseInLTM, rep.ProbeContamination)
	}
}

func TestEvalNurtureEmbeddedFlooding(t *testing.T) {
	s := newEmbeddedNurtureStore(t)
	kit := FloodingNurtureKit()
	rep := growNurture(t, s, kit, nurtureConditions{Reflect: true, Distill: true})
	t.Logf("embedded flooding: probes %d/%d live=%d noiseLive=%d contamination=%d",
		rep.ProbeHits, rep.ProbeTotal, rep.LiveTotal, rep.NoiseLive, rep.ProbeContamination)

	// The flooding claim: a once-stated novel fact stays reachable by meaning
	// even when the namespace is saturated with stored template noise.
	var factTier, factKind string
	var factAccess int
	s.db.QueryRow(`SELECT tier, kind, access_count FROM memories WHERE deleted_at IS NULL AND ns = ?
		AND key NOT LIKE 'exchange-%' AND content LIKE '%ceramic frog%' LIMIT 1`, kit.NS).Scan(&factTier, &factKind, &factAccess)
	t.Logf("fact standing: tier=%q kind=%q access=%d", factTier, factKind, factAccess)
	if len(rep.ProbeMisses) > 0 {
		t.Errorf("flooded out — paraphrase probes missed the once-stated fact:\n%s",
			strings.Join(rep.ProbeMisses, "\n"))
	}

	// Flood share: assemble the day-33 probe context once more and measure how
	// much of it is noise. A gate that merely includes the fact but buries it
	// under near-duplicate noise still fails the reader.
	ctx := context.Background()
	res, err := s.Context(ctx, ContextParams{NS: kit.NS,
		Query: "where can someone find the backup way into the home",
		Budget: 2000, MinScore: 0.3, ExcludePinned: true})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	noise := 0
	for _, m := range res.Memories {
		if isNurtureNoise(m.Content) {
			noise++
		}
	}
	if len(res.Memories) > 0 && noise*2 > len(res.Memories) {
		t.Errorf("probe context is majority noise: %d/%d memories are flood", noise, len(res.Memories))
	}
}

func TestEvalNurtureEmbeddedParaphraseRecall(t *testing.T) {
	s := newEmbeddedNurtureStore(t)
	kit := DefaultNurtureKit()
	rep := growNurture(t, s, kit, nurtureConditions{Reflect: true, Distill: true})
	t.Logf("embedded baseline: probes %d/%d promoted=%d live=%d", rep.ProbeHits, rep.ProbeTotal, rep.Promoted, rep.LiveTotal)

	// The FTS-only diagnostic: at day 42, a paraphrase query with no shared
	// content terms could not reach the day-14 correction. The vector channel
	// must close that gap.
	ctx := context.Background()
	res, err := s.Context(ctx, ContextParams{NS: kit.NS, Query: "which spot in the garden are the roses in these days",
		Budget: 2000, MinScore: 0.3, ExcludePinned: true})
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	var joined strings.Builder
	for _, m := range res.Memories {
		joined.WriteString(m.Content + "\n")
	}
	if !strings.Contains(joined.String(), "back patio") {
		t.Errorf("paraphrase query missed the correction (vector channel should reach it); got:\n%s", joined.String())
	}
}
