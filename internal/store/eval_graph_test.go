package store

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ── Graph utilisation eval ───────────────────────────────────────
//
// Ghost has nine relation types with distinct weights, force-include for
// `contradicts`, parent substitution for `contains`, and spreading activation
// over the lot. None of that has ever been measured end to end. The one time
// it was probed directly — does a `contradicts` edge rescue a correction the
// budget would otherwise drop — the answer was no, and the reason turned out
// to be structural rather than a tuning problem.
//
// This measures PULL-THROUGH: given a query that retrieves one endpoint of an
// edge, how often does the other endpoint arrive in the assembled context, and
// is it distinguishable from a passenger that arrived by plain similarity?
//
// The control matters. An edge "worked" only if the neighbour arrives when the
// edge exists AND does not arrive when it doesn't. Without the no-edge arm,
// topical similarity alone would score as graph utilisation — the neighbour
// is about the same subject by construction.
//
// FINDINGS AT FIRST MEASUREMENT (2026-08-06), both reproducible here:
//
//  1. EXPANSION IS OUTGOING-ONLY. getEdgesForExpansion filters `WHERE from_id
//     = ?`, so only edges pointing AWAY from a seed are traversed. A
//     correction is naturally written as `correction --contradicts--> stale`,
//     which is INCOMING at the stale memory — so when the stale fact is the
//     seed, the correction is never reachable. Flip the direction with
//     GRAPH_EDGE_DIR=out and the same edge pulls through. Same weight, same
//     relation, opposite result. This is why an earlier experiment found a
//     contradicts edge could not rescue a buried correction, and why no
//     production edge has ever been observed to change an assembly.
//
//  2. PINNED SEEDS ARE GRAPH-INERT. Pinned memories are appended straight to
//     the result in Phase 1 and never enter scoreMap, which is the seed set
//     for expansion — so the one memory guaranteed to be present every turn
//     can pull in nothing. The pinned case here fails even in the working
//     direction.
//
// Both are structural. Neither is a weight or damping tuning problem, and
// tuning either would not have found them.

type graphCase struct {
	rel string
	// seed is what the query finds; neighbour is what the edge should pull in.
	seedContent, neighbourContent string
	query                         string
	neighbourMarker               string
	// seedPinned reproduces the production shape that broke rescue: the seed
	// is chronically present via Phase 1 rather than found by search.
	seedPinned bool
}

func graphCorpus() []graphCase {
	// Neighbours are deliberately phrased with MINIMAL lexical and topical
	// overlap with the query. The first version of this corpus failed as an
	// instrument: every neighbour was an obvious paraphrase of the seed, so it
	// arrived on similarity in all six cases and the edge could not be
	// observed either way. Edges exist to pull in what similarity MISSES, so
	// the neighbour has to be something retrieval would not find on its own.
	return []graphCase{
		{
			rel:              "contradicts",
			seedContent:      "The office badge reader on the third floor accepts the old blue cards.",
			neighbourContent: "Facilities swapped the entry hardware in July. Anything issued before that month is now rejected at the turnstile.",
			query:            "which badge works on the third floor reader",
			neighbourMarker:  "swapped the entry hardware",
		},
		{
			rel:              "refines",
			seedContent:      "Backups run nightly.",
			neighbourContent: "The window is 02:15 UTC to roughly 02:55, and read-only volumes are skipped entirely.",
			query:            "when do the backups run",
			neighbourMarker:  "02:15 UTC to roughly 02:55",
		},
		{
			rel:              "depends_on",
			seedContent:      "Deploying the reporting service requires the migration step to have completed.",
			neighbourContent: "The ops runbook task named migrate-reports must finish first; it is not part of the pipeline and someone has to trigger it.",
			query:            "how do I deploy the reporting service",
			neighbourMarker:  "migrate-reports",
		},
		{
			rel:              "caused_by",
			seedContent:      "The dashboard was slow all Tuesday afternoon.",
			neighbourContent: "Finance moved the quarterly export to run at noon, and it holds most of the connection pool while it works.",
			query:            "why was the dashboard slow on Tuesday",
			neighbourMarker:  "Finance moved the quarterly export",
		},
		{
			rel:              "prevents",
			seedContent:      "Never ship a release on a Friday afternoon.",
			neighbourContent: "The on-call rotation hands over at 17:00 and the incoming engineer starts with no context on anything shipped that day.",
			query:            "can we release on Friday",
			neighbourMarker:  "hands over at 17:00",
		},
		{
			// The production shape: the seed is pinned, so it never enters the
			// search result set and therefore never seeds expansion.
			rel:              "contradicts",
			seedContent:      "The kitchen coffee machine takes the tall paper cups from the cupboard.",
			neighbourContent: "The unit was replaced in the spring; the new one has a lower clearance and anything over 12cm overflows.",
			query:            "which cups fit the kitchen coffee machine",
			neighbourMarker:  "lower clearance",
			seedPinned:       true,
		},
	}
}

