package store

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/rcliao/agent-memory/internal/model"
)

// ContextParams holds parameters for context assembly.
type ContextParams struct {
	NS     string
	Query  string
	Kind   string
	Tags   []string
	Budget int // max chars in output (rough token proxy: 1 token ≈ 4 chars)
}

// ContextMemory is a scored memory for context output.
type ContextMemory struct {
	NS      string  `json:"ns"`
	Key     string  `json:"key"`
	Kind    string  `json:"kind"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
	Excerpt bool    `json:"excerpt,omitempty"`
}

// ContextResult is the assembled context response.
type ContextResult struct {
	Budget   int             `json:"budget"`
	Used     int             `json:"used"`
	Memories []ContextMemory `json:"memories"`
}

// Context assembles relevant memories within a token budget.
func (s *SQLiteStore) Context(ctx context.Context, p ContextParams) (*ContextResult, error) {
	budget := p.Budget
	if budget <= 0 {
		budget = 4000
	}
	// Convert token budget to char budget (rough: 4 chars/token)
	charBudget := budget * 4

	// Search for candidates (get more than we need for scoring)
	results, err := s.Search(ctx, SearchParams{
		NS:    p.NS,
		Query: p.Query,
		Kind:  p.Kind,
		Limit: 50,
	})
	if err != nil {
		return nil, err
	}

	if len(results) == 0 {
		return &ContextResult{Budget: budget, Used: 0, Memories: []ContextMemory{}}, nil
	}

	// Score each memory
	now := time.Now()
	type scored struct {
		memory model.Memory
		score  float64
	}
	var candidates []scored

	for _, r := range results {
		m := r.Memory
		// Relevance: position-based (earlier = more relevant from search)
		// Since search already orders by relevance, use inverse position
		relevance := 1.0 // base relevance from search match

		// Recency: exponential decay, half-life of 7 days
		age := now.Sub(m.CreatedAt).Hours() / 24.0 // days
		recency := math.Exp(-0.1 * age)

		// Importance: priority-based
		importance := priorityScore(m.Priority)

		// Access frequency: log scale
		accessFreq := 0.0
		if m.AccessCount > 0 {
			accessFreq = math.Log(float64(m.AccessCount)+1) / math.Log(100)
			if accessFreq > 1 {
				accessFreq = 1
			}
		}

		// Composite score (matching design doc weights)
		score := relevance*0.4 + recency*0.2 + importance*0.2 + accessFreq*0.2

		candidates = append(candidates, scored{memory: m, score: score})
	}

	// Sort by score descending
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Greedy packing into budget
	result := &ContextResult{Budget: budget, Memories: []ContextMemory{}}
	used := 0

	for _, c := range candidates {
		contentLen := len(c.memory.Content)
		if used+contentLen <= charBudget {
			// Fits entirely
			result.Memories = append(result.Memories, ContextMemory{
				NS:      c.memory.NS,
				Key:     c.memory.Key,
				Kind:    c.memory.Kind,
				Content: c.memory.Content,
				Score:   math.Round(c.score*100) / 100,
			})
			used += contentLen
		} else if remaining := charBudget - used; remaining >= 100 {
			// Partial fit — excerpt
			excerpt := c.memory.Content
			if len(excerpt) > remaining {
				excerpt = excerpt[:remaining] + "..."
			}
			result.Memories = append(result.Memories, ContextMemory{
				NS:      c.memory.NS,
				Key:     c.memory.Key,
				Kind:    c.memory.Kind,
				Content: excerpt,
				Score:   math.Round(c.score*100) / 100,
				Excerpt: true,
			})
			used += len(excerpt)
			break // budget full
		} else {
			break
		}
	}

	// Convert used chars back to approximate tokens
	result.Used = used / 4

	return result, nil
}

func priorityScore(p string) float64 {
	switch p {
	case "critical":
		return 1.0
	case "high":
		return 0.75
	case "normal":
		return 0.5
	case "low":
		return 0.25
	default:
		return 0.5
	}
}
