package store

import (
	"context"
	"strings"
	"testing"
)

// ── Source identity: declared aliases (source-identity-design.md) ─────
// The record stays verbatim; resolution happens at read. These tests pin
// the CRUD contract, the two chain guards, case-insensitivity, and the
// two read points that resolve through the table.

func TestSourceAliasCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ns := "agent:home"

	for alias, canonical := range map[string]string{
		"Jenny (mom / display)": "mami",
		"Jenny":                 "mami",
	} {
		if err := s.SetSourceAlias(ctx, ns, alias, canonical); err != nil {
			t.Fatalf("SetSourceAlias(%q): %v", alias, err)
		}
	}

	aliases, err := s.ListSourceAliases(ctx, ns)
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 2 {
		t.Fatalf("want 2 aliases, got %d: %+v", len(aliases), aliases)
	}
	for _, a := range aliases {
		if a.Canonical != "mami" {
			t.Errorf("alias %q resolves to %q, want mami", a.Alias, a.Canonical)
		}
	}

	// Re-declaring an alias updates its canonical (upsert, reversible).
	if err := s.SetSourceAlias(ctx, ns, "Jenny", "mother"); err != nil {
		t.Fatalf("re-declare: %v", err)
	}
	r := s.loadSourceResolver(ctx, ns)
	if got := r.canonical("Jenny"); got != "mother" {
		t.Errorf("re-declared canonical = %q, want mother", got)
	}

	// Unmerge: the alias resolves to itself again; memories are untouched.
	if err := s.DeleteSourceAlias(ctx, ns, "Jenny"); err != nil {
		t.Fatal(err)
	}
	r = s.loadSourceResolver(ctx, ns)
	if got := r.canonical("Jenny"); got != "Jenny" {
		t.Errorf("after delete, canonical = %q, want Jenny (itself)", got)
	}
	if err := s.DeleteSourceAlias(ctx, ns, "Jenny"); err == nil {
		t.Error("deleting a missing alias should error")
	}

	// Namespace isolation: agent vocabularies do not leak.
	other := s.loadSourceResolver(ctx, "agent:other")
	if got := other.canonical("Jenny (mom / display)"); got != "Jenny (mom / display)" {
		t.Errorf("alias leaked across namespaces: %q", got)
	}
}

func TestSourceAliasChainRejectedBothDirections(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ns := "agent:home"

	if err := s.SetSourceAlias(ctx, ns, "Jenny", "mami"); err != nil {
		t.Fatal(err)
	}

	// Forward chain: canonical must not itself be a declared alias.
	if err := s.SetSourceAlias(ctx, ns, "mom", "Jenny"); err == nil {
		t.Error("aliasing onto a declared alias should be rejected (chain)")
	} else if !strings.Contains(err.Error(), "mami") {
		t.Errorf("chain rejection should suggest the flattened target, got: %v", err)
	}

	// Reverse collision: an in-use canonical must not become an alias —
	// that would split one person across two ids.
	if err := s.SetSourceAlias(ctx, ns, "mami", "mother"); err == nil {
		t.Error("aliasing away an in-use canonical should be rejected (identity split)")
	}

	// Self-alias is meaningless.
	if err := s.SetSourceAlias(ctx, ns, "mami", "MAMI"); err == nil {
		t.Error("alias equal to its canonical (case-folded) should be rejected")
	}
}

func TestSourceAliasCaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ns := "agent:home"

	if err := s.SetSourceAlias(ctx, ns, "Jenny", "mami"); err != nil {
		t.Fatal(err)
	}
	r := s.loadSourceResolver(ctx, ns)
	for _, v := range []string{"jenny", "JENNY", "Jenny"} {
		if got := r.canonical(v); got != "mami" {
			t.Errorf("canonical(%q) = %q, want mami (ASCII case-insensitive)", v, got)
		}
	}
	// CJK does not case-fold and must pass through untouched.
	if got := r.canonical("媽媽"); got != "媽媽" {
		t.Errorf("non-ASCII label mangled: %q", got)
	}
	// Empty never matches anything: unknown origin stays unknown.
	if r.sameSource("", "mami") || r.sameSource("mami", "") {
		t.Error("empty label must never match a source")
	}
}

// The fragmentation shape from the day-1 census: the curated canonical was
// stored under a name variant, and an exact-match boost missed it. With the
// alias declared, the boost must fire for the variant-labelled memory.
func TestForUserBoostAcrossAliases(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ns := "agent:home"
	puts := []PutParams{
		// The interlocutor's fact, stored under a VARIANT spelling.
		{Key: "mami-trains", Content: "Mami prefers the window seat on long train rides.",
			SourceUser: "Jenny", SourceKind: "stated", Importance: 0.5},
		{Key: "papi-trains", Content: "Papi prefers the aisle seat on long train rides.",
			SourceUser: "papi", SourceKind: "stated", Importance: 0.9},
	}
	for _, p := range puts {
		p.NS, p.Kind, p.Tier = ns, "semantic", "ltm"
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatal(err)
		}
	}

	// Without the alias, the variant does not match ForUser=mami and papi
	// keeps the contested slot.
	res, err := s.Context(ctx, ContextParams{NS: ns, Query: "train seat preferences", Budget: 40, ForUser: "mami"})
	if err != nil {
		t.Fatal(err)
	}
	if got := keysOf(res); len(got) != 1 || got[0] != "papi-trains" {
		t.Fatalf("precondition: without the alias papi must win the slot, got %v", got)
	}

	// Declare the alias; the boost fires retroactively — zero row rewrites.
	if err := s.SetSourceAlias(ctx, ns, "Jenny", "mami"); err != nil {
		t.Fatal(err)
	}
	res, err = s.Context(ctx, ContextParams{NS: ns, Query: "train seat preferences", Budget: 40, ForUser: "mami"})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, m := range res.Memories {
		keys[m.Key] = true
	}
	if !keys["mami-trains"] {
		t.Errorf("declared alias did not extend the boost to the variant-labelled fact; got %v", keysOf(res))
	}
}

func TestListFilterAcrossAliases(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	ns := "agent:home"
	for key, user := range map[string]string{
		"fact-variant-full":  "Jenny (mom / display)",
		"fact-variant-short": "Jenny",
		"fact-canonical":     "mami",
		"fact-other":         "papi",
	} {
		if _, err := s.Put(ctx, PutParams{NS: ns, Key: key, Content: "fact from " + user,
			Kind: "semantic", Tier: "ltm", SourceUser: user, SourceKind: "stated"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, alias := range []string{"Jenny (mom / display)", "Jenny"} {
		if err := s.SetSourceAlias(ctx, ns, alias, "mami"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.List(ctx, ListParams{NS: ns, SourceUser: "mami"})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, m := range got {
		keys[m.Key] = true
	}
	if len(got) != 3 || !keys["fact-variant-full"] || !keys["fact-variant-short"] || !keys["fact-canonical"] {
		t.Errorf("List(SourceUser=mami) should match all declared variants, got %v", keys)
	}
	if keys["fact-other"] {
		t.Error("List matched a different person's rows")
	}

	// Filtering BY a variant finds the same set (symmetry through canonical).
	got, err = s.List(ctx, ListParams{NS: ns, SourceUser: "jenny"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("List by variant should resolve through canonical, got %d rows", len(got))
	}
}
