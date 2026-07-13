package store

import (
	"math"
	"regexp"
	"time"
)

// Temporal window anchoring (default ON for temporal-intent queries).
//
// "what happened last week" should target memories from ~a week ago — but
// generic recency scoring is monotonic newest-first, so the newest memory
// wins even when the query names an earlier window ("This week: ..." beats
// the actual last-week deploy). parseTemporalWindow turns relative time
// expressions into an event-time window; the temporal sort then scores
// candidates by window proximity instead of raw recency. Fully LLM-free:
// regex + date arithmetic. Expressions without a bounded window ("recent",
// "latest") keep the existing relative-recency behavior.

var (
	todayRe     = regexp.MustCompile(`(?i)\btoday\b|\bthis morning\b|\btonight\b`)
	yesterdayRe = regexp.MustCompile(`(?i)\byesterday\b`)
	lastWeekRe  = regexp.MustCompile(`(?i)\blast week\b|\ba week ago\b|\bpast week\b`)
	thisWeekRe  = regexp.MustCompile(`(?i)\bthis week\b`)
	lastMonthRe = regexp.MustCompile(`(?i)\blast month\b|\ba month ago\b|\bpast month\b`)
)

// parseTemporalWindow extracts a bounded event-time window from a relative
// time expression in the query. Windows are deliberately fuzzy — people say
// "last week" meaning roughly 4–12 days ago — and the proximity score decays
// smoothly outside them, so boundary choices are soft.
func parseTemporalWindow(query string, now time.Time) (from, to time.Time, ok bool) {
	switch {
	case yesterdayRe.MatchString(query):
		return now.Add(-48 * time.Hour), now.Add(-12 * time.Hour), true
	case lastWeekRe.MatchString(query):
		return now.Add(-12 * 24 * time.Hour), now.Add(-4 * 24 * time.Hour), true
	case lastMonthRe.MatchString(query):
		return now.Add(-45 * 24 * time.Hour), now.Add(-18 * 24 * time.Hour), true
	case todayRe.MatchString(query):
		return now.Add(-24 * time.Hour), now, true
	case thisWeekRe.MatchString(query):
		return now.Add(-7 * 24 * time.Hour), now, true
	}
	return time.Time{}, time.Time{}, false
}

// windowProximity scores how well a timestamp fits the window: 1.0 inside,
// exponentially decaying with distance outside (half-life = half the window
// width), so near-misses still rank above far-misses.
func windowProximity(t, from, to time.Time) float64 {
	if !t.Before(from) && !t.After(to) {
		return 1.0
	}
	width := to.Sub(from).Hours()
	if width <= 0 {
		width = 24
	}
	var distance float64
	if t.Before(from) {
		distance = from.Sub(t).Hours()
	} else {
		distance = t.Sub(to).Hours()
	}
	// Exponential decay: proximity halves every width/2 hours of distance.
	halfLife := width / 2
	return math.Exp2(-distance / halfLife)
}
