package store

import (
	"context"
	"testing"
)

// Write-time triage v1: REINFORCE (assimilation). The parenting kit showed
// that a lesson restated in different words becomes N sibling memories, each
// with too little spaced rehearsal to promote — repetition doesn't aggregate.
// With Dedup enabled, a put whose content is a paraphrase of an existing live
// memory (high bidirectional distinctive-term overlap; cosine when embedded)
// must STRENGTHEN that memory instead of creating a sibling: one rehearsal
// access is logged (being told twice = rehearsed twice — feeds the
// spaced-promotion gate) and importance takes the max of the two.
func TestEvalWriteTriageReinforce(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	orig, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "pref-short-answers",
		Content: "I always prefer when you keep your answers short and gentle in our chats.",
		Kind:    "semantic", Tier: "stm", Importance: 0.5})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	// A paraphrase of the same lesson, as capture would distill it next week.
	got, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "pref-short-answers-b",
		Content: "Remember, I always prefer short and gentle answers from you in our chats.",
		Kind:    "semantic", Tier: "stm", Importance: 0.65, Dedup: true})
	if err != nil {
		t.Fatalf("paraphrase put: %v", err)
	}
	if got.ID != orig.ID {
		t.Fatalf("paraphrase created a sibling (id %s) instead of reinforcing %s", got.ID, orig.ID)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND ns = 'agent:t'`).Scan(&n)
	if n != 1 {
		t.Fatalf("want 1 live memory after reinforcement, got %d", n)
	}
	// Reinforcement = a logged rehearsal (spaced-promotion fuel)...
	var accesses int
	s.db.QueryRow(`SELECT COUNT(*) FROM memory_accesses WHERE memory_id = ?`, orig.ID).Scan(&accesses)
	if accesses == 0 {
		t.Error("reinforcement did not log a rehearsal access")
	}
	// ...and importance keeps the stronger evidence.
	var imp float64
	s.db.QueryRow(`SELECT importance FROM memories WHERE id = ?`, orig.ID).Scan(&imp)
	if imp < 0.65 {
		t.Errorf("importance %.2f — should keep the max of old/new (0.65)", imp)
	}

	// A genuinely different fact must NOT reinforce — precision guard: losing
	// a new fact to a false merge is worse than a duplicate.
	other, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "fact-dinner",
		Content: "We always order the mushroom soup and garlic bread at Verano's on Fridays.",
		Kind:    "semantic", Tier: "stm", Importance: 0.5, Dedup: true})
	if err != nil {
		t.Fatalf("other put: %v", err)
	}
	if other.ID == orig.ID {
		t.Fatal("unrelated fact was falsely reinforced onto the preference memory")
	}
	// Template-sharing but materially different content must stay separate
	// (the meal-memo shape: same scaffold, different day and payload).
	m1, _ := s.Put(ctx, PutParams{NS: "agent:t", Key: "lunch-0713",
		Content: "Lunch memo 7/13: rice bowl with beef, pickled cucumbers, and barley tea.",
		Kind:    "episodic", Tier: "stm", Dedup: true})
	m2, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "lunch-0714",
		Content: "Lunch memo 7/14: rice bowl with chicken, seaweed salad, and barley tea.",
		Kind:    "episodic", Tier: "stm", Dedup: true})
	if err != nil {
		t.Fatalf("memo put: %v", err)
	}
	if m2.ID == m1.ID {
		t.Fatal("different days' memos falsely merged")
	}
}
