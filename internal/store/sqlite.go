package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	_ "modernc.org/sqlite"

	"github.com/rcliao/agent-memory/internal/chunker"
	"github.com/rcliao/agent-memory/internal/model"
)

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db      *sql.DB
	entropy *rand.Rand
}

// NewSQLiteStore opens or creates a SQLite database at the given path.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	s := &SQLiteStore{
		db:      db,
		entropy: rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) newID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), s.entropy).String()
}

func (s *SQLiteStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS memories (
		id          TEXT PRIMARY KEY,
		ns          TEXT NOT NULL,
		key         TEXT NOT NULL,
		content     TEXT NOT NULL,
		kind        TEXT NOT NULL DEFAULT 'semantic',
		tags        TEXT,
		version     INTEGER NOT NULL DEFAULT 1,
		supersedes  TEXT,
		created_at  TEXT NOT NULL,
		deleted_at  TEXT,
		priority    TEXT NOT NULL DEFAULT 'normal',
		access_count INTEGER NOT NULL DEFAULT 0,
		last_accessed_at TEXT,
		meta        TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_memories_ns_key ON memories(ns, key);
	CREATE INDEX IF NOT EXISTS idx_memories_ns_kind ON memories(ns, kind);
	CREATE INDEX IF NOT EXISTS idx_memories_created ON memories(created_at DESC);
	CREATE INDEX IF NOT EXISTS idx_memories_deleted ON memories(deleted_at);
	CREATE INDEX IF NOT EXISTS idx_memories_priority ON memories(ns, priority);

	CREATE TABLE IF NOT EXISTS chunks (
		id          TEXT PRIMARY KEY,
		memory_id   TEXT NOT NULL REFERENCES memories(id),
		seq         INTEGER NOT NULL,
		text        TEXT NOT NULL,
		start_line  INTEGER,
		end_line    INTEGER
	);
	CREATE INDEX IF NOT EXISTS idx_chunks_memory ON chunks(memory_id);
	`
	_, err := s.db.Exec(schema)
	return err
}

func (s *SQLiteStore) Put(ctx context.Context, p PutParams) (*model.Memory, error) {
	now := time.Now().UTC()
	id := s.newID()

	kind := p.Kind
	if kind == "" {
		kind = "semantic"
	}
	priority := p.Priority
	if priority == "" {
		priority = "normal"
	}

	var tagsJSON *string
	if len(p.Tags) > 0 {
		b, _ := json.Marshal(p.Tags)
		s := string(b)
		tagsJSON = &s
	}

	var metaPtr *string
	if p.Meta != "" {
		metaPtr = &p.Meta
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Check for existing latest version
	var prevID string
	var prevVersion int
	err = tx.QueryRowContext(ctx,
		`SELECT id, version FROM memories
		 WHERE ns = ? AND key = ? AND deleted_at IS NULL
		 ORDER BY version DESC LIMIT 1`, p.NS, p.Key).Scan(&prevID, &prevVersion)

	version := 1
	var supersedes *string
	if err == nil {
		version = prevVersion + 1
		supersedes = &prevID
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO memories (id, ns, key, content, kind, tags, version, supersedes, created_at, priority, access_count, meta)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		id, p.NS, p.Key, p.Content, kind, tagsJSON, version, supersedes,
		now.Format(time.RFC3339), priority, metaPtr)
	if err != nil {
		return nil, fmt.Errorf("insert memory: %w", err)
	}

	// Chunk the content
	chunks := chunker.Chunk(p.Content, chunker.DefaultOptions())
	for i, c := range chunks {
		chunkID := s.newID()
		_, err = tx.ExecContext(ctx,
			`INSERT INTO chunks (id, memory_id, seq, text, start_line, end_line)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			chunkID, id, i, c.Text, c.StartLine, c.EndLine)
		if err != nil {
			return nil, fmt.Errorf("insert chunk: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	mem := &model.Memory{
		ID:         id,
		NS:         p.NS,
		Key:        p.Key,
		Content:    p.Content,
		Kind:       kind,
		Tags:       p.Tags,
		Version:    version,
		CreatedAt:  now,
		Priority:   priority,
		Meta:       p.Meta,
		ChunkCount: len(chunks),
	}
	if supersedes != nil {
		mem.Supersedes = *supersedes
	}

	return mem, nil
}

func (s *SQLiteStore) Get(ctx context.Context, p GetParams) ([]model.Memory, error) {
	var query string
	var args []interface{}

	if p.History {
		query = `SELECT id, ns, key, content, kind, tags, version, supersedes,
				        created_at, deleted_at, priority, access_count, last_accessed_at, meta
				 FROM memories WHERE ns = ? AND key = ? AND deleted_at IS NULL
				 ORDER BY version DESC`
		args = []interface{}{p.NS, p.Key}
	} else if p.Version > 0 {
		query = `SELECT id, ns, key, content, kind, tags, version, supersedes,
				        created_at, deleted_at, priority, access_count, last_accessed_at, meta
				 FROM memories WHERE ns = ? AND key = ? AND version = ? AND deleted_at IS NULL
				 LIMIT 1`
		args = []interface{}{p.NS, p.Key, p.Version}
	} else {
		query = `SELECT id, ns, key, content, kind, tags, version, supersedes,
				        created_at, deleted_at, priority, access_count, last_accessed_at, meta
				 FROM memories WHERE ns = ? AND key = ? AND deleted_at IS NULL
				 ORDER BY version DESC LIMIT 1`
		args = []interface{}{p.NS, p.Key}
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []model.Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}

	if len(memories) == 0 {
		return nil, fmt.Errorf("memory not found: %s/%s", p.NS, p.Key)
	}

	// Update access tracking for the latest
	if !p.History {
		now := time.Now().UTC().Format(time.RFC3339)
		s.db.ExecContext(ctx,
			`UPDATE memories SET access_count = access_count + 1, last_accessed_at = ? WHERE id = ?`,
			now, memories[0].ID)
	}

	return memories, nil
}

func (s *SQLiteStore) List(ctx context.Context, p ListParams) ([]model.Memory, error) {
	limit := p.Limit
	if limit <= 0 {
		limit = 20
	}

	// Build a query that returns only the latest version of each ns+key
	where := []string{"m.deleted_at IS NULL"}
	args := []interface{}{}

	if p.NS != "" {
		where = append(where, "m.ns = ?")
		args = append(args, p.NS)
	}
	if p.Kind != "" {
		where = append(where, "m.kind = ?")
		args = append(args, p.Kind)
	}

	// Tag filtering
	for _, tag := range p.Tags {
		where = append(where, "m.tags LIKE ?")
		args = append(args, "%\""+tag+"\"%")
	}

	query := fmt.Sprintf(`
		SELECT m.id, m.ns, m.key, m.content, m.kind, m.tags, m.version, m.supersedes,
		       m.created_at, m.deleted_at, m.priority, m.access_count, m.last_accessed_at, m.meta
		FROM memories m
		INNER JOIN (
			SELECT ns, key, MAX(version) AS max_ver
			FROM memories WHERE deleted_at IS NULL
			GROUP BY ns, key
		) latest ON m.ns = latest.ns AND m.key = latest.key AND m.version = latest.max_ver
		WHERE %s
		ORDER BY m.created_at DESC
		LIMIT ?`, strings.Join(where, " AND "))
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var memories []model.Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, err
		}
		memories = append(memories, m)
	}

	return memories, nil
}

