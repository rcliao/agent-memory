package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rcliao/ghost/internal/store"
	"github.com/spf13/cobra"
)

func init() {
	cmd := &cobra.Command{
		Use:   "patch [unified-diff]",
		Short: "Apply a unified diff to a memory's content (safer than put for edits)",
		Long: `Apply a unified diff to a memory's current content.

Edits only the touched lines: everything the diff does not mention survives
byte-for-byte, so repeated edits cannot slowly reword the rest of the memory
the way whole-value rewrites do. Two guards make failures loud instead of
silent:

  - context verification: hunks whose context lines do not match the current
    content VERBATIM are refused ("patch context mismatch") — regenerate the
    diff against what is actually stored ('ghost get') and retry.
  - compare-and-swap: the write lands only if the head is still the version
    the patch was applied to; a concurrent writer surfaces as a version
    conflict, never a silent overwrite.

The diff is read from a positional argument, --patch-file, or stdin. Workflow:

  ghost get note --ns agent:me            # read current content + version
  <generate a unified diff against it>
  ghost patch --key note --ns agent:me --dry-run < edit.diff   # preview
  ghost patch --key note --ns agent:me < edit.diff             # apply`,
		Run: runPatch,
	}

	cmd.Flags().StringP("ns", "n", "", "Namespace (required)")
	cmd.Flags().StringP("key", "k", "", "Key (required)")
	cmd.Flags().String("patch-file", "", "Read the diff from a file instead of stdin")
	cmd.Flags().Int("base-version", 0, "Version the diff was generated against; fails with a version conflict if the head moved (0 = current head)")
	cmd.Flags().Bool("dry-run", false, "Validate and show the resulting content without writing")
	cmd.Flags().Bool("allow-empty", false, "Permit a patch whose result is empty (refused otherwise — an emptied memory is almost always an upstream failure)")
	cmd.Flags().Bool("unlock", false, "Authorize patching a memory tagged 'locked' (owner-initiated edits only)")

	cmd.MarkFlagRequired("ns")
	cmd.MarkFlagRequired("key")

	RootCmd.AddCommand(cmd)
}

func runPatch(cmd *cobra.Command, args []string) {
	ns, _ := cmd.Flags().GetString("ns")
	key, _ := cmd.Flags().GetString("key")
	patchFile, _ := cmd.Flags().GetString("patch-file")
	baseVersion, _ := cmd.Flags().GetInt("base-version")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	allowEmpty, _ := cmd.Flags().GetBool("allow-empty")
	unlock, _ := cmd.Flags().GetBool("unlock")

	if err := store.ValidateNS(ns); err != nil {
		exitErr("patch", fmt.Errorf("invalid namespace %q", ns))
	}

	var patch string
	switch {
	case patchFile != "":
		b, err := os.ReadFile(patchFile)
		if err != nil {
			exitErr("read patch file", err)
		}
		patch = string(b)
	case len(args) > 0:
		patch = strings.Join(args, " ")
	default:
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			b, err := io.ReadAll(os.Stdin)
			if err != nil {
				exitErr("read stdin", err)
			}
			patch = string(b)
		}
	}
	if strings.TrimSpace(patch) == "" {
		exitErr("patch", fmt.Errorf("a unified diff is required (positional arg, --patch-file, or stdin)"))
	}

	res, err := store.PatchMemory(cmd.Context(), st, store.PatchMemoryParams{
		NS: ns, Key: key, Patch: patch,
		BaseVersion: baseVersion, DryRun: dryRun,
		AllowEmpty: allowEmpty, Unlock: unlock,
	})
	if err != nil {
		// Typed categories so scripts and agents can pick the right repair:
		// a conflict means re-read and re-diff; a context mismatch means the
		// diff never matched; malformed means fix the diff format.
		switch {
		case errors.Is(err, store.ErrVersionConflict):
			exitErr("patch: version_conflict", err)
		case errors.Is(err, store.ErrPatchContext):
			exitErr("patch: context_mismatch", err)
		case errors.Is(err, store.ErrPatchMalformed):
			exitErr("patch: malformed_patch", err)
		default:
			exitErr("patch", err)
		}
	}
	outputJSON(cmd, res)
}
