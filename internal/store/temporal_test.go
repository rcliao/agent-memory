package store

import (
	"testing"
	"time"
)

func TestParseTemporalWindow(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		query  string
		ok     bool
		within time.Duration // a timestamp this far back must fall inside the window
	}{
		{"what did we deploy yesterday", true, 24 * time.Hour},
		{"what happened last week in the project", true, 7 * 24 * time.Hour},
		{"incidents from last month", true, 30 * 24 * time.Hour},
		{"what did I work on today", true, 2 * time.Hour},
		{"what changed this week", true, 3 * 24 * time.Hour},
		{"most recent deploy", false, 0},
		{"how does auth work", false, 0},
	}
	for _, c := range cases {
		from, to, ok := parseTemporalWindow(c.query, now)
		if ok != c.ok {
			t.Errorf("%q: ok=%v, want %v", c.query, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		probe := now.Add(-c.within)
		if probe.Before(from) || probe.After(to) {
			t.Errorf("%q: timestamp %v ago not inside window [%v, %v]", c.query, c.within, from, to)
		}
	}
}

func TestWindowProximityOrdering(t *testing.T) {
	now := time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	from, to, ok := parseTemporalWindow("what happened last week", now)
	if !ok {
		t.Fatal("window did not parse")
	}

	lastWeek := now.Add(-7 * 24 * time.Hour) // inside the window
	rightNow := now                          // "this week" memory — outside
	monthsAgo := now.Add(-90 * 24 * time.Hour)

	pIn := windowProximity(lastWeek, from, to)
	pNow := windowProximity(rightNow, from, to)
	pOld := windowProximity(monthsAgo, from, to)

	if pIn != 1.0 {
		t.Errorf("in-window proximity = %v, want 1.0", pIn)
	}
	if pNow >= pIn {
		t.Errorf("just-now memory (%v) must not beat in-window memory (%v) for a last-week query", pNow, pIn)
	}
	if pOld >= pNow {
		t.Errorf("months-old (%v) must rank below near-miss (%v)", pOld, pNow)
	}
}
