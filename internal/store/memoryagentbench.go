package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// MemoryAgentBench — Conflict Resolution track (retrieval adaptation).
//
// MemoryAgentBench (ICLR 2026, MIT, HF `ai-hyz/MemoryAgentBench`) scores four
// competencies; its Conflict Resolution track ("factconsolidation") is the
// one ghost lacked externally: a stream of facts where later facts UPDATE
// earlier ones, and questions probe the FINAL state. Facts are ingested
// incrementally in stream order (order carries the recency signal — this is
// a write-path-order-sensitive benchmark, not batch indexing), then each
// question is answered by Search and scored LLM-free: a question counts
// correct when a gold answer string appears in a top-k retrieved memory.
//
// Tracks: factconsolidation_sh (single-hop: find the updated fact) and
// factconsolidation_mh (multi-hop: chain two facts) at 6k/32k/64k/262k-token
// context sizes.
//
// Dataset: testdata/memoryagentbench/conflict_resolution.json — see the
// README there for the parquet→JSON conversion snippet.

// MABRow is one Conflict Resolution instance.
type MABRow struct {
	ID        string     `json:"id"`
	Context   string     `json:"context"`
	Questions []string   `json:"questions"`
	Answers   [][]string `json:"answers"`
}

// MABResult aggregates per-track scores.
type MABResult struct {
	Track     string  `json:"track"`
	Questions int     `json:"questions"`
	HitAt5    float64 `json:"hit@5"`
	HitAt10   float64 `json:"hit@10"`
	MRR       float64 `json:"mrr"` // rank of first answer-bearing memory
}

// LoadMemoryAgentBenchCR parses the converted Conflict Resolution JSON.
func LoadMemoryAgentBenchCR(path string) ([]MABRow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []MABRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rows, nil
}

// mabFactLines splits the numbered fact list ("0. fact\n1. fact\n...") into
// bare fact strings, preserving stream order.
func mabFactLines(contextStr string) []string {
	var facts []string
	for _, line := range strings.Split(contextStr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i := strings.Index(line, ". "); i > 0 && i <= 8 {
			if _, err := fmt.Sscanf(line[:i], "%d", new(int)); err == nil {
				facts = append(facts, line[i+2:])
				continue
			}
		}
		// Header lines ("Here is a list of facts:") are skipped.
	}
	return facts
}

// RunMemoryAgentBenchCR ingests each row's fact stream in order and scores
// every question by answer-string presence in top-k retrieved memories.
func RunMemoryAgentBenchCR(rows []MABRow, makeStore func() (*SQLiteStore, func(), error),
	progress func(done, total int)) ([]MABResult, error) {
	ctx := context.Background()
	var results []MABResult

	for ri, row := range rows {
		s, cleanup, err := makeStore()
		if err != nil {
			return nil, err
		}

		ns := "bench:mab"
		facts := mabFactLines(row.Context)
		// Simulate stream time: one minute per fact on the injectable clock.
		// The track is order-sensitive (later facts update earlier ones);
		// without this every fact lands in the same RFC3339 second and
		// recency cannot distinguish an update from the fact it replaces.
		streamStart := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		cursor := streamStart
		s.SetClock(func() time.Time { return cursor })
		for i, fact := range facts {
			cursor = streamStart.Add(time.Duration(i) * time.Minute)
			if _, err := s.Put(ctx, PutParams{
				NS:           ns,
				Key:          fmt.Sprintf("fact-%05d", i),
				Content:      fact,
				Kind:         "semantic",
				SkipAutoLink: true,
			}); err != nil {
				cleanup()
				return nil, fmt.Errorf("row %s ingest fact %d: %w", row.ID, i, err)
			}
		}

		// Queries run "now": just after the stream ends.
		cursor = streamStart.Add(time.Duration(len(facts)+60) * time.Minute)

		res := MABResult{Track: row.ID, Questions: len(row.Questions)}
		var hit5, hit10, mrrSum float64
		for qi, q := range row.Questions {
			golds := row.Answers[qi]
			found, err := s.Search(ctx, SearchParams{NS: ns, Query: q, Limit: 10})
			if err != nil {
				cleanup()
				return nil, fmt.Errorf("row %s q%d: %w", row.ID, qi, err)
			}
			rank := -1
			for i, r := range found {
				content := strings.ToLower(r.Content)
				for _, g := range golds {
					if g != "" && strings.Contains(content, strings.ToLower(g)) {
						rank = i
						break
					}
				}
				if rank >= 0 {
					break
				}
			}
			if rank >= 0 {
				mrrSum += 1.0 / float64(rank+1)
				if rank < 10 {
					hit10++
				}
				if rank < 5 {
					hit5++
				}
			}
		}
		n := float64(len(row.Questions))
		res.HitAt5 = hit5 / n
		res.HitAt10 = hit10 / n
		res.MRR = mrrSum / n
		results = append(results, res)
		cleanup()
		if progress != nil {
			progress(ri+1, len(rows))
		}
	}
	return results, nil
}
