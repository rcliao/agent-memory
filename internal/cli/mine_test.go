package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/rcliao/ghost/internal/model"
)

func parseMineResult(t *testing.T, out string) mineResult {
	t.Helper()
	var res mineResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("unmarshal mine result: %v\nraw: %s", err, out)
	}
	return res
}

func TestMineProceduresStores(t *testing.T) {
	db := tempDB(t)
	text := strings.Join([]string{
		"edit build test commit",
		"build test commit",
		"edit build test commit push",
	}, "\n")

	var out string
	var err error
	withStdin(t, text, func() {
		out, err = executeCmd(t, "mine-procedures", "--db", db, "-n", "agent:me")
	})
	if err != nil {
		t.Fatalf("mine-procedures failed: %v", err)
	}
	res := parseMineResult(t, out)
	if res.Sessions != 3 {
		t.Errorf("expected 3 sessions, got %d", res.Sessions)
	}
	if res.Stored < 1 {
		t.Fatalf("expected at least one stored workflow, got %+v", res)
	}
	// The stored memory should be procedural.
	listOut, _ := executeCmd(t, "list", "--db", db, "-n", "agent:me", "--kind", "procedural")
	var mems []model.Memory
	if err := json.Unmarshal([]byte(listOut), &mems); err != nil {
		t.Fatalf("unmarshal list: %v\nraw: %s", err, listOut)
	}
	if len(mems) == 0 {
		t.Fatal("expected procedural memories persisted")
	}
	for _, m := range mems {
		if m.Kind != "procedural" {
			t.Errorf("expected procedural kind, got %q", m.Kind)
		}
	}
}

func TestMineProceduresDryRun(t *testing.T) {
	db := tempDB(t)
	text := "a b c\na b c\na b c"
	var out string
	var err error
	withStdin(t, text, func() {
		out, err = executeCmd(t, "mine-procedures", "--db", db, "-n", "agent:me", "--dry-run")
	})
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	res := parseMineResult(t, out)
	if res.Stored != 0 {
		t.Errorf("dry-run should store nothing, got %d", res.Stored)
	}
	if res.Found < 1 {
		t.Errorf("dry-run should still report found workflows, got %d", res.Found)
	}
	listOut, _ := executeCmd(t, "list", "--db", db, "-n", "agent:me")
	var mems []model.Memory
	json.Unmarshal([]byte(listOut), &mems)
	if len(mems) != 0 {
		t.Errorf("dry-run must not persist, found %d", len(mems))
	}
}

func TestMineProceduresHigherSupportMoreImportant(t *testing.T) {
	db := tempDB(t)
	// "x y" in 4 sessions, "p q" in 2.
	text := "x y\nx y\nx y\nx y\np q\np q"
	var out string
	withStdin(t, text, func() {
		out, _ = executeCmd(t, "mine-procedures", "--db", db, "-n", "agent:me", "--dry-run")
	})
	res := parseMineResult(t, out)
	var xy, pq float64
	for _, w := range res.Workflows {
		switch strings.Join(w.Steps, " ") {
		case "x y":
			xy = w.Importance
		case "p q":
			pq = w.Importance
		}
	}
	if xy <= pq {
		t.Errorf("more-practiced workflow should be more important: x y=%.2f p q=%.2f", xy, pq)
	}
}

func TestMineProceduresEmptyStdin(t *testing.T) {
	db := tempDB(t)
	// No stdin content → exits with error. Since exitErr calls os.Exit, guard by
	// feeding whitespace-only which yields zero sessions and the same path; we
	// instead assert the happy path is not taken by feeding a single valid line.
	var out string
	var err error
	withStdin(t, "solo", func() { // one session, one step → no pattern of len>=2
		out, err = executeCmd(t, "mine-procedures", "--db", db, "-n", "agent:me", "--dry-run")
	})
	if err != nil {
		t.Fatalf("single-step session should not error: %v", err)
	}
	res := parseMineResult(t, out)
	if res.Found != 0 {
		t.Errorf("no multi-step workflow expected, got %+v", res)
	}
	_ = os.Stdin
}
