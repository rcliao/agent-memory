package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ── Is ghost concurrent-write safe? ──────────────────────────────────
//
// Prompted by the buzz NIP-AE read (docs/research/buzz-nip-ae-memory-
// comparison.md): buzz makes concurrency safety a headline feature (base-hash
// CAS, a dedicated write-conflict exit code). Ghost's production shape is
// genuinely multi-writer — the daemon (capture, reflect, consolidate), the
// MCP subprocess, the CLI and heartbeats all write the same file — so the
// question deserves measured answers, one layer at a time:
//
//	L1  SQLite layer: do concurrent transactions lose or corrupt writes?
//	    Covered since #91 (_txlock=immediate) for different keys on one
//	    connection pool. Extended here to the harder shapes: the SAME key
//	    hammered from one pool, and from TWO separate store instances —
//	    which is the daemon + MCP-subprocess reality.
//	L2  Application invariants under concurrency: version chains must stay
//	    unique and gapless, exactly one live head per key, delete/put races
//	    must converge to a consistent state.
//	L3  Read-modify-write across calls: two agents Get the same memory, both
//	    edit, both Put. UNSAFE BY DESIGN today — last write wins, first edit
//	    is silently buried in history. Documented executable below; the fix
//	    is CAS-on-version (S1 in the buzz comparison doc).
//	L4  Check-then-act inside Put: the dedup probes run OUTSIDE the write
//	    transaction, so two simultaneous Puts of the same content can both
//	    pass the "no similar memory exists" check and both insert. Measured
//	    below.

