package store

import (
	"testing"

	"github.com/rcliao/ghost/internal/model"
)

func mmrCand(key, content string, score float64) contextCandidate {
	return contextCandidate{
		memory: model.Memory{ID: key, Key: key, Content: content},
		score:  score,
	}
}

func TestMMRRerankDemotesNearDuplicate(t *testing.T) {
	// Two near-identical memories at ranks 1-2, a distinct one at rank 3.
	// MMR should pull the distinct memory ahead of the duplicate.
	cands := []contextCandidate{
		mmrCand("a", "the postgres connection pool size was raised to fifty for the beta cluster", 1.0),
		mmrCand("a-dup", "postgres connection pool size raised to fifty on the beta cluster yesterday", 0.95),
		mmrCand("b", "user prefers dark mode and compact layout in every editor", 0.9),
	}

	out := mmrRerank(cands, 0.7)
	if out[0].memory.Key != "a" {
		t.Fatalf("top result changed: got %s, want a", out[0].memory.Key)
	}
	if out[1].memory.Key != "b" {
		t.Fatalf("distinct memory not promoted: got order [%s %s %s]",
			out[0].memory.Key, out[1].memory.Key, out[2].memory.Key)
	}
}

func TestMMRRerankKeepsAllCandidates(t *testing.T) {
	cands := []contextCandidate{
		mmrCand("a", "alpha content about deployment", 1.0),
		mmrCand("b", "beta content about testing", 0.8),
		mmrCand("c", "gamma content about caching", 0.6),
		mmrCand("d", "delta content about logging", 0.4),
	}
	out := mmrRerank(cands, 0.5)
	if len(out) != len(cands) {
		t.Fatalf("MMR dropped candidates: got %d, want %d", len(out), len(cands))
	}
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.memory.Key] = true
	}
	for _, c := range cands {
		if !seen[c.memory.Key] {
			t.Fatalf("candidate %s missing after rerank", c.memory.Key)
		}
	}
}

func TestMMRRerankNoopWhenDisabled(t *testing.T) {
	cands := []contextCandidate{
		mmrCand("a", "first memory content here", 1.0),
		mmrCand("b", "second memory content here", 0.5),
		mmrCand("c", "third memory content here", 0.2),
	}
	for _, lambda := range []float64{0, 1, -0.5} {
		out := mmrRerank(cands, lambda)
		for i := range cands {
			if out[i].memory.Key != cands[i].memory.Key {
				t.Fatalf("lambda=%v should be a no-op, order changed at %d", lambda, i)
			}
		}
	}
}

func TestMMRLambdaEnvParsing(t *testing.T) {
	cases := map[string]float64{
		"":     0,
		"0.7":  0.7,
		"1":    1.0,
		"0":    0,
		"-1":   0,
		"2":    0,
		"junk": 0,
	}
	for env, want := range cases {
		t.Setenv("GHOST_MMR_LAMBDA", env)
		if got := mmrLambda(); got != want {
			t.Errorf("GHOST_MMR_LAMBDA=%q: got %v, want %v", env, got, want)
		}
	}
}
