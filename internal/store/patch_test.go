package store

import (
	"errors"
	"strings"
	"testing"
)

// ── Context-verified patch (S2 from the buzz NIP-AE read) ────────────
//
// applyPatch applies a unified diff to a memory's current content, refusing
// any hunk whose context lines do not match the current value VERBATIM. The
// point is byte-exact preservation of everything the edit does not touch:
// ghost's only write primitive was whole-value replacement, which forces an
// LLM to regenerate ten sentences to change one — and regeneration never
// reproduces text exactly, which is one engine of the measured restatement
// drift. A patch mutates only the touched lines, and a stale mental model of
// the content is refused instead of fuzzily applied.

const dentistNote = `The dentist is Dr. Lin at the clinic on 5th.
Appointments are twice yearly for the kids.
Insurance card is in the kitchen drawer.
The clinic closes at noon on Fridays.`

func TestApplyPatchCleanEdit(t *testing.T) {
	patch := `@@ -1,4 +1,4 @@
 The dentist is Dr. Lin at the clinic on 5th.
 Appointments are twice yearly for the kids.
-Insurance card is in the kitchen drawer.
+Insurance card is now in the blue folder by the door.
 The clinic closes at noon on Fridays.`

	got, err := applyPatch(dentistNote, patch)
	if err != nil {
		t.Fatalf("clean patch refused: %v", err)
	}
	want := strings.Replace(dentistNote,
		"Insurance card is in the kitchen drawer.",
		"Insurance card is now in the blue folder by the door.", 1)
	if got != want {
		t.Errorf("patched content wrong:\n got: %q\nwant: %q", got, want)
	}
	// The untouched lines must survive BYTE-EXACT — that is the entire point.
	for _, line := range []string{
		"The dentist is Dr. Lin at the clinic on 5th.",
		"Appointments are twice yearly for the kids.",
		"The clinic closes at noon on Fridays.",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("untouched line mutated or lost: %q", line)
		}
	}
}

func TestApplyPatchRefusesStaleContext(t *testing.T) {
	// The patch was written against an older wording ("kitchen drawer"), but
	// the memory has since been reworded. Verbatim context match must refuse —
	// fuzzy application here is how a confidently-stale edit corrupts a
	// memory.
	current := strings.Replace(dentistNote,
		"Insurance card is in the kitchen drawer.",
		"The insurance card lives in the hallway cabinet now.", 1)
	patch := `@@ -1,4 +1,4 @@
 The dentist is Dr. Lin at the clinic on 5th.
 Appointments are twice yearly for the kids.
-Insurance card is in the kitchen drawer.
+Insurance card is now in the blue folder by the door.
 The clinic closes at noon on Fridays.`

	_, err := applyPatch(current, patch)
	if err == nil {
		t.Fatal("stale patch applied over reworded content — corruption instead of refusal")
	}
	if !errors.Is(err, ErrPatchContext) {
		t.Errorf("refusal must be the typed ErrPatchContext so callers know to re-read; got %v", err)
	}
}

func TestApplyPatchMultiHunk(t *testing.T) {
	patch := `@@ -1,2 +1,2 @@
-The dentist is Dr. Lin at the clinic on 5th.
+The dentist is Dr. Park at the clinic on 5th.
 Appointments are twice yearly for the kids.
@@ -4,1 +4,1 @@
-The clinic closes at noon on Fridays.
+The clinic closes at two on Fridays.`

	got, err := applyPatch(dentistNote, patch)
	if err != nil {
		t.Fatalf("multi-hunk patch refused: %v", err)
	}
	if !strings.Contains(got, "Dr. Park") || !strings.Contains(got, "at two on Fridays") {
		t.Errorf("multi-hunk edits not applied: %q", got)
	}
	if !strings.Contains(got, "Insurance card is in the kitchen drawer.") {
		t.Errorf("line between hunks mutated: %q", got)
	}
}

func TestApplyPatchIgnoresFileHeaders(t *testing.T) {
	// Agents copy the format git produces; a single ---/+++ header pair is
	// noise, not an error.
	patch := `--- a/memory
+++ b/memory
@@ -3,1 +3,1 @@
-Insurance card is in the kitchen drawer.
+Insurance card is now in the blue folder by the door.`
	got, err := applyPatch(dentistNote, patch)
	if err != nil {
		t.Fatalf("patch with file headers refused: %v", err)
	}
	if !strings.Contains(got, "blue folder") {
		t.Errorf("edit not applied: %q", got)
	}
}

func TestApplyPatchRefusesGarbage(t *testing.T) {
	for name, patch := range map[string]string{
		"empty":        "",
		"no hunks":     "just some prose that is not a diff at all",
		"context only": "@@ -1,1 +1,1 @@\n The dentist is Dr. Lin at the clinic on 5th.",
	} {
		if _, err := applyPatch(dentistNote, patch); err == nil {
			t.Errorf("%s: accepted a patch with no effective edit", name)
		}
	}
}

