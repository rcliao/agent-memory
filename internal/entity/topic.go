package entity

import (
	"math"
	"sort"
	"strings"
)

// Topic represents an extracted topic keyword with its score.
type Topic struct {
	Term  string
	Score float64 // TF-IDF score — higher = more distinctive to this document
}

// ExtractTopics extracts distinctive topic keywords from a document
// relative to a corpus. Uses TF-IDF: terms that appear frequently in this
// document but rarely across the corpus are the most distinctive topics.
func ExtractTopics(doc string, corpus []string, topN int) []Topic {
	if topN <= 0 {
		topN = 5
	}

	// Compute document frequency (how many documents each term appears in)
	df := make(map[string]int)
	for _, d := range corpus {
		addDocFreq(df, tokenize(d))
	}

	numDocs := float64(len(corpus))
	if numDocs < 1 {
		numDocs = 1
	}
	return topicsFromTokens(tokenize(doc), df, numDocs, topN)
}

// ExtractTopicsBatch extracts topics for every document in a corpus while
// building the document-frequency table only ONCE. This is O(total tokens)
// instead of the O(n·corpus) cost of calling ExtractTopics per document, which
// is quadratic and impractical on large stores (e.g. relinking thousands of
// memories). Results align by index with docs.
func ExtractTopicsBatch(docs []string, topN int) [][]Topic {
	if topN <= 0 {
		topN = 5
	}

	tokens := make([][]string, len(docs))
	df := make(map[string]int)
	for i, d := range docs {
		tokens[i] = tokenize(d)
		addDocFreq(df, tokens[i])
	}

	numDocs := float64(len(docs))
	if numDocs < 1 {
		numDocs = 1
	}

	out := make([][]Topic, len(docs))
	for i := range docs {
		out[i] = topicsFromTokens(tokens[i], df, numDocs, topN)
	}
	return out
}

// addDocFreq increments df for each distinct token in a document.
func addDocFreq(df map[string]int, docTokens []string) {
	seen := make(map[string]bool, len(docTokens))
	for _, w := range docTokens {
		if !seen[w] {
			df[w]++
			seen[w] = true
		}
	}
}

// topicsFromTokens scores a document's tokens by TF-IDF against a prebuilt df
// table and returns the top-N distinctive topics.
func topicsFromTokens(docTokens []string, df map[string]int, numDocs float64, topN int) []Topic {
	if len(docTokens) == 0 {
		return nil
	}
	tf := make(map[string]int, len(docTokens))
	for _, w := range docTokens {
		tf[w]++
	}

	type scored struct {
		term  string
		score float64
	}
	var scores []scored
	for term, count := range tf {
		if len(term) < 3 || commonWords[term] {
			continue
		}
		termTF := float64(count) / float64(len(docTokens))
		termIDF := math.Log(1 + numDocs/float64(1+df[term]))
		scores = append(scores, scored{term: term, score: termTF * termIDF})
	}

	// Deterministic order: score desc, then term asc so equal-scored topics
	// (common in short docs where every term has tf=1) don't reorder run-to-run.
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].score != scores[j].score {
			return scores[i].score > scores[j].score
		}
		return scores[i].term < scores[j].term
	})

	if len(scores) > topN {
		scores = scores[:topN]
	}

	topics := make([]Topic, len(scores))
	for i, s := range scores {
		topics[i] = Topic{Term: s.term, Score: s.score}
	}
	return topics
}

// TopicOverlap returns the number of shared topics between two topic lists
// and a similarity score.
func TopicOverlap(topicsA, topicsB []Topic) (shared []string, score float64) {
	setA := make(map[string]float64, len(topicsA))
	for _, t := range topicsA {
		setA[t.Term] = t.Score
	}

	var common []string
	var dotProduct, normA, normB float64
	for _, t := range topicsB {
		if scoreA, ok := setA[t.Term]; ok {
			common = append(common, t.Term)
			dotProduct += scoreA * t.Score
		}
		normB += t.Score * t.Score
	}
	for _, t := range topicsA {
		normA += t.Score * t.Score
	}

	if normA == 0 || normB == 0 {
		return common, 0
	}
	// Cosine similarity of TF-IDF vectors
	return common, dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// tokenize splits text into lowercase non-stopword tokens.
func tokenize(text string) []string {
	var tokens []string
	for _, w := range strings.Fields(strings.ToLower(text)) {
		w = strings.TrimFunc(w, func(r rune) bool {
			return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
		})
		if len(w) >= 3 && !commonWords[w] {
			tokens = append(tokens, w)
		}
	}
	return tokens
}
