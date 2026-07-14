package store

import (
	"context"
	"strings"
	"time"
	"unicode"
)

// Write-time triage v1 — the REINFORCE disposition (assimilation).
//
// The nurture parenting eval measured that a lesson restated in different
// words becomes N sibling memories, each with too little spaced rehearsal to
// promote: repetition never aggregates. When a Dedup-enabled put arrives and
// a live memory already says the same thing (exact content, cosine >= 0.82
// when embedded, or high bidirectional distinctive-term overlap in LLM-free
// mode), ghost strengthens the existing memory instead of inserting a
// sibling: one rehearsal access is logged — being told twice IS spaced
// rehearsal, fueling the promotion gate — and importance keeps the stronger
// evidence. Precision over recall: a false merge silently loses a new fact,
// so the term matcher requires high overlap in BOTH directions and several
// shared distinctive tokens (template siblings like day-stamped meal memos
// stay separate because their payload tokens differ).

const (
	triageMinSharedTokens = 5
	triageMinOverlap      = 0.7
)

// reinforce strengthens an existing memory in place of an incoming duplicate:
// a rehearsal access at the store clock plus importance max-merge.
func (s *SQLiteStore) reinforce(ctx context.Context, id string, incomingImportance float64) {
	s.logAccesses(ctx, []string{id})
	_, _ = s.db.ExecContext(ctx, `
		UPDATE memories SET importance = MAX(importance, ?),
			access_count = access_count + 1,
			last_accessed_at = ?
		WHERE id = ? AND deleted_at IS NULL`,
		incomingImportance, s.now().UTC().Format(time.RFC3339), id)
}

// distinctiveTokens returns the comparable token set of a content string:
// lowercased ASCII words of >=4 runes plus CJK bigrams. Numbers count —
// day-stamped template memories must not merge across days.
func distinctiveTokens(content string) map[string]bool {
	out := map[string]bool{}
	var word []rune
	flush := func() {
		if len(word) >= 4 {
			w := strings.ToLower(string(word))
			if !triageStopwords[w] {
				out[w] = true
			}
		}
		word = word[:0]
	}
	var prevCJK rune
	for _, r := range content {
		switch {
		case unicode.IsLetter(r) && r < 0x2E80, unicode.IsDigit(r):
			word = append(word, r)
			prevCJK = 0
		case unicode.Is(unicode.Han, r):
			flush()
			if prevCJK != 0 {
				out[string([]rune{prevCJK, r})] = true
			}
			prevCJK = r
		default:
			flush()
			prevCJK = 0
		}
	}
	flush()
	return out
}

// paraphraseOf reports whether two contents state the same thing, by
// bidirectional distinctive-token overlap.
func paraphraseOf(a, b string) bool {
	ta, tb := distinctiveTokens(a), distinctiveTokens(b)
	if len(ta) == 0 || len(tb) == 0 {
		return false
	}
	shared := 0
	for tok := range ta {
		if tb[tok] {
			shared++
		}
	}
	if shared < triageMinSharedTokens {
		return false
	}
	return float64(shared)/float64(len(ta)) >= triageMinOverlap &&
		float64(shared)/float64(len(tb)) >= triageMinOverlap
}

// findParaphraseForDedup looks for a live memory in the namespace that says
// the same thing as content, using FTS candidates re-checked with the strict
// bidirectional overlap. Returns the key of the match, or "".
func (s *SQLiteStore) findParaphraseForDedup(ctx context.Context, ns, content string) string {
	tokens := distinctiveTokens(content)
	if len(tokens) < triageMinSharedTokens {
		return ""
	}
	// Build a conservative FTS OR-query from a handful of tokens.
	var terms []string
	for tok := range tokens {
		if isASCIIToken(tok) {
			terms = append(terms, `"`+tok+`"`)
		}
		if len(terms) == 8 {
			break
		}
	}
	if len(terms) < 3 {
		return ""
	}
	query := strings.Join(terms, " OR ")
	rows, err := s.db.QueryContext(ctx, `
		WITH matched AS (
			SELECT rowid FROM chunks_fts WHERE chunks_fts MATCH ? LIMIT 50
		)
		SELECT DISTINCT m.key, m.content FROM chunks c
		JOIN matched ON matched.rowid = c.rowid
		JOIN memories m ON m.id = c.memory_id
		WHERE m.ns = ? AND m.deleted_at IS NULL
		  AND m.tier != 'sensory'
		LIMIT 20`, query, ns)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var key, existing string
		if rows.Scan(&key, &existing) != nil {
			continue
		}
		if paraphraseOf(content, existing) {
			return key
		}
	}
	return ""
}

func isASCIIToken(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

// triageStopwords are filler words that inflate overlap denominators without
// carrying meaning — conversational scaffolding, not content.
var triageStopwords = map[string]bool{
	"when": true, "your": true, "yours": true, "this": true, "that": true,
	"with": true, "from": true, "then": true, "them": true, "they": true,
	"will": true, "would": true, "should": true, "about": true, "there": true,
	"which": true, "these": true, "those": true, "were": true, "been": true,
	"into": true, "over": true, "just": true, "some": true, "than": true,
	"only": true, "what": true, "have": true, "also": true, "very": true,
	"really": true, "please": true, "remember": true, "keep": true,
	"always": true, "never": true, "want": true, "need": true, "like": true,
}

func importanceOrDefault(imp float64) float64 {
	if imp <= 0 {
		return 0.5
	}
	return imp
}