func (s *SQLiteStore) Rm(ctx context.Context, p RmParams) error {
	if p.Hard {
		if p.AllVersions {
			// Delete chunks first
			_, err := s.db.ExecContext(ctx,
				`DELETE FROM chunks WHERE memory_id IN (SELECT id FROM memories WHERE ns = ? AND key = ?)`,
				p.NS, p.Key)
			if err != nil {
				return err
			}
			_, err = s.db.ExecContext(ctx, `DELETE FROM memories WHERE ns = ? AND key = ?`, p.NS, p.Key)
			return err
		}
		// Hard delete latest only
		var id string
		err := s.db.QueryRowContext(ctx,
			`SELECT id FROM memories WHERE ns = ? AND key = ? AND deleted_at IS NULL ORDER BY version DESC LIMIT 1`,
			p.NS, p.Key).Scan(&id)
		if err != nil {
			return fmt.Errorf("memory not found: %s/%s", p.NS, p.Key)
		}
		s.db.ExecContext(ctx, `DELETE FROM chunks WHERE memory_id = ?`, id)
		_, err = s.db.ExecContext(ctx, `DELETE FROM memories WHERE id = ?`, id)
		return err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if p.AllVersions {
		_, err := s.db.ExecContext(ctx,
			`UPDATE memories SET deleted_at = ? WHERE ns = ? AND key = ? AND deleted_at IS NULL`,
			now, p.NS, p.Key)
		return err
	}

	// Soft-delete latest version only
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM memories WHERE ns = ? AND key = ? AND deleted_at IS NULL ORDER BY version DESC LIMIT 1`,
		p.NS, p.Key).Scan(&id)
	if err != nil {
		return fmt.Errorf("memory not found: %s/%s", p.NS, p.Key)
	}
	_, err = s.db.ExecContext(ctx, `UPDATE memories SET deleted_at = ? WHERE id = ?`, now, id)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

type scanner interface {
	Scan(dest ...interface{}) error
}

func scanMemory(row scanner) (model.Memory, error) {
	var m model.Memory
	var tagsJSON, supersedes, deletedAt, lastAccessed, meta sql.NullString
	var createdAt string

	err := row.Scan(
		&m.ID, &m.NS, &m.Key, &m.Content, &m.Kind, &tagsJSON,
		&m.Version, &supersedes, &createdAt, &deletedAt,
		&m.Priority, &m.AccessCount, &lastAccessed, &meta,
	)
	if err != nil {
		return m, err
	}

	m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	if supersedes.Valid {
		m.Supersedes = supersedes.String
	}
	if deletedAt.Valid {
		t, _ := time.Parse(time.RFC3339, deletedAt.String)
		m.DeletedAt = &t
	}
	if lastAccessed.Valid {
		t, _ := time.Parse(time.RFC3339, lastAccessed.String)
		m.LastAccessedAt = &t
	}
	if meta.Valid {
		m.Meta = meta.String
	}
	if tagsJSON.Valid {
		json.Unmarshal([]byte(tagsJSON.String), &m.Tags)
	}

	return m, nil
}
