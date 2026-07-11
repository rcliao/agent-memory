package store

import (
	"context"
	"os"
	"regexp"

	"github.com/rcliao/ghost/internal/entity"
)

// Freshness / supersede detection (opt-in via GHOST_FRESHNESS=1).
//
// A personal agent must not resurface a fact that a newer one has replaced
// ("we switched from Webpack to Vite"). When enabled, detectSupersede spots a
// change announcement on write and, if the new memory shares a named entity
// with an existing one, marks the older memory as superseded: it creates a
// `contradicts` edge (new → old, so force-include still surfaces the old fact
// for transparency) and diminishes the old memory's importance so the current
// truth ranks above it.
//
// This is fully LLM-free and requires no schema change. It is gated OFF by
// default so existing behavior and the public benchmarks are untouched until
// the change has been A/B'd; the personal-agent eval exercises it with the knob
// on. See docs/eval.md and docs/research/personal-agent-roadmap.md (C4).

// changeCueRe matches phrases that announce a fact has changed.
var changeCueRe = regexp.MustCompile(`(?i)\b(switched (from|to)|now use[sd]?|no longer|instead of|replaced|moved to|migrated to|updated to|deprecated|superseded)\b`)

// freshnessEnabled reports whether opt-in supersede detection is active.
func freshnessEnabled() bool { return os.Getenv("GHOST_FRESHNESS") == "1" }

// supersedeDemoteFactor is how much a superseded memory's importance is scaled.
const supersedeDemoteFactor = 0.5
const supersedeImportanceFloor = 0.1

// detectSupersede is called after a Put. It is a cheap no-op unless the knob is
// set and the new content announces a change. Best-effort: all errors ignored.
func (s *SQLiteStore) detectSupersede(ctx context.Context, newID, ns, key, content string) {
	if !freshnessEnabled() || !changeCueRe.MatchString(content) {
		return
	}

	newEnts := entity.Extract(content)
	if len(newEnts) == 0 {
		return
	}
	newSet := make(map[string]bool, len(newEnts))
	for _, e := range newEnts {
		newSet[e.Text] = true
	}

	// Scan recent latest-version memories in the same namespace, excluding this
	// memory and its own key. Pick the one sharing the most named entities.
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.key, m.content FROM memories m
		INNER JOIN (
			SELECT ns, key, MAX(version) AS mv FROM memories
			WHERE deleted_at IS NULL GROUP BY ns, key
		) l ON m.ns = l.ns AND m.key = l.key AND m.version = l.mv
		WHERE m.ns = ? AND m.deleted_at IS NULL AND m.id <> ? AND m.key <> ?
		ORDER BY m.created_at DESC LIMIT 100`, ns, newID, key)
	if err != nil {
		return
	}
	defer rows.Close()

	var bestKey string
	bestOverlap := 0
	for rows.Next() {
		var id, k, c string
		if err := rows.Scan(&id, &k, &c); err != nil {
			continue
		}
		overlap := 0
		for _, e := range entity.Extract(c) {
			if newSet[e.Text] {
				overlap++
			}
		}
		if overlap > bestOverlap {
			bestOverlap = overlap
			bestKey = k
		}
	}
	if bestOverlap == 0 || bestKey == "" {
		return
	}

	// New contradicts old — force-include keeps the old fact visible, but the
	// demotion below drops it beneath the current truth.
	_, _ = s.CreateEdge(ctx, EdgeParams{
		FromNS: ns, FromKey: key, ToNS: ns, ToKey: bestKey, Rel: "contradicts",
	})

	// Diminish the superseded memory's importance (latest version row).
	_, _ = s.db.ExecContext(ctx, `
		UPDATE memories SET importance = MAX(?, importance * ?)
		WHERE ns = ? AND key = ? AND deleted_at IS NULL
		  AND version = (SELECT MAX(version) FROM memories WHERE ns = ? AND key = ? AND deleted_at IS NULL)`,
		supersedeImportanceFloor, supersedeDemoteFactor, ns, bestKey, ns, bestKey)
}
