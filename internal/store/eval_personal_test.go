package store

import (
	"context"
	"encoding/json"
	"os"
	"sort"
	"testing"
	"time"
)

// rankOf returns the 0-based position of key in keys, or -1 if absent.
func rankOf(keys []string, key string) int {
	for i, k := range keys {
		if k == key {
			return i
		}
	}
	return -1
}

func seedPersonalStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := SeedStore(ctx, s, PersonalSeedCorpus()); err != nil {
		t.Fatalf("seed personal corpus: %v", err)
	}
	// The contradiction scenario needs an explicit contradicts edge so the
	// force-include path has something to traverse (auto-linking is cosine-only
	// and off in the FTS-only test store).
	if _, err := s.CreateEdge(ctx, EdgeParams{
		FromNS: PersonalNS, FromKey: "auth-new",
		ToNS: PersonalNS, ToKey: "auth-old", Rel: "contradicts",
	}); err != nil {
		t.Fatalf("create contradicts edge: %v", err)
	}
	// Multi-hop: link phoenix-arch → phoenix-ha so spreading activation can reach
	// the answer the query itself doesn't name.
	if _, err := s.CreateEdge(ctx, EdgeParams{
		FromNS: PersonalNS, FromKey: "phoenix-arch",
		ToNS: PersonalNS, ToKey: "phoenix-ha", Rel: "relates_to",
	}); err != nil {
		t.Fatalf("create relates_to edge: %v", err)
	}
	return s
}

// TestEvalPersonal measures personal-agent retrieval quality across preference,
// procedural, decision, same-day, freshness, and contradiction slices. It gates
// on the categories that should already pass at the FTS baseline and RECORDS the
// ranking signals (fresh-over-stale) that upcoming scoring work is meant to move.
// runPersonalCases runs every personal scenario against a seeded store and
// returns the scored results (no assertions) — shared by TestEvalPersonal and
// the A/B matrix.
func runPersonalCases(t *testing.T, ctx context.Context, s *SQLiteStore) []ScenarioResult {
	var scenarios []ScenarioResult

	for _, c := range PersonalCases() {
		var keys []string
		if c.UseContext {
			res, err := s.Context(ctx, ContextParams{NS: PersonalNS, Query: c.Query, Budget: 1000, MinScore: c.MinScore})
			if err != nil {
				t.Fatalf("%s: context: %v", c.Name, err)
			}
			keys = contextKeys(res)
		} else {
			res, err := s.Search(ctx, SearchParams{NS: PersonalNS, Query: c.Query, Limit: 10})
			if err != nil {
				t.Fatalf("%s: search: %v", c.Name, err)
			}
			keys = extractKeys(res)
		}

		relevant := map[string]bool{}
		for _, k := range c.Relevant {
			relevant[k] = true
		}

		sc := ScenarioResult{
			Name:     c.Name,
			Category: c.Category,
			Metrics:  map[string]float64{},
		}
		mrr := MRR(keys, relevant)
		recall := RecallAtK(keys, c.Relevant, 5)
		sc.Metrics["mrr"] = mrr
		sc.Metrics["recall@5"] = recall

		present := true
		for _, k := range c.Relevant {
			if rankOf(keys, k) < 0 {
				present = false
				sc.Errors = append(sc.Errors, "relevant key not retrieved: "+k)
			}
		}

		// Record the ranking signal: does the fresh/valid memory rank at or above
		// the stale one? Not yet a hard gate — see the scoring roadmap items.
		if c.PreferOver[0] != "" {
			fresh, stale := c.PreferOver[0], c.PreferOver[1]
			pf, ps := rankOf(keys, fresh), rankOf(keys, stale)
			outranks := 0.0
			if pf >= 0 && (ps < 0 || pf < ps) {
				outranks = 1.0
			}
			sc.Metrics["fresh_outranks_stale"] = outranks
			t.Logf("[%s] %s: fresh %q rank=%d, stale %q rank=%d, outranks=%.0f",
				c.Category, c.Name, fresh, pf, stale, ps, outranks)
		}

		// Determine pass by category.
		switch c.Category {
		case "preference-recall", "procedural-recall", "decision-recall":
			sc.Pass = mrr >= 0.5
			if !sc.Pass {
				sc.Errors = append(sc.Errors, "mrr below 0.5 baseline")
			}
		case "contradiction-surfacing":
			sc.Pass = true
			for _, k := range c.MustInclude {
				if rankOf(keys, k) < 0 {
					sc.Pass = false
					sc.Errors = append(sc.Errors, "must-include key not surfaced: "+k)
				}
			}
		case "abstention":
			// The tool must not fabricate: nothing should survive the score floor.
			sc.Pass = len(keys) == 0
			sc.Metrics["surfaced"] = float64(len(keys))
			if !sc.Pass {
				sc.Errors = append(sc.Errors, "fabricated results for an absent-answer query")
			}
		case "multi-hop":
			// Answer only reachable via edge expansion.
			sc.Pass = present
			if !present {
				sc.Errors = append(sc.Errors, "edge-only evidence not surfaced (spreading activation failed)")
			}
		default:
			// same-day-recall, freshness-update: gate on presence only; ranking
			// is the target metric recorded above.
			sc.Pass = present
		}

		scenarios = append(scenarios, sc)
	}
	return scenarios
}

