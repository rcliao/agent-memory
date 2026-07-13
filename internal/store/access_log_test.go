package store

import (
	"context"
	"testing"
	"time"

	"github.com/rcliao/ghost/internal/model"
)

func TestAccessLogWrittenOnGetAndContext(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem, err := s.Put(ctx, PutParams{
		NS: "agent:test", Key: "fact", Content: "The staging cluster runs on port 8443.",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	if _, err := s.Get(ctx, GetParams{NS: "agent:test", Key: "fact"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if _, err := s.Context(ctx, ContextParams{NS: "agent:test", Query: "staging cluster port", Budget: 500}); err != nil {
		t.Fatalf("context: %v", err)
	}

	times, err := s.loadAccessTimes(ctx, []string{mem.ID})
	if err != nil {
		t.Fatalf("load access times: %v", err)
	}
	if got := len(times[mem.ID]); got < 2 {
		t.Errorf("expected >=2 logged accesses (get + context touch), got %d", got)
	}
}

func TestAccessLogRespectsSensoryExclusion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem, err := s.Put(ctx, PutParams{
		NS: "agent:test", Key: "chatter", Content: "The deploy pipeline hiccuped this morning.", Tier: "sensory",
	})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	// Context touch must not log sensory memories (same rule as access_count).
	if err := s.touchMemories(ctx, []string{mem.ID}); err != nil {
		t.Fatalf("touch: %v", err)
	}
	times, err := s.loadAccessTimes(ctx, []string{mem.ID})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(times[mem.ID]) != 0 {
		t.Errorf("sensory memory got %d access-log rows from context touch, want 0", len(times[mem.ID]))
	}
}

func TestAccessLogDisabledByEnv(t *testing.T) {
	t.Setenv("GHOST_ACCESS_LOG", "0")
	s := newTestStore(t)
	ctx := context.Background()

	mem, err := s.Put(ctx, PutParams{NS: "agent:test", Key: "fact", Content: "Backups run nightly at 2am."})
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := s.Get(ctx, GetParams{NS: "agent:test", Key: "fact"}); err != nil {
		t.Fatalf("get: %v", err)
	}
	times, err := s.loadAccessTimes(ctx, []string{mem.ID})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(times[mem.ID]) != 0 {
		t.Errorf("GHOST_ACCESS_LOG=0 still logged %d rows", len(times[mem.ID]))
	}
}

func TestAccessLogPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mem, err := s.Put(ctx, PutParams{NS: "agent:test", Key: "fact", Content: "Old but touched constantly."})
	if err != nil {
		t.Fatalf("put: %v", err)
	}

	// Insert rows past retention and beyond the per-memory cap.
	stale := time.Now().UTC().Add(-accessLogRetention - 24*time.Hour).Format(time.RFC3339)
	if _, err := s.db.Exec(
		`INSERT INTO memory_accesses (memory_id, accessed_at) VALUES (?, ?)`, mem.ID, stale); err != nil {
		t.Fatalf("insert stale: %v", err)
	}
	for i := 0; i < accessLogMaxPerMemory+10; i++ {
		at := time.Now().UTC().Add(-time.Duration(i) * time.Minute).Format(time.RFC3339)
		if _, err := s.db.Exec(
			`INSERT INTO memory_accesses (memory_id, accessed_at) VALUES (?, ?)`, mem.ID, at); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// Orphan row for a hard-deleted memory.
	if _, err := s.db.Exec(
		`INSERT INTO memory_accesses (memory_id, accessed_at) VALUES ('ghost-orphan', ?)`,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert orphan: %v", err)
	}

	pruned, err := s.pruneAccessLog(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if pruned < 11 { // 1 stale + 10 over cap + 1 orphan, minus overlap slack
		t.Errorf("pruned %d rows, expected at least 11", pruned)
	}

	times, err := s.loadAccessTimes(ctx, []string{mem.ID})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := len(times[mem.ID]); got > accessLogMaxPerMemory {
		t.Errorf("per-memory cap not enforced: %d rows remain (cap %d)", got, accessLogMaxPerMemory)
	}
	var orphans int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM memory_accesses WHERE memory_id = 'ghost-orphan'`).Scan(&orphans); err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Errorf("orphan rows survived prune: %d", orphans)
	}
}

func memWithAccess(created time.Time, count int, last *time.Time) model.Memory {
	return model.Memory{CreatedAt: created, AccessCount: count, LastAccessedAt: last}
}

// TestACTRExactBeatsApproximationOnBurst verifies the point of the log: a
// burst of recent accesses (invisible to the uniform-spacing approximation,
// which spreads them over the memory's whole lifetime) must raise exact
// activation above the approximation's estimate.
func TestACTRExactBeatsApproximationOnBurst(t *testing.T) {
	now := time.Now()
	created := now.Add(-90 * 24 * time.Hour) // 90 days old

	// 10 accesses, all within the last hour (a hot burst on an old memory).
	var burst []time.Time
	for i := 0; i < 10; i++ {
		burst = append(burst, now.Add(-time.Duration(i+1)*5*time.Minute))
	}
	exact := actrActivationExact(created, burst, now)

	// The approximation only sees n=10 spread across 90 days.
	last := now.Add(-5 * time.Minute)
	approx := actrActivation(memWithAccess(created, 10, &last), now)

	if exact <= approx {
		t.Errorf("recent burst should out-activate uniform spread: exact %.4f <= approx %.4f", exact, approx)
	}
}
