package store

import (
	"context"
	"os"
)

// Stats holds database statistics.
type Stats struct {
	DBPath         string           `json:"db_path"`
	DBSizeBytes    int64            `json:"db_size_bytes"`
	TotalMemories  int              `json:"total_memories"`
	ActiveMemories int              `json:"active_memories"`
	TotalChunks    int              `json:"total_chunks"`
	Namespaces     []NamespaceStats `json:"namespaces"`
}

// NamespaceStats holds per-namespace counts.
type NamespaceStats struct {
	NS    string `json:"ns"`
	Count int    `json:"count"`
	Keys  int    `json:"keys"`
}

// Stats returns database statistics.
func (s *SQLiteStore) Stats(ctx context.Context, dbPath string) (*Stats, error) {
	st := &Stats{DBPath: dbPath}

	// DB file size
	if info, err := os.Stat(dbPath); err == nil {
		st.DBSizeBytes = info.Size()
	}

	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories`).Scan(&st.TotalMemories)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL`).Scan(&st.ActiveMemories)
	s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks`).Scan(&st.TotalChunks)

	rows, err := s.db.QueryContext(ctx, `
		SELECT ns, COUNT(*) as cnt, COUNT(DISTINCT key) as keys
		FROM memories WHERE deleted_at IS NULL
		GROUP BY ns ORDER BY cnt DESC`)
	if err != nil {
		return st, err
	}
	defer rows.Close()

	for rows.Next() {
		var ns NamespaceStats
		rows.Scan(&ns.NS, &ns.Count, &ns.Keys)
		st.Namespaces = append(st.Namespaces, ns)
	}

	return st, nil
}
