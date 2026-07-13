package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/rcliao/ghost/internal/embedding"
)

func TestEmbeddingCodecRoundTrip(t *testing.T) {
	vec := embedding.Vector{0.125, -3.5, 0, 1e-7, 42.75}
	got, err := decodeEmbedding(encodeEmbedding(vec))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(vec) {
		t.Fatalf("len %d != %d", len(got), len(vec))
	}
	for i := range vec {
		if got[i] != vec[i] {
			t.Errorf("elem %d: %v != %v", i, got[i], vec[i])
		}
	}
}

func TestEmbeddingCodecReadsLegacyJSON(t *testing.T) {
	vec := embedding.Vector{1.5, -2.25, 0.001}
	raw, _ := json.Marshal(vec)
	got, err := decodeEmbedding(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != -2.25 {
		t.Fatalf("legacy decode wrong: %v", got)
	}
}

func TestEmbeddingBlobMigration(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "m.db")
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "t", Key: "a", Content: "the quick brown fox jumps over the lazy dog"}); err != nil {
		t.Fatal(err)
	}
	// Simulate a legacy row: overwrite with JSON text.
	legacy, _ := json.Marshal(embedding.Vector{0.5, 0.25, -0.125})
	if _, err := s.db.Exec(`UPDATE chunks SET embedding = ? WHERE 1`, string(legacy)); err != nil {
		t.Fatal(err)
	}
	var typ string
	s.db.QueryRow(`SELECT typeof(embedding) FROM chunks LIMIT 1`).Scan(&typ)
	if typ != "text" {
		t.Fatalf("setup failed: typeof=%s", typ)
	}
	s.Close()

	// Reopen: migration converts to blob and the value round-trips.
	s2, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var raw []byte
	s2.db.QueryRow(`SELECT typeof(embedding), embedding FROM chunks LIMIT 1`).Scan(&typ, &raw)
	if typ != "blob" {
		t.Fatalf("migration did not convert: typeof=%s", typ)
	}
	vec, err := decodeEmbedding(raw)
	if err != nil || len(vec) != 3 || vec[2] != -0.125 {
		t.Fatalf("migrated value wrong: %v %v", vec, err)
	}
}
