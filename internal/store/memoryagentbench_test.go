package store

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestMemoryAgentBenchCR runs the Conflict Resolution track.
//
// Usage:
//
//	GHOST_BENCH_MAB=testdata/memoryagentbench/conflict_resolution.json \
//	  go test ./internal/store/ -run TestMemoryAgentBenchCR -v -timeout 30m
//
// GHOST_BENCH_MAB_MAX_FACTS caps ingested facts per row (default 5000 —
// covers the 6k and 32k tracks fully; the 64k/262k tracks are truncated,
// which invalidates their scores, so they are skipped unless raised).
func TestMemoryAgentBenchCR(t *testing.T) {
	path := os.Getenv("GHOST_BENCH_MAB")
	if path == "" {
		t.Skip("GHOST_BENCH_MAB not set — skipping MemoryAgentBench")
	}
	path = resolveRepoPath(path)

	rows, err := LoadMemoryAgentBenchCR(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	maxFacts := 5000
	if v := os.Getenv("GHOST_BENCH_MAB_MAX_FACTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxFacts = n
		}
	}
	var keep []MABRow
	for _, r := range rows {
		if len(mabFactLines(r.Context)) <= maxFacts {
			keep = append(keep, r)
		} else {
			t.Logf("skipping %s: exceeds GHOST_BENCH_MAB_MAX_FACTS=%d", r.ID, maxFacts)
		}
	}

	results, err := RunMemoryAgentBenchCR(keep, func() (*SQLiteStore, func(), error) {
		dir := t.TempDir()
		s, err := NewSQLiteStore(filepath.Join(dir, "mab.db"))
		if err != nil {
			return nil, nil, err
		}
		return s, func() { s.Close() }, nil
	}, func(done, total int) { t.Logf("progress %d/%d", done, total) })
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	t.Logf("\n=== MemoryAgentBench Conflict Resolution ===")
	for _, r := range results {
		t.Logf("%-28s n=%d hit@5=%.3f hit@10=%.3f mrr=%.3f", r.Track, r.Questions, r.HitAt5, r.HitAt10, r.MRR)
	}
}

func TestMABFactLines(t *testing.T) {
	ctx := "Here is a list of facts:\n0. Alpha is beta.\n1. Gamma is delta.\n\n2. Epsilon is zeta."
	facts := mabFactLines(ctx)
	if len(facts) != 3 {
		t.Fatalf("got %d facts, want 3: %v", len(facts), facts)
	}
	if facts[1] != "Gamma is delta." {
		t.Errorf("fact order broken: %v", facts)
	}
	if strings.Contains(facts[0], "0.") {
		t.Errorf("numbering not stripped: %q", facts[0])
	}
}
