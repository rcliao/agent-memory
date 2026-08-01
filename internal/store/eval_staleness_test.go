package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ── Stale-fact eval, render-matched ──────────────────────────────
//
// A fact is recorded, then later corrected. The question is whether what
// reaches the agent's context leads with the CURRENT value or the retired one.
//
// Why this harness is shaped the way it is:
//
//  1. Similarity cannot answer it. A correction is typically MORE
//     embedding-similar to the fact it refutes than a rephrasing is —
//     measured here at 0.83 for correction-vs-stale against 0.72–0.75 for
//     restatements, and independently at AUROC 0.59 (near chance) in
//     arXiv:2606.26511. So no reweighting of the composite score fixes this,
//     and an eval that only reports "did the right memory rank first" will
//     keep reporting noise.
//
//  2. Presentation confounds mechanism. arXiv:2607.16019 compared a
//     deprecation-tracking memory against flat retrieval and found +0.159 of
//     a +0.182 advantage came from LAYOUT — the mechanism residual was
//     indistinguishable from zero once render was held fixed. So this harness
//     crosses the two dimensions instead of bundling them:
//
//     render:    bare bullet list      vs  dated, structured lines
//     mechanism: no supersession       vs  supersession resolves the conflict
//
//     Four arms. If we ship typed edges and a new output field together and
//     see a gain, we will not know which one paid. This is the control that
//     lets us find out.
//
// What is measured is RETRIEVAL-SIDE EXPOSURE, not model behaviour: whether
// the retired value reaches the context unmarked and at or above the current
// one. Ghost is LLM-free, so there is no judge here and this deliberately
// does not claim to measure whether an agent would answer correctly.
//
// KNOWN LIMITATION — the render arms are currently INERT, by construction.
// A structural metric scores substring positions, and a presentation change
// that does not reorder anything cannot move it. The render effect the paper
// measured acts on a READER, so detecting it needs a judge, which ghost's
// test suite does not have. Arms B and D are therefore scaffolding: they will
// stay equal to A and C until a judged variant exists, and must NOT be read
// as "presentation does not help". They are kept so the 2x2 is already wired
// when a judge lands, and so nobody re-derives the control from scratch.
//
// FIRST MEASUREMENT (2026-08-01, n=6): every arm scored correct=4
// stale-exposed=2, and the two failures were EXACTLY the two cases where the
// retired fact was PINNED — nothing else failed. Unpinned corrections win on
// their own: recency and relevance are enough. A pinned stale fact wins
// regardless, because Phase 1 admits it before search runs at all. Pinning is
// the staleness amplifier, and the earlier live instance (a pinned cat colour
// that stayed wrong for four weeks after being corrected) is this exact
// shape.
//
// The corpus is synthetic by construction. Real household facts never enter
// this repo.

type staleCase struct {
	name string
	// The original fact, then the correction that retires it. Same subject,
	// different value — the shape a real correction takes.
	staleKey, staleContent string
	freshKey, freshContent string
	// Distinctive substrings that identify each value in rendered CONTEXT.
	staleMarker, freshMarker string
	// Tokens that identify each value in a terse ANSWER. These must be
	// separate from the context markers: a context marker has to be long
	// enough to avoid matching the correction's own text (the correction says
	// "NOT black"), but an answer is terse and says just "Black." The first
	// judged run scored exactly that answer as UNSCORED — the instrument
	// missed the failure it was built to catch. Fresh tokens are checked
	// first, so "blue-gray, not black" scores correct.
	freshAnswer, staleAnswer []string
	query                    string
	// pinnedStale reproduces the worst case: the retired value was pinned, so
	// it is chronically present while the correction must win a retrieval.
	pinnedStale bool
}

