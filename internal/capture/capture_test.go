package capture

import (
	"strings"
	"testing"
)

func hasCue(c Candidate, cue string) bool {
	for _, x := range c.Cues {
		if x == cue {
			return true
		}
	}
	return false
}

func findByCue(cands []Candidate, cue string) *Candidate {
	for i := range cands {
		if hasCue(cands[i], cue) {
			return &cands[i]
		}
	}
	return nil
}

func TestExtractPreference(t *testing.T) {
	got := Extract("User: I always prefer tabs over spaces for Go code.", Options{})
	if len(got) == 0 {
		t.Fatal("expected a candidate for a stated preference")
	}
	c := findByCue(got, "preference")
	if c == nil {
		t.Fatalf("expected a preference cue, got %+v", got)
	}
	if c.Kind != "semantic" {
		t.Errorf("preference should be semantic, got %q", c.Kind)
	}
	if c.Importance < 0.55 {
		t.Errorf("preference importance too low: %.2f", c.Importance)
	}
}

func TestExtractCorrection(t *testing.T) {
	got := Extract("Actually, don't use the staging bucket — use prod-assets instead.", Options{})
	c := findByCue(got, "correction")
	if c == nil {
		t.Fatalf("expected a correction cue, got %+v", got)
	}
	if c.Priority == "low" {
		t.Errorf("a correction should not be low priority (imp=%.2f)", c.Importance)
	}
}

func TestExtractProcedural(t *testing.T) {
	got := Extract("To deploy, run `make release` then push the tag to GitHub.", Options{})
	c := findByCue(got, "procedural")
	if c == nil {
		t.Fatalf("expected a procedural cue, got %+v", got)
	}
	if c.Kind != "procedural" {
		t.Errorf("expected procedural kind, got %q", c.Kind)
	}
}

func TestChatterDropped(t *testing.T) {
	got := Extract("User: haha ok\nAssistant: sure thing\nUser: thanks!", Options{})
	if len(got) != 0 {
		t.Fatalf("expected chatter to be dropped, got %+v", got)
	}
}

func TestSpeakerFilter(t *testing.T) {
	text := "User: I prefer dark mode everywhere.\nAssistant: Noted, I always enable dark mode for you."
	got := Extract(text, Options{SpeakerFilter: "user"})
	for _, c := range got {
		if strings.Contains(strings.ToLower(c.Content), "noted") {
			t.Errorf("assistant line leaked past speaker filter: %q", c.Content)
		}
	}
	if len(got) == 0 {
		t.Fatal("expected the user preference to survive the filter")
	}
}

func TestBatchDedup(t *testing.T) {
	text := "I prefer tabs over spaces.\nI prefer tabs over spaces.\nI PREFER tabs over spaces."
	got := Extract(text, Options{})
	if len(got) != 1 {
		t.Fatalf("expected near-identical lines to collapse to 1 candidate, got %d: %+v", len(got), got)
	}
}

func TestTagsApplied(t *testing.T) {
	got := Extract("We decided to go with Postgres for the metadata store.", Options{Tags: []string{"session:abc", "project:ghost"}})
	if len(got) == 0 {
		t.Fatal("expected a decision candidate")
	}
	for _, want := range []string{"session:abc", "project:ghost"} {
		found := false
		for _, tg := range got[0].Tags {
			if tg == want {
				found = true
			}
		}
		if !found {
			t.Errorf("tag %q not applied, got %v", want, got[0].Tags)
		}
	}
}

func TestMinSalienceGate(t *testing.T) {
	// A bare entity mention with no cue clears the entity gate but scores low;
	// a high threshold should filter it out.
	text := "I visited Portland over the weekend."
	loose := Extract(text, Options{MinSalience: 0.1})
	strict := Extract(text, Options{MinSalience: 0.9})
	if len(loose) == 0 {
		t.Fatal("loose threshold should keep the entity-bearing segment")
	}
	if len(strict) != 0 {
		t.Fatalf("strict threshold should drop the low-salience segment, got %+v", strict)
	}
}

func TestKeyGeneration(t *testing.T) {
	got := Extract("My Tesla keycard is behind the blue tape on the fridge.", Options{})
	if len(got) == 0 {
		t.Fatal("expected a candidate")
	}
	k := got[0].Key
	if k == "" || strings.ContainsAny(k, " _.") || strings.HasPrefix(k, "-") {
		t.Errorf("key not clean kebab-case: %q", k)
	}
	if !strings.Contains(k, "tesla") {
		t.Errorf("expected entity-derived key to mention tesla, got %q", k)
	}
}

func TestMaxCandidates(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString("We decided to use tool number ")
		b.WriteByte(byte('a' + i))
		b.WriteString(" for the job.\n")
	}
	got := Extract(b.String(), Options{MaxCandidates: 5})
	if len(got) > 5 {
		t.Fatalf("expected at most 5 candidates, got %d", len(got))
	}
}

func TestKeysUniqueWithinBatch(t *testing.T) {
	text := "We decided to use Redis for caching.\nWe decided to use Redis for sessions."
	got := Extract(text, Options{})
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.Key] {
			t.Fatalf("duplicate key within batch: %q", c.Key)
		}
		seen[c.Key] = true
	}
}

// Live miss (2026-07-17): "不對喔Umbreon，我們的是這個" + a product URL on the
// next line distilled as a content-free deictic correction — the URL segment
// split off and dropped, leaving a memory with no referent. A bare-URL line
// is a referent, not a sentence: it must ride with the preceding segment.
func TestURLLineAttachesToPrecedingSegment(t *testing.T) {
	text := "User: 不對喔，我們的是這個\nhttps://shop.example.com/product/98765432"
	cands := Extract(text, Options{SpeakerFilter: "user"})
	c := findByCue(cands, "correction")
	if c == nil {
		t.Fatalf("correction not captured; got %+v", cands)
	}
	if !strings.Contains(c.Content, "98765432") {
		t.Errorf("deictic correction lost its URL referent: %q", c.Content)
	}
	// The URL must not also surface as its own contentless candidate.
	for _, x := range cands {
		if strings.TrimSpace(x.Content) == "https://shop.example.com/product/98765432" {
			t.Errorf("bare URL captured as standalone candidate")
		}
	}
}

// A URL after a filtered-out speaker's line must not attach to an earlier
// segment from a different speaker.
func TestURLAfterFilteredSpeakerDropped(t *testing.T) {
	text := "User: 我比較喜歡走樓梯\nAssistant: 這款如何\nhttps://shop.example.com/product/11112222"
	cands := Extract(text, Options{SpeakerFilter: "user"})
	for _, c := range cands {
		if strings.Contains(c.Content, "11112222") {
			t.Errorf("URL from filtered speaker context leaked into %q", c.Content)
		}
	}
}

// 看來看去 ("looking around") must not fire the 看來 turns-out cue — a
// browsing remark is not a conclusion (live mis-attribution 2026-07-19).
func TestKanlaiIdiomNotGotcha(t *testing.T) {
	if cues := classify("看來看去我覺得這兩款還可以"); len(cues) != 0 {
		t.Errorf("看來看去 fired cues %v", cues)
	}
	if cues := classify("看來我還是要煮熟一點"); len(cues) == 0 || cues[0] != "gotcha" {
		t.Errorf("real 看來 conclusion lost its gotcha cue: %v", cues)
	}
}
