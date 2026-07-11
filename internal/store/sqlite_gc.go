package store

import (
	"context"
	"fmt"
	"time"
)

// GCResult holds the result of an expired-memory garbage collection.
type GCResult struct {
	MemoriesDeleted int64
	ChunksFreed     int64
}

// GCStaleResult holds the result of a stale-memory garbage collection.
type GCStaleResult struct {
	MemoriesDeleted int64
	ProtectedCount  int64
}

// PurgeResult holds the result of purging soft-deleted memories.
type PurgeResult struct {
	MemoriesPurged int64
	ChunksFreed    int64
}

// purgeSelector matches soft-deleted memories older than the cutoff.
const purgeSelector = `SELECT id FROM memories WHERE deleted_at IS NOT NULL AND deleted_at <= ?`

// PurgeDeletedDryRun counts soft-deleted memories (and their chunks) older than
// olderThan without deleting anything. olderThan == 0 counts all soft-deleted.
func (s *SQLiteStore) PurgeDeletedDryRun(ctx context.Context, olderThan time.Duration) (PurgeResult, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var r PurgeResult
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, cutoff).Scan(&r.MemoriesPurged); err != nil {
		return r, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chunks WHERE memory_id IN (`+purgeSelector+`)`, cutoff).Scan(&r.ChunksFreed); err != nil {
		return r, err
	}
	return r, nil
}

// PurgeDeleted permanently removes soft-deleted memories older than olderThan
// (0 = all soft-deleted) plus their chunks, edges, links, and file refs. Live
// memories are never touched — retrieval always joins on the latest live version,
// so results are unaffected; only dead rows and their orphaned embeddings are
// reclaimed. Run Vacuum afterward to shrink the file.
func (s *SQLiteStore) PurgeDeleted(ctx context.Context, olderThan time.Duration) (PurgeResult, error) {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	var r PurgeResult

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()

	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chunks WHERE memory_id IN (`+purgeSelector+`)`, cutoff).Scan(&r.ChunksFreed); err != nil {
		return r, err
	}

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memory_edges WHERE from_id IN (`+purgeSelector+`) OR to_id IN (`+purgeSelector+`)`, cutoff, cutoff); err != nil {
		return r, fmt.Errorf("purge edges: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memory_links WHERE from_id IN (`+purgeSelector+`) OR to_id IN (`+purgeSelector+`)`, cutoff, cutoff); err != nil {
		return r, fmt.Errorf("purge links: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM memory_files WHERE memory_id IN (`+purgeSelector+`)`, cutoff); err != nil {
		return r, fmt.Errorf("purge file refs: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM chunks WHERE memory_id IN (`+purgeSelector+`)`, cutoff); err != nil {
		return r, fmt.Errorf("purge chunks: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		`DELETE FROM memories WHERE deleted_at IS NOT NULL AND deleted_at <= ?`, cutoff)
	if err != nil {
		return r, fmt.Errorf("purge memories: %w", err)
	}
	r.MemoriesPurged, _ = res.RowsAffected()

	if err := tx.Commit(); err != nil {
		return r, err
	}
	return r, nil
}

// Vacuum rebuilds the database file to reclaim space freed by deletes. Cannot
// run inside a transaction.
func (s *SQLiteStore) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `VACUUM`)
	return err
}

// GCStaleDryRun counts stale memories (not accessed within the given duration)
// without deleting them. Memories with priority "high" or "critical" are skipped.
func (s *SQLiteStore) GCStaleDryRun(ctx context.Context, staleThreshold time.Duration) (GCStaleResult, error) {
	cutoff := time.Now().UTC().Add(-staleThreshold).Format(time.RFC3339)
	var result GCStaleResult

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories
		 WHERE deleted_at IS NULL
		   AND priority NOT IN ('high', 'critical')
		   AND COALESCE(last_accessed_at, created_at) < ?`, cutoff).Scan(&result.MemoriesDeleted)
	if err != nil {
		return result, err
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories
		 WHERE deleted_at IS NULL
		   AND priority IN ('high', 'critical')
		   AND COALESCE(last_accessed_at, created_at) < ?`, cutoff).Scan(&result.ProtectedCount)
	if err != nil {
		return result, err
	}

	return result, nil
}

