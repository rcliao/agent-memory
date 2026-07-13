package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Per-access log (memory_accesses table).
//
// access_count + last_accessed_at compress a memory's whole retrieval history
// into two numbers, which is what forced the ACT-R prototype into a uniform-
// spacing approximation (measured below baseline — see actr.go) and blocks the
// roadmap's B3 grounded-recall→utility feedback loop. This table keeps the
// actual access timestamps: one row per (memory, retrieval), written wherever
// access_count is bumped for a memory (Get, context touch). Rows are pruned by
// GC after accessLogRetention and capped per memory, so the table stays small
// relative to chunks/embeddings.
//
// Logging is on by default (append-only, no read-path effect); set
// GHOST_ACCESS_LOG=0 to disable writes.

const (
	// accessLogRetention is how long access rows are kept before GC prunes them.
	accessLogRetention = 120 * 24 * time.Hour
	// accessLogMaxPerMemory caps rows per memory; the oldest beyond the cap are
	// pruned by GC. ACT-R's t^-d weighting makes ancient accesses negligible.
	accessLogMaxPerMemory = 100
)

func accessLogEnabled() bool { return envStr("GHOST_ACCESS_LOG", "1") != "0" }

// logAccesses appends one row per memory ID at the store's current clock.
// Best-effort: failures are ignored, retrieval never breaks on logging.
func (s *SQLiteStore) logAccesses(ctx context.Context, ids []string) {
	if !accessLogEnabled() || len(ids) == 0 {
		return
	}
	now := s.now().UTC().Format(time.RFC3339)
	var sb strings.Builder
	sb.WriteString(`INSERT INTO memory_accesses (memory_id, accessed_at) VALUES `)
	args := make([]interface{}, 0, len(ids)*2)
	for i, id := range ids {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("(?, ?)")
		args = append(args, id, now)
	}
	_, _ = s.db.ExecContext(ctx, sb.String(), args...)
}

// loadAccessTimes returns the logged access timestamps (newest first) for the
// given memory IDs. Memories with no rows are absent from the map.
func (s *SQLiteStore) loadAccessTimes(ctx context.Context, ids []string) (map[string][]time.Time, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT memory_id, accessed_at FROM memory_accesses
		 WHERE memory_id IN (%s) ORDER BY accessed_at DESC`, placeholders), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string][]time.Time{}
	for rows.Next() {
		var id, at string
		if err := rows.Scan(&id, &at); err != nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, at)
		if err != nil {
			continue
		}
		out[id] = append(out[id], t)
	}
	return out, rows.Err()
}

// pruneAccessLog removes rows past retention, rows beyond the per-memory cap,
// and rows orphaned by hard-deleted memories. Returns rows removed.
func (s *SQLiteStore) pruneAccessLog(ctx context.Context) (int64, error) {
	cutoff := s.now().UTC().Add(-accessLogRetention).Format(time.RFC3339)
	var total int64

	res, err := s.db.ExecContext(ctx,
		`DELETE FROM memory_accesses WHERE accessed_at < ?`, cutoff)
	if err != nil {
		return total, err
	}
	if n, err := res.RowsAffected(); err == nil {
		total += n
	}

	res, err = s.db.ExecContext(ctx, `
		DELETE FROM memory_accesses WHERE rowid IN (
			SELECT rowid FROM (
				SELECT rowid, ROW_NUMBER() OVER (
					PARTITION BY memory_id ORDER BY accessed_at DESC
				) AS rn FROM memory_accesses
			) WHERE rn > ?
		)`, accessLogMaxPerMemory)
	if err != nil {
		return total, err
	}
	if n, err := res.RowsAffected(); err == nil {
		total += n
	}

	res, err = s.db.ExecContext(ctx, `
		DELETE FROM memory_accesses WHERE memory_id NOT IN (SELECT id FROM memories)`)
	if err != nil {
		return total, err
	}
	if n, err := res.RowsAffected(); err == nil {
		total += n
	}
	return total, nil
}
