package store

import (
	"context"
	"testing"
	"time"
)

// A memory's rehearsal trace must survive a rewrite of its own key.
//
// Promotion to ltm requires >= 3 distinct access DAYS, counted over
// memory_accesses which is keyed by memory_id — and every version gets a
// fresh ULID. Before this was fixed, rewriting a memory reset its rehearsal
// history to zero, which silently blocks restatement aggregation: merging N
// sibling rewordings into one canonical key is only a win if the survivor
// keeps the credit the siblings earned.
func TestRehearsalSurvivesVersioning(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	base := evalClockEpoch
	clock := base
	s.SetClock(func() time.Time { return clock })

	const ns, key = "agent:rehearsal", "standing-rule"
	mem, err := s.Put(ctx, PutParams{NS: ns, Key: key,
		Content: "Keep replies short and gentle in our chats.",
		Kind:    "semantic", Tier: "stm", Importance: 0.6})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	// Retrieved on three distinct days — the spaced-rehearsal signal.
	// Mirror the retrieval path, which both logs the access and bumps the
	// ambient counter.
	for d := 0; d < 3; d++ {
		clock = base.Add(time.Duration(d) * 24 * time.Hour)
		s.logAccesses(ctx, []string{mem.ID})
		s.db.Exec(`UPDATE memories SET access_count = access_count + 1 WHERE id = ?`, mem.ID)
	}
	before, err := s.loadSpacedAccessDays(ctx, ns)
	if err != nil {
		t.Fatalf("distinct days: %v", err)
	}
	if before[mem.ID] < 3 {
		t.Fatalf("setup: expected 3 distinct access days, got %d", before[mem.ID])
	}

	// The aggregation move: rewrite the slot with a consolidated wording.
	clock = base.Add(4 * 24 * time.Hour)
	rewritten, err := s.Put(ctx, PutParams{NS: ns, Key: key,
		Content: "Keep replies short and gentle — one consolidated rule.",
		Kind:    "semantic", Tier: "stm", Importance: 0.6})
	if err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if rewritten.ID == mem.ID {
		t.Fatalf("expected a new version row")
	}

	after, err := s.loadSpacedAccessDays(ctx, ns)
	if err != nil {
		t.Fatalf("distinct days after: %v", err)
	}
	if after[rewritten.ID] < 3 {
		t.Errorf("rehearsal trace lost across versioning: %d distinct access days after rewrite, want >= 3 (promotion gate)",
			after[rewritten.ID])
	}

	var ac int
	s.db.QueryRow(`SELECT access_count FROM memories WHERE id = ?`, rewritten.ID).Scan(&ac)
	if ac < 3 {
		t.Errorf("access_count reset across versioning: got %d, want >= 3", ac)
	}

	// Moved, not copied — repeated rewrites must not grow the log.
	var total int
	s.db.QueryRow(`SELECT COUNT(*) FROM memory_accesses WHERE memory_id = ?`, mem.ID).Scan(&total)
	if total != 0 {
		t.Errorf("old version still holds %d access rows — rows should move, not copy", total)
	}
}
