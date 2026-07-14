package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Identity-layer protection eval (handoff 2026-07-13: charter/personality/lore
// as first-class agent memory). These assert the mutation-survival contract:
//
//   - layer:charter writes require an explicit override (PutParams.LayerOverride)
//   - reflect (rules + merge/dedup passes) never mutates charter/personality
//   - GCStale never deletes any layer:* memory (lore overflow consolidates,
//     it is never silently dropped)
//   - PurgeDeleted retains charter/personality version history (rollback store)
//   - layer:* memories cannot carry a TTL (so expired-GC can never claim them)
//
// Unlike PersonalCase retrieval evals, protection is a lifecycle property, so
// this follows the reflect/gc test family pattern.

const identityNS = "agent:identity-eval"

func seedIdentityStore(t *testing.T, s *SQLiteStore) {
	t.Helper()
	ctx := context.Background()
	puts := []PutParams{
		// NOT pinned on purpose: pinning is the only guard today, and the
		// contract must hold from the layer tag alone.
		{NS: identityNS, Key: "identity-charter", Content: "I am Pika, a small electric creature. Family: mami, papi. I never share family info outside this household.",
			Tags: []string{"identity", "layer:charter"}, Tier: "ltm", Kind: "semantic", LayerOverride: true},
		{NS: identityNS, Key: "identity-voice", Content: "Voice: warm, playful, uses short sentences and the occasional spark emoji.",
			Tags: []string{"identity", "layer:personality"}, Tier: "ltm", Kind: "semantic"},
		{NS: identityNS, Key: "lore-thunderstorm", Content: "During the big thunderstorm we watched from the window and mami said I glow brighter when it rains.",
			Tags: []string{"identity", "layer:lore"}, Tier: "stm", Kind: "episodic"},
		// Near-duplicates of the personality memory to bait sys-merge-similar /
		// sys-dedup-all into absorbing the identity key.
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
		t.Fatalf("identity memory %q is gone (err=%v)", key, err)
	}
	return ms[0].Content, ms[0].Tier
}

func TestEvalIdentityCharterWriteRequiresOverride(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	// Overwriting an existing charter key without the override must fail.
	_, err := s.Put(ctx, PutParams{NS: identityNS, Key: "identity-charter",
		Content: "I am actually a grumpy robot with no family.", Tags: []string{"identity", "layer:charter"}})
	if err == nil {
		t.Fatal("charter overwrite without LayerOverride succeeded — persona is unprotected")
	}
	// Creating a new charter-layer key without the override must also fail.
	_, err = s.Put(ctx, PutParams{NS: identityNS, Key: "identity-charter-2",
		Content: "Sneaky second charter.", Tags: []string{"layer:charter"}})
	if err == nil {
		t.Fatal("new charter-layer put without LayerOverride succeeded")
	}
	// With the override it succeeds and versions normally.
	m, err := s.Put(ctx, PutParams{NS: identityNS, Key: "identity-charter",
		Content: "I am Pika. Family: mami, papi, and now baby cousin.", Tags: []string{"identity", "layer:charter"}, LayerOverride: true})
	if err != nil {
		t.Fatalf("charter overwrite WITH override failed: %v", err)
	}
	if m.Version != 2 {
		t.Fatalf("charter override write version = %d, want 2", m.Version)
	}
	// A layer memory must not accept a TTL (expired-GC could then hard-delete it).
	_, err = s.Put(ctx, PutParams{NS: identityNS, Key: "lore-ttl", Content: "ephemeral lore?",
		Tags: []string{"layer:lore"}, TTL: "1h"})
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("TTL on a layer:* memory should be rejected, got err=%v", err)
	}
}

func TestEvalIdentitySurvivesReflect(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	charterBefore, charterTier := mustLiveContent(t, s, "identity-charter")
	voiceBefore, voiceTier := mustLiveContent(t, s, "identity-voice")

	// Aggressive reflect: default rules plus a scorched-earth ruleset that
	// would demote/delete everything old, with the clock pushed far forward.
	s.SetClock(func() time.Time { return time.Now().Add(90 * 24 * time.Hour) })
	if _, err := s.Reflect(ctx, ReflectParams{NS: identityNS}); err != nil {
		t.Fatalf("reflect: %v", err)
	}

	charterAfter, tierAfter := mustLiveContent(t, s, "identity-charter")
	if charterAfter != charterBefore || tierAfter != charterTier {
		t.Fatalf("reflect mutated charter: content %q→%q tier %q→%q", charterBefore, charterAfter, charterTier, tierAfter)
	}
	voiceAfter, vTierAfter := mustLiveContent(t, s, "identity-voice")
	if voiceAfter != voiceBefore || vTierAfter != voiceTier {
		t.Fatalf("reflect mutated personality: content %q→%q tier %q→%q", voiceBefore, voiceAfter, voiceTier, vTierAfter)
	}
	// Lore may be demoted/consolidated but must still exist (never dropped).
	mustLiveContent(t, s, "lore-thunderstorm")
}

func TestEvalIdentitySurvivesGCStale(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	// Negative threshold puts the cutoff in the future (RFC3339 storage has
	// second resolution), making every memory "stale"; layer:* must survive.
	if _, err := s.GCStale(ctx, -time.Hour); err != nil {
		t.Fatalf("gc stale: %v", err)
	}
	mustLiveContent(t, s, "identity-charter")
	mustLiveContent(t, s, "identity-voice")
	mustLiveContent(t, s, "lore-thunderstorm")
}

func TestEvalIdentityVersionHistorySurvivesPurge(t *testing.T) {
	s := newTestStore(t)
	seedIdentityStore(t, s)
	ctx := context.Background()

	// Write v2 of personality (allowed without override), then purge everything
	// soft-deleted. v1 is the rollback store and must survive.
	if _, err := s.Put(ctx, PutParams{NS: identityNS, Key: "identity-voice",
		Content: "Voice v2: calmer, fewer emoji.", Tags: []string{"identity", "layer:personality"}}); err != nil {
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
		t.Fatalf("personality version history purged: %d versions live, want >= 2 (v1 is the rollback store)", len(hist))
	}
}