func staleCorpus() []staleCase {
	return []staleCase{
		{
			name:        "color-correction",
			staleKey:    "yard-visitor-profile",
			staleContent: "The stray cat that visits the yard at night is a small BLACK short-haired cat, first noted in June.",
			freshKey:    "yard-visitor-color-corrected",
			freshContent: "Correction: the yard cat is BLUE-GRAY short-haired with yellow-green eyes, NOT black. It only looked black under the night camera.",
			staleMarker: "BLACK short-haired", freshMarker: "BLUE-GRAY",
			query:       "what colour is the cat that visits the yard",
			freshAnswer: []string{"blue-gray", "blue gray", "bluish", "gray", "grey"},
			staleAnswer: []string{"black"},
			pinnedStale: true,
		},
		{
			name:        "renamed-api",
			staleKey:    "api-export-endpoint",
			staleContent: "To export a report, call the endpoint /v1/report/export with a POST request.",
			freshKey:    "api-export-endpoint-renamed",
			freshContent: "The export endpoint was renamed: use /v2/reports:export instead of /v1/report/export, which now returns 410.",
			staleMarker: "/v1/report/export with a POST", freshMarker: "/v2/reports:export",
			query:       "which endpoint exports a report",
			freshAnswer: []string{"/v2/reports:export", "v2"},
			staleAnswer: []string{"/v1/report/export", "v1"},
		},
		{
			name:        "moved-meeting",
			staleKey:    "team-sync-schedule",
			staleContent: "The weekly team sync happens Tuesdays at 10:00 in the small conference room.",
			freshKey:    "team-sync-moved-thursday",
			freshContent: "The weekly team sync moved to Thursdays at 14:00. It is no longer on Tuesday morning.",
			staleMarker: "Tuesdays at 10:00", freshMarker: "Thursdays at 14:00",
			query:       "when is the weekly team sync",
			freshAnswer: []string{"thursday"},
			staleAnswer: []string{"tuesday"},
		},
		{
			name:        "deprecated-flag",
			staleKey:    "build-flag-usage",
			staleContent: "Enable the fast path by passing --enable-turbo to the build command.",
			freshKey:    "build-flag-renamed",
			freshContent: "The --enable-turbo flag was removed. The fast path is now on by default and the flag errors out if passed.",
			staleMarker: "--enable-turbo to the build", freshMarker: "on by default",
			query:       "how do I turn on the build fast path",
			freshAnswer: []string{"default", "removed", "no longer"},
			staleAnswer: []string{"--enable-turbo", "enable-turbo"},
			pinnedStale: true,
		},
		{
			name:        "changed-preference",
			staleKey:    "reviewer-preference",
			staleContent: "The reviewer prefers pull requests split into many small commits for easier review.",
			freshKey:    "reviewer-preference-updated",
			freshContent: "The reviewer now prefers a single squashed commit per pull request; many small commits made bisecting harder.",
			staleMarker: "many small commits for easier", freshMarker: "single squashed commit",
			query:       "how should I structure commits in a pull request",
			freshAnswer: []string{"squash", "single commit", "one commit"},
			staleAnswer: []string{"many small", "small commits"},
		},
		{
			name:        "relocated-service",
			staleKey:    "metrics-dashboard-location",
			staleContent: "The metrics dashboard lives on the staging host under port 9090.",
			freshKey:    "metrics-dashboard-relocated",
			freshContent: "The metrics dashboard was relocated to the observability cluster on port 3000; the staging host no longer serves it.",
			staleMarker: "staging host under port 9090", freshMarker: "observability cluster on port 3000",
			query:       "where is the metrics dashboard hosted",
			freshAnswer: []string{"observability", "3000"},
			staleAnswer: []string{"staging", "9090"},
		},
	}
}

// renderMode is the presentation dimension. It varies layout and explicit
// fields ONLY — never correctness labels, which belong to the mechanism.
type renderMode int

const (
	renderBare   renderMode = iota // "- <content>", today's shape
	renderDated                    // "- [<key> | recorded <date>] <content>"
)

func (r renderMode) String() string {
	if r == renderDated {
		return "dated"
	}
	return "bare"
}

// supersedeResolver is the mechanism dimension. Given the assembled context it
// returns the set of memory keys known to be retired, so render can mark or
// drop them. nil means the mechanism is off.
//
// Cross-key supersession does not exist yet — that is the point of writing
// this first. resolverNone is today's behaviour and the arms using it are
// expected to be RED.
type supersedeResolver func(ctx context.Context, s *SQLiteStore, ns string, keys []string) map[string]bool

func resolverNone(context.Context, *SQLiteStore, string, []string) map[string]bool { return nil }