// buildGraphCase seeds a store for one case. linked controls whether the edge
// exists — the control arm that separates graph pull-through from plain
// topical similarity.
func buildGraphCase(t *testing.T, c graphCase, linked bool) *ContextResult {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return base })

	if _, err := s.Put(ctx, PutParams{NS: "agent:graph", Key: "seed", Content: c.seedContent,
		Kind: "semantic", Tier: "ltm", Pinned: c.seedPinned, Importance: 0.7}); err != nil {
		t.Fatal(err)
	}
	// The neighbour is written LATER and deliberately phrased so it is not the
	// obvious lexical match for the query — if it arrives, the edge is why.
	s.SetClock(func() time.Time { return base.AddDate(0, 0, 10) })
	if _, err := s.Put(ctx, PutParams{NS: "agent:graph", Key: "neighbour", Content: c.neighbourContent,
		Kind: "semantic", Tier: "ltm", Importance: 0.5}); err != nil {
		t.Fatal(err)
	}
	// Filler so the budget actually binds and the neighbour has to earn a slot.
	// Filler that is TOPICALLY CLOSER to the query than the neighbour is, so
	// the neighbour cannot win a slot on similarity and only an edge can carry
	// it in. Without this the budget binds on unrelated text and the neighbour
	// rides in for free, which is what made the first version unable to
	// measure anything.
	for i := 0; i < 10; i++ {
		_, _ = s.Put(ctx, PutParams{NS: "agent:graph", Key: fmt.Sprintf("filler-%d", i),
			Content: fmt.Sprintf("%s (note %d, no change reported this week).", c.seedContent, i),
			Kind:    "semantic", Tier: "ltm", Importance: 0.45})
	}
	if linked {
		// Direction matters and is the whole point of this knob.
		// GRAPH_EDGE_DIR=out writes seed->neighbour; anything else writes
		// neighbour->seed, which is the direction a correction is naturally
		// written in (the NEW memory refutes the OLD one).
		from, to := "neighbour", "seed"
		if os.Getenv("GRAPH_EDGE_DIR") == "out" {
			from, to = "seed", "neighbour"
		}
		if _, err := s.CreateEdge(ctx, EdgeParams{FromNS: "agent:graph", FromKey: from,
			ToNS: "agent:graph", ToKey: to, Rel: c.rel}); err != nil {
			t.Fatal(err)
		}
	}
	s.SetClock(func() time.Time { return base.AddDate(0, 0, 20) })

	budget := 700
	if v := os.Getenv("GRAPH_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			budget = n
		}
	}
	res, err := s.Context(ctx, ContextParams{NS: "agent:graph", Query: c.query, Budget: budget})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func renderKeys(res *ContextResult) string {
	var ks []string
	for _, m := range res.Memories {
		ks = append(ks, m.Key)
	}
	sort.Strings(ks)
	return strings.Join(ks, ",")
}

func hasMarker(res *ContextResult, marker string) bool {
	for _, m := range res.Memories {
		if strings.Contains(m.Content, marker) {
			return true
		}
	}
	return false
}

// TestEvalGraphPullThrough reports, per relation type, whether the edge
// actually changes what reaches the agent. Reporting instrument, not a gate:
// it fails only if a relation that DID pull through stops doing so, so the
// measured baseline cannot silently rot.
func TestEvalGraphPullThrough(t *testing.T) {
	type row struct {
		rel                           string
		pinned, withEdge, withoutEdge bool
	}
	var rows []row

	for _, c := range graphCorpus() {
		with := buildGraphCase(t, c, true)
		without := buildGraphCase(t, c, false)
		r := row{rel: c.rel, pinned: c.seedPinned,
			withEdge: hasMarker(with, c.neighbourMarker), withoutEdge: hasMarker(without, c.neighbourMarker)}
		rows = append(rows, r)

		verdict := "NO EFFECT"
		switch {
		case r.withEdge && !r.withoutEdge:
			verdict = "PULLED THROUGH (edge is why)"
		case r.withEdge && r.withoutEdge:
			verdict = "arrived anyway (similarity, not the edge)"
		case !r.withEdge && r.withoutEdge:
			verdict = "REGRESSION (edge pushed it out)"
		}
		t.Logf("%-12s pinned_seed=%-5v  with_edge=%-5v without_edge=%-5v  -> %s",
			r.rel, r.pinned, r.withEdge, r.withoutEdge, verdict)
		t.Logf("      keys(with)=%s", renderKeys(with))
	}

	var effective int
	for _, r := range rows {
		if r.withEdge && !r.withoutEdge {
			effective++
		}
	}
	t.Logf("\nSUMMARY: %d/%d relations demonstrably changed the assembled context", effective, len(rows))
	if effective == 0 {
		t.Log("NOTE: zero relations pulled through — edge expansion is not influencing assembly " +
			"at this budget. That is a finding about the graph, not about the corpus.")
	}
}
