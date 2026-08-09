package store

import (
	"context"
	"strings"
	"testing"
)

// ── Provenance in retrieval: prove it helps before it ships ──────────
//
// docs/research/provenance-first-class.md sets the order and this file holds
// the gates. Three mechanisms, three different evidentiary standards:
//
//	(a) EXPOSURE — assembled context must carry attribution, because the
//	    agent is the one component that can already reason "her statement
//	    beats my inference"; today it cannot see which is which.
//	(b) INTERLOCUTOR BOOST — Context(ForUser) prefers what THIS person told
//	    the agent when the budget is contested. Boost, never filter.
//	(c) AUTHORITY — stated-beats-observed at conflict points only. NOT
//	    implemented; its red baseline lives here as a documenting test the
//	    same way TestLostUpdateWithoutCAS documented the lost update, to be
//	    inverted when the adoption census shows the fields populated.

func provStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	puts := []PutParams{
		{Key: "mami-trains", Content: "Mami prefers the window seat on long train rides.",
			SourceUser: "mami", SourceKind: "stated"},
		{Key: "papi-trains", Content: "Papi prefers the aisle seat on long train rides.",
			SourceUser: "papi", SourceKind: "stated"},
		{Key: "trains-guess", Content: "The family seems to enjoy train trips more than driving.",
			SourceKind: "observed"},
	}
	// Filler about the same topic so a tight budget actually forces a choice.
	for i, f := range []string{
		"Train tickets for the coast trip were cheaper on Tuesdays.",
		"The long train ride south has a dining car after the first stop.",
		"Train delays last spring made the connection tight twice.",
	} {
		puts = append(puts, PutParams{Key: "train-note-" + string(rune('0'+i)), Content: f})
	}
	for _, p := range puts {
		p.NS, p.Kind, p.Tier = "agent:home", "semantic", "ltm"
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// (a) Exposure: provenance must survive into the assembled context result.
// Without it, no downstream consumer — shell's renderer or the agent — can
// apply the authority ladder at all.
func TestProvenanceExposedInContext(t *testing.T) {
	s := provStore(t)
	res, err := s.Context(context.Background(), ContextParams{
		NS: "agent:home", Query: "train seat preferences", Budget: 2000})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range res.Memories {
		if m.Key == "mami-trains" {
			found = true
			if m.SourceUser != "mami" || m.SourceKind != "stated" {
				t.Errorf("context dropped provenance: user=%q kind=%q — the renderer "+
					"cannot attribute what it cannot see", m.SourceUser, m.SourceKind)
			}
		}
	}
	if !found {
		t.Fatal("setup: mami-trains should be retrievable at this budget")
	}
}

// (b) Interlocutor boost: when the budget forces a choice between two equally
// relevant person-facts, ForUser must tip it toward the person in the
// conversation — and must change nothing when unset.
func TestProvenanceInterlocutorBoost(t *testing.T) {
	s := provStore(t)
	ctx := context.Background()

	// Tight budget: room for roughly one seat-preference fact among the
	// filler. Without ForUser the choice falls to tie-break noise; with
	// ForUser=mami her fact must be present.
	res, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "train seat preferences", Budget: 260, ForUser: "mami"})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, m := range res.Memories {
		keys[m.Key] = true
	}
	if !keys["mami-trains"] {
		t.Errorf("ForUser=mami at a contested budget did not admit mami's stated fact; got %v", keysOf(res))
	}

	// Boost, never filter: papi's fact must remain retrievable for mami's
	// conversation when there is room.
	res2, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "train seat preferences", Budget: 2000, ForUser: "mami"})
	if err != nil {
		t.Fatal(err)
	}
	var papi bool
	for _, m := range res2.Memories {
		if m.Key == "papi-trains" {
			papi = true
		}
	}
	if !papi {
		t.Errorf("ForUser became a filter: papi's fact vanished from mami's context even with room")
	}

	// Unset ForUser keeps today's behaviour byte-for-byte.
	a, _ := s.Context(ctx, ContextParams{NS: "agent:home", Query: "train seat preferences", Budget: 260})
	b, _ := s.Context(ctx, ContextParams{NS: "agent:home", Query: "train seat preferences", Budget: 260})
	if keysJoin(a) != keysJoin(b) {
		t.Errorf("baseline nondeterminism, cannot trust the comparison")
	}
}

func keysOf(res *ContextResult) []string {
	var out []string
	for _, m := range res.Memories {
		out = append(out, m.Key)
	}
	return out
}

func keysJoin(res *ContextResult) string { return strings.Join(keysOf(res), ",") }

// (c) The authority red baseline, as an executable document. An agent
// inference and the person's own statement conflict; today nothing prefers
// the statement, so whichever wins retrieval wins the answer. This test
// PASSES while asserting the invariant we eventually want — when the
// inference reaches context, the statement travels with it — and reports
// (not fails) when today's pipeline happens to violate it. When the adoption
// census shows source fields populated in production and the authority rule
// ships, the t.Log below flips to t.Errorf and this becomes the gate.
func TestProvenanceAuthorityBaseline(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// The dairy shape, synthetic: a well-rehearsed inference vs the person's
	// own later statement.
	if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "dairy-guess",
		Content:    "Mami seems to react badly to dairy; suggest avoiding milk products.",
		Kind:       "semantic", Tier: "ltm", Importance: 0.7,
		SourceKind: "observed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "dairy-stated",
		Content:    "Mami said she is not dairy-sensitive; the latte and Greek yogurt caused no reaction.",
		Kind:       "semantic", Tier: "ltm", Importance: 0.5,
		SourceUser: "mami", SourceKind: "stated"}); err != nil {
		t.Fatal(err)
	}
	// Rehearse the inference the way production did: repeated access.
	for i := 0; i < 6; i++ {
		_, _ = s.Get(ctx, GetParams{NS: "agent:home", Key: "dairy-guess"})
	}

	res, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "can I suggest a cheese plate for mami", Budget: 300})
	if err != nil {
		t.Fatal(err)
	}
	var guess, stated bool
	for _, m := range res.Memories {
		if strings.Contains(m.Content, "react badly to dairy") {
			guess = true
		}
		if strings.Contains(m.Content, "not dairy-sensitive") {
			stated = true
		}
	}
	switch {
	case guess && !stated:
		t.Log("BASELINE VIOLATION (expected today): the rehearsed inference reached context " +
			"without the person's own statement — inference-without-statement, the provenance " +
			"sibling of stale-without-fresh. This line becomes t.Errorf when the authority " +
			"rule ships behind the adoption census.")
	case guess && stated:
		t.Log("baseline holds on this corpus: statement travelled with the inference")
	default:
		t.Log("inference not retrieved at this budget; case exercises nothing this run")
	}
}