// resolverContradictsEdge is the mechanism as it exists today: a key is
// retired if some other retrieved key points at it with a `contradicts` edge.
// Since those edges are currently minted only by same-key supersede, this
// finds nothing for cross-key corrections — which is exactly the gap.
func resolverContradictsEdge(ctx context.Context, s *SQLiteStore, ns string, keys []string) map[string]bool {
	retired := map[string]bool{}
	for _, k := range keys {
		got, err := s.Get(ctx, GetParams{NS: ns, Key: k})
		if err != nil || len(got) == 0 {
			continue
		}
		id := got[0].ID
		edges, err := s.GetEdgesByNSKey(ctx, ns, k)
		if err != nil {
			continue
		}
		for _, e := range edges {
			// Only an INCOMING contradicts retires this key: something else
			// refutes it. An outgoing one means this memory is the refuter.
			if e.Rel == "contradicts" && e.ToID == id {
				retired[k] = true
			}
		}
	}
	return retired
}

type staleOutcome struct{ correct, staleExposed, miss int }

func (o staleOutcome) String() string {
	return fmt.Sprintf("correct=%d stale-exposed=%d miss=%d", o.correct, o.staleExposed, o.miss)
}

// runStaleArm builds a fresh store per case, assembles context, renders it
// under the given mode with the given mechanism, and scores exposure.
func runStaleArm(t *testing.T, mode renderMode, resolve supersedeResolver) staleOutcome {
	t.Helper()
	ctx := context.Background()
	var out staleOutcome

	for _, c := range staleCorpus() {
		s := newTestStore(t)
		base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

		// The stale fact is recorded first, the correction three weeks later.
		// Order matters: recency is one of the few signals that currently
		// separates them at all, so the gap has to be real.
		s.SetClock(func() time.Time { return base })
		if _, err := s.Put(ctx, PutParams{NS: "agent:eval", Key: c.staleKey, Content: c.staleContent,
			Kind: "semantic", Tier: "ltm", Pinned: c.pinnedStale, Importance: 0.6}); err != nil {
			t.Fatal(err)
		}
		later := base.AddDate(0, 0, 21)
		s.SetClock(func() time.Time { return later })
		if _, err := s.Put(ctx, PutParams{NS: "agent:eval", Key: c.freshKey, Content: c.freshContent,
			Kind: "semantic", Tier: "ltm", Importance: 0.6}); err != nil {
			t.Fatal(err)
		}
		seedDistractors(ctx, s, staleScale())
		seedSiblings(ctx, s, c, staleSiblings(), later)
		s.SetClock(func() time.Time { return later.AddDate(0, 0, 1) })

		res, err := s.Context(ctx, ContextParams{NS: "agent:eval", Query: c.query, Budget: 1200})
		if err != nil {
			t.Fatal(err)
		}
		keys := make([]string, 0, len(res.Memories))
		for _, m := range res.Memories {
			keys = append(keys, m.Key)
		}
		retired := map[string]bool{}
		if resolve != nil {
			retired = resolve(ctx, s, "agent:eval", keys)
		}

		rendered := renderContext(res, mode, retired, caseDates(c))
		before := out.staleExposed
		out.tally(&out, rendered, c, retired)
		if out.staleExposed > before {
			t.Logf("    STALE EXPOSED [%s] pinned=%v", c.name, c.pinnedStale)
		}
	}
	return out
}

