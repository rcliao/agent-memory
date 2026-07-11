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
	return s
}

// TestEvalPersonal measures personal-agent retrieval quality across preference,
// procedural, decision, same-day, freshness, and contradiction slices. It gates
// on the categories that should already pass at the FTS baseline and RECORDS the
// ranking signals (fresh-over-stale) that upcoming scoring work is meant to move.
func TestEvalPersonal(t *testing.T) {
	s := seedPersonalStore(t)
	ctx := context.Background()

	var scenarios []ScenarioResult

	for _, c := range PersonalCases() {
		var keys []string
		if c.UseContext {
			res, err := s.Context(ctx, ContextParams{NS: PersonalNS, Query: c.Query, Budget: 1000})
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
		default:
			// same-day-recall, freshness-update: gate on presence only; ranking
			// is the target metric recorded above.
			sc.Pass = present
		}

		scenarios = append(scenarios, sc)
	}

	report := buildPersonalReport(scenarios)
	logPersonalReport(t, report)
	writePersonalReport(t, report)

	for _, sc := range scenarios {
		if !sc.Pass {
			t.Errorf("scenario %q (%s) failed: %v [metrics=%v]", sc.Name, sc.Category, sc.Errors, sc.Metrics)
		}
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
