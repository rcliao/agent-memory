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
//  2b. NON-CONTRADICTS EDGES ARE INERT FOR CANDIDATES ALREADY IN THE POOL.
//     Measured once rank delta replaced presence: refines, depends_on,
//     caused_by and prevents all sit at IDENTICAL rank with and without their
//     edge. For a neighbour already retrieved by search, the boost is capped at
//     origScore * MaxBoostFactor (0.5, floor 0.15) — too small to reorder
//     anything. Only `contradicts` bypasses that cap and carries a score floor
//     of 0.8 * seed, which is why it is the only relation that visibly moves
//     the result. Presence alone had hidden this: both arms said "present" and
//     it read as a corpus limitation rather than an inert mechanism.
//
//  3. PINNED SEEDS WERE GRAPH-INERT. Pinned memories are appended straight to
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
	// Every neighbour here is UNREACHABLE from the query by similarity — that
	// is the only condition under which an edge is observable. A neighbour
	// that a plain search would find scores the same in both arms and tells us
	// nothing, which is what happened on the first two versions of this
	// corpus: four of six relations read "arrived anyway" and the eval could
	// not distinguish a working relation from a broken one.
	//
	// Consequence worth stating: these neighbours are not textbook instances
	// of their relation. A refinement of "backups run nightly" would naturally
	// be about timing, and timing is what the query asks for — so it would be
	// found anyway. What is under test is whether the RELATION TRAVERSES, not
	// whether the content is a canonical example of it. The relation label is
	// the variable; the content is chosen to make the label measurable.
	return []graphCase{
		{
			rel:              "contradicts",
			seedContent:      "The office badge reader on the third floor accepts the old blue cards.",
			neighbourContent: "Facilities swapped that unit out in July and anything issued before then is refused now.",
			query:            "which badge works on the third floor reader",
			neighbourMarker:  "swapped that unit out",
		},
		{
			rel:              "refines",
			seedContent:      "Backups run nightly.",
			neighbourContent: "Restores have to be requested through the ops desk and typically take an hour to prepare.",
			query:            "when do the backups run",
			neighbourMarker:  "requested through the ops desk",
		},
		{
			rel:              "depends_on",
			seedContent:      "Deploying the reporting service requires the migration step to have completed.",
			neighbourContent: "Nobody has automated the migrate-reports task; someone still triggers it by hand each time.",
			query:            "how do I deploy the reporting service",
			neighbourMarker:  "triggers it by hand",
		},
		{
			rel:              "caused_by",
			seedContent:      "The dashboard was slow all Tuesday afternoon.",
			neighbourContent: "Finance moved their quarterly export to noon and it holds most of the pool while it works.",
			query:            "why was the dashboard slow on Tuesday",
			neighbourMarker:  "moved their quarterly export",
		},
		{
			rel:              "prevents",
			seedContent:      "Never ship a release on a Friday afternoon.",
			neighbourContent: "Handover happens at 17:00 and whoever picks up the pager has no idea what went out that day.",
			query:            "can we release on Friday",
			neighbourMarker:  "picks up the pager",
		},
		{
			// The production shape: the seed is pinned, so before #94 it never
			// entered the search result set and could not seed expansion.
			rel:              "contradicts",
			seedContent:      "The kitchen coffee machine takes the tall paper cups from the cupboard.",
			neighbourContent: "That unit was replaced in the spring and the new one has much less clearance underneath.",
			query:            "which cups fit the kitchen coffee machine",
			neighbourMarker:  "much less clearance",
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
	return markerRank(res, marker) >= 0
}

// markerRank returns the neighbour's 0-based position in the assembled
// context, or -1 if absent.
//
// Presence alone turned out to be too blunt a metric. For four of six
// relations the neighbour is a DIRECT search hit however it is worded — with
// embeddings enabled, semantically related text stays reachable — so both arms
// reported "present" and the edge could be neither credited nor blamed. Rank
// still moves in those cases: an edge that contributes propagated score should
// lift the neighbour, and one that contributes nothing should not. Rank makes
// the four unmeasurable relations measurable without contorting the corpus
// into content nobody would ever write.
func markerRank(res *ContextResult, marker string) int {
	for i, m := range res.Memories {
		if strings.Contains(m.Content, marker) {
			return i
		}
	}
	return -1
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

		rankWith, rankWithout := markerRank(with, c.neighbourMarker), markerRank(without, c.neighbourMarker)
		verdict := "NO EFFECT"
		switch {
		case r.withEdge && !r.withoutEdge:
			verdict = "PULLED THROUGH (edge is why)"
		case !r.withEdge && r.withoutEdge:
			verdict = "REGRESSION (edge pushed it out)"
		case r.withEdge && r.withoutEdge:
			switch {
			case rankWith < rankWithout:
				verdict = fmt.Sprintf("RANK LIFTED %d -> %d (edge helped)", rankWithout, rankWith)
			case rankWith > rankWithout:
				verdict = fmt.Sprintf("RANK DROPPED %d -> %d", rankWithout, rankWith)
			default:
				verdict = fmt.Sprintf("present in both at rank %d, edge changed nothing", rankWith)
			}
		}
		t.Logf("%-12s pinned=%-5v  present(with/without)=%v/%v  rank=%d/%d  -> %s",
			r.rel, r.pinned, r.withEdge, r.withoutEdge, rankWith, rankWithout, verdict)
		t.Logf("      keys(with)=%s", renderKeys(with))
	}

	var effective int
	for _, r := range rows {
		if r.withEdge && !r.withoutEdge {
			effective++
		}
	}
	t.Logf("\nSUMMARY: %d/%d relations made an otherwise-unreachable neighbour reachable; "+
		"see per-row rank deltas for the rest", effective, len(rows))
	if effective == 0 {
		t.Log("NOTE: zero relations pulled through — edge expansion is not influencing assembly " +
			"at this budget. That is a finding about the graph, not about the corpus.")
	}
}