// TestEvalPersonal seeds the personal store and asserts each slice's gate.
func TestEvalPersonal(t *testing.T) {
	s := seedPersonalStore(t)
	scenarios := runPersonalCases(t, context.Background(), s)

	report := buildPersonalReport(scenarios)
	logPersonalReport(t, report)
	writePersonalReport(t, report)

	for _, sc := range scenarios {
		if !sc.Pass {
			t.Errorf("scenario %q (%s) failed: %v [metrics=%v]", sc.Name, sc.Category, sc.Errors, sc.Metrics)
		}
	}
}

// TestEvalPersonalABMatrix runs the full personal eval under each knob
// configuration and prints a comparison matrix, so the eval itself measures the
// impact of the opt-in personalization features. It is a measurement (it does
// not fail) — read the logged table.
func TestEvalPersonalABMatrix(t *testing.T) {
	knobKeys := []string{"GHOST_FRESHNESS", "GHOST_UTILITY_WEIGHT"}
	orig := map[string]*string{}
	for _, k := range knobKeys {
		if v, ok := os.LookupEnv(k); ok {
			vv := v
			orig[k] = &vv
		}
	}
	t.Cleanup(func() {
		for _, k := range knobKeys {
			if orig[k] == nil {
				os.Unsetenv(k)
			} else {
				os.Setenv(k, *orig[k])
			}
		}
	})

	// Explicit knob control: freshness now defaults ON, so the off-baseline must
	// set GHOST_FRESHNESS=0 to be a true "nothing on" comparison point.
	configs := []struct {
		name string
		env  map[string]string
	}{
		{"baseline", map[string]string{"GHOST_FRESHNESS": "0"}},
		{"freshness", map[string]string{"GHOST_FRESHNESS": "1"}},
		{"utility", map[string]string{"GHOST_FRESHNESS": "0", "GHOST_UTILITY_WEIGHT": "0.6"}},
		{"both", map[string]string{"GHOST_FRESHNESS": "1", "GHOST_UTILITY_WEIGHT": "0.6"}},
	}

	metricOf := func(scs []ScenarioResult, name, metric string) float64 {
		for _, sc := range scs {
			if sc.Name == name {
				return sc.Metrics[metric]
			}
		}
		return -1
	}
	passOf := func(scs []ScenarioResult, name string) bool {
		for _, sc := range scs {
			if sc.Name == name {
				return sc.Pass
			}
		}
		return false
	}
	yn := func(ok bool) string {
		if ok {
			return "y"
		}
		return "n"
	}

	ctx := context.Background()
	t.Logf("── Personal-agent A/B matrix (FTS-only) — fresh↑/util↑/sameday↑ = fresh-outranks-stale ──")
	t.Logf("%-10s | pass  | fresh↑ util↑ sameday↑ | multihop abstain | meanMRR", "config")
	for _, cfg := range configs {
		for _, k := range knobKeys {
			os.Unsetenv(k)
		}
		for k, v := range cfg.env {
			os.Setenv(k, v)
		}
		s := seedPersonalStore(t)
		scs := runPersonalCases(t, ctx, s)
		pass, mrrSum := 0, 0.0
		for _, sc := range scs {
			if sc.Pass {
				pass++
			}
			mrrSum += sc.Metrics["mrr"]
		}
		t.Logf("%-10s | %2d/%2d | %5.0f %6.0f %8.0f | %8s %7s | %.3f",
			cfg.name, pass, len(scs),
			metricOf(scs, "bundler-update", "fresh_outranks_stale"),
			metricOf(scs, "utility-deploy", "fresh_outranks_stale"),
			metricOf(scs, "same-day-parking", "fresh_outranks_stale"),
			yn(passOf(scs, "phoenix-failover")), yn(passOf(scs, "absent-topic")),
			mrrSum/float64(len(scs)))
	}
}