func TestApplyPatchAdditionAndTrailingNewline(t *testing.T) {
	// Pure addition anchored on context, and trailing-newline preservation:
	// content that ends with \n must still end with \n after patching, and
	// content that does not must not gain one. Silent newline churn is drift.
	patch := `@@ -4,1 +4,2 @@
 The clinic closes at noon on Fridays.
+Parking is behind the building, entrance off the alley.`

	got, err := applyPatch(dentistNote, patch)
	if err != nil {
		t.Fatalf("addition refused: %v", err)
	}
	if !strings.HasSuffix(got, "Parking is behind the building, entrance off the alley.") {
		t.Errorf("added line missing or newline appended: %q", got)
	}

	withNL := dentistNote + "\n"
	got2, err := applyPatch(withNL, patch)
	if err != nil {
		t.Fatalf("addition on newline-terminated content refused: %v", err)
	}
	if !strings.HasSuffix(got2, "entrance off the alley.\n") {
		t.Errorf("trailing newline not preserved: %q", got2[len(got2)-40:])
	}
}

// ── PatchMemory: the composed read → verify → CAS-write loop ─────────

func patchStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s := newTestStore(t)
	if _, err := s.Put(t.Context(), PutParams{NS: "agent:p", Key: "dentist",
		Content: dentistNote, Kind: "semantic", Tier: "ltm", Importance: 0.7,
		Tags: []string{"household"}}); err != nil {
		t.Fatal(err)
	}
	return s
}

const insurancePatch = `@@ -3,1 +3,1 @@
-Insurance card is in the kitchen drawer.
+Insurance card is now in the blue folder by the door.`

func TestPatchMemory(t *testing.T) {
	s := patchStore(t)
	res, err := PatchMemory(t.Context(), s, PatchMemoryParams{
		NS: "agent:p", Key: "dentist", Patch: insurancePatch})
	if err != nil {
		t.Fatal(err)
	}
	if res.OldVersion != 1 || res.NewVersion != 2 || res.Memory == nil {
		t.Fatalf("version bookkeeping wrong: %+v", res)
	}
	head, _ := s.Get(t.Context(), GetParams{NS: "agent:p", Key: "dentist"})
	if !strings.Contains(head[0].Content, "blue folder") {
		t.Errorf("edit not persisted")
	}
	// Metadata carries over: a patch edits content, not classification.
	if head[0].Kind != "semantic" || head[0].Tier != "ltm" || head[0].Importance != 0.7 {
		t.Errorf("metadata mutated by patch: kind=%s tier=%s imp=%.2f",
			head[0].Kind, head[0].Tier, head[0].Importance)
	}
	if !strings.Contains(head[0].Content, "The clinic closes at noon on Fridays.") {
		t.Errorf("untouched line mutated")
	}
}

func TestPatchMemoryStaleBaseVersion(t *testing.T) {
	s := patchStore(t)
	// Someone rewrites the memory wholesale after our read.
	if _, err := s.Put(t.Context(), PutParams{NS: "agent:p", Key: "dentist",
		Content: "Completely restructured dentist note.", Kind: "semantic", Tier: "ltm"}); err != nil {
		t.Fatal(err)
	}
	_, err := PatchMemory(t.Context(), s, PatchMemoryParams{
		NS: "agent:p", Key: "dentist", Patch: insurancePatch, BaseVersion: 1})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale BaseVersion must conflict, got: %v", err)
	}
}

func TestPatchMemoryDryRun(t *testing.T) {
	s := patchStore(t)
	res, err := PatchMemory(t.Context(), s, PatchMemoryParams{
		NS: "agent:p", Key: "dentist", Patch: insurancePatch, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun || res.Memory != nil || !strings.Contains(res.Content, "blue folder") {
		t.Fatalf("dry run shape wrong: %+v", res)
	}
	head, _ := s.Get(t.Context(), GetParams{NS: "agent:p", Key: "dentist"})
	if head[0].Version != 1 || strings.Contains(head[0].Content, "blue folder") {
		t.Errorf("dry run wrote: v%d", head[0].Version)
	}
}

func TestPatchMemoryEmptyGuard(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Put(t.Context(), PutParams{NS: "agent:p", Key: "tiny",
		Content: "Only line.", Kind: "semantic", Tier: "stm"}); err != nil {
		t.Fatal(err)
	}
	wipe := "@@ -1,1 +1,0 @@\n-Only line."
	if _, err := PatchMemory(t.Context(), s, PatchMemoryParams{
		NS: "agent:p", Key: "tiny", Patch: wipe}); err == nil {
		t.Fatal("patch emptied a memory without AllowEmpty")
	}
	if _, err := PatchMemory(t.Context(), s, PatchMemoryParams{
		NS: "agent:p", Key: "tiny", Patch: wipe, AllowEmpty: true}); err != nil {
		t.Fatalf("explicit AllowEmpty must be honoured: %v", err)
	}
}

func TestPatchMemoryRewordedContent(t *testing.T) {
	s := patchStore(t)
	if _, err := s.Put(t.Context(), PutParams{NS: "agent:p", Key: "dentist",
		Content: strings.Replace(dentistNote, "kitchen drawer", "hallway cabinet", 1),
		Kind:    "semantic", Tier: "ltm"}); err != nil {
		t.Fatal(err)
	}
	_, err := PatchMemory(t.Context(), s, PatchMemoryParams{
		NS: "agent:p", Key: "dentist", Patch: insurancePatch})
	if !errors.Is(err, ErrPatchContext) {
		t.Fatalf("patch over reworded content must refuse with ErrPatchContext, got: %v", err)
	}
}
