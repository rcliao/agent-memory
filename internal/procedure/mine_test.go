package procedure

import (
	"strings"
	"testing"
)

func joinPat(p Pattern) string { return strings.Join(p.Steps, " ") }

func findPat(pats []Pattern, steps string) *Pattern {
	for i := range pats {
		if joinPat(pats[i]) == steps {
			return &pats[i]
		}
	}
	return nil
}

func TestMineFindsRecurringWorkflow(t *testing.T) {
	sessions := [][]string{
		{"edit", "build", "test", "commit"},
		{"build", "test", "commit"},
		{"edit", "build", "test", "commit", "push"},
		{"lint", "build", "test"},
	}
	pats := Mine(sessions, Options{MinSupport: 2})
	// "build test" appears in all 4; "build test commit" in 3.
	if p := findPat(pats, "build test commit"); p == nil || p.Support != 3 {
		t.Fatalf("expected 'build test commit' support 3, got %+v", pats)
	}
}

func TestMineMaximalDropsRedundantSubruns(t *testing.T) {
	sessions := [][]string{
		{"a", "b", "c"},
		{"a", "b", "c"},
		{"a", "b", "c"},
	}
	pats := Mine(sessions, Options{MinSupport: 2})
	// "a b c" (support 3) dominates "a b" and "b c" (also support 3) — only the
	// maximal one should survive.
	if findPat(pats, "a b c") == nil {
		t.Fatalf("expected maximal 'a b c', got %+v", pats)
	}
	if findPat(pats, "a b") != nil || findPat(pats, "b c") != nil {
		t.Errorf("redundant sub-runs with equal support should be dropped, got %+v", pats)
	}
}

func TestMineKeepsSubrunWithHigherSupport(t *testing.T) {
	sessions := [][]string{
		{"a", "b", "c"},
		{"a", "b", "c"},
		{"a", "b"}, // "a b" now has support 3 > "a b c" support 2 — keep both
	}
	pats := Mine(sessions, Options{MinSupport: 2})
	ab := findPat(pats, "a b")
	abc := findPat(pats, "a b c")
	if ab == nil || ab.Support != 3 {
		t.Errorf("'a b' should survive with support 3, got %+v", pats)
	}
	if abc == nil || abc.Support != 2 {
		t.Errorf("'a b c' should survive with support 2, got %+v", pats)
	}
}

func TestMineSupportCountsDistinctSessions(t *testing.T) {
	// A routine repeated within one session counts once.
	sessions := [][]string{
		{"open", "close", "open", "close"},
		{"open", "close"},
	}
	pats := Mine(sessions, Options{MinSupport: 2})
	if p := findPat(pats, "open close"); p == nil || p.Support != 2 {
		t.Fatalf("expected 'open close' support 2 (distinct sessions), got %+v", pats)
	}
}

func TestMineBelowSupportDropped(t *testing.T) {
	sessions := [][]string{
		{"a", "b"},
		{"c", "d"},
	}
	if pats := Mine(sessions, Options{MinSupport: 2}); len(pats) != 0 {
		t.Errorf("no pattern meets support 2, expected none, got %+v", pats)
	}
}

func TestMineDeterministicOrder(t *testing.T) {
	sessions := [][]string{
		{"x", "y"}, {"x", "y"}, {"p", "q"}, {"p", "q"}, {"p", "q"},
	}
	a := Mine(sessions, Options{MinSupport: 2})
	b := Mine(sessions, Options{MinSupport: 2})
	if len(a) != len(b) {
		t.Fatal("nondeterministic length")
	}
	for i := range a {
		if joinPat(a[i]) != joinPat(b[i]) {
			t.Fatalf("nondeterministic order at %d: %q vs %q", i, joinPat(a[i]), joinPat(b[i]))
		}
	}
	// Highest support first: "p q" (3) before "x y" (2).
	if joinPat(a[0]) != "p q" {
		t.Errorf("expected highest-support pattern first, got %q", joinPat(a[0]))
	}
}