// liveRows returns (live, total, distinctVersions, maxVersion) for a key.
func liveRows(t *testing.T, s *SQLiteStore, ns, key string) (int, int, int, int) {
	t.Helper()
	var live, total, distinct, maxV int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FILTER (WHERE deleted_at IS NULL), COUNT(*),
		        COUNT(DISTINCT version), COALESCE(MAX(version),0)
		 FROM memories WHERE ns = ? AND key = ?`, ns, key).
		Scan(&live, &total, &distinct, &maxV); err != nil {
		t.Fatal(err)
	}
	return live, total, distinct, maxV
}

// hammerSameKey runs W goroutines x N puts against one key through the given
// stores (round-robin), returning the error count.
func hammerSameKey(t *testing.T, stores []*SQLiteStore, ns, key string, writers, perWriter int) int {
	t.Helper()
	ctx := context.Background()
	var wg sync.WaitGroup
	errCh := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			s := stores[w%len(stores)]
			for i := 0; i < perWriter; i++ {
				if _, err := s.Put(ctx, PutParams{NS: ns, Key: key,
					Content: fmt.Sprintf("Revision by writer %d iteration %d of the shared shopping note.", w, i),
					Kind:    "semantic", Tier: "stm", Importance: 0.5}); err != nil {
					errCh <- err
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	n := 0
	for err := range errCh {
		n++
		t.Logf("  write error: %v", err)
	}
	return n
}

// L1+L2, one connection pool: the same key hammered concurrently must yield a
// unique, gapless version chain with exactly one live head and zero lost
// writes.
func TestConcurrentSameKeyVersionChain(t *testing.T) {
	s := newTestStore(t)
	const writers, perWriter = 8, 5

	errs := hammerSameKey(t, []*SQLiteStore{s}, "agent:conc", "shopping", writers, perWriter)
	live, total, distinct, maxV := liveRows(t, s, "agent:conc", "shopping")
	want := writers * perWriter
	t.Logf("same key, one pool: %d writes -> live=%d total=%d distinctVersions=%d maxVersion=%d errors=%d",
		want, live, total, distinct, maxV, errs)

	if errs > 0 {
		t.Errorf("%d writes failed outright", errs)
	}
	if live != 1 {
		t.Errorf("expected exactly one live head, got %d", live)
	}
	if total != want {
		t.Errorf("lost writes: %d rows for %d puts", total, want)
	}
	if distinct != want || maxV != want {
		t.Errorf("version chain corrupt: %d distinct versions, max %d, want %d gapless — "+
			"concurrent Puts read the same prev version and collided", distinct, maxV, want)
	}
}

// L1+L2, TWO store instances on one file — the daemon + MCP-subprocess shape.
// Separate connection pools mean separate WAL clients; this is where
// busy_timeout and _txlock=immediate either hold or don't.
func TestConcurrentSameKeyTwoStores(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shared.db")
	a, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	const writers, perWriter = 6, 5
	errs := hammerSameKey(t, []*SQLiteStore{a, b}, "agent:conc", "shared-note", writers, perWriter)
	live, total, distinct, maxV := liveRows(t, a, "agent:conc", "shared-note")
	want := writers * perWriter
	t.Logf("same key, two stores: %d writes -> live=%d total=%d distinctVersions=%d maxVersion=%d errors=%d",
		want, live, total, distinct, maxV, errs)

	if errs > 0 {
		t.Errorf("%d cross-store writes failed — busy_timeout should have absorbed contention", errs)
	}
	if live != 1 {
		t.Errorf("expected exactly one live head across stores, got %d", live)
	}
	if total != want || distinct != want || maxV != want {
		t.Errorf("cross-store version chain corrupt: total=%d distinct=%d max=%d want %d",
			total, distinct, maxV, want)
	}
}

// L2: Put racing soft-delete on the same key must converge to a consistent
// state — either a live head (a Put landed last) or none (the delete did) —
// never two live rows, and never an error.
func TestConcurrentPutAndDelete(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "agent:conc", Key: "victim",
		Content: "Original note about the garage code.", Kind: "semantic", Tier: "stm"}); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 40)
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Put(ctx, PutParams{NS: "agent:conc", Key: "victim",
				Content: fmt.Sprintf("Rewrite %d of the garage note.", i), Kind: "semantic", Tier: "stm"}); err != nil {
				errCh <- fmt.Errorf("put: %w", err)
			}
		}(i)
		go func() {
			defer wg.Done()
			if err := s.Rm(ctx, RmParams{NS: "agent:conc", Key: "victim"}); err != nil &&
				!strings.Contains(err.Error(), "not found") {
				errCh <- fmt.Errorf("delete: %w", err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("unexpected: %v", err)
	}

	live, total, _, _ := liveRows(t, s, "agent:conc", "victim")
	t.Logf("put/delete race: live=%d total=%d", live, total)
	if live > 1 {
		t.Errorf("delete/put race left %d live rows for one key", live)
	}
}

// L3: the documented lost update. Two writers read the same version, both
// write; the second silently buries the first. This test PASSES — it is the
// executable record of today's unsafe-by-design behaviour, and the red test
// for CAS-on-version is its inverse: when PutParams grows BaseVersion, flip
// this to assert the stale writer FAILS.
func TestLostUpdateWithoutCAS(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Put(ctx, PutParams{NS: "agent:conc", Key: "plan",
		Content: "Meet at the north entrance at ten.", Kind: "semantic", Tier: "stm"}); err != nil {
		t.Fatal(err)
	}
	// Writer A and writer B both read v1.
	a, _ := s.Get(ctx, GetParams{NS: "agent:conc", Key: "plan"})
	b, _ := s.Get(ctx, GetParams{NS: "agent:conc", Key: "plan"})
	if a[0].Version != 1 || b[0].Version != 1 {
		t.Fatalf("setup: both readers should see v1")
	}
	// A commits an edit; then B commits an edit made against the same v1.
	if _, err := s.Put(ctx, PutParams{NS: "agent:conc", Key: "plan",
		Content: "Meet at the north entrance at ten — gate B, not the main gate.", Kind: "semantic", Tier: "stm"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "agent:conc", Key: "plan",
		Content: "Meet at the north entrance at HALF PAST ten.", Kind: "semantic", Tier: "stm"}); err != nil {
		t.Fatal(err)
	}

	head, _ := s.Get(ctx, GetParams{NS: "agent:conc", Key: "plan"})
	if !strings.Contains(head[0].Content, "HALF PAST") {
		t.Fatalf("expected B's write to win")
	}
	if strings.Contains(head[0].Content, "gate B") {
		t.Fatalf("expected A's edit to be buried")
	}
	t.Log("CONFIRMED: last write wins; A's gate correction is silently buried in history. " +
		"No error, no conflict signal, nothing tells either writer. This is the failure " +
		"CAS-on-version (S1) exists to catch — when it lands, invert this test.")
}

// L4: the dedup check-then-act race. Put's duplicate probes (exact content,
// vector, paraphrase) all run BEFORE the write transaction, so simultaneous
// writers of identical content can each pass "nothing similar exists" and all
// insert. Reporting instrument: it fails only if the race stops being
// measurable while the checks still claim to dedup (i.e. someone should
// delete this test when dedup moves inside the transaction).
func TestConcurrentDedupTOCTOU(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	const writers = 6
	content := "The recycling pickup moved to Thursday mornings from next month."

	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			_, _ = s.Put(ctx, PutParams{NS: "agent:conc", Key: fmt.Sprintf("recycling-%d", w),
				Content: content, Kind: "semantic", Tier: "stm", Dedup: true})
		}(w)
	}
	close(start)
	wg.Wait()

	var stored int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM memories WHERE ns='agent:conc' AND content=? AND deleted_at IS NULL`,
		content).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d simultaneous writers of identical content with Dedup:true -> %d stored", writers, stored)
	if stored > 1 {
		t.Logf("CONFIRMED TOCTOU: the dedup probes run outside the write transaction, so a "+
			"burst of restatements lands %d copies. Bounded severity — it feeds the "+
			"near-duplicate glut rather than corrupting anything — but it means Dedup:true "+
			"is advisory under concurrency, not a guarantee.", stored)
	}
	if stored == 0 {
		t.Error("nothing stored at all — the writers all failed, which is a different bug")
	}
}
