// Package capture turns raw usage — conversation transcripts, exchanges, notes —
// into candidate memories using only deterministic heuristics. No LLM required.
//
// It is the LLM-free tier of ghost's write path: an agent (or the ghost CLI)
// can feed a transcript in and get back weighted, kind-inferred, keyed memory
// candidates ready to Put. An LLM may still be used out-of-band to produce
// higher-fidelity candidates, but capture never needs one.
//
// The extractor reuses internal/entity for salience (rule-based NER) and adds
// regex intent classifiers (preference / correction / decision / gotcha /
// procedural / fact) that both gate what is worth remembering and drive the
// kind and mechanical importance of each candidate.
package capture

import (
	"regexp"
	"sort"
	"strings"

	"github.com/rcliao/ghost/internal/entity"
)

// Candidate is a proposed memory extracted from raw text. Its JSON shape is
// stable so an LLM tier can emit the same structure and feed it through the
// identical commit path (see `ghost capture --json`).
type Candidate struct {
	Content    string   `json:"content"`
	Key        string   `json:"key"`
	Kind       string   `json:"kind"` // semantic | episodic | procedural
	Tags       []string `json:"tags,omitempty"`
	Importance float64  `json:"importance"`         // 0..1 mechanical salience
	Priority   string   `json:"priority"`           // low | normal | high | critical
	Cues       []string `json:"cues,omitempty"`     // intent signals that fired (explainability)
	Entities   []string `json:"entities,omitempty"` // named entities detected in the segment
}

// Options tune extraction. The zero value is usable; Extract fills sane defaults.
type Options struct {
	// MinSalience drops candidates whose mechanical importance is below this.
	// Default 0.35.
	MinSalience float64
	// MaxCandidates caps how many candidates are returned (highest importance
	// first). Default 12. Zero means default; negative means unlimited.
	MaxCandidates int
	// SpeakerFilter, when non-empty, keeps only lines spoken by this speaker
	// (case-insensitive match on a "Speaker: ..." prefix, e.g. "user"). Empty
	// captures from every line.
	SpeakerFilter string
	// Tags are added to every returned candidate (e.g. a session or date tag).
	Tags []string
}

const (
	defaultMinSalience   = 0.35
	defaultMaxCandidates = 12
	minSegmentLen        = 20
	minSegmentLenCJK     = 12
	maxSegmentLen        = 400
)

