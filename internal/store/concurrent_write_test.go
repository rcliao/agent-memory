package store

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Concurrent writers from separate connections must not lose writes.
//
// The daemon, the CLI and the MCP server all write the same file, and every
// write path here uses BeginTx(ctx, nil) — DEFERRED. A deferred transaction
// takes the write lock lazily, so one that reads before it writes has to
// upgrade; if another writer committed in between, SQLite fails the upgrade
// with an instant SQLITE_BUSY that busy_timeout cannot retry. Callers logged
// that at WARN and moved on, so the memory silently never existed: 22 such
// failures across the two live stores by 2026-08-05, including three distilled
// same-day facts.
//
// The DSN now takes the write lock up front (_txlock=immediate) so the wait
// applies. This test fails loudly if that regresses.
func TestConcurrentWritersDoNotLoseMemories(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const writers, perWriter = 6, 12
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				// Read-then-write in one logical unit: the shape that forces a
				// lock upgrade under a deferred transaction.
				_, _ = s.Search(ctx, SearchParams{NS: "agent:conc", Query: "household", Limit: 5})
				if _, err := s.Put(ctx, PutParams{
					NS:      "agent:conc",
					Key:     fmt.Sprintf("w%d-%d", w, i),
					Content: fmt.Sprintf("Household note %d from writer %d about the kitchen.", i, w),
					Kind:    "semantic", Tier: "stm", Importance: 0.5,
				}); err != nil {
					errs <- fmt.Errorf("w%d-%d: %w", w, i, err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)

	var busy, other int
	for err := range errs {
		if strings.Contains(err.Error(), "database is locked") || strings.Contains(err.Error(), "SQLITE_BUSY") {
			busy++
		} else {
			other++
			t.Errorf("unexpected write error: %v", err)
		}
	}
	if busy > 0 {
		t.Errorf("%d writes lost to SQLITE_BUSY — deferred-transaction upgrade is failing again", busy)
	}

	got, err := s.List(ctx, ListParams{NS: "agent:conc", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != writers*perWriter {
		t.Errorf("stored %d memories, want %d — writes went missing", len(got), writers*perWriter)
	}
}
