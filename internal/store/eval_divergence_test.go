package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// Cross-agent divergence probe.
//
// The household runs several agents that evolve INDEPENDENTLY — separate
// stores, no shared namespace, knowledge crossing only by conversation
// ("regroup sessions"). That is the experiment, not a defect. But independent
// evolution has one failure mode worth measuring: a fact can live in one
// agent and be absent from another. Observed live 2026-07-30, where the two
// agents gave conflicting restaurant advice and the miss was a food ALLERGY.
//
// This probe asks every agent's store the same questions and reports where
// they disagree. It is strictly read-only and never moves data between
// stores — measuring the experiment must not contaminate it.
//
// Interpreting the result over a regroup:
//   - "only-one-knows" should SHRINK, especially in the safety class
//   - the agents' own wordings should NOT converge; if their memories start
//     matching verbatim they are copying, not learning, and the independence
//     the experiment is testing has been lost
//
// Probes are supplied from OUTSIDE the repo (household facts are private):
//
//	GHOST_DIVERGENCE_STORES='pika=/path/a.db:agent:pikamini,umbreon=/path/b.db:agent:umbreonmini' \
//	GHOST_DIVERGENCE_PROBES=/path/probes.json \
//	GHOST_EMBED_PROVIDER=local go test ./internal/store/ -run TestEvalCrossAgentDivergence -v
//
// The probe mirrors how shell actually assembles an agent's view, which is
// NOT a single Context() call: the pinned set is listed UNBOUNDED into the
// system prompt (no token budget, no importance cut), and Context() then runs
// with ExcludePinned to add the per-turn delta. Assembling both here matters —
// scoring a plain Context() call instead makes pinned facts look missing when
// production would have had them, which is exactly the false alarm this
// comment exists to prevent. Set GHOST_DIVERGENCE_EXCLUDE_PINNED=1 to score
// the retrieved delta ALONE, i.e. to ask what the agent could recall if it
// were not already carrying the fact in its prompt.
//
// probes.json: [{"q":"question text","expect":"substring that proves recall","class":"safety|household|..."}]
type divergenceProbe struct {
	Q      string `json:"q"`
	Expect string `json:"expect"`
	Class  string `json:"class"`
}

func TestEvalCrossAgentDivergence(t *testing.T) {
	spec := os.Getenv("GHOST_DIVERGENCE_STORES")
	probeFile := os.Getenv("GHOST_DIVERGENCE_PROBES")
	if spec == "" || probeFile == "" {
		t.Skip("set GHOST_DIVERGENCE_STORES and GHOST_DIVERGENCE_PROBES to run")
	}
	raw, err := os.ReadFile(probeFile)
	if err != nil {
		t.Fatalf("probes: %v", err)
	}
	var probes []divergenceProbe
	if err := json.Unmarshal(raw, &probes); err != nil {
		t.Fatalf("probes json: %v", err)
	}

	type agent struct {
		name  string
		store *SQLiteStore
		ns    string
	}
	var agents []agent
	for _, part := range strings.Split(spec, ",") {
		nameRest := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(nameRest) != 2 {
			t.Fatalf("bad store spec %q, want name=path:ns", part)
		}
		pathNS := strings.SplitN(nameRest[1], ":", 2)
		if len(pathNS) != 2 {
			t.Fatalf("bad store spec %q, want name=path:ns", part)
		}
		s, err := NewSQLiteStore(pathNS[0])
		if err != nil {
			t.Fatalf("open %s: %v", nameRest[0], err)
		}
		defer s.Close()
		agents = append(agents, agent{nameRest[0], s, pathNS[1]})
	}

	ctx := context.Background()
	byClass := map[string]struct{ all, some, none int }{}
	var gaps []string

	for _, p := range probes {
		var knows []string
		for _, a := range agents {
			var joined strings.Builder
			deltaOnly := os.Getenv("GHOST_DIVERGENCE_EXCLUDE_PINNED") == "1"
			if !deltaOnly {
				pins, err := a.store.List(ctx, ListParams{NS: a.ns, PinnedOnly: true, Limit: 500})
				if err != nil {
					t.Fatalf("%s pinned list: %v", a.name, err)
				}
				for _, m := range pins {
					joined.WriteString(m.Content)
					joined.WriteString("\n")
				}
			}
			res, err := a.store.Context(ctx, ContextParams{NS: a.ns, Query: p.Q,
				Budget: 2000, MinScore: 0.3, ExcludePinned: true})
			if err != nil {
				t.Fatalf("%s context %q: %v", a.name, p.Q, err)
			}
			for _, m := range res.Memories {
				joined.WriteString(m.Content)
				joined.WriteString("\n")
			}
			if strings.Contains(strings.ToLower(joined.String()), strings.ToLower(p.Expect)) {
				knows = append(knows, a.name)
			}
		}
		c := byClass[p.Class]
		switch {
		case len(knows) == len(agents):
			c.all++
		case len(knows) > 0:
			c.some++
			var missing []string
			for _, a := range agents {
				found := false
				for _, k := range knows {
					if k == a.name {
						found = true
					}
				}
				if !found {
					missing = append(missing, a.name)
				}
			}
			gaps = append(gaps, fmt.Sprintf("  [%s] %q — known by %s, MISSING from %s",
				p.Class, trimQ(p.Q), strings.Join(knows, "+"), strings.Join(missing, "+")))
		default:
			c.none++
			gaps = append(gaps, fmt.Sprintf("  [%s] %q — NO agent recalled it", p.Class, trimQ(p.Q)))
		}
		byClass[p.Class] = c
	}

	var classes []string
	for k := range byClass {
		classes = append(classes, k)
	}
	sort.Strings(classes)
	t.Logf("cross-agent divergence over %d probes, %d agents", len(probes), len(agents))
	for _, cl := range classes {
		c := byClass[cl]
		t.Logf("  %-12s all=%-3d only-some=%-3d none=%-3d", cl, c.all, c.some, c.none)
	}
	for _, g := range gaps {
		t.Log(g)
	}

	// Divergence in the safety class is the one thing worth failing on: a
	// fact one agent knows and another does not, where the consequence is
	// medical. Everything else is reported, not asserted — uneven knowledge
	// is the expected state of independently evolving agents.
	if c := byClass["safety"]; c.some > 0 || c.none > 0 {
		t.Errorf("safety-class divergence: %d probes known to only some agents, %d known to none", c.some, c.none)
	}
}

func trimQ(s string) string {
	if len(s) > 46 {
		return s[:46] + "…"
	}
	return s
}
