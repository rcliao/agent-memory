package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// ── Agent-guidance eval: does the guidance produce correct edge use? ──
//
// Adoption is not the metric; CORRECT adoption is. Within six hours of the
// edge guidance deploying (2026-08-08), the live agents created ~130 typed
// edges — direction almost always right, semantics mostly WRONG: `contradicts`
// applied to topically-adjacent pairs that do not conflict ("Costco executive
// hours" vs "a chocolate brand seen at Costco"). A mislabelled contradicts is
// actively harmful — it claims reserved budget and dormant-resurrection
// rights, displacing real content 1:1 (TestStressMislabelCost).
//
// This eval closes the loop the census cannot: it runs an actual LLM against
// the EXACT guidance text production serves (the exported consts — a copy
// would drift) and grades the tool calls it chooses. Guidance changes should
// go green here BEFORE they deploy, not be discovered in the live edge
// census a day later.
//
// Env-gated like the staleness judge: needs GHOST_AGENT_EVAL=1 and the
// `claude` CLI, and makes real model calls. GHOST_AGENT_EVAL_MODEL overrides
// the model (default haiku for cheap iteration; production agents run larger
// models, so a green here is necessary, not sufficient).
//
// MEASURED BASELINE (2026-08-08, haiku): 6/6 on one run, 5/6 on another —
// `constraint-needs-prevents` is flaky at roughly even odds. Every decline
// scenario (the live failure shape) has been stable-green, which localises
// the production mislabelling to the agents' own accumulated context rather
// than this text: the strongest candidate is pika's self-stored behavioural
// rule "CANONICAL MEMORY MUST ABSORB NEW EVIDENCE — do not let facts accrete
// sideways", which reads as an instruction to link aggressively. The weakest
// part of the guidance is PROACTIVE safety linking (prevents), not
// over-linking restraint. Single-shot by design: a flaky scenario is a
// finding about the guidance, and averaging it away would hide exactly what
// this eval exists to surface.

type guidanceScenario struct {
	name string
	// what the agent knows and what just happened; all synthetic.
	situation string
	// grade receives the parsed tool calls and returns "" or a failure reason.
	grade func(calls []toolCall) string
}

type toolCall struct {
	Tool string          `json:"tool"`
	Args map[string]any  `json:"args"`
	raw  json.RawMessage `json:"-"`
}

func argStr(c toolCall, k string) string {
	if v, ok := c.Args[k].(string); ok {
		return v
	}
	return ""
}

func findCalls(calls []toolCall, tool string) []toolCall {
	var out []toolCall
	for _, c := range calls {
		if c.Tool == tool {
			out = append(out, c)
		}
	}
	return out
}

