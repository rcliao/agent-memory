package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/rcliao/ghost/internal/procedure"
	"github.com/rcliao/ghost/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:   "mine-procedures",
		Short: "Induce recurring workflows from usage sequences into procedural memories (no LLM)",
		Long: `Reads usage sessions from stdin — one session per line, steps separated by
whitespace (e.g. tool or command names) — and mines recurring step sequences by
frequency. High-support routines are stored as procedural memories whose
importance scales with how often the workflow recurs. Pure counting, no LLM.

Example:
  printf 'edit build test commit\nbuild test commit\nedit build test\n' \
    | ghost mine-procedures -n agent:me`,
		Run: runMineProcedures,
	}

	cmd.Flags().StringP("ns", "n", "", "Namespace (required)")
	cmd.Flags().StringP("tags", "t", "", "Comma-separated tags added to every stored workflow")
	cmd.Flags().Int("min-support", 2, "Minimum sessions a workflow must appear in")
	cmd.Flags().Int("min-len", 2, "Minimum steps per workflow")
	cmd.Flags().Int("max-len", 5, "Maximum steps per workflow")
	cmd.Flags().Int("top", 20, "Maximum workflows to store")
	cmd.Flags().Bool("dry-run", false, "Print mined workflows as JSON without storing")

	cmd.MarkFlagRequired("ns")
	RootCmd.AddCommand(cmd)
}

type mineResult struct {
	Namespace string           `json:"ns"`
	Sessions  int              `json:"sessions"`
	Found     int              `json:"found"`
	Stored    int              `json:"stored"`
	DryRun    bool             `json:"dry_run,omitempty"`
	Workflows []mineStoredItem `json:"workflows"`
}

type mineStoredItem struct {
	Key        string   `json:"key"`
	Steps      []string `json:"steps"`
	Support    int      `json:"support"`
	Importance float64  `json:"importance"`
}

func runMineProcedures(cmd *cobra.Command, args []string) {
	ns, _ := cmd.Flags().GetString("ns")
	tagsStr, _ := cmd.Flags().GetString("tags")
	minSupport, _ := cmd.Flags().GetInt("min-support")
	minLen, _ := cmd.Flags().GetInt("min-len")
	maxLen, _ := cmd.Flags().GetInt("max-len")
	top, _ := cmd.Flags().GetInt("top")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	if err := store.ValidateNS(ns); err != nil {
		exitErr("mine-procedures", fmt.Errorf("invalid namespace %q", ns))
	}

	sessions := readSessions(cmd, args)
	if len(sessions) == 0 {
		exitErr("mine-procedures", fmt.Errorf("no sessions on stdin (one session per line, steps whitespace-separated)"))
	}

	patterns := procedure.Mine(sessions, procedure.Options{
		MinSupport: minSupport,
		MinLen:     minLen,
		MaxLen:     maxLen,
		TopN:       top,
	})

	baseTags := mergeTags([]string{"procedural", "workflow"}, parseTags(tagsStr))

	res := mineResult{Namespace: ns, Sessions: len(sessions), Found: len(patterns), DryRun: dryRun}
	for _, p := range patterns {
		key := "workflow-" + kebabSteps(p.Steps)
		imp := workflowImportance(p.Support)
		content := fmt.Sprintf("Recurring workflow (seen %d×): %s", p.Support, strings.Join(p.Steps, " → "))

		if !dryRun {
			if _, err := st.Put(cmd.Context(), store.PutParams{
				NS:         ns,
				Key:        key,
				Content:    content,
				Kind:       "procedural",
				Tags:       baseTags,
				Priority:   "normal",
				Importance: imp,
				Dedup:      true,
			}); err != nil {
				exitErr("mine-procedures", fmt.Errorf("store %q: %w", key, err))
			}
			res.Stored++
		}
		res.Workflows = append(res.Workflows, mineStoredItem{Key: key, Steps: p.Steps, Support: p.Support, Importance: imp})
	}

	outputJSON(cmd, res)
}

// readSessions reads stdin as one session per line, each split on whitespace.
func readSessions(cmd *cobra.Command, args []string) [][]string {
	var text string
	if len(args) > 0 {
		text = strings.Join(args, "\n")
	} else {
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				exitErr("mine-procedures read stdin", err)
			}
			text = string(b)
		}
	}
	var sessions [][]string
	for _, line := range strings.Split(text, "\n") {
		steps := strings.Fields(line)
		if len(steps) > 0 {
			sessions = append(sessions, steps)
		}
	}
	return sessions
}

// workflowImportance scales importance with support (practice), capped so a
// mined routine never outweighs a curated critical fact.
func workflowImportance(support int) float64 {
	imp := 0.4 + 0.05*float64(support)
	if imp > 0.85 {
		imp = 0.85
	}
	return imp
}

func kebabSteps(steps []string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(strings.Join(steps, "-")) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		case !lastDash:
			b.WriteRune('-')
			lastDash = true
		}
	}
	key := strings.Trim(b.String(), "-")
	if len(key) > 60 {
		key = strings.Trim(key[:60], "-")
	}
	if key == "" {
		key = "steps"
	}
	return key
}
