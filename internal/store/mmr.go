package store

import (
	"os"
	"strconv"
	"strings"
)

// mmrLambda is the opt-in relevance/diversity trade-off for MMR re-ranking of
// context candidates (GHOST_MMR_LAMBDA, default 0 = off). At λ=1 the order is
// pure relevance (identical to no MMR); lower values penalize candidates that
// are textually redundant with already-selected ones, so near-duplicate
// memories stop crowding the packing budget.
//
// Measured (2026-07-12): in-repo suites unchanged; LongMemEval_S context-mode
// REGRESSES at λ=0.7 (MRR 0.502→0.472, R@5 0.502→0.380) because evidence is
// often several windowed chunks of the same session and diversity demotes the
// relevant near-duplicates — same failure mode as the search-path MMR. Keep
// off by default; only worth trying on redundancy-heavy personal DBs.
// envBool reports whether a boolean env knob is switched on ("1").
func envBool(name string) bool { return os.Getenv(name) == "1" }

func mmrLambda() float64 {
	if env := os.Getenv("GHOST_MMR_LAMBDA"); env != "" {
		if v, err := strconv.ParseFloat(env, 64); err == nil && v > 0 && v <= 1 {
			return v
		}
	}
	return 0
}

// mmrRerank re-orders score-sorted candidates by Maximal Marginal Relevance:
// each step picks argmax λ·relevance − (1−λ)·maxSim(candidate, selected).
// Relevance is the context score normalized by the top score; similarity is
// content-token Jaccard, so it needs no embeddings and works on the FTS-only
// path. O(n²) over the candidate pool (≤ ~50 after Phase 2's cap).
func mmrRerank(candidates []contextCandidate, lambda float64) []contextCandidate {
	if lambda <= 0 || lambda >= 1 || len(candidates) < 3 {
		return candidates
	}

	topScore := candidates[0].score
	if topScore <= 0 {
		return candidates
	}

	tokens := make([]map[string]bool, len(candidates))
	for i, c := range candidates {
		tokens[i] = contentTokenSet(c.memory.Content)
	}

	selected := make([]contextCandidate, 0, len(candidates))
	selectedTokens := make([]map[string]bool, 0, len(candidates))
	remaining := make([]int, len(candidates))
	for i := range candidates {
		remaining[i] = i
	}

	for len(remaining) > 0 {
		bestPos, bestVal := 0, -1.0
		for pos, idx := range remaining {
			rel := candidates[idx].score / topScore
			maxSim := 0.0
			for _, sel := range selectedTokens {
				if sim := jaccard(tokens[idx], sel); sim > maxSim {
					maxSim = sim
				}
			}
			val := lambda*rel - (1-lambda)*maxSim
			if val > bestVal {
				bestVal, bestPos = val, pos
			}
		}
		idx := remaining[bestPos]
		selected = append(selected, candidates[idx])
		selectedTokens = append(selectedTokens, tokens[idx])
		remaining = append(remaining[:bestPos], remaining[bestPos+1:]...)
	}
	return selected
}

// contentTokenSet lowercases and splits content into a set of words ≥3 chars,
// the cheap tokenization MMR needs for redundancy detection.
func contentTokenSet(content string) map[string]bool {
	set := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(content), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	}) {
		if len(w) >= 3 {
			set[w] = true
		}
	}
	return set
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	inter := 0
	for w := range small {
		if large[w] {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