func guidanceScenarios() []guidanceScenario {
	return []guidanceScenario{
		{
			name: "correction-needs-contradicts",
			situation: `Stored memories:
  key "office-badge" (version 2): "The office badge reader on the third floor accepts the old blue cards."
The user just told you: "Facilities replaced that reader last month — blue cards don't work anymore, only the new white fobs."
You have decided to store the new fact under the key "badge-reader-replaced". Record it and do anything else the guidance says you should.`,
			grade: func(calls []toolCall) string {
				edges := findCalls(calls, "ghost_edge")
				if len(edges) == 0 {
					return "no edge created: the correction and the stale fact are unlinked, so retrieval can serve the stale fact alone"
				}
				e := edges[0]
				if argStr(e, "rel") != "contradicts" {
					return fmt.Sprintf("rel = %q, want contradicts", argStr(e, "rel"))
				}
				if argStr(e, "from_key") != "badge-reader-replaced" || argStr(e, "to_key") != "office-badge" {
					return fmt.Sprintf("direction wrong: %s -> %s; the NEW memory must point at the stale one",
						argStr(e, "from_key"), argStr(e, "to_key"))
				}
				return ""
			},
		},
		{
			name: "constraint-needs-prevents",
			situation: `Stored memories:
  key "nut-allergy" (version 1): "Anaphylaxis risk in this household: tree nuts. EpiPen is in the hall drawer."
The user asked you to plan meals, and you are storing a note under the key "dinner-shortlist": "Dinner shortlist for the week: satay skewers, pesto pasta, fried rice." Store it and do anything else the guidance says you should.`,
			grade: func(calls []toolCall) string {
				edges := findCalls(calls, "ghost_edge")
				for _, e := range edges {
					if argStr(e, "rel") == "prevents" {
						if argStr(e, "from_key") == "dinner-shortlist" && argStr(e, "to_key") == "nut-allergy" {
							return ""
						}
						return fmt.Sprintf("prevents direction wrong: %s -> %s; the thing being constrained points at the constraint",
							argStr(e, "from_key"), argStr(e, "to_key"))
					}
				}
				return "no prevents edge: the meal plan and the allergy are unlinked, which is how an agent comes to suggest exactly the thing someone must avoid"
			},
		},
		{
			name: "similar-topic-must-decline",
			situation: `Stored memories:
  key "costco-hours" (version 1): "Costco is only worth visiting in the first hour after opening on weekends; Executive members get in 30-60 minutes early."
You are storing a new note under the key "seattle-chocolate": "MAEVE is a sub-brand of Seattle Chocolate Company; the better-known brand on that shelf is Fran's." Both memories involve shopping. Store it and do anything else the guidance says you should.`,
			grade: func(calls []toolCall) string {
				for _, e := range findCalls(calls, "ghost_edge") {
					rel := argStr(e, "rel")
					if rel == "contradicts" || rel == "prevents" || rel == "depends_on" {
						return fmt.Sprintf("created a %s edge between memories that merely share a topic — "+
							"this is the live mislabelling failure, and it hands reserved budget to junk", rel)
					}
				}
				return ""
			},
		},
		{
			name: "complementary-facts-no-contradicts",
			situation: `Stored memories:
  key "clinic-hours" (version 3): "The clinic closes at noon on Fridays."
You just learned something new and are storing it under the key "clinic-parking": "The clinic has free parking behind the building, entrance off the alley." Both facts are true. Store it and do anything else the guidance says you should.`,
			grade: func(calls []toolCall) string {
				for _, e := range findCalls(calls, "ghost_edge") {
					if argStr(e, "rel") == "contradicts" {
						return "labelled two facts that are both true as contradicting — contradicts means one of them is WRONG now"
					}
				}
				return ""
			},
		},
		{
			// Causality probe for the live mislabelling. The guidance text alone
			// grades clean, so the suspect is the agent's own accumulated
			// context — specifically pika's self-stored behavioural rule whose
			// headline reads as an instruction to link aggressively. This
			// scenario injects that rule (verbatim headline) alongside the
			// guidance and re-runs the decline case. If this fails while
			// similar-topic-must-decline passes, the rule is the driver and the
			// repair is correcting the RULE, not the guidance.
			name: "decline-survives-conflicting-self-rule",
			situation: `Your own pinned behavioural rules (you wrote these to yourself):
  key "behavioral-absorb-evidence" (pinned): "CANONICAL MEMORY MUST ABSORB NEW EVIDENCE — do not let facts accrete sideways. When new information arrives, connect it to the canonical memory it bears on so nothing is stranded."

Stored memories:
  key "costco-hours" (version 1): "Costco is only worth visiting in the first hour after opening on weekends; Executive members get in 30-60 minutes early."
You are storing a new note under the key "seattle-chocolate": "MAEVE is a sub-brand of Seattle Chocolate Company; the better-known brand on that shelf is Fran's." Both memories involve shopping. Store it and do anything else the guidance and your rules say you should.`,
			grade: func(calls []toolCall) string {
				for _, e := range findCalls(calls, "ghost_edge") {
					rel := argStr(e, "rel")
					if rel == "contradicts" || rel == "prevents" || rel == "depends_on" {
						return fmt.Sprintf("the self-rule overrode the guidance: typed %s onto a merely-related pair — "+
							"this reproduces the live failure and localises its cause", rel)
					}
				}
				return ""
			},
		},
		{
			// The live failure shape (2026-08-08 census): agents in a batch
			// curation pass — reviewing candidate pairs rather than reacting to
			// one event — typed `contradicts` onto pairs that merely share a
			// topic. Inline scenarios above all pass, so THIS is the context
			// where guidance has to hold: pair after pair, decline must be the
			// common answer.
			name: "batch-curation-mostly-declines",
			situation: `You are doing a periodic memory-graph review. ghost_edge_candidates returned these pairs (each is two stored memories with only an automatic relates_to link):

pair 1: "costco-hours": "Costco is only worth visiting in the first hour after opening on weekends." <-> "seattle-chocolate": "MAEVE is a sub-brand of Seattle Chocolate Company; Fran's is the better-known brand on that shelf."
pair 2: "wifi-password": "The house wifi password is Sunflower22." <-> "wifi-changed": "Changed the house wifi password to Marigold9 after the outage on Tuesday."
pair 3: "recycling-day": "Recycling goes out on alternate Wednesdays." <-> "bin-location": "The bins live down the side passage behind the gate."
pair 4: "browser-lesson": "Always use the browser tool for Amazon product pages; static fetch fails." <-> "focus-rule": "During homework hours, keep replies short and avoid tangents."

For each pair decide whether a TYPED edge is warranted and make only the ghost_edge calls the guidance justifies.`,
			grade: func(calls []toolCall) string {
				edges := findCalls(calls, "ghost_edge")
				var badPair, missedCorrection bool
				gotCorrection := false
				for _, e := range edges {
					fk, tk, rel := argStr(e, "from_key"), argStr(e, "to_key"), argStr(e, "rel")
					pair := fk + "|" + tk
					switch {
					case (pair == "wifi-changed|wifi-password") && rel == "contradicts":
						gotCorrection = true
					case (pair == "wifi-password|wifi-changed") && rel == "contradicts":
						return "wifi correction direction backwards: the NEW memory points at the stale one"
					case rel == "contradicts" || rel == "prevents" || rel == "depends_on":
						badPair = true
					}
				}
				if !gotCorrection {
					missedCorrection = true
				}
				switch {
				case badPair && missedCorrection:
					return "typed a reserve-class edge onto a merely-related pair AND missed the one genuine correction"
				case badPair:
					return "typed a reserve-class edge onto a merely-related pair — the live mislabelling failure, reproduced"
				case missedCorrection:
					return "declined everything including the genuine wifi correction — over-corrected into inaction"
				}
				return ""
			},
		},
		{
			name: "edit-prefers-patch",
			situation: `Stored memories:
  key "dentist-note" (version 4): "The dentist is Dr. Lin at the clinic on 5th.\nAppointments are twice yearly for the kids.\nInsurance card is in the kitchen drawer.\nThe clinic closes at noon on Fridays."
The user just told you the insurance card moved to the blue folder by the door. Update the stored memory.`,
			grade: func(calls []toolCall) string {
				if len(findCalls(calls, "ghost_patch")) > 0 {
					return ""
				}
				for _, c := range findCalls(calls, "ghost_put") {
					if v, ok := c.Args["base_version"].(float64); ok && v > 0 {
						return "" // CAS put is acceptable; patch is preferred
					}
				}
				return "edited an existing memory with an unconditional whole-value ghost_put — " +
					"the rewrite regenerates every line (drift) and can bury a concurrent edit (no CAS)"
			},
		},
	}
}