// GCStale soft-deletes memories not accessed within the given duration.
// Memories with priority "high" or "critical" are skipped.
func (s *SQLiteStore) GCStale(ctx context.Context, staleThreshold time.Duration) (GCStaleResult, error) {
	now := time.Now().UTC()
	cutoff := now.Add(-staleThreshold).Format(time.RFC3339)
	nowStr := now.Format(time.RFC3339)
	var result GCStaleResult

	// Count protected memories (high/critical that are stale but skipped)
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories
		 WHERE deleted_at IS NULL
		   AND priority IN ('high', 'critical')
		   AND COALESCE(last_accessed_at, created_at) < ?`, cutoff).Scan(&result.ProtectedCount)
	if err != nil {
		return result, err
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE memories SET deleted_at = ?
		 WHERE deleted_at IS NULL
		   AND priority NOT IN ('high', 'critical')
		   AND COALESCE(last_accessed_at, created_at) < ?`, nowStr, cutoff)
	if err != nil {
		return result, fmt.Errorf("soft-delete stale memories: %w", err)
	}

	result.MemoriesDeleted, err = res.RowsAffected()
	if err != nil {
		return result, err
	}

	// Also prune low-utility memories: accessed 5+ times but useful <20%
	utilRes, err := s.db.ExecContext(ctx,
		`UPDATE memories SET deleted_at = ?
		 WHERE deleted_at IS NULL
		   AND access_count >= 5
		   AND utility_count > 0
		   AND CAST(utility_count AS REAL) / CAST(access_count AS REAL) < 0.2`, nowStr)
	if err == nil {
		n, _ := utilRes.RowsAffected()
		result.MemoriesDeleted += n
	}

	return result, nil
}

// GCDryRun counts expired memories and their chunks without deleting.
func (s *SQLiteStore) GCDryRun(ctx context.Context) (GCResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var result GCResult

	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?`, now).Scan(&result.MemoriesDeleted)
	if err != nil {
		return result, err
	}

	err = s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chunks WHERE memory_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		)`, now).Scan(&result.ChunksFreed)
	if err != nil {
		return result, err
	}

	return result, nil
}

// GC deletes expired memories (where expires_at < now) and their chunks.
func (s *SQLiteStore) GC(ctx context.Context) (GCResult, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var result GCResult

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	// Count chunks to be deleted
	err = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM chunks WHERE memory_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		)`, now).Scan(&result.ChunksFreed)
	if err != nil {
		return result, err
	}

	// Delete edges referencing expired memories
	_, err = tx.ExecContext(ctx,
		`DELETE FROM memory_edges WHERE from_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		) OR to_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		)`, now, now)
	if err != nil {
		return result, fmt.Errorf("delete expired edges: %w", err)
	}

	// Delete links referencing expired memories (FK would otherwise block the memory delete)
	_, err = tx.ExecContext(ctx,
		`DELETE FROM memory_links WHERE from_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		) OR to_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		)`, now, now)
	if err != nil {
		return result, fmt.Errorf("delete expired links: %w", err)
	}

	// Delete file refs belonging to expired memories (FK would otherwise block the memory delete)
	_, err = tx.ExecContext(ctx,
		`DELETE FROM memory_files WHERE memory_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		)`, now)
	if err != nil {
		return result, fmt.Errorf("delete expired file refs: %w", err)
	}

	// Delete chunks belonging to expired memories
	_, err = tx.ExecContext(ctx,
		`DELETE FROM chunks WHERE memory_id IN (
			SELECT id FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?
		)`, now)
	if err != nil {
		return result, fmt.Errorf("delete expired chunks: %w", err)
	}

	// Delete expired memories
	res, err := tx.ExecContext(ctx,
		`DELETE FROM memories WHERE expires_at IS NOT NULL AND expires_at < ?`, now)
	if err != nil {
		return result, fmt.Errorf("delete expired memories: %w", err)
	}

	result.MemoriesDeleted, err = res.RowsAffected()
	if err != nil {
		return result, err
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}

	return result, nil
}

// MemoryCount returns the number of active (non-deleted, non-expired) memories.
func (s *SQLiteStore) MemoryCount(ctx context.Context) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	var count int64
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, now).Scan(&count)
	return count, err
}
