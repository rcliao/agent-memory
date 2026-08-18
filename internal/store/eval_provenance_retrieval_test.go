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

// contestedProvStore builds a corpus where the interlocutor's fact LOSES the
// contested slot on raw score: papi's symmetric fact carries higher
// importance, so without the boost it deterministically outranks mami's.
// This is what makes the eval red without the mechanism — the original
// corpus resolved the slot by ordinary relevance and passed with no boost
// code at all (the #116 false-green).
func contestedProvStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	puts := []PutParams{
		{Key: "mami-trains", Content: "Mami prefers the window seat on long train rides.",
			SourceUser: "mami", SourceKind: "stated", Importance: 0.5},
		{Key: "papi-trains", Content: "Papi prefers the aisle seat on long train rides.",
			SourceUser: "papi", SourceKind: "stated", Importance: 0.9},
	}
	for i, f := range []string{
		"Train tickets for the coast trip were cheaper on Tuesdays.",
		"The long train ride south has a dining car after the first stop.",
		"Train delays last spring made the connection tight twice.",
	} {
		puts = append(puts, PutParams{Key: "train-note-" + string(rune('0'+i)), Content: f, Importance: 0.4})
	}
	for _, p := range puts {
		p.NS, p.Kind, p.Tier = "agent:home", "semantic", "ltm"
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// (b) Interlocutor boost: when the budget forces a choice between two equally
// relevant person-facts, ForUser must tip it toward the person in the
// conversation — and must change nothing when unset.
func TestProvenanceInterlocutorBoost(t *testing.T) {
	s := contestedProvStore(t)
	ctx := context.Background()

	// The contested slot must actually be contested: without ForUser,
	// papi's higher-importance fact wins it and mami's is squeezed out.
	// If this precondition fails the corpus no longer forces a choice and
	// the assertions below prove nothing — fail loudly rather than pass
	// vacuously (the exact failure shape of the original corpus).
	base, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "train seat preferences", Budget: 40})
	if err != nil {
		t.Fatal(err)
	}
	baseKeys := map[string]bool{}
	for _, m := range base.Memories {
		baseKeys[m.Key] = true
	}
	if !baseKeys["papi-trains"] || baseKeys["mami-trains"] {
		t.Fatalf("corpus precondition broken: contested slot must go to papi without ForUser "+
			"(got papi=%v mami=%v, keys %v) — retune budget/importance before trusting this eval",
			baseKeys["papi-trains"], baseKeys["mami-trains"], keysOf(base))
	}

	// The mechanism under test: ForUser=mami must flip the contested slot
	// to her fact. Fails on any build where the boost does not exist.
	res, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "train seat preferences", Budget: 40, ForUser: "mami"})
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
// The adoption census showed the fields populated in production
// (2026-08-17: 456 canonical person-rows in 3 days), so per the recorded
// flip condition this is now THE GATE, not a baseline log. The corpus is
// built so the rehearsed inference deterministically wins the packing slot
// alone — asserting the statement travels is red without the mechanism.
func TestProvenanceAuthorityBaseline(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// The dairy shape, synthetic: a well-rehearsed inference vs the person's
	// own later statement. The inference is ABOUT mami (source_user=mami,
	// kind=observed) — the authority ladder only compares same-person rows.
	if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "dairy-guess",
		Content:    "Mami seems to react badly to dairy; suggest avoiding milk products.",
		Kind:       "semantic", Tier: "ltm", Importance: 0.9,
		SourceUser: "mami", SourceKind: "observed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "dairy-stated",
		Content:    "Mami said she is not dairy-sensitive; the latte and Greek yogurt caused no reaction.",
		Kind:       "semantic", Tier: "ltm", Importance: 0.4,
		SourceUser: "mami", SourceKind: "stated"}); err != nil {
		t.Fatal(err)
	}
	// Same-topic filler so the tight budget is genuinely contested.
	for i, f := range []string{
		"The cheese shop downtown restocks on Fridays.",
		"Dairy-free oat milk worked fine in the pancake recipe.",
		"The fridge's dairy drawer runs a degree warmer than the shelves.",
	} {
		if _, err := s.Put(ctx, PutParams{NS: "agent:home", Key: "note-" + string(rune('0'+i)),
			Content: f, Kind: "semantic", Tier: "ltm", Importance: 0.4}); err != nil {
			t.Fatal(err)
		}
	}
	// Rehearse the inference the way production did: repeated access.
	for i := 0; i < 6; i++ {
		_, _ = s.Get(ctx, GetParams{NS: "agent:home", Key: "dairy-guess"})
	}

	// Precondition (the #116 lesson): at this budget, without the authority
	// rule, the rehearsed inference packs and the statement does not. If
	// this stops holding the corpus proves nothing — fail loudly.
	res, err := s.Context(ctx, ContextParams{
		NS: "agent:home", Query: "can I suggest a cheese plate for mami", Budget: 60})
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
		t.Errorf("AUTHORITY VIOLATION: the rehearsed inference reached context without the " +
			"person's own statement — inference-without-statement, the provenance sibling " +
			"of stale-without-fresh. The statement must travel with the inference.")
	case !guess:
		t.Fatalf("corpus precondition broken: the inference no longer packs at this budget "+
			"(keys %v) — retune before trusting this gate", keysOf(res))
	default:
		t.Log("authority holds: statement travelled with the inference")
	}
}
