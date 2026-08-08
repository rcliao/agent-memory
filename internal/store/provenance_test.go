package store

import (
	"context"
	"testing"
)

// ── Write provenance: memories come from PEOPLE ──────────────────────
//
// From the 2026-08 landscape RPI (steal-list tier 1, owner-approved with one
// refinement that shaped the design): provenance is about the PERSON a memory
// originated from, not the chat — a chat is a channel and stays in tags.
// Two nullable fields:
//
//	source_user — the person whose words or actions this memory records
//	              ("mami", "papi"), empty when unknown or not person-sourced.
//	source_kind — how it entered the store:
//	              stated   — the person explicitly said it (highest authority)
//	              observed — the agent inferred it from behaviour (ambient)
//	              self     — the agent's own note to itself
//	              peer     — arrived from another agent (sync/A2A)
//
// What this buys, in order of nearness: agents can fit responses to the
// interacting user (a mami-stated preference outranks an agent inference
// ABOUT mami); the 76%-dead-weight disease becomes attributable; and a
// poisoned memory stops being forensically invisible — MINJA-style attacks
// work precisely because origin is unrecorded.

func TestProvenanceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "mami-coffee",
		Content: "Mami takes her coffee with oat milk, no sugar.",
		Kind:    "semantic", Tier: "ltm",
		SourceUser: "mami", SourceKind: "stated"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, GetParams{NS: "agent:home", Key: "mami-coffee"})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].SourceUser != "mami" || got[0].SourceKind != "stated" {
		t.Errorf("provenance lost: user=%q kind=%q", got[0].SourceUser, got[0].SourceKind)
	}
}

func TestProvenanceAbsentOnOldRows(t *testing.T) {
	// Pre-provenance rows (and writers that do not set it) must read back as
	// empty — unknown origin is represented honestly, never backfilled.
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "legacy",
		Content: "A note from before provenance existed.", Kind: "semantic", Tier: "stm"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, GetParams{NS: "agent:home", Key: "legacy"})
	if got[0].SourceUser != "" || got[0].SourceKind != "" {
		t.Errorf("unset provenance must stay empty, got user=%q kind=%q",
			got[0].SourceUser, got[0].SourceKind)
	}
}

func TestProvenanceSurvivesPatch(t *testing.T) {
	// A patch edits content, not origin: the memory still records what mami
	// said, lightly corrected — so provenance carries over like the rest of
	// the metadata.
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "mami-coffee",
		Content: "Mami takes her coffee with oat milk, no sugar.",
		Kind:    "semantic", Tier: "ltm", SourceUser: "mami", SourceKind: "stated"}); err != nil {
		t.Fatal(err)
	}
	if _, err := PatchMemory(ctx, s, PatchMemoryParams{NS: "agent:home", Key: "mami-coffee",
		Patch: "@@ -1,1 +1,1 @@\n-Mami takes her coffee with oat milk, no sugar.\n+Mami takes her coffee with oat milk, no sugar, extra hot."}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, GetParams{NS: "agent:home", Key: "mami-coffee"})
	if got[0].Version != 2 || got[0].SourceUser != "mami" || got[0].SourceKind != "stated" {
		t.Errorf("patch dropped provenance: v%d user=%q kind=%q",
			got[0].Version, got[0].SourceUser, got[0].SourceKind)
	}
}

func TestProvenanceListFilter(t *testing.T) {
	// The fitting-to-the-user read path: list what a specific person told me.
	s := newTestStore(t)
	ctx := context.Background()
	puts := []PutParams{
		{NS: "agent:home", Key: "m1", Content: "Mami prefers the window seat on trains.", SourceUser: "mami", SourceKind: "stated"},
		{NS: "agent:home", Key: "m2", Content: "Mami seemed tired after the market trip.", SourceUser: "mami", SourceKind: "observed"},
		{NS: "agent:home", Key: "p1", Content: "Papi wants deploy summaries kept short.", SourceUser: "papi", SourceKind: "stated"},
		{NS: "agent:home", Key: "s1", Content: "Note to self: check the queue drain weekly.", SourceKind: "self"},
	}
	for _, p := range puts {
		p.Kind, p.Tier = "semantic", "ltm"
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.List(ctx, ListParams{NS: "agent:home", SourceUser: "mami"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		keys := []string{}
		for _, m := range got {
			keys = append(keys, m.Key)
		}
		t.Errorf("SourceUser filter returned %d memories (%v), want 2", len(got), keys)
	}
}
