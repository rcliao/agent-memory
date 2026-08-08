package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rcliao/ghost/internal/model"
)

// ── Context-verified patch (S2 from the buzz NIP-AE read) ────────────
//
// applyPatch applies a unified diff to content, refusing any hunk whose
// context or removal lines do not match the current value VERBATIM — no
// fuzzing, no whitespace normalisation. Two failure modes are deliberately
// loud instead of silent:
//
//   - a patch generated against an older wording refuses with ErrPatchContext
//     (the caller's picture of the memory is stale — re-read and remake), and
//   - a patch that edits nothing is refused outright (it is almost always a
//     malformed diff, and "applied successfully, changed nothing" would hide
//     that).
//
// The reason this exists: ghost's only write primitive was whole-value
// replacement, which forces an LLM to regenerate every sentence to change
// one. Regeneration never reproduces text byte-for-byte, which is one engine
// of measured restatement drift. A patch touches only the edited lines;
// everything else survives byte-exact because it is never re-generated.
// Modelled on buzz `mem patch` (docs/research/buzz-nip-ae-memory-comparison.md),
// which in turn behaves like `git apply` without fuzz.

// ErrPatchContext is returned when a hunk's context or removal lines do not
// match the current content exactly. The caller's view is stale: re-read the
// memory, regenerate the diff against what is actually there, retry. Distinct
// from ErrVersionConflict so callers can tell "the value moved" from "my diff
// never matched".
var ErrPatchContext = errors.New("patch context mismatch")

// ErrPatchMalformed is returned for input that is not a usable unified diff:
// no hunks, hunks with no additions or removals, or lines that are not valid
// hunk content.
var ErrPatchMalformed = errors.New("malformed patch")

type patchHunk struct {
	startLine int      // 1-based line in the ORIGINAL content, from @@ -a,b
	lines     []string // raw hunk body lines, each starting ' ', '-' or '+'
}

// parsePatch extracts hunks from a unified diff. A single `--- ` / `+++ `
// file-header pair is tolerated (agents copy what git prints); anything else
// outside a hunk is an error rather than silently skipped.
func parsePatch(patch string) ([]patchHunk, error) {
	lines := strings.Split(patch, "\n")
	var hunks []patchHunk
	var cur *patchHunk
	edits := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "@@"):
			var a, b, c, d int
			if _, err := fmt.Sscanf(line, "@@ -%d,%d +%d,%d @@", &a, &b, &c, &d); err != nil {
				// Tolerate the single-line form "@@ -3 +3 @@".
				if _, err2 := fmt.Sscanf(line, "@@ -%d +%d @@", &a, &c); err2 != nil {
					return nil, fmt.Errorf("bad hunk header %q: %w", line, ErrPatchMalformed)
				}
			}
			hunks = append(hunks, patchHunk{startLine: a})
			cur = &hunks[len(hunks)-1]
		case cur != nil && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "-") || strings.HasPrefix(line, "+")):
			cur.lines = append(cur.lines, line)
			if line[0] == '-' || line[0] == '+' {
				edits++
			}
		case cur == nil && (strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ")):
			continue // one file-header pair before the first hunk
		case strings.TrimSpace(line) == "":
			continue // blank separator (or trailing newline in the patch text)
		default:
			return nil, fmt.Errorf("line %d is neither hunk header nor hunk content: %q: %w",
				i+1, line, ErrPatchMalformed)
		}
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("no hunks found: %w", ErrPatchMalformed)
	}
	if edits == 0 {
		return nil, fmt.Errorf("patch contains context only, no additions or removals: %w", ErrPatchMalformed)
	}
	return hunks, nil
}

// applyPatch applies a unified diff to content and returns the result.
// Context and removal lines must match the current content verbatim at the
// position each hunk lands; the trailing-newline state of the input is
// preserved exactly (silent newline churn is drift too).
func applyPatch(content, patch string) (string, error) {
	hunks, err := parsePatch(patch)
	if err != nil {
		return "", err
	}

	hadTrailingNL := strings.HasSuffix(content, "\n")
	src := strings.Split(strings.TrimSuffix(content, "\n"), "\n")

	var out []string
	pos := 0 // index into src of the next unconsumed line

	for hi, h := range hunks {
		anchor := h.startLine - 1 // 0-based
		if anchor < pos || anchor > len(src) {
			return "", fmt.Errorf("hunk %d starts at line %d, which is before the previous hunk ended or past the end (%d lines): %w",
				hi+1, h.startLine, len(src), ErrPatchContext)
		}
		// Copy the untouched region before this hunk verbatim.
		out = append(out, src[pos:anchor]...)
		pos = anchor

		for _, hl := range h.lines {
			op, text := hl[0], hl[1:]
			switch op {
			case ' ', '-':
				if pos >= len(src) {
					return "", fmt.Errorf("hunk %d expects %q at line %d but content ends at line %d: %w",
						hi+1, text, pos+1, len(src), ErrPatchContext)
				}
				if src[pos] != text {
					return "", fmt.Errorf("hunk %d expects %q at line %d but content has %q — the memory has changed since this patch was written: %w",
						hi+1, text, pos+1, src[pos], ErrPatchContext)
				}
				if op == ' ' {
					out = append(out, src[pos])
				}
				pos++
			case '+':
				out = append(out, text)
			}
		}
	}
	// Copy everything after the last hunk verbatim.
	out = append(out, src[pos:]...)

	result := strings.Join(out, "\n")
	if hadTrailingNL {
		result += "\n"
	}
	return result, nil
}