func buildPersonalReport(scenarios []ScenarioResult) EvalReport {
	report := EvalReport{
		Timestamp: time.Now(),
		EmbedMode: os.Getenv("GHOST_EMBED_PROVIDER"),
		Scenarios: scenarios,
	}
	if report.EmbedMode == "" {
		report.EmbedMode = "none"
	}
	var sumMRR, sumRecall float64
	for _, sc := range scenarios {
		report.Summary.Total++
		if sc.Pass {
			report.Summary.Passed++
		} else {
			report.Summary.Failed++
		}
		sumMRR += sc.Metrics["mrr"]
		sumRecall += sc.Metrics["recall@5"]
	}
	if n := float64(len(scenarios)); n > 0 {
		report.Summary.MeanMRR = sumMRR / n
		report.Summary.MeanRecall = sumRecall / n
	}
	return report
}

func logPersonalReport(t *testing.T, report EvalReport) {
	t.Helper()
	// Per-category aggregate for a readable baseline table.
	type agg struct {
		n, pass       int
		mrr, outranks float64
		hasOutranks   bool
	}
	cats := map[string]*agg{}
	var order []string
	for _, sc := range report.Scenarios {
		a := cats[sc.Category]
		if a == nil {
			a = &agg{}
			cats[sc.Category] = a
			order = append(order, sc.Category)
		}
		a.n++
		if sc.Pass {
			a.pass++
		}
		a.mrr += sc.Metrics["mrr"]
		if v, ok := sc.Metrics["fresh_outranks_stale"]; ok {
			a.outranks += v
			a.hasOutranks = true
		}
	}
	sort.Strings(order)
	t.Logf("── Personal-agent eval (embed=%s) ──", report.EmbedMode)
	for _, cat := range order {
		a := cats[cat]
		line := cat + ":"
		if a.hasOutranks {
			t.Logf("  %-26s pass %d/%d  meanMRR %.2f  fresh-outranks %.0f/%d", line, a.pass, a.n, a.mrr/float64(a.n), a.outranks, a.n)
		} else {
			t.Logf("  %-26s pass %d/%d  meanMRR %.2f", line, a.pass, a.n, a.mrr/float64(a.n))
		}
	}
	t.Logf("  TOTAL: %d/%d passed, meanMRR %.3f, meanRecall@5 %.3f",
		report.Summary.Passed, report.Summary.Total, report.Summary.MeanMRR, report.Summary.MeanRecall)
}

// writePersonalReport emits the JSON report to GHOST_PERSONAL_EVAL_OUT if set.
func writePersonalReport(t *testing.T, report EvalReport) {
	t.Helper()
	out := os.Getenv("GHOST_PERSONAL_EVAL_OUT")
	if out == "" {
		return
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal personal report: %v", err)
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatalf("write personal report: %v", err)
	}
	t.Logf("wrote personal eval report to %s", out)
}
