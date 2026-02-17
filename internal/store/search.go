package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rcliao/agent-memory/internal/model"
)

// SearchParams holds parameters for searching memories.
type SearchParams struct {
	NS    string
	Query string
	Kind  string
	Limit int
}

// SearchResult wraps a memory with optional chunk match info.
type SearchResult struct {
	model.Memory
	MatchChunk *model.Chunk `json:"match_chunk,omitempty"`
}

// Search finds memories whose content or chunks match the query substring.
func (s *SQLiteStore) Search(ctx context.Context, p SearchParams) ([]SearchResult, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	now := time.Now().UTC().Format(time.RFC3339)
	where := []string{"m.deleted_at IS NULL", "(m.expires_at IS NULL OR m.expires_at > ?)"}
	args := []interface{}{now}

	if p.NS != "" {
		where = append(where, "m.ns = ?")
		args = append(args, p.NS)
	}
	if p.Kind != "" {
		where = append(where, "m.kind = ?")
		args = append(args, p.Kind)
	}

	// Try FTS5 first for ranked results, fall back to LIKE for simple substrings
	// FTS5 query: split terms and join with AND for better matching
	ftsQuery := strings.Join(strings.Fields(p.Query), " AND ")

	sql := fmt.Sprintf(`
		SELECT m.id, m.ns, m.key, m.content, m.kind, m.tags, m.version, m.supersedes,
		       m.created_at, m.deleted_at, m.priority, m.access_count, m.last_accessed_at, m.meta, m.expires_at
		FROM memories m
		INNER JOIN (
			SELECT ns, key, MAX(version) AS max_ver
			FROM memories WHERE deleted_at IS NULL
			GROUP BY ns, key
		) latest ON m.ns = latest.ns AND m.key = latest.key AND m.version = latest.max_ver
		INNER JOIN chunks c ON c.memory_id = m.id
		INNER JOIN chunks_fts fts ON c.rowid = fts.rowid
		WHERE %s AND chunks_fts MATCH ?
		GROUP BY m.id
		ORDER BY
			(CASE m.priority WHEN 'critical' THEN 4 WHEN 'high' THEN 3 WHEN 'normal' THEN 2 ELSE 1 END) * 0.2
			+ (julianday(m.created_at) / julianday('now')) * 0.3
			+ (MIN(fts.rank) * -0.5)
			DESC
		LIMIT ?`, strings.Join(where, " AND "))

	args = append(args, ftsQuery, limit)

	// Try FTS5 first; on error fall back to LIKE entirely
	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return s.searchLike(ctx, p, where, limit)
	}
	defer rows.Close()

	var results []SearchResult
	seen := map[string]bool{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		results = append(results, SearchResult{Memory: m})
	}

	// Supplement with LIKE matches (catches key matches and content that FTS5 tokenizer misses)
	if len(results) < limit {
		likeResults, err := s.searchLike(ctx, p, where, limit-len(results))
		if err == nil {
			for _, r := range likeResults {
				if !seen[r.ID] {
					seen[r.ID] = true
					results = append(results, r)
				}
			}
		}
	}

	return results, nil
}

// searchLike is the fallback when FTS5 fails.
func (s *SQLiteStore) searchLike(ctx context.Context, p SearchParams, baseWhere []string, limit int) ([]SearchResult, error) {
	likeQuery := "%" + p.Query + "%"
	now := time.Now().UTC().Format(time.RFC3339)
	where := []string{"m.deleted_at IS NULL", "(m.expires_at IS NULL OR m.expires_at > ?)"}
	args := []interface{}{now}

	if p.NS != "" {
		where = append(where, "m.ns = ?")
		args = append(args, p.NS)
	}
	if p.Kind != "" {
		where = append(where, "m.kind = ?")
		args = append(args, p.Kind)
	}
	_ = baseWhere // we rebuild where clauses here

	sql := fmt.Sprintf(`
		SELECT DISTINCT m.id, m.ns, m.key, m.content, m.kind, m.tags, m.version, m.supersedes,
		       m.created_at, m.deleted_at, m.priority, m.access_count, m.last_accessed_at, m.meta, m.expires_at
		FROM memories m
		INNER JOIN (
			SELECT ns, key, MAX(version) AS max_ver
			FROM memories WHERE deleted_at IS NULL
			GROUP BY ns, key
		) latest ON m.ns = latest.ns AND m.key = latest.key AND m.version = latest.max_ver
		LEFT JOIN chunks c ON c.memory_id = m.id
		WHERE %s AND (m.content LIKE ? OR m.key LIKE ? OR c.text LIKE ?)
		ORDER BY m.created_at DESC
		LIMIT ?`, strings.Join(where, " AND "))

	args = append(args, likeQuery, likeQuery, likeQuery, limit)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	seen := map[string]bool{}
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		results = append(results, SearchResult{Memory: m})
	}
	return results, nil
}
