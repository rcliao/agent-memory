package store

import (
	"context"
	"testing"
)

// ── source_scope in retrieval (provenance-encoding-context-design.md) ──
//
// The coding-agent shape: one user, many projects, one namespace. Where a
// memory was BORN is the load-bearing context. Same evidentiary standard as
// the interlocutor boost: the eval's contested slot must be winnable only
// by the mechanism, with a precondition that fails loudly if the corpus
// stops forcing the choice (the #116 false-green lesson).

func contestedScopeStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	puts := []PutParams{
		// The in-scope fact: born in the project the conversation is in.
		{Key: "ghost-migrations", Content: "Schema migrations live in sqlite.go and run idempotently at open.",
			SourceScope: "project:ghost", SourceKind: "self", Importance: 0.5},
		// The out-of-scope twin: symmetric content, higher importance, so it
		// deterministically wins the contested slot without ForScope.
		{Key: "webapp-migrations", Content: "Schema migrations live in schema.sql and run manually at deploy.",
			SourceScope: "project:webapp", SourceKind: "self", Importance: 0.9},
	}
	for i, f := range []string{
		"Migration retries were flaky on the shared runner last spring.",
		"The migration guide got a rewrite with rollback examples.",
		"Old migrations were squashed once the schema stabilized.",
	} {
		puts = append(puts, PutParams{Key: "note-" + string(rune('0'+i)), Content: f, Importance: 0.4})
	}
	for _, p := range puts {
		p.NS, p.Kind, p.Tier = "agent:coder", "semantic", "ltm"
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestScopeBoostContested(t *testing.T) {
	s := contestedScopeStore(t)
	ctx := context.Background()

	// Precondition: without ForScope the out-of-scope twin wins the slot.
	base, err := s.Context(ctx, ContextParams{
		NS: "agent:coder", Query: "where do schema migrations live", Budget: 45})
	if err != nil {
		t.Fatal(err)
	}
	baseKeys := map[string]bool{}
	for _, m := range base.Memories {
		baseKeys[m.Key] = true
	}
	if !baseKeys["webapp-migrations"] || baseKeys["ghost-migrations"] {
		t.Fatalf("corpus precondition broken: contested slot must go to the out-of-scope twin "+
			"without ForScope (got webapp=%v ghost=%v, keys %v) — retune before trusting this eval",
			baseKeys["webapp-migrations"], baseKeys["ghost-migrations"], keysOf(base))
	}

	// The mechanism: ForScope must flip the slot to the in-scope fact.
	res, err := s.Context(ctx, ContextParams{
		NS: "agent:coder", Query: "where do schema migrations live", Budget: 45, ForScope: "project:ghost"})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, m := range res.Memories {
		keys[m.Key] = true
	}
	if !keys["ghost-migrations"] {
		t.Errorf("ForScope did not admit the in-scope fact at a contested budget; got %v", keysOf(res))
	}

	// Boost, never filter: the out-of-scope fact stays retrievable with room.
	res2, err := s.Context(ctx, ContextParams{
		NS: "agent:coder", Query: "where do schema migrations live", Budget: 2000, ForScope: "project:ghost"})
	if err != nil {
		t.Fatal(err)
	}
	var webapp bool
	for _, m := range res2.Memories {
		if m.Key == "webapp-migrations" {
			webapp = true
		}
	}
	if !webapp {
		t.Errorf("ForScope became a filter: the out-of-scope fact vanished even with room")
	}

	// Unset ForScope keeps prior behaviour byte-for-byte.
	a, _ := s.Context(ctx, ContextParams{NS: "agent:coder", Query: "where do schema migrations live", Budget: 260})
	b, _ := s.Context(ctx, ContextParams{NS: "agent:coder", Query: "where do schema migrations live", Budget: 260})
	if keysJoin(a) != keysJoin(b) {
		t.Errorf("baseline nondeterminism, cannot trust the comparison")
	}
}

// Worktree reality: the same project appears under checkout variants. A
// declared scope alias must extend the boost, exactly like person aliases.
func TestScopeBoostAcrossAliases(t *testing.T) {
	s := contestedScopeStore(t)
	ctx := context.Background()

	// The conversation identifies its scope by a worktree path; the memory
	// was born under the canonical project id. Without the alias, no boost.
	res, err := s.Context(ctx, ContextParams{
		NS: "agent:coder", Query: "where do schema migrations live", Budget: 45,
		ForScope: "/Users/dev/worktrees/ghost-fix"})
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(res); len(got) != 1 || got[0] != "webapp-migrations" {
		t.Fatalf("precondition: unaliased worktree scope must not boost, got %v", got)
	}

	if err := s.SetSourceAlias(ctx, "agent:coder", "/Users/dev/worktrees/ghost-fix", "project:ghost"); err != nil {
		t.Fatal(err)
	}
	res, err = s.Context(ctx, ContextParams{
		NS: "agent:coder", Query: "where do schema migrations live", Budget: 45,
		ForScope: "/Users/dev/worktrees/ghost-fix"})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, m := range res.Memories {
		keys[m.Key] = true
	}
	if !keys["ghost-migrations"] {
		t.Errorf("declared scope alias did not extend the boost; got %v", keysOf(res))
	}
}

// The record itself: scope round-trips through put/get/context and survives
// a patch (a correction edits content, not birthplace).
func TestSourceScopeRoundTripAndPatchCarry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, err := s.Put(ctx, PutParams{NS: "agent:coder", Key: "fact",
		Content: "Deploys are two-step: library then PATH binary.",
		Kind:    "semantic", Tier: "ltm",
		SourceUser: "papi", SourceKind: "stated", SourceScope: "project:ghost"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, GetParams{NS: "agent:coder", Key: "fact"})
	if err != nil || len(got) == 0 {
		t.Fatal(err)
	}
	if got[0].SourceScope != "project:ghost" {
		t.Errorf("scope did not round-trip: %q", got[0].SourceScope)
	}

	res, err := s.Context(ctx, ContextParams{NS: "agent:coder", Query: "deploy steps", Budget: 2000})
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, m := range res.Memories {
		if m.Key == "fact" {
			seen = true
			if m.SourceScope != "project:ghost" {
				t.Errorf("context dropped scope: %q", m.SourceScope)
			}
		}
	}
	if !seen {
		t.Fatal("setup: fact should be retrievable at this budget")
	}

	patch := "--- a/fact\n+++ b/fact\n@@ -1 +1 @@\n-Deploys are two-step: library then PATH binary.\n+Deploys are two-step: library then PATH binary, inode-verified.\n"
	if _, err := PatchMemory(ctx, s, PatchMemoryParams{NS: "agent:coder", Key: "fact", Patch: patch}); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get(ctx, GetParams{NS: "agent:coder", Key: "fact"})
	if err != nil || len(got) == 0 {
		t.Fatal(err)
	}
	if got[0].SourceScope != "project:ghost" || got[0].SourceUser != "papi" {
		t.Errorf("patch dropped provenance: scope=%q user=%q", got[0].SourceScope, got[0].SourceUser)
	}
}
