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

// TestEvalNurtureEmbeddedLLMRestatement documents the live sibling-
// proliferation gap (2026-07-18): the distillation tier rewrites the SAME
// lesson in fresh LLM wording each heartbeat, and one day of traffic produced
// three sibling copies of one conduct rule. RATCHET, not aspiration — the
// measured geometry makes the obvious fixes unsafe:
//
//	restatement pairs (must merge):        cosine 0.72–0.75
//	correction vs stale fact (must NOT):   cosine 0.83
//	same template, different day:          cosine 0.98
//
// The must-merge zone sits BELOW the must-not-merge zone, so no cosine
// threshold separates them (a high-cosine fast path would merge corrections
// into the facts they refute — the worst failure available). Aggregating
// these needs a polarity-aware design, likely offline in reflect where
// both contents can be compared with full context. Until then this test
// pins the current behavior: 3 restatements → 3 siblings, no reinforcement.
// If a triage change starts merging them, this trips — flip it into the
// aspirational assertions (live==1, maxAccess>=2) deliberately.
func TestEvalNurtureEmbeddedLLMRestatement(t *testing.T) {
	s := newEmbeddedNurtureStore(t)
	ctx := context.Background()
	ns := "agent:nurture-restate"
	// Three LLM-style rewrites of one rule — synthetic mirror of the live
	// triple (invented-idiom diary rule, reworded per heartbeat).
	restatements := []struct{ key, content string }{
		{"behavioral-avoid-invented-idioms",
			"Avoid inventing non-standard idiom phrasing in diary entries (e.g. reduplicating a brand name as a verb) — the reader gets confused and asks what it means."},
		{"behavioral-diary-no-invented-idioms",
			"Do not use invented or non-standard idioms and metaphors in diary entries — the reader will ask what they mean if the phrasing is coined."},
		{"diary-language-standard-only",
			"Diary language rule: stick to standard phrasing only; coined or invented expressions in diary entries confuse the reader and need follow-up explanation."},
	}
	for _, r := range restatements {
		if _, err := s.Put(ctx, PutParams{NS: ns, Key: r.key, Content: r.content,
			Kind: "semantic", Tier: "stm", Importance: 0.6, Dedup: true}); err != nil {
			t.Fatalf("put %s: %v", r.key, err)
		}
	}
	var live int
	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND ns = ?`, ns).Scan(&live)
	var maxAccess int
	s.db.QueryRow(`SELECT COALESCE(MAX(access_count),0) FROM memories WHERE deleted_at IS NULL AND ns = ?`, ns).Scan(&maxAccess)
	t.Logf("restatement aggregation: live=%d maxAccess=%d", live, maxAccess)
	if live != 3 {
		t.Errorf("ratchet: expected the documented 3-sibling gap, got live=%d — triage behavior changed; re-measure and flip this test to the aspirational assertions", live)
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
