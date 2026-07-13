package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"

	"github.com/rcliao/ghost/internal/embedding"
)

// Embedding storage codec.
//
// Vectors were stored as JSON text (~5KB per 384-dim vector); searchVector
// JSON-decodes every embedded chunk per query, which measured ~300ms of every
// vector search on a 58k-chunk production DB (BACKLOG #2). New writes use a
// compact binary encoding — a 0x01 version byte followed by little-endian
// float32s (1537 bytes for 384 dims) — stored in the SAME column: SQLite
// keeps BLOB values as-is regardless of column affinity, so no schema change.
// The decoder accepts both formats (JSON text starts with '[' and can never
// collide with the version byte), and migrate() converts old rows once.

const embeddingBlobVersion = 0x01

// encodeEmbedding serializes a vector to the binary storage format.
func encodeEmbedding(vec embedding.Vector) []byte {
	out := make([]byte, 1+4*len(vec))
	out[0] = embeddingBlobVersion
	for i, f := range vec {
		binary.LittleEndian.PutUint32(out[1+4*i:], math.Float32bits(f))
	}
	return out
}

// decodeEmbedding parses either storage format (binary blob or legacy JSON).
func decodeEmbedding(raw []byte) (embedding.Vector, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty embedding")
	}
	if raw[0] == embeddingBlobVersion {
		if (len(raw)-1)%4 != 0 {
			return nil, fmt.Errorf("malformed embedding blob: %d bytes", len(raw))
		}
		n := (len(raw) - 1) / 4
		vec := make(embedding.Vector, n)
		for i := 0; i < n; i++ {
			vec[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[1+4*i:]))
		}
		return vec, nil
	}
	var vec embedding.Vector
	if err := json.Unmarshal(raw, &vec); err != nil {
		return nil, err
	}
	return vec, nil
}

// migrateEmbeddingBlobs rewrites legacy JSON-text embeddings as binary
// blobs. Best-effort and idempotent: rows are detected by SQLite value type,
// converted in one transaction, and unparseable rows are left as-is (the
// decoder still reads both formats).
func (s *SQLiteStore) migrateEmbeddingBlobs() {
	var legacy int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM chunks WHERE embedding IS NOT NULL AND typeof(embedding) = 'text'`).Scan(&legacy); err != nil || legacy == 0 {
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()

	rows, err := tx.Query(
		`SELECT id, embedding FROM chunks WHERE embedding IS NOT NULL AND typeof(embedding) = 'text'`)
	if err != nil {
		return
	}
	type conv struct {
		id   string
		blob []byte
	}
	var convs []conv
	for rows.Next() {
		var id string
		var raw []byte
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		vec, err := decodeEmbedding(raw)
		if err != nil {
			continue
		}
		convs = append(convs, conv{id: id, blob: encodeEmbedding(vec)})
	}
	rows.Close()

	stmt, err := tx.Prepare(`UPDATE chunks SET embedding = ? WHERE id = ?`)
	if err != nil {
		return
	}
	for _, c := range convs {
		_, _ = stmt.Exec(c.blob, c.id)
	}
	stmt.Close()
	_ = tx.Commit()
}
