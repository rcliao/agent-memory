package store

import (
	"context"
	"fmt"
	"strings"
)

// Supersede guard: graph knowledge outranks the reranker.
//
// The freshness detector records "A supersedes B" as a contradicts edge
// (A → B, A newer). The cross-encoder scores topical match only and is blind
// to that — and questions are usually phrased in the OLD fact's vocabulary
// ("what do we use to bundle" matches "We use Webpack to bundle..." better
// than "We switched to Vite for bundling"), so a pure reranker resurfaces
// obsolete facts above their successors. Measured on the lifecycle eval:
// FAMA fresh-wins 0.67 → 0.33 with the reranker on, before this guard.
//
// enforceSupersedeOrder runs after ranking: for every contradicts pair where
// both sides were retrieved and the superseded memory ranks above its
// successor, the successor is moved directly in front of it. Relative order
// of everything else is preserved. The superseded fact stays in the results —
// transparency is the point of contradicts edges — it just can't outrank the
// current truth.
func (s *SQLiteStore) enforceSupersedeOrder(ctx context.Context, results []SearchResult) []SearchResult {
	if len(results) < 2 {
		return results
	}
	pos := make(map[string]int, len(results))
	var ids []string
	for i, r := range results {
		pos[r.ID] = i
		ids = append(ids, r.ID)
	}

	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, 0, len(ids)*2)
	for _, id := range ids {
		args = append(args, id)
	}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT from_id, to_id FROM memory_edges
		 WHERE rel = 'contradicts' AND from_id IN (%s) AND to_id IN (%s)`,
		placeholders, placeholders), args...)
	if err != nil {
		return results
	}
	defer rows.Close()

	type pair struct{ newer, older string }
	var pairs []pair
	for rows.Next() {
		var from, to string
		if rows.Scan(&from, &to) == nil {
			pairs = append(pairs, pair{newer: from, older: to})
		}
	}
	if len(pairs) == 0 {
		return results
	}

	// Iterate to a fixed point (supersede chains: v3 → v2 → v1).
	for iter := 0; iter < len(pairs)+1; iter++ {
		moved := false
		for _, p := range pairs {
			ni, okN := pos[p.newer]
			oi, okO := pos[p.older]
			if !okN || !okO || ni < oi {
				continue
			}
			// Move the newer memory to directly before the older one.
			newer := results[ni]
			copy(results[oi+1:ni+1], results[oi:ni])
			results[oi] = newer
			for i := oi; i <= ni; i++ {
				pos[results[i].ID] = i
			}
			moved = true
		}
		if !moved {
			break
		}
	}
	return results
}