// Extract runs the deterministic pipeline over raw text and returns memory
// candidates ordered by descending mechanical importance. It never calls an LLM
// and never touches a database.
func Extract(text string, opts Options) []Candidate {
	if opts.MinSalience == 0 {
		opts.MinSalience = defaultMinSalience
	}
	if opts.MaxCandidates == 0 {
		opts.MaxCandidates = defaultMaxCandidates
	}
	filter := strings.ToLower(strings.TrimSpace(opts.SpeakerFilter))

	// A2A relay wrapper: a fellow agent's group message forwarded into this
	// agent's turn. The inner speech belongs to the PEER, not the user —
	// distilling it stores wrong-provenance facts (2 live FPs 2026-07-18).
	// Transport metadata, same class as media placeholders.
	if a2aRelayRe.MatchString(text) {
		return nil
	}

	var out []Candidate
	seenKeys := map[string]int{}
	seenContent := map[string]bool{}

	for _, seg := range segment(text, filter) {
		trimmed := strings.TrimSpace(seg)
		// Bridge-generated media placeholders ("(sticker) [emoji: ...]",
		// "(photo)") are transport metadata, not user prose — entity-bearing
		// set names were distilling as memories (found live 2026-07-14).
		if mediaPlaceholderRe.MatchString(trimmed) {
			continue
		}
		// CJK packs far more meaning per byte ("我們不吃牛" is a durable
		// family fact in 15 bytes), so CJK-bearing segments get a lower
		// floor; the cue/entity gate still drops short chatter.
		floor := minSegmentLen
		if containsCJKRune(trimmed) {
			floor = minSegmentLenCJK
		}
		if len(trimmed) < floor {
			continue
		}
		if len(trimmed) > maxSegmentLen {
			trimmed = trimmed[:maxSegmentLen]
		}

		cues := classify(trimmed)
		// Bullet-line fragments ("-One Mighty的吐司+spread") are pieces of a
		// structured message, not standalone facts — without an intent cue
		// the line rides on its parent memo (live FP 2026-07-18).
		if len(cues) == 0 && bulletLineRe.MatchString(trimmed) {
			continue
		}
		// Entity extraction tokenizes on spaces, so Latin names embedded in a
		// CJK run ("看了Milo的燈") are invisible without padding the script
		// boundaries first. Stored content stays original.
		ents := entity.Extract(padScriptBoundaries(trimmed))

		// A segment is worth remembering only if it carries an intent cue or a
		// named entity. Pure chatter ("haha ok thanks") has neither and is
		// dropped. A name in VOCATIVE position ("週末我不想那麼早起欸Nova",
		// "Nova 我回來囉") is the addressee, not subject matter — it cannot be
		// the entity that qualifies the segment (2 live FPs 2026-07-18).
		if len(cues) == 0 && countNonVocative(trimmed, ents) == 0 {
			continue
		}
		// Entity-only pure questions are inquiries ABOUT the world, not facts
		// about the asker ("你們知道Paze嗎？" made Paze a "memory" — 4 live
		// FPs). A question still captures when an intent cue fired or when it
		// states the asker's own plan (first-person marker present).
		if len(cues) == 0 && isPureQuestion(trimmed) && !hasFirstPerson(trimmed) {
			continue
		}

		imp := importance(trimmed, cues, ents)
		if imp < opts.MinSalience {
			continue
		}

		// Collapse near-identical content within a single batch so the same fact
		// stated twice in one transcript does not yield two candidates.
		norm := normalizeContent(trimmed)
		if seenContent[norm] {
			continue
		}
		seenContent[norm] = true

		entTexts := make([]string, 0, len(ents))
		for _, e := range ents {
			entTexts = append(entTexts, e.Text)
		}

		key := makeKey(trimmed, ents)
		if n := seenKeys[key]; n > 0 {
			key = dedupeKey(key, n)
		}
		seenKeys[key]++

		out = append(out, Candidate{
			Content:    trimmed,
			Key:        key,
			Kind:       inferKind(cues),
			Tags:       append([]string(nil), opts.Tags...),
			Importance: imp,
			Priority:   priorityFor(imp),
			Cues:       cues,
			Entities:   entTexts,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Importance > out[j].Importance
	})
	if opts.MaxCandidates > 0 && len(out) > opts.MaxCandidates {
		out = out[:opts.MaxCandidates]
	}
	return out
}

// segment splits raw text into candidate units. It handles speaker-prefixed
// conversation lines ("User: ...", "Assistant: ..."), applies the speaker
// filter, and splits each line into sentences so a multi-fact utterance yields
// multiple candidates.
func segment(text string, speakerFilter string) []string {
	var segs []string
	prevKept := false
	for _, rawLine := range strings.Split(text, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			prevKept = false
			continue
		}

		speaker, body := splitSpeaker(line)
		if speakerFilter != "" && speaker != "" && strings.ToLower(speaker) != speakerFilter {
			prevKept = false
			continue
		}
		if body == "" {
			body = line
		}

		// A bare-URL line is a referent for the sentence above it, not a
		// sentence of its own ("不對喔，我們的是這個" + link — the deictic
		// correction is content-free without the URL). Attach it to the
		// previous segment when that segment came from the same kept speaker.
		if bareURLRe.MatchString(body) {
			if prevKept && len(segs) > 0 {
				segs[len(segs)-1] += " " + body
			}
			continue
		}
		prevKept = true

		for _, sent := range splitSentences(body) {
			sent = strings.TrimSpace(sent)
			if sent != "" {
				segs = append(segs, sent)
			}
		}
	}
	return segs
}

// splitSpeaker separates a short "Speaker: rest" prefix from the body. It only
// treats the prefix as a speaker when it is short and single-token, mirroring
// entity.Extract's heuristic so "http://..." style colons are not mistaken.
func splitSpeaker(line string) (speaker, body string) {
	idx := strings.Index(line, ": ")
	if idx <= 0 || idx >= 30 {
		return "", line
	}
	prefix := line[:idx]
	if len(prefix) >= 20 || strings.ContainsAny(prefix, " \t/") {
		return "", line
	}
	return prefix, strings.TrimSpace(line[idx+2:])
}

