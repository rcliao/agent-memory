package store

import (
	"context"
	"testing"
	"time"
)

// TestPurgeDeletedRemovesDeadRowsOnly verifies purge removes soft-deleted rows
// and their chunks/edges while leaving live memories (and retrieval) intact.
func TestPurgeDeletedRemovesDeadRows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Two versions of the same key: v1 becomes soft-deleted, v2 stays live.
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "k", Content: "version one content", Kind: "semantic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "k", Content: "version two content", Kind: "semantic"}); err != nil {
		t.Fatal(err)
	}
	// A standalone soft-deleted memory.
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "gone", Content: "to be removed", Kind: "semantic"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rm(ctx, RmParams{NS: "t", Key: "gone"}); err != nil {
		t.Fatal(err)
	}

	var deletedBefore int
	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NOT NULL`).Scan(&deletedBefore)
	if deletedBefore < 2 {
		t.Fatalf("expected >=2 soft-deleted rows (v1 + gone), got %d", deletedBefore)
	}

	dry, err := s.PurgeDeletedDryRun(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if dry.MemoriesPurged != int64(deletedBefore) {
		t.Errorf("dry-run count %d != soft-deleted %d", dry.MemoriesPurged, deletedBefore)
	}

	r, err := s.PurgeDeleted(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.MemoriesPurged != int64(deletedBefore) {
		t.Errorf("purged %d, expected %d", r.MemoriesPurged, deletedBefore)
	}

	// No soft-deleted rows remain; live memory 'k' still retrievable at v2.
	var deletedAfter int
	s.db.QueryRow(`SELECT COUNT(*) FROM memories WHERE deleted_at IS NOT NULL`).Scan(&deletedAfter)
	if deletedAfter != 0 {
		t.Errorf("expected 0 soft-deleted rows after purge, got %d", deletedAfter)
	}
	got, err := s.Get(ctx, GetParams{NS: "t", Key: "k"})
	if err != nil || len(got) == 0 || got[0].Content != "version two content" {
		t.Errorf("live memory should survive purge intact, got %+v (err %v)", got, err)
	}
	// No orphaned chunks left behind.
	var orphanChunks int
	s.db.QueryRow(`SELECT COUNT(*) FROM chunks c WHERE NOT EXISTS (SELECT 1 FROM memories m WHERE m.id=c.memory_id)`).Scan(&orphanChunks)
	if orphanChunks != 0 {
		t.Errorf("expected no orphaned chunks after purge, got %d", orphanChunks)
	}
}

// TestPurgeDeletedRespectsAge only removes rows older than the cutoff.
func TestPurgeDeletedRespectsAge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "recent", Content: "recently deleted", Kind: "semantic"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Rm(ctx, RmParams{NS: "t", Key: "recent"}); err != nil {
		t.Fatal(err)
	}
	// Purge only rows deleted >24h ago — the just-deleted one must survive.
	r, err := s.PurgeDeleted(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if r.MemoriesPurged != 0 {
		t.Errorf("recently-deleted row should not be purged by 24h cutoff, purged %d", r.MemoriesPurged)
	}
}
