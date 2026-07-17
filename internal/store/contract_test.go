package store

import (
	"context"
	"path/filepath"
	"testing"
)

// Store-integrity contract (shell wiring asks, 2026-07-14/15):
//
// 1. VERSION HANDSHAKE — lock enforcement is library-level, so a stale ghost
//    binary silently bypasses it (live near-miss: a pre-#37 CLI overwrote the
//    locked identity-preamble). The DB now records a contract version
//    (PRAGMA user_version); a binary whose supported contract is OLDER than
//    the DB's must refuse writes.
// 2. TAG-OP GATING — RemoveTag/RenameTag could strip the `locked` tag itself,
//    the one remaining bypass of the read-only bit.
func TestContractVersionHandshake(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "c.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// A fresh store stamps its contract version.
	var uv int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&uv); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if uv != storeContractVersion {
		t.Fatalf("fresh DB user_version = %d, want %d", uv, storeContractVersion)
	}

	// Simulate a FUTURE DB (newer contract than this binary understands).
	if _, err := s.db.Exec(`PRAGMA user_version = 9999`); err != nil {
		t.Fatalf("bump: %v", err)
	}
	s.Close()

	s2, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	// Reads still work (an old binary may inspect)…
	if _, err := s2.List(ctx, ListParams{NS: "agent:t"}); err != nil {
		t.Errorf("read on newer-contract DB should work: %v", err)
	}
	// …but writes are refused: the binary cannot uphold guarantees it
	// doesn't know about.
	if _, err := s2.Put(ctx, PutParams{NS: "agent:t", Key: "x", Content: "should be refused"}); err == nil {
		t.Fatal("write on newer-contract DB succeeded — stale binaries can bypass newer guarantees")
	}
}

func TestLockedTagOpsGated(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "charter",
		Content: "I am Nova. Family: Alex and Sam.", Tags: []string{"identity", LockedTag}, Pinned: true}); err != nil {
		t.Fatalf("put: %v", err)
	}

	// Removing the locked tag itself must be refused.
	if _, err := s.RemoveTag(ctx, LockedTag, "agent:t"); err == nil {
		t.Fatal("RemoveTag stripped the locked bit — read-only bypass")
	}
	// Renaming it away likewise.
	if _, err := s.RenameTag(ctx, LockedTag, "unlocked", "agent:t"); err == nil {
		t.Fatal("RenameTag renamed the locked bit away — read-only bypass")
	}
	// Ordinary tag ops on other tags still work.
	if _, err := s.RenameTag(ctx, "identity", "persona", "agent:t"); err != nil {
		t.Fatalf("ordinary rename should work: %v", err)
	}
}

// Curate is MCP-exposed — an agent must not be able to curate away its own
// locked charter.
func TestLockedCurateGated(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.Put(ctx, PutParams{NS: "agent:t", Key: "charter",
		Content: "I am Nova. Family: Alex and Sam.", Tags: []string{LockedTag}, Pinned: true}); err != nil {
		t.Fatalf("put: %v", err)
	}
	for _, op := range []string{"delete", "demote", "archive", "diminish", "unpin"} {
		if _, err := s.Curate(ctx, CurateParams{NS: "agent:t", Key: "charter", Op: op}); err == nil {
			t.Errorf("curate %s succeeded on a locked key", op)
		}
	}
	// Non-destructive ops still allowed.
	if _, err := s.Curate(ctx, CurateParams{NS: "agent:t", Key: "charter", Op: "boost"}); err != nil {
		t.Errorf("boost on locked key should be allowed: %v", err)
	}
}