// renderContext produces what the agent would actually read.
//
// dates carries created_at per key. ContextMemory does not expose it (adding
// a field would be a JSON output change), but the dated arm is worthless
// without it — "recorded on" is the strongest cue a reader has for deciding
// which of two conflicting values is current, and it is exactly the kind of
// explicit field the render-confound paper found was doing the work.
func renderContext(res *ContextResult, mode renderMode, retired map[string]bool, dates map[string]time.Time) string {
	var sb strings.Builder
	for _, m := range res.Memories {
		switch mode {
		case renderDated:
			when := ""
			if d, ok := dates[m.Key]; ok {
				when = " | recorded " + d.Format("2006-01-02")
			}
			sb.WriteString(fmt.Sprintf("- [%s%s] %s", m.Key, when, m.Content))
		default:
			sb.WriteString("- " + m.Content)
		}
		if retired[m.Key] {
			sb.WriteString("  (SUPERSEDED — no longer current)")
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// tally scores one case. "Stale exposed" is the failure that matters: the
// retired value reached the agent unmarked, with the current value absent or
// below it. A retired value that is present but MARKED is not an error — the
// agent can see it is history.
func (staleOutcome) tally(out *staleOutcome, rendered string, c staleCase, retired map[string]bool) {
	staleIdx := strings.Index(rendered, c.staleMarker)
	freshIdx := strings.Index(rendered, c.freshMarker)
	staleMarked := retired[c.staleKey]

	switch {
	case freshIdx < 0 && staleIdx < 0:
		out.miss++
	case freshIdx >= 0 && (staleIdx < 0 || staleMarked || freshIdx < staleIdx):
		out.correct++
	default:
		out.staleExposed++
	}
}

// TestEvalStalenessRenderMatched runs the 2x2 and reports every arm. It is a
// BASELINE instrument, not a gate: the mechanism arms are expected to be no
// better than the control until cross-key supersession exists. It fails only
// if the control regresses below what we measured when writing it, so the
// numbers below cannot silently rot.
func TestEvalStalenessRenderMatched(t *testing.T) {
	arms := []struct {
		render    renderMode
		mechanism supersedeResolver
		label     string
	}{
		{renderBare, resolverNone, "A control          (bare  / no mechanism)"},
		{renderDated, resolverNone, "B render-only      (dated / no mechanism) [inert: needs a judge]"},
		{renderBare, resolverContradictsEdge, "C mechanism-only   (bare  / contradicts)"},
		{renderDated, resolverContradictsEdge, "D both             (dated / contradicts)  [inert: needs a judge]"},
	}
	total := len(staleCorpus())
	results := map[string]staleOutcome{}
	for _, a := range arms {
		got := runStaleArm(t, a.render, a.mechanism)
		results[a.label] = got
		t.Logf("%s  %s  (n=%d)", a.label, got, total)
	}

	control := results["A control          (bare  / no mechanism)"]
	// Ratchet: whatever the control scores today, it must not get worse.
	// Recorded when this harness landed so a future change cannot quietly
	// degrade the baseline the mechanism will be judged against.
	if control.staleExposed > 4 {
		t.Errorf("control stale-exposure regressed: %s (n=%d)", control, total)
	}
	mech := results["C mechanism-only   (bare  / contradicts)"]
	if mech.staleExposed < control.staleExposed {
		t.Logf("NOTE: the contradicts-edge mechanism now beats the control (%d -> %d exposed) — "+
			"cross-key supersession may have landed; tighten this ratchet.", control.staleExposed, mech.staleExposed)
	}
}

// ── Judged variant: the memory quiz ──────────────────────────────
//
// The structural harness above cannot see presentation: it scores substring
// positions, and a render change that does not reorder anything cannot move
// it. But the effect the render-confound paper measured acts on a READER —
// so to measure it we have to put a reader in the loop.
//
// This is deliberately a QUIZ, not a judge-of-a-judge. The model gets only
// the rendered context and the question, and answers. Grading is primarily
// mechanical (does the answer carry the current value or the retired one),
// falling back to the existing llmJudgeScore only when the answer paraphrases
// past both markers. Fewer LLM decisions in the scoring path means fewer ways
// for the instrument to drift.
//
// Gated: needs the `claude` CLI and costs real calls, so CI never runs it.
//
//	GHOST_STALE_JUDGE=1 GHOST_EMBED_PROVIDER=local \
//	  go test ./internal/store/ -run TestEvalStalenessJudged -v -timeout 20m
//
// Read the result as a PAIRED comparison between render modes on identical
// retrieval — that is the whole point of holding mechanism fixed across them.
const staleQuizPrompt = `You are answering from a memory system's retrieved context.
Answer the question using ONLY the context provided. Be terse: one short sentence.
If the context contains conflicting values, answer with the one that is currently true.`

func TestEvalStalenessJudged(t *testing.T) {
	if os.Getenv("GHOST_STALE_JUDGE") != "1" {
		t.Skip("set GHOST_STALE_JUDGE=1 (requires the claude CLI; makes real calls)")
	}
	var llm LLMClient
	model := os.Getenv("GHOST_BENCH_LLM_MODEL")
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		llm = NewAnthropicClient(model)
	} else {
		llm = NewClaudeCLIClient(model)
	}
	t.Logf("quiz reader: %s", llm.Name())

	ctx := context.Background()
	type armResult struct{ correct, stale, miss int }
	results := map[renderMode]*armResult{renderBare: {}, renderDated: {}}

	for _, mode := range []renderMode{renderBare, renderDated} {
		for _, c := range staleCorpus() {
			rendered, err := assembleForCase(t, c, mode)
			if err != nil {
				t.Fatalf("%s: %v", c.name, err)
			}
			answer, err := llm.Generate(ctx, staleQuizPrompt,
				fmt.Sprintf("Context:\n%s\nQuestion: %s", rendered, c.query))
			if err != nil {
				t.Logf("  [%s/%s] reader error: %v", mode, c.name, err)
				results[mode].miss++
				continue
			}
			a := strings.ToLower(answer)
			switch {
			case containsAny(a, c.freshAnswer) || strings.Contains(a, strings.ToLower(c.freshMarker)):
				results[mode].correct++
			case containsAny(a, c.staleAnswer) || strings.Contains(a, strings.ToLower(c.staleMarker)):
				results[mode].stale++
				t.Logf("  [%s/%s] STALE ANSWER: %s", mode, c.name, trimQ(answer))
			default:
				// Paraphrased past both markers — fall back to the judge.
				if llmJudgeScore(ctx, llm, c.query, answer, c.freshContent) >= 1.0 {
					results[mode].correct++
				} else {
					results[mode].miss++
					t.Logf("  [%s/%s] UNSCORED: %s", mode, c.name, trimQ(answer))
				}
			}
		}
	}

	n := len(staleCorpus())
	for _, mode := range []renderMode{renderBare, renderDated} {
		r := results[mode]
		t.Logf("render=%-6s correct=%d stale=%d miss=%d (n=%d)", mode, r.correct, r.stale, r.miss, n)
	}
	delta := results[renderDated].correct - results[renderBare].correct
	t.Logf("render effect (dated - bare) on correct answers: %+d of %d", delta, n)
	t.Logf("NOTE: single run, no repeats — treat a delta of +/-1 as noise, not signal.")
}

// assembleForCase builds one case's store and returns the rendered context,
// with the mechanism held OFF so the two render arms differ only in layout.
func assembleForCase(t *testing.T, c staleCase, mode renderMode) (string, error) {
	budget := 1200
	if v := os.Getenv("GHOST_STALE_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			budget = n
		}
	}
	return assembleForCaseBudget(t, c, mode, budget)
}

func assembleForCaseBudget(t *testing.T, c staleCase, mode renderMode, budget int) (string, error) {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	s.SetClock(func() time.Time { return base })
	if _, err := s.Put(ctx, PutParams{NS: "agent:eval", Key: c.staleKey, Content: c.staleContent,
		Kind: "semantic", Tier: "ltm", Pinned: c.pinnedStale, Importance: 0.6}); err != nil {
		return "", err
	}
	later := base.AddDate(0, 0, 21)
	s.SetClock(func() time.Time { return later })
	if _, err := s.Put(ctx, PutParams{NS: "agent:eval", Key: c.freshKey, Content: c.freshContent,
		Kind: "semantic", Tier: "ltm", Importance: 0.6}); err != nil {
		return "", err
	}
	seedDistractors(ctx, s, staleScale())
	seedSiblings(ctx, s, c, staleSiblings(), later)
	s.SetClock(func() time.Time { return later.AddDate(0, 0, 1) })

	res, err := s.Context(ctx, ContextParams{NS: "agent:eval", Query: c.query, Budget: budget})
	if err != nil {
		return "", err
	}
	return renderContext(res, mode, nil, caseDates(c)), nil
}

// caseDates mirrors the write timeline used when seeding each case.
func caseDates(c staleCase) map[string]time.Time {
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	return map[string]time.Time{
		c.staleKey: base,
		c.freshKey: base.AddDate(0, 0, 21),
	}
}


// TestEvalStalenessOmissionProbe locates the failure mode that actually
// matters. The judged quiz scored 6/6 in both render modes at budget 1200:
// when both values are present, a capable reader picks the current one and
// ordering barely matters. So mis-RANKING is not the risk — OMISSION is. This
// sweeps the budget to find where the correction stops reaching context at
// all, which is the point past which no render and no reader can help.
func TestEvalStalenessOmissionProbe(t *testing.T) {
	for _, budget := range []int{1200, 800, 600, 400, 300, 200} {
		var freshMissing, staleSurvives int
		for _, c := range staleCorpus() {
			rendered, err := assembleForCaseBudget(t, c, renderBare, budget)
			if err != nil {
				t.Fatal(err)
			}
			hasFresh := strings.Contains(rendered, c.freshMarker)
			hasStale := strings.Contains(rendered, c.staleMarker)
			if !hasFresh {
				freshMissing++
				if hasStale {
					staleSurvives++
					t.Logf("  budget=%-5d [%s] correction ABSENT but stale value present (pinned=%v)", budget, c.name, c.pinnedStale)
				}
			}
		}
		t.Logf("budget=%-5d correction missing in %d/%d cases; of those, %d still show the stale value",
			budget, freshMissing, len(staleCorpus()), staleSurvives)
	}
}


// staleScale controls how many distractors compete with the correction.
//
// This exists because the first judged run scored 6/6 in both render modes and
// the omission probe found the correction present at every budget down to 200
// tokens — on a FIVE-memory store. That is not a finding about ghost, it is a
// finding about the corpus: with nothing to compete against, the correction
// cannot lose. The live failure that motivated all of this happened in a store
// holding 8,155 memories. Competition is the variable that was missing.
func staleScale() int {
	if v := os.Getenv("GHOST_STALE_SCALE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 3
}

// seedDistractors writes n unrelated-but-plausible memories so the correction
// has to earn its slot. Deterministic content: no clock, no randomness.
func seedDistractors(ctx context.Context, s *SQLiteStore, n int) {
	topics := []string{
		"The office kitchen restocks coffee beans on Monday mornings",
		"Parking badges expire at the end of each quarter and renew automatically",
		"The wiki search box only indexes page titles, not body text",
		"Meeting rooms on the third floor need a badge tap to unlock",
		"Expense reports over 200 dollars require a second approver",
		"The staging database is reset every Sunday at midnight",
		"Laptop encryption checks run automatically on the first login of the week",
		"Visitor wifi passwords rotate on the first of each month",
	}
	for i := 0; i < n; i++ {
		_, _ = s.Put(ctx, PutParams{NS: "agent:eval",
			Key:     fmt.Sprintf("distractor-%d", i),
			Content: fmt.Sprintf("%s (note %d).", topics[i%len(topics)], i),
			Kind:    "semantic", Tier: "ltm", Importance: 0.4})
	}
}


// staleSiblings controls how many SAME-SUBJECT memories compete with the
// correction. This is the variable that actually reproduces the live failure.
//
// Unrelated volume does nothing: at 150 unrelated distractors the correction
// still reached context at every budget down to 200 tokens, because it wins
// on relevance uncontested. But the live case had FOURTEEN memories about the
// same cat — the correction was one voice among thirteen siblings, all
// scoring well on the same query, several of them more recent than it. That
// is what buries a correction: not noise, but its own topic.
func staleSiblings() int {
	if v := os.Getenv("GHOST_STALE_SIBLINGS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// seedSiblings writes n memories about the SAME subject as the case, carrying
// incidental detail rather than the disputed value. They are written AFTER the
// correction so they also out-recency it — the shape the live data had.
func seedSiblings(ctx context.Context, s *SQLiteStore, c staleCase, n int, after time.Time) {
	if n == 0 {
		return
	}
	subject := strings.SplitN(c.query, " ", 4)
	topic := strings.Join(subject[min(len(subject), 2):], " ")
	details := []string{
		"was observed again today, no change to note",
		"came up in conversation and nobody had questions",
		"was mentioned in the weekly notes as routine",
		"appeared in the log twice this week",
		"was reviewed and left as-is",
	}
	for i := 0; i < n; i++ {
		when := after.AddDate(0, 0, i+1)
		s.SetClock(func() time.Time { return when })
		_, _ = s.Put(ctx, PutParams{NS: "agent:eval",
			Key:     fmt.Sprintf("%s-sibling-%d", c.freshKey, i),
			Content: fmt.Sprintf("Regarding %s: it %s.", topic, details[i%len(details)]),
			Kind:    "episodic", Tier: "ltm", Importance: 0.5})
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}


func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(haystack, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
