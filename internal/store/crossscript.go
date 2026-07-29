package store

import (
	"os"
	"unicode"
)

// Cross-script rerank skip.
//
// The cross-encoder (ms-marco family) is English-trained: on a CJK query
// against English documents it emits noise, overwriting a vector ranking
// that handled the pair fine. Measured on a production snapshot
// (2026-07-28, multilingual embedder): the correct cross-script hit ranked
// #1 at cosine 0.747 with the CE off and fell out of the top-15 with it
// on. Same shape as the temporal-intent skip: when the CE is structurally
// blind to what the ranking depends on, keep the fused order.
//
// A multilingual CE (mmarco) was measured as the alternative and rejected
// for now: its sharply bimodal scores drop weak-but-true pairs below the
// MinScore floor (13/20 on the cross-lingual suite vs 20/20 without CE).
//
// The skip triggers only when the query and the rerank window genuinely
// straddle scripts: the query is CJK-dominant while most candidate docs are
// not (or the reverse). Same-script traffic keeps the CE and its measured
// English gains. Disable via GHOST_RERANK_CROSS_SCRIPT=1 (forces CE on).

// cjkShare returns the fraction of letter runes that are Han/kana.
func cjkShare(s string) float64 {
	var cjk, letters int
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r), r >= 0x3040 && r <= 0x30FF:
			cjk++
			letters++
		case unicode.IsLetter(r):
			letters++
		}
	}
	if letters == 0 {
		return 0
	}
	return float64(cjk) / float64(letters)
}

// crossScriptQuery reports whether reranking this query over these results
// would put an English-trained CE across a script boundary for most of the
// window.
func crossScriptQuery(query string, window []SearchResult) bool {
	if os.Getenv("GHOST_RERANK_CROSS_SCRIPT") == "1" {
		return false
	}
	if len(window) == 0 {
		return false
	}
	// Queries count as CJK at a LOWER bar than documents: an embedded Latin
	// brand name outweighs CJK by rune count ("冰淇淋 Bainbridge 推薦" is 1/3
	// CJK by letters but is a Chinese sentence) — any substantial CJK share
	// means the user is writing Chinese around a name.
	qCJK := cjkShare(query) >= 0.3
	mismatched := 0
	for _, r := range window {
		if (cjkShare(r.Content) >= 0.5) != qCJK {
			mismatched++
		}
	}
	return mismatched*2 > len(window)
}
