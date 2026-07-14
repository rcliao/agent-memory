package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Protected-memory eval (locked.go). Ghost has no identity subsystem — callers
// like shell express charter/personality/lore as their own tags and rely on two
// general store guarantees, asserted here with a persona-shaped fixture:
//
//   - overwriting a live `locked` memory requires PutParams.Unlock
//   - automatic lifecycle (reflect rules + merge/dedup, stale-GC) never
//     mutates pinned or locked memories
//   - soft-deleted versions of pinned/locked keys survive PurgeDeleted
//     (version history is the rollback store)
//   - pinned/locked memories cannot carry a TTL (expired-GC hard-deletes)
//
// Protection is a mutation-survival property, so this follows the reflect/gc
// test family rather than the PersonalCase retrieval pattern.

const identityNS = "agent:identity-eval"

func seedIdentityStore(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	puts := []PutParams{
		// Shell's convention: charter = pinned + locked; the layer tag is
		// opaque to ghost.
		{NS: identityNS, Key: "identity-charter", Content: "I am Pika, a small electric creature. Family: mami, papi. I never share family info outside this household.",
			Tags: []string{"identity", "layer:charter", LockedTag}, Tier: "ltm", Kind: "semantic", Pinned: true},
		// Personality = pinned, unlocked (agent may evolve it deliberately).
		{NS: identityNS, Key: "identity-voice", Content: "Voice: warm, playful, uses short sentences and the occasional spark emoji.",
			Tags: []string{"identity", "layer:personality"}, Tier: "ltm", Kind: "semantic", Pinned: true},
		// Lore = locked but NOT pinned: protection must hold from either bit.
		{NS: identityNS, Key: "lore-thunderstorm", Content: "During the big thunderstorm we watched from the window and mami said I glow brighter when it rains.",
			Tags: []string{"identity", "layer:lore", LockedTag}, Tier: "stm", Kind: "episodic"},
		// Near-duplicates of the personality memory to bait sys-merge-similar /
		// sys-dedup-all into absorbing the persona keys.
		{NS: identityNS, Key: "note-voice-copy-1", Content: "Voice: warm, playful, uses short sentences and the occasional spark emoji!",
			Tier: "stm", Kind: "semantic"},
		{NS: identityNS, Key: "note-voice-copy-2", Content: "Voice: warm and playful, uses short sentences and the occasional spark emoji.",
			Tier: "stm", Kind: "semantic"},
	}
	for _, p := range puts {
		if _, err := s.Put(ctx, p); err != nil {
			t.Fatalf("seed %s: %v", p.Key, err)
		}
	}
}

// mustLiveContent fetches the latest live version and fails if missing.
func mustLiveContent(t *testing.T, s *SQLiteStore, key string) (content, tier string) {
	t.Helper()
	ms, err := s.Get(context.Background(), GetParams{NS: identityNS, Key: key})
	if err != nil || len(ms) == 0 {
		t.Fatalf("protected memory %q is gone (err=%v)", key, err)
	}
	return ms[0].Content, ms[0].Tier
}

func TestEvalLockedWriteRequiresUnlock(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	// Overwriting a live locked key without Unlock must fail.
	_, err := s.Put(ctx, PutParams{NS: identityNS, Key: "identity-charter",
		Content: "I am actually a grumpy robot with no family.", Tags: []string{"identity", "layer:charter", LockedTag}, Pinned: true})
	if err == nil {
		t.Fatal("locked overwrite without Unlock succeeded — persona core is unprotected")
	}
	// With Unlock it succeeds and versions normally (deliberate change).
	m, err := s.Put(ctx, PutParams{NS: identityNS, Key: "identity-charter",
		Content: "I am Pika. Family: mami, papi, and now baby cousin.", Tags: []string{"identity", "layer:charter", LockedTag}, Pinned: true, Unlock: true})
	if err != nil {
		t.Fatalf("locked overwrite WITH Unlock failed: %v", err)
	}
	if m.Version != 2 {
		t.Fatalf("unlocked write version = %d, want 2", m.Version)
	}
	// Personality (pinned, not locked) evolves without any flag.
	if _, err := s.Put(ctx, PutParams{NS: identityNS, Key: "identity-voice",
		Content: "Voice v2: calmer, fewer emoji.", Tags: []string{"identity", "layer:personality"}, Pinned: true}); err != nil {
		t.Fatalf("unlocked personality write should not need Unlock: %v", err)
	}
	// Protected memories must not accept a TTL.
	if _, err := s.Put(ctx, PutParams{NS: identityNS, Key: "pinned-ttl", Content: "ephemeral pin?", Pinned: true, TTL: "1h"}); err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("TTL on a pinned memory should be rejected, got err=%v", err)
	}
	if _, err := s.Put(ctx, PutParams{NS: identityNS, Key: "locked-ttl", Content: "ephemeral lock?", Tags: []string{LockedTag}, TTL: "1h"}); err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("TTL on a locked memory should be rejected, got err=%v", err)
	}
}

func TestEvalProtectedSurvivesReflect(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	charterBefore, charterTier := mustLiveContent(t, s, "identity-charter")
	voiceBefore, voiceTier := mustLiveContent(t, s, "identity-voice")
	loreBefore, loreTier := mustLiveContent(t, s, "lore-thunderstorm")

	// Aggressive reflect: default rules with the clock pushed far forward.
	s.SetClock(func() time.Time { return time.Now().Add(90 * 24 * time.Hour) })
	if _, err := s.Reflect(ctx, ReflectParams{NS: identityNS}); err != nil {
		t.Fatalf("reflect: %v", err)
	}

	for _, tc := range []struct{ key, content, tier string }{
		{"identity-charter", charterBefore, charterTier},
		{"identity-voice", voiceBefore, voiceTier},
		{"lore-thunderstorm", loreBefore, loreTier}, // locked-but-unpinned must hold too
	} {
		after, tierAfter := mustLiveContent(t, s, tc.key)
		if after != tc.content || tierAfter != tc.tier {
			t.Fatalf("reflect mutated protected %q: content %q→%q tier %q→%q", tc.key, tc.content, after, tc.tier, tierAfter)
		}
	}
}

func TestEvalProtectedSurvivesGCStale(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	// Negative threshold puts the cutoff in the future (RFC3339 storage has
	// second resolution), making every memory "stale"; pinned/locked survive.
	if _, err := s.GCStale(ctx, -time.Hour); err != nil {
		t.Fatalf("gc stale: %v", err)
	}
	mustLiveContent(t, s, "identity-charter")
	mustLiveContent(t, s, "identity-voice")
	mustLiveContent(t, s, "lore-thunderstorm")
}

func TestEvalProtectedVersionHistorySurvivesPurge(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	// Write v2 of the pinned personality key, then purge everything
	// soft-deleted. v1 is the rollback store and must survive.
	if _, err := s.Put(ctx, PutParams{NS: identityNS, Key: "identity-voice",
		Content: "Voice v2: calmer, fewer emoji.", Tags: []string{"identity", "layer:personality"}, Pinned: true}); err != nil {
		t.Fatalf("personality v2 put: %v", err)
	}
	if _, err := s.PurgeDeleted(ctx, 0); err != nil {
		t.Fatalf("purge: %v", err)
	}
	hist, err := s.History(ctx, HistoryParams{NS: identityNS, Key: "identity-voice"})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) < 2 {
		t.Fatalf("pinned key's version history purged: %d versions live, want >= 2 (v1 is the rollback store)", len(hist))
	}
}
