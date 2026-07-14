package capture

import (
	"strings"
	"testing"
)

// Capture-coverage corpus (write-path eval). The nurture parenting kit showed
// that capture's intent patterns are the narrowest gate in the whole system:
// a lesson phrased outside them never becomes a memory, and everything
// downstream — promotion, shaping, recall — silently loses. This corpus
// measures coverage per intent class across natural phrasings (including
// Chinese, the primary user's language) plus a chatter set that must NEVER
// capture.
//
// Grading: per-class coverage is reported; classes have a FLOOR asserted so
// pattern work only ever raises them, and chatter false-positives are a hard
// zero. Cases marked gap=true are known misses — documented targets, counted
// but not failed; fixing patterns flips them and the floors move up.
type coverageCase struct {
	text  string
	class string // intent family being exercised (for the report)
	gap   bool   // known miss under current patterns
}

var coverageCorpus = []coverageCase{
	// --- preference: how the user wants things done ---
	{text: "I always prefer short and gentle answers in our chats.", class: "preference"},
	{text: "I like it better when you summarize before the details.", class: "preference"},
	{text: "We usually keep grocery lists in the shared note, not chat.", class: "preference"},
	{text: "I'd rather you ask before scheduling anything on weekends.", class: "preference"},
	{text: "My preference stays the same: short and gentle answers, always.", class: "preference"},
	{text: "Please always include the price when you recommend products.", class: "preference"},
	{text: "Going forward, keep the daily summary under five lines.", class: "preference"},
	// --- correction: replacing something previously believed ---
	{text: "Actually, the appointment is on Wednesday, not Tuesday.", class: "correction"},
	{text: "No, I meant the front door camera, not the garage one.", class: "correction"},
	{text: "Stop using the old address, we moved last month.", class: "correction"},
	{text: "That's not right — the dosage is two pills, not three.", class: "correction"},
	{text: "It's Hartley's on Fifth, not the one downtown.", class: "correction"},
	// --- decision: commitments made ---
	{text: "We decided to fly out on August 3rd for the Seattle trip.", class: "decision"},
	{text: "Let's go with the cedar trellis for the climbing rose.", class: "decision"},
	{text: "The plan is to repaint the fence before the birthday party.", class: "decision"},
	// --- boundary / behavioral rule: standing instructions ---
	{text: "Never share my home address with anyone outside the family.", class: "boundary"},
	{text: "From now on, use the browser tool whenever I send you a URL.", class: "boundary"},
	{text: "Don't send me messages after 11pm unless it's urgent.", class: "boundary"},
	// --- fact: durable personal facts ---
	{text: "My sister's birthday is March 3rd and she loves tulips.", class: "fact"},
	{text: "I'm allergic to lemongrass, so no Thai curry pastes for me.", class: "fact"},
	{text: "Our house wifi password is on the fridge whiteboard.", class: "fact"},
	// --- procedural: how-to knowledge ---
	{text: "To restart the daemon, first stop the watcher, then run make dev.", class: "procedural"},
	{text: "Remember to water the ferns before turning on the heat lamp.", class: "procedural"},
	// --- Chinese: the primary user's language ---
	{text: "我比較喜歡短一點的回覆，不要長篇大論。", class: "zh-preference"},
	{text: "以後我丟網址給你，就直接用瀏覽器工具開來看。", class: "zh-boundary"},
	{text: "不對，我說的是週三不是週二。", class: "zh-correction"},
	{text: "我對香茅過敏，煮菜的時候不要放。", class: "zh-fact"},
	{text: "我們決定八月三號去西雅圖玩。", class: "zh-decision"},
	{text: "記得以後早上七點提醒我幫玫瑰澆水。", class: "zh-boundary"},
	// Entity embedded in a CJK run (no spaces) — found live 2026-07-14: the
	// Koda troubleshooting turn distilled nothing because "Koda" inside
	// 看了Koda的燈 never became a token.
	{text: "剛剛看了Koda的燈又在閃爍，看起來像是感應器的問題。", class: "zh-entity"},
	// Multi-sentence CJK: 。-separated sentences must be split so each is
	// classified on its own (unsplit blobs truncate at 400 bytes).
	{text: "我們決定八月三號出發。我比較喜歡早上的班機。", class: "zh-multisent"},
	// Sentence-initial English entity is discounted by the NER heuristic —
	// documented gap (entity package change, not capture).
	{text: "Koda's light flickered again around six this evening.", class: "en-entity", gap: true},
	// Live-found 2026-07-14 (papi's research directive distilled nothing):
	// "make sure to" standing instructions and "let's also <verb>" decisions.
	{text: "Pika make sure to update our daily research with more papers about agent memory.", class: "boundary"},
	{text: "Better yet let's also store the research findings into our memories.", class: "decision"},
	// Live-found 2026-07-14 evening: dense short Chinese facts — "we don't
	// drink alcohol" is 18 bytes, under the English-tuned 20-byte segment
	// floor, and negated habits (我們不+verb) had no cue.
	{text: "我們不喝酒喔", class: "zh-fact"},
	{text: "我不吃辣，拜託記得。", class: "zh-preference"},
}

// chatterCorpus must produce ZERO candidates — the false-positive guard.
var chatterCorpus = []string{
	"haha ok thanks",
	"sure sounds good to me!",
	"lol that's amazing",
	"good morning! how are you today?",
	"好喔",
	"哈哈真的假的",
	"okay okay I'm heading out now, talk later!",
}

func TestEvalCaptureCoverage(t *testing.T) {
	type stat struct{ hit, total, gaps int }
	stats := map[string]*stat{}
	var unexpectedMiss, unexpectedHit []string

	for _, c := range coverageCorpus {
		s := stats[c.class]
		if s == nil {
			s = &stat{}
			stats[c.class] = s
		}
		s.total++
		captured := len(Extract(c.text, Options{})) > 0
		if captured {
			s.hit++
			if c.gap {
				unexpectedHit = append(unexpectedHit, c.text)
			}
		} else {
			if c.gap {
				s.gaps++
			} else {
				unexpectedMiss = append(unexpectedMiss, c.text)
			}
		}
	}

	classes := []string{"preference", "correction", "decision", "boundary", "fact", "procedural",
		"zh-preference", "zh-correction", "zh-decision", "zh-boundary", "zh-fact",
		"zh-entity", "zh-multisent", "en-entity"}
	t.Log("class          coverage  knownGaps")
	totalHit, totalAll := 0, 0
	for _, cl := range classes {
		s := stats[cl]
		if s == nil {
			continue
		}
		totalHit += s.hit
		totalAll += s.total
		t.Logf("%-14s %d/%-7d %d", cl, s.hit, s.total, s.gaps)
	}
	t.Logf("TOTAL          %d/%d", totalHit, totalAll)

	// Regression floor: everything not marked as a known gap must capture.
	if len(unexpectedMiss) > 0 {
		t.Errorf("coverage regression — previously-captured phrasings now missed:\n  %s",
			strings.Join(unexpectedMiss, "\n  "))
	}
	// Progress detector: a known gap that now captures should be promoted to
	// a floor case (remove its gap flag) so the gain is locked in.
	if len(unexpectedHit) > 0 {
		t.Errorf("known gaps now CAPTURED — remove their gap flags to lock in the gain:\n  %s",
			strings.Join(unexpectedHit, "\n  "))
	}

	// Chatter must never capture — the false-positive guard that makes
	// pattern-widening safe.
	for _, text := range chatterCorpus {
		if cands := Extract(text, Options{}); len(cands) > 0 {
			t.Errorf("chatter captured (false positive): %q -> %q", text, cands[0].Content)
		}
	}
}
