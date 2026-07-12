package store

import (
	"time"
)

// Bi-temporal validity (opt-in via GHOST_BITEMPORAL=1).
//
// Graphiti-style event-time tracking: memories carry `valid_from`/`valid_to`
// timestamps alongside the transaction-time `created_at`/`deleted_at` pair.
// Nothing is deleted — when freshness/supersede detection (freshness.go) finds
// that a new fact replaces an old one, the old memory's `valid_to` is stamped
// with the supersede time. With the knob on, default retrieval hard-retires
// invalidated facts (they stop matching Search), while `AsOf` recall can still
// reconstruct what was believed true at any past instant. With the knob off
// (default), the columns are inert and retrieval is byte-identical — the
// LongMemEval_S Search-path gate (R@5 0.908 / MRR 0.827) sees no change.
//
// False-supersede risk (an Omission) is why hard-retire stays gated: the C4
// demote-importance path remains the default freshness behavior, and the
// `contradicts` force-include edge keeps retired facts visible in context
// expansion for transparency.

func bitemporalEnabled() bool { return envBool("GHOST_BITEMPORAL") }

// appendValidityFilter adds the event-time validity clauses to a WHERE list.
// Applied when bi-temporal mode is on, or when the caller asks for point-in-
// time recall via AsOf (which works regardless of the knob — the columns are
// only ever written under the knob, so an unstamped DB just has NULLs and the
// filter passes everything).
func appendValidityFilter(where []string, args []interface{}, p SearchParams) ([]string, []interface{}) {
	asOf := p.AsOf
	if asOf.IsZero() {
		if !bitemporalEnabled() {
			return where, args
		}
		asOf = time.Now().UTC()
	}
	ts := asOf.UTC().Format(time.RFC3339)
	// valid_from is rarely stamped; a fact's event-time validity starts at its
	// creation unless a caller set valid_from explicitly.
	where = append(where, "(COALESCE(m.valid_from, m.created_at) <= ?)")
	args = append(args, ts)
	where = append(where, "(m.valid_to IS NULL OR m.valid_to > ?)")
	args = append(args, ts)
	return where, args
}

// invalidateMemory stamps valid_to on the latest live version of (ns, key),
// retiring the fact as of `at` event time. No-op on rows already invalidated.
func (s *SQLiteStore) invalidateMemory(ns, key string, at time.Time) {
	_, _ = s.db.Exec(`
		UPDATE memories SET valid_to = ?
		WHERE ns = ? AND key = ? AND deleted_at IS NULL AND valid_to IS NULL
		  AND version = (SELECT MAX(version) FROM memories WHERE ns = ? AND key = ? AND deleted_at IS NULL)`,
		at.UTC().Format(time.RFC3339), ns, key, ns, key)
}
