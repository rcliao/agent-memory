package store

import (
	"database/sql/driver"
	"fmt"
	"strings"
	"sync"
	"unicode"

	sqlite "modernc.org/sqlite"
)

// CJK bigram indexing.
//
// FTS5's unicode61 tokenizer treats a contiguous CJK run as ONE token, so a
// query for 拖鞋 can never match "我目前的拖鞋是台灣買的" (one giant token) —
// measured live: 拖鞋 present in 7 chunks, 0 FTS matches, while 鴻禧菇 only
// matched because meal memos happen to punctuate around it. Chinese recall
// was riding the LIKE fallback (no term scoring) and vectors.
//
// Standard fix, pure-Go: index CJK text as overlapping bigrams ("我目 目前
// 前的 的拖 拖鞋"), and rewrite CJK query terms as phrase queries over their
// bigrams ("拖鞋" → "拖鞋"; "鴻禧菇" → "鴻禧 禧菇" as a phrase). The FTS
// table stores segmented text (standalone, no longer content=chunks); the
// cjk_segment SQL function keeps triggers in sync on every write.

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		(r >= 0x3040 && r <= 0x30FF) || // hiragana + katakana
		(r >= 0xAC00 && r <= 0xD7AF) // hangul
}

// cjkSegment rewrites text so CJK runs become space-separated overlapping
// bigrams; non-CJK text passes through unchanged.
func cjkSegment(text string) string {
	var out strings.Builder
	out.Grow(len(text) * 2)
	var run []rune
	flush := func() {
		if len(run) == 0 {
			return
		}
		if len(run) == 1 {
			out.WriteRune(run[0])
		} else {
			for i := 0; i+1 < len(run); i++ {
				if i > 0 {
					out.WriteByte(' ')
				}
				out.WriteRune(run[i])
				out.WriteRune(run[i+1])
			}
		}
		run = run[:0]
	}
	prevNonCJK := false
	for _, r := range text {
		if isCJK(r) {
			if len(run) == 0 && prevNonCJK && !unicode.IsSpace(rune(lastByte(&out))) {
				out.WriteByte(' ')
			}
			run = append(run, r)
			prevNonCJK = false
			continue
		}
		if len(run) > 0 {
			flush()
			out.WriteByte(' ')
		}
		out.WriteRune(r)
		prevNonCJK = true
	}
	flush()
	return out.String()
}

// cjkQueryTerm rewrites one query term for the bigram index: CJK runs become
// quoted phrases of their bigrams (adjacent-token phrase semantics restore
// the original adjacency constraint). Non-CJK terms return unchanged.
func cjkQueryTerm(term string) string {
	runes := []rune(term)
	hasCJK := false
	for _, r := range runes {
		if isCJK(r) {
			hasCJK = true
			break
		}
	}
	if !hasCJK {
		return term
	}
	seg := cjkSegment(term)
	if !strings.Contains(seg, " ") {
		return seg // single char or single bigram — plain token
	}
	return `"` + seg + `"` // phrase over bigrams
}

func lastByte(b *strings.Builder) byte {
	str := b.String()
	if len(str) == 0 {
		return ' '
	}
	return str[len(str)-1]
}

var cjkUDFOnce sync.Once

// registerCJKSegmentUDF exposes cjk_segment to SQL so the chunks_fts sync
// triggers can segment on write. Registration is process-wide and applies to
// connections opened afterwards — call before opening the DB.
func registerCJKSegmentUDF() {
	cjkUDFOnce.Do(func() {
		err := sqlite.RegisterScalarFunction("cjk_segment", 1,
			func(ctx *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
				switch v := args[0].(type) {
				case string:
					return cjkSegment(v), nil
				case []byte:
					return cjkSegment(string(v)), nil
				case nil:
					return nil, nil
				default:
					return nil, fmt.Errorf("cjk_segment: unsupported type %T", v)
				}
			})
		if err != nil && !strings.Contains(err.Error(), "already registered") {
			panic(fmt.Sprintf("register cjk_segment: %v", err))
		}
	})
}

// gradedTermRelevance scores how much of the query's content vocabulary a
// memory actually matches, in [0,1]. ASCII terms match on word equality or a
// shared ≥4-char prefix (a cheap stand-in for stemming: bundle↔bundling);
// CJK terms match by substring (the bigram index guarantees findability, this
// grades strength). Stopwords are ignored. Replaces the flat "any FTS match
// = 0.5 relevance" that let recency float topically-unmatched filler over
// the score floor (measured live: a novel-topic query injected 12+ memos).
func gradedTermRelevance(query, content string, stopwords map[string]bool) float64 {
	contentLower := strings.ToLower(content)
	contentWords := map[string]bool{}
	for _, w := range strings.FieldsFunc(contentLower, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	}) {
		if len(w) >= 2 {
			contentWords[w] = true
		}
	}

	total, matched := 0, 0
	for _, term := range strings.Fields(strings.ToLower(query)) {
		term = strings.Trim(term, "?.,!\"'()[]{}:;，。？！")
		if term == "" || stopwords[term] {
			continue
		}
		hasCJK := false
		for _, r := range term {
			if isCJK(r) {
				hasCJK = true
				break
			}
		}
		total++
		if hasCJK {
			// CJK terms may glue onto particles ("oil對關節有用嗎") — grade
			// by the longest CJK sub-run present in the content.
			hit := false
			runs := splitCJKRuns(term)
			for _, run := range runs {
				if len([]rune(run)) >= 2 && strings.Contains(contentLower, run) {
					hit = true
					break
				}
			}
			if hit {
				matched++
			}
			continue
		}
		if contentWords[term] {
			matched++
			continue
		}
		if len(term) >= 4 {
			prefix := term[:4]
			for w := range contentWords {
				if strings.HasPrefix(w, prefix) {
					matched++
					break
				}
			}
		}
	}
	if total == 0 {
		return 0.5 // no content terms to grade — neutral
	}
	return float64(matched) / float64(total)
}

// splitCJKRuns extracts the contiguous CJK runs from a mixed term.
func splitCJKRuns(term string) []string {
	var runs []string
	var cur []rune
	for _, r := range term {
		if isCJK(r) {
			cur = append(cur, r)
		} else if len(cur) > 0 {
			runs = append(runs, string(cur))
			cur = nil
		}
	}
	if len(cur) > 0 {
		runs = append(runs, string(cur))
	}
	return runs
}
