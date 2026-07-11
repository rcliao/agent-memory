package entity

import (
	"math"
	"testing"
)

// TestExtractTopicsBatchMatchesPerDoc verifies the batch extractor produces the
// same topics/scores as calling ExtractTopics per document (it must — it only
// changes WHEN the shared df table is built, not the math).
func TestExtractTopicsBatchMatchesPerDoc(t *testing.T) {
	corpus := []string{
		"Postgres handles transactions for the metadata store reliably.",
		"We deploy the service with ArgoCD after pushing a git tag.",
		"The staging pipeline runs integration tests before production.",
		"Redis powers the session cache with low latency lookups.",
		"", // empty doc must yield nil in both paths
	}
	batch := ExtractTopicsBatch(corpus, 5)
	if len(batch) != len(corpus) {
		t.Fatalf("batch length %d != corpus %d", len(batch), len(corpus))
	}
	for i, doc := range corpus {
		want := ExtractTopics(doc, corpus, 5)
		got := batch[i]
		if len(got) != len(want) {
			t.Fatalf("doc %d: len batch=%d per-doc=%d", i, len(got), len(want))
		}
		for j := range want {
			if got[j].Term != want[j].Term {
				t.Errorf("doc %d topic %d: term batch=%q per-doc=%q", i, j, got[j].Term, want[j].Term)
			}
			if math.Abs(got[j].Score-want[j].Score) > 1e-9 {
				t.Errorf("doc %d topic %d (%s): score batch=%.6f per-doc=%.6f", i, j, want[j].Term, got[j].Score, want[j].Score)
			}
		}
	}
}

func TestExtractTopicsBatchEmpty(t *testing.T) {
	if got := ExtractTopicsBatch(nil, 5); got != nil && len(got) != 0 {
		t.Errorf("empty corpus should yield empty result, got %v", got)
	}
}