// splitSentences breaks a line on sentence terminators while keeping the
// terminator attached. It is intentionally simple (no abbreviation handling) —
// over-splitting is harmless because tiny fragments are dropped by minSegmentLen.
func splitSentences(s string) []string {
	var out []string
	var b strings.Builder
	for _, r := range s {
		b.WriteRune(r)
		if r == '.' || r == '!' || r == '?' || r == '\n' || r == '。' || r == '！' || r == '？' {
			out = append(out, b.String())
			b.Reset()
		}
	}
	if strings.TrimSpace(b.String()) != "" {
		out = append(out, b.String())
	}
	return out
}

func normalizeContent(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// padScriptBoundaries inserts spaces where Latin/digit runs abut CJK runs so
// space-tokenized consumers (entity extraction) see embedded names like
// "Milo" in 看了Milo的燈 as their own tokens.
func padScriptBoundaries(s string) string {
	isCJK := func(r rune) bool {
		return (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0x3040 && r <= 0x30FF) || (r >= 0xF900 && r <= 0xFAFF)
	}
	isLatin := func(r rune) bool {
		return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	var prev rune
	for i, r := range s {
		if i > 0 && ((isCJK(prev) && isLatin(r)) || (isLatin(prev) && isCJK(r))) {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// containsCJKRune reports whether the string carries any Han/kana rune.
func containsCJKRune(s string) bool {
	for _, r := range s {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3400 && r <= 0x4DBF) ||
			(r >= 0x3040 && r <= 0x30FF) {
			return true
		}
	}
	return false
}

// mediaPlaceholderRe matches transport-layer media placeholder segments.
var mediaPlaceholderRe = regexp.MustCompile(`^\((?:sticker|photo|video|voice(?:\s+message)?|file|document|audio)\)`)

// bareURLRe matches a line that is nothing but a single URL.
var bareURLRe = regexp.MustCompile(`^https?://\S+$`)

// a2aRelayRe matches the bridge's fellow-agent relay preamble at the start
// of a turn (optionally after a "User: " speaker prefix).
var a2aRelayRe = regexp.MustCompile(`^(?:\w+:\s*)?\[\S+ \(your fellow agent\) said this in the group`)

// bulletLineRe matches memo-style list-item lines.
var bulletLineRe = regexp.MustCompile(`^[-–•▫️*]\s*\S`)

// countNonVocative returns how many entities are NOT in vocative position:
// at the segment end right after a CJK vocative particle, or leading the
// segment as a standalone address followed by CJK text (or a comma).
func countNonVocative(seg string, ents []entity.Entity) int {
	n := 0
	for _, e := range ents {
		if !isVocative(seg, e.Text) {
			n++
		}
	}
	return n
}

func isVocative(seg, name string) bool {
	if name == "" {
		return false
	}
	// Entity texts arrive lowercased; compare case-insensitively.
	low, lname := strings.ToLower(seg), strings.ToLower(name)
	// Trailing "…欸Nova" / "…啊Nova": name ends the segment and the rune
	// before it is a vocative particle.
	if strings.HasSuffix(low, lname) {
		head := []rune(seg[:len(seg)-len(name)])
		if len(head) > 0 {
			switch head[len(head)-1] {
			case '欸', '啊', '喔', '呀', '嘛', '，', ' ':
				return true
			}
		}
	}
	// Leading "Nova 我回來囉" / "Nova，…": standalone address followed by
	// CJK prose (English "Nova's…" or "Nova make sure…" stays a subject).
	if strings.HasPrefix(low, lname) {
		rest := []rune(seg[len(name):])
		if len(rest) >= 2 && (rest[0] == '，' || rest[0] == ',' || rest[0] == ' ') && containsCJKRune(string(rest[1:2])) {
			return true
		}
	}
	return false
}

// isPureQuestion reports whether the segment reads as an interrogative:
// question-mark terminated, or carrying CJK question particles.
func isPureQuestion(s string) bool {
	t := strings.TrimSpace(s)
	if strings.HasSuffix(t, "?") || strings.HasSuffix(t, "？") {
		return true
	}
	for _, p := range []string{"嗎？", "嗎?", "是不是", "為什麼", "怎麼會"} {
		if strings.Contains(t, p) {
			return true
		}
	}
	return false
}

// hasFirstPerson reports a first-person stake — the asker talking about their
// own plans or situation ("所以我們下個月到機場之後..." is a plan, not an inquiry).
func hasFirstPerson(s string) bool {
	if strings.Contains(s, "我") {
		return true
	}
	lower := " " + strings.ToLower(s) + " "
	for _, w := range []string{" i ", " my ", " we ", " our ", " i'm ", " i'll "} {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}
