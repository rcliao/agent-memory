package store

import (
	"context"
	"sort"
	"strings"

	"github.com/rcliao/ghost/internal/entity"
)

// Entity-bridge second hop (opt-in via GHOST_HOP2=1) — HippoRAG-lite.
//
// Multi-hop questions name the START of a chain, not its middle: "what
// database does the gateway route users to" retrieves the routing fact, but
// the user-service and data-layer facts share no vocabulary with the query.
// The bridge is an ENTITY that appears in the first-hop results but not in
// the query (the service name, the person, the tool). hop2Expand extracts
// those bridge entities from the top first-hop results, runs one side query
// with them, and merges the hits into the candidate pool — where the
// reranker (expand-then-rerank) can pull the chained evidence into the
// final top-k. Fully LLM-free: rule-based NER + one extra FTS/vector pass.
//
// Measured (2026-07-12): NO-GO as a default — keep opt-in, expectations low.
// On MemoryAgentBench factconsolidation_mh (6k), hop2 adds nothing over the
// reranker alone (hit@5 0.23 / hit@10 0.38, identical to 3 decimals), and
// FTS-only it moves hit@10 by ~+0.02: either the hop-1 fact misses the seed
// window, the bridge is the wrong token, or the cross-encoder cannot tie
// chained evidence back to a compositional question it cannot decompose.
// The same experiment showed the REAL multi-hop lever on that benchmark is
// the reranker itself (sh hit@5 0.59→0.84, mh 0.15→0.23). Compositional
// chains beyond that need semantic bridging (embeddings) or an
// OpenIE-style graph built at ingest — both out of hop2's reach.

const (
	hop2SeedDocs   = 5  // first-hop results to mine for bridge entities
	hop2MaxEnts    = 5  // bridge entities fanned out as side queries
	hop2MaxResults = 10 // hits merged into the pool
)

func hop2Enabled() bool { return envBool("GHOST_HOP2") }

// hop2Expand mines bridge entities from the top results and appends
// second-hop hits to the pool (deduplicated, tail-ranked — the reranker or
// downstream scoring decides whether they deserve the top-k).
func (s *SQLiteStore) hop2Expand(ctx context.Context, p SearchParams, results []SearchResult, poolLimit int) []SearchResult {
	if len(results) == 0 {
		return results
	}

	queryLower := strings.ToLower(p.Query)
	seen := make(map[string]bool, len(results))
	for _, r := range results {
		seen[r.ID] = true
	}

	// Collect bridge entities: in the top results, absent from the query.
	seedN := hop2SeedDocs
	if seedN > len(results) {
		seedN = len(results)
	}
	entCount := map[string]int{}
	addBridge := func(text string, weight int) {
		text = strings.TrimSpace(strings.Trim(text, ".,!?;:\"'"))
		if len(text) < 3 || strings.Contains(queryLower, strings.ToLower(text)) || englishStopWords[strings.ToLower(text)] {
			return
		}
		entCount[text] += weight
	}
	for _, r := range results[:seedN] {
		// Named entities (capitalized) — people, tools, places.
		for _, e := range entity.Extract(r.Content) {
			addBridge(e.Text, 2)
		}
		// Relation objects: the tail of a fact sentence ("...is associated
		// with the sport of baseball.") is usually the VALUE the next hop
		// needs, and it is often a lowercase noun the NER cannot see. Take
		// the final content tokens of the first sentence.
		sentence := r.Content
		if i := strings.IndexAny(sentence, ".!?\n"); i > 0 {
			sentence = sentence[:i]
		}
		words := strings.Fields(sentence)
		start := len(words) - 3
		if start < 0 {
			start = 0
		}
		for _, w := range words[start:] {
			addBridge(w, 1)
		}
	}
	if len(entCount) == 0 {
		return results
	}
	bridges := make([]string, 0, len(entCount))
	for e := range entCount {
		bridges = append(bridges, e)
	}
	// Prefer entities corroborated across seed docs, then deterministic order.
	sort.Slice(bridges, func(i, j int) bool {
		if entCount[bridges[i]] != entCount[bridges[j]] {
			return entCount[bridges[i]] > entCount[bridges[j]]
		}
		return bridges[i] < bridges[j]
	})
	if len(bridges) > hop2MaxEnts {
		bridges = bridges[:hop2MaxEnts]
	}

	// One side query PER bridge entity. A single joined query dilutes the
	// discriminative entity among the noisy ones (measured: joint query gave
	// +0.02 hit@10 on MemoryAgentBench mh; per-entity queries are the
	// HippoRAG-style seed fan-out). noHop2 guards recursion; the side passes
	// skip PRF/MMR/edges — they exist only to fetch chained evidence.
	added := 0
	perEntity := hop2MaxResults/2 + 1
	for _, bridge := range bridges {
		if added >= hop2MaxResults {
			break
		}
		subP := p
		subP.Query = bridge
		subP.noHop2 = true
		subP.PRF = false
		subP.MMR = false
		subP.MultiQuery = false
		subP.ExpandEdges = false
		subP.Limit = perEntity

		hop2, err := s.Search(ctx, subP)
		if err != nil {
			continue
		}
		for _, r := range hop2 {
			if seen[r.ID] || added >= hop2MaxResults {
				continue
			}
			results = append(results, r)
			seen[r.ID] = true
			added++
		}
	}
	if len(results) > poolLimit+hop2MaxResults {
		results = results[:poolLimit+hop2MaxResults]
	}
	return results
}
