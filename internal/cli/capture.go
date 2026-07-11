package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rcliao/ghost/internal/capture"
	"github.com/rcliao/ghost/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:   "capture [text]",
		Short: "Extract and store memories from raw text — no LLM required",
		Long: `Capture turns raw usage (a transcript, an exchange, a note) into memories
using deterministic heuristics: named-entity salience plus intent classifiers
(preference, correction, decision, gotcha, procedural, fact). Each candidate gets
an inferred kind, a mechanical importance score, and a descriptive key, then is
stored via the normal Put path (deduped by default).

Text is read from a positional arg or stdin. This is the LLM-free tier of the
write path; it needs no API key.

With --json, stdin is instead a JSON array of pre-extracted candidates (the same
shape Extract emits) — so an LLM tier can produce higher-fidelity candidates and
commit them through this identical dedup+store path. With or without an LLM in
front, capture is the one write command.`,
		Run: runCapture,
	}

	cmd.Flags().StringP("ns", "n", "", "Namespace (required)")
	cmd.Flags().StringP("tags", "t", "", "Comma-separated tags added to every memory")
	cmd.Flags().String("source", "", "Source tag added to every memory (e.g. session:abc123)")
	cmd.Flags().String("kind", "", "Override inferred kind: semantic, episodic, procedural")
	cmd.Flags().Float64("min-salience", 0.35, "Drop candidates below this mechanical importance")
	cmd.Flags().Int("max", 12, "Maximum candidates to store (highest importance first; -1 = unlimited)")
	cmd.Flags().String("speaker", "", "Only capture lines from this speaker (e.g. user)")
	cmd.Flags().Bool("json", false, "Read stdin as a JSON array of pre-extracted candidates (LLM tier)")
	cmd.Flags().Bool("dry-run", false, "Print candidates as JSON without storing")
	cmd.Flags().Bool("dedup", true, "Skip storing when a semantically similar memory already exists")

	cmd.MarkFlagRequired("ns")

	RootCmd.AddCommand(cmd)
}

// captureResult is the JSON summary printed after a capture run.
type captureResult struct {
	Namespace  string              `json:"ns"`
	Candidates int                 `json:"candidates"`
	Stored     int                 `json:"stored"`
	DryRun     bool                `json:"dry_run,omitempty"`
	Memories   []captureStoredItem `json:"memories"`
}

type captureStoredItem struct {
	Key        string   `json:"key"`
	Kind       string   `json:"kind"`
	Importance float64  `json:"importance"`
	Priority   string   `json:"priority"`
	Tags       []string `json:"tags,omitempty"`
	Cues       []string `json:"cues,omitempty"`
}

func runCapture(cmd *cobra.Command, args []string) {
	ns, _ := cmd.Flags().GetString("ns")
	tagsStr, _ := cmd.Flags().GetString("tags")
	source, _ := cmd.Flags().GetString("source")
	kindOverride, _ := cmd.Flags().GetString("kind")
	minSalience, _ := cmd.Flags().GetFloat64("min-salience")
	maxCap, _ := cmd.Flags().GetInt("max")
	speaker, _ := cmd.Flags().GetString("speaker")
	asJSON, _ := cmd.Flags().GetBool("json")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	dedup, _ := cmd.Flags().GetBool("dedup")

	if err := store.ValidateNS(ns); err != nil {
		exitErr("capture", fmt.Errorf("invalid namespace %q — use letters, digits, hyphens, and colons (e.g. 'agent:pikamini')", ns))
	}
	if err := validateKind(kindOverride); err != nil {
		exitErr("capture", err)
	}

	input := readCaptureInput(cmd, args)
	if strings.TrimSpace(input) == "" {
		exitErr("capture", fmt.Errorf("no input (provide text as an argument, via stdin, or a JSON array with --json)"))
	}

	// Base tags applied to every stored memory, from both tiers, so behavior is
	// identical whether or not an LLM produced the candidates.
	baseTags := parseTags(tagsStr)
	if source != "" {
		baseTags = append(baseTags, source)
	}

	var candidates []capture.Candidate
	if asJSON {
		if err := json.Unmarshal([]byte(input), &candidates); err != nil {
			exitErr("capture", fmt.Errorf("invalid --json input: %w", err))
		}
	} else {
		candidates = capture.Extract(input, capture.Options{
			MinSalience:   minSalience,
			MaxCandidates: maxCap,
			SpeakerFilter: speaker,
		})
	}

	if kindOverride != "" {
		for i := range candidates {
			candidates[i].Kind = kindOverride
		}
	}

	res := captureResult{Namespace: ns, Candidates: len(candidates), DryRun: dryRun}
	for _, c := range candidates {
		tags := mergeTags(c.Tags, baseTags)

		if !dryRun {
			if _, err := st.Put(cmd.Context(), store.PutParams{
				NS:         ns,
				Key:        c.Key,
				Content:    c.Content,
				Kind:       c.Kind,
				Tags:       tags,
				Priority:   c.Priority,
				Importance: c.Importance,
				Dedup:      dedup,
			}); err != nil {
				exitErr("capture", fmt.Errorf("store %q: %w", c.Key, err))
			}
			res.Stored++
		}

		res.Memories = append(res.Memories, captureStoredItem{
			Key:        c.Key,
			Kind:       c.Kind,
			Importance: c.Importance,
			Priority:   c.Priority,
			Tags:       tags,
			Cues:       c.Cues,
		})
	}

	outputJSON(cmd, res)
}

// readCaptureInput returns the positional arg joined, or stdin when no arg and
// stdin is piped.
func readCaptureInput(cmd *cobra.Command, args []string) string {
	if len(args) > 0 {
		return strings.Join(args, " ")
	}
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			exitErr("capture read stdin", err)
		}
		return string(b)
	}
	return ""
}

func parseTags(s string) []string {
	var tags []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// mergeTags returns the union of a candidate's tags and the base tags, order
// preserved (candidate first), deduplicated.
func mergeTags(candidate, base []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append(append([]string(nil), candidate...), base...) {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