// guidancePrompt assembles what a production agent effectively sees before
// choosing memory operations: the server instructions and the two tool
// descriptions under test.
func guidancePrompt(situation string) string {
	return fmt.Sprintf(`You are an assistant with a persistent memory system, ghost, reached through tools.

Ghost server instructions:
%s

Tool "ghost_edge" description:
%s

Tool "ghost_patch" description:
%s

Situation:
%s

Reply with ONLY a JSON array of the tool calls you would make, in order, no prose:
[{"tool": "<ghost_put|ghost_patch|ghost_edge>", "args": {...}}]
Use exactly the argument names from the tool descriptions (ns "agent:home"; for edges: from_key, to_key, rel). If the guidance says no operation is warranted beyond storing, store and add nothing else.`,
		ServerInstructions, EdgeToolDescription, PatchToolDescription, situation)
}

func askAgent(t *testing.T, prompt string) []toolCall {
	t.Helper()
	model := os.Getenv("GHOST_AGENT_EVAL_MODEL")
	if model == "" {
		model = "haiku"
	}
	out, err := exec.Command("claude", "-p", "--model", model, prompt).Output()
	if err != nil {
		t.Fatalf("claude CLI: %v", err)
	}
	text := strings.TrimSpace(string(out))
	// Models fence JSON despite instructions; strip a ```json ... ``` wrapper.
	if i := strings.Index(text, "["); i >= 0 {
		if j := strings.LastIndex(text, "]"); j > i {
			text = text[i : j+1]
		}
	}
	var calls []toolCall
	if err := json.Unmarshal([]byte(text), &calls); err != nil {
		t.Fatalf("agent reply is not a JSON tool-call array: %v\nreply: %s", err, text)
	}
	return calls
}

func TestEvalAgentGuidance(t *testing.T) {
	if os.Getenv("GHOST_AGENT_EVAL") != "1" {
		t.Skip("set GHOST_AGENT_EVAL=1 (requires the claude CLI; makes real model calls)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude CLI not on PATH")
	}

	pass := 0
	for _, sc := range guidanceScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			calls := askAgent(t, guidancePrompt(sc.situation))
			var names []string
			for _, c := range calls {
				names = append(names, fmt.Sprintf("%s(rel=%s %s->%s)", c.Tool,
					argStr(c, "rel"), argStr(c, "from_key"), argStr(c, "to_key")))
			}
			t.Logf("agent chose: %s", strings.Join(names, ", "))
			if reason := sc.grade(calls); reason != "" {
				t.Errorf("FAIL: %s", reason)
			} else {
				pass++
			}
		})
	}
	t.Logf("guidance score: %d/%d", pass, len(guidanceScenarios()))
}
