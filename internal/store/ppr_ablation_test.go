package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"testing"

	"github.com/rcliao/ghost/internal/embedding"
	"github.com/rcliao/ghost/internal/entity"
)

// TestPPRAblationEdgeProvenance classifies a live edge graph by provenance to
// decide the PPR go/no-go: PPR's multi-hop win requires ENTITY/TOPIC bridge edges
// (low cosine, shared entity) that connect passages dense retrieval can't; if the
// graph is dominated by cosine-similarity echoes, PPR just re-ranks toward
// dense-central nodes and is NO-GO. Point GHOST_ABLATION_DB at a copy of a live DB.
//
//	GHOST_ABLATION_DB=/tmp/live.db GHOST_ABLATION_NS=agent:pikamini \
//	  go test ./internal/store/ -run TestPPRAblationEdgeProvenance -v
func TestPPRAblationEdgeProvenance(t *testing.T) {
	dbPath := os.Getenv("GHOST_ABLATION_DB")
	if dbPath == "" {
		t.Skip("set GHOST_ABLATION_DB (a copy of a live DB) to run")
	}
	ns := os.Getenv("GHOST_ABLATION_NS")
	if ns == "" {
		ns = "agent:pikamini"
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Load content + seq-0 embedding for every latest-version memory in the ns.
	type mem struct {
		content string
		vec     embedding.Vector
		ents    []entity.Entity
	}
	mems := map[string]*mem{}
	rows, err := db.Query(`
		SELECT m.id, m.content, c.embedding FROM memories m
		LEFT JOIN chunks c ON c.memory_id = m.id AND c.seq = 0
		WHERE m.ns = ? AND m.deleted_at IS NULL`, ns)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, content string
		var emb sql.NullString
		if rows.Scan(&id, &content, &emb) != nil {
			continue
		}
		m := &mem{content: content, ents: entity.Extract(content)}
		if emb.Valid {
			json.Unmarshal([]byte(emb.String), &m.vec)
		}
		mems[id] = m
	}
	rows.Close()

	// Classify each edge by why it would have been linked.
	erows, err := db.Query(`
		SELECT e.from_id, e.to_id FROM memory_edges e
		JOIN memories m ON m.id = e.from_id
		WHERE m.ns = ?`, ns)
	if err != nil {
		t.Fatal(err)
	}
	defer erows.Close()

	var total, cosineEcho, entityBridge, topicBridge, both, weak, noEmb int
	for erows.Next() {
		var from, to string
		if erows.Scan(&from, &to) != nil {
			continue
		}
		a, b := mems[from], mems[to]
		if a == nil || b == nil {
			continue
		}
		total++
		var cos float64
		haveEmb := len(a.vec) > 0 && len(b.vec) > 0
		if haveEmb {
			cos = embedding.CosineSimilarity(a.vec, b.vec)
		} else {
			noEmb++
		}
		_, entScore := entity.EntityOverlap(a.ents, b.ents)
		aT := entity.ExtractTopics(a.content, []string{a.content, b.content}, 10)
		bT := entity.ExtractTopics(b.content, []string{a.content, b.content}, 10)
		_, topScore := entity.TopicOverlap(aT, bT)

		cosLinked := haveEmb && cos >= 0.85
		entLinked := entScore >= 0.15
		topLinked := topScore >= 0.3

		switch {
		case cosLinked && (entLinked || topLinked):
			both++
		case cosLinked:
			cosineEcho++
		case entLinked && cos < 0.85:
			entityBridge++
		case topLinked && cos < 0.85:
			topicBridge++
		default:
			weak++
		}
	}

	bridges := entityBridge + topicBridge
	t.Logf("── PPR edge-provenance ablation (%s, %d edges) ──", ns, total)
	t.Logf("  cosine echo (cos>=0.85, no entity/topic): %d (%.1f%%)", cosineEcho, pct(cosineEcho, total))
	t.Logf("  BRIDGE — entity (cos<0.85, shared entity): %d (%.1f%%)", entityBridge, pct(entityBridge, total))
	t.Logf("  BRIDGE — topic  (cos<0.85, shared topic):  %d (%.1f%%)", topicBridge, pct(topicBridge, total))
	t.Logf("  both signals: %d (%.1f%%)   weak/other: %d   no-embedding: %d", both, pct(both, total), weak, noEmb)
	t.Logf("  → BRIDGE edges (PPR's substrate): %d (%.1f%%)", bridges, pct(bridges, total))
	if total > 0 && pct(bridges, total) < 5 {
		t.Logf("  VERDICT LEAN: NO-GO — <5%% bridge edges; PPR would echo dense retrieval.")
	} else {
		t.Logf("  VERDICT LEAN: worth the retrieval ablation — meaningful bridge substrate exists.")
	}
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}