// PatchMemoryParams drives PatchMemory. BaseVersion, when non-zero, must
// match the head that the patch is applied to (the caller captured it at
// read time); zero means "whatever the head is right now", which is still
// CAS-protected against a concurrent move between the read and the write.
type PatchMemoryParams struct {
	NS    string
	Key   string
	Patch string
	// BaseVersion pins the version the diff was generated against.
	BaseVersion int
	// DryRun applies and validates the patch, returning the would-be content
	// without writing anything.
	DryRun bool
	// AllowEmpty permits a patch whose result is empty. Without it an
	// all-lines-removed result is refused: an emptied memory is almost always
	// an upstream failure, and Put would soft-delete every prior version and
	// install the empty string as the live head.
	AllowEmpty bool
	// Unlock passes through to Put for memories carrying the locked tag.
	Unlock bool
}

// PatchResult is what a patch produces. On DryRun, NewVersion is the version
// the write WOULD create and Memory is nil.
type PatchResult struct {
	Content    string        `json:"content"`
	OldVersion int           `json:"old_version"`
	NewVersion int           `json:"new_version"`
	DryRun     bool          `json:"dry_run,omitempty"`
	Memory     *model.Memory `json:"memory,omitempty"`
}

// PatchMemory reads the live head of ns/key, applies a context-verified
// unified diff to it, and writes the result back under CAS — the write lands
// only if the head is still the version the patch was applied to. Composed
// entirely from Get + applyPatch + Put(BaseVersion), so it needs no new
// storage primitive: the same serialization that keeps version chains gapless
// makes the read-apply-write loop safe, and a concurrent writer surfaces as
// ErrVersionConflict rather than a corrupted merge.
//
// Everything the diff does not touch survives byte-exact. Kind, tags, tier,
// priority, importance and pinned state are carried over from the head
// unchanged — a patch edits content, not metadata.
func PatchMemory(ctx context.Context, st Store, p PatchMemoryParams) (*PatchResult, error) {
	head, err := st.Get(ctx, GetParams{NS: p.NS, Key: p.Key})
	if err != nil {
		return nil, err
	}
	if len(head) == 0 {
		return nil, fmt.Errorf("%s/%s not found", p.NS, p.Key)
	}
	h := head[0]
	if p.BaseVersion > 0 && h.Version != p.BaseVersion {
		return nil, fmt.Errorf("%s/%s: patch was generated against version %d but head is %d: %w",
			p.NS, p.Key, p.BaseVersion, h.Version, ErrVersionConflict)
	}

	patched, err := applyPatch(h.Content, p.Patch)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(patched) == "" && !p.AllowEmpty {
		return nil, fmt.Errorf("%s/%s: patch result is empty; refusing to blank a memory "+
			"(pass allow-empty if this is deliberate): %w", p.NS, p.Key, ErrPatchMalformed)
	}

	res := &PatchResult{Content: patched, OldVersion: h.Version, NewVersion: h.Version + 1}
	if p.DryRun {
		res.DryRun = true
		return res, nil
	}

	mem, err := st.Put(ctx, PutParams{
		NS: p.NS, Key: p.Key, Content: patched,
		Kind: h.Kind, Tags: h.Tags, Priority: h.Priority,
		Importance: h.Importance, Tier: h.Tier, Pinned: h.Pinned,
		Meta: h.Meta, Unlock: p.Unlock,
		// A patch edits content, not origin: the memory still records what
		// the same person said, lightly corrected.
		SourceUser: h.SourceUser, SourceKind: h.SourceKind,
		// CAS against the exact head the patch was applied to: if anything
		// moved the key between our Get and this Put, conflict — never a
		// silent overwrite of the concurrent edit.
		BaseVersion: h.Version,
		// The patched text intentionally differs by a few lines; similarity
		// dedup would see a near-duplicate and could short-circuit the write.
		SkipAutoLink: false,
	})
	if err != nil {
		return nil, err
	}
	res.Memory = mem
	res.NewVersion = mem.Version
	return res, nil
}
