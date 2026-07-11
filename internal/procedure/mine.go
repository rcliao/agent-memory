// Package procedure mines recurring workflows from sequences of usage steps —
// e.g. the tool-call or command sequences an agent performs across sessions —
// using only frequency counting. No LLM required.
//
// The goal is procedural memory: "how the user works." When the same short
// sequence of steps (build → test → commit) recurs across many sessions, it is
// a routine worth remembering as a first-class procedural memory whose
// importance grows with how often it is practiced.
package procedure

import (
	"sort"
	"strings"
)

// Pattern is a recurring contiguous run of steps and how many sessions it
// appeared in (support).
type Pattern struct {
	Steps   []string `json:"steps"`
	Support int      `json:"support"`
}

// Options tune mining. The zero value is usable; Mine fills defaults.
type Options struct {
	MinSupport int // min sessions a pattern must appear in (default 2)
	MinLen     int // min steps per pattern (default 2)
	MaxLen     int // max steps per pattern (default 5)
	TopN       int // cap on returned patterns (default 20; <=0 keeps default)
}

const sep = "\x00"

// Mine returns recurring step patterns ordered by support (then length), keeping
// only maximal patterns: a shorter run is dropped when a strictly longer run
// with the same support contains it, so "build test commit" wins over its
// redundant sub-runs. Support counts DISTINCT sessions (a routine repeated twice
// in one session still counts once).
func Mine(sessions [][]string, opts Options) []Pattern {
	if opts.MinSupport <= 0 {
		opts.MinSupport = 2
	}
	if opts.MinLen <= 0 {
		opts.MinLen = 2
	}
	if opts.MaxLen <= 0 {
		opts.MaxLen = 5
	}
	if opts.TopN <= 0 {
		opts.TopN = 20
	}
	if opts.MinLen > opts.MaxLen {
		opts.MinLen = opts.MaxLen
	}

	support := map[string]int{}
	steps := map[string][]string{}

	for _, sess := range sessions {
		seen := map[string]bool{} // dedupe within a session so support = #sessions
		for l := opts.MinLen; l <= opts.MaxLen; l++ {
			for i := 0; i+l <= len(sess); i++ {
				gram := sess[i : i+l]
				key := strings.Join(gram, sep)
				if seen[key] {
					continue
				}
				seen[key] = true
				support[key]++
				if _, ok := steps[key]; !ok {
					steps[key] = append([]string(nil), gram...)
				}
			}
		}
	}

	var pats []Pattern
	for key, sup := range support {
		if sup >= opts.MinSupport {
			pats = append(pats, Pattern{Steps: steps[key], Support: sup})
		}
	}

	pats = keepMaximal(pats)

	sort.Slice(pats, func(i, j int) bool {
		if pats[i].Support != pats[j].Support {
			return pats[i].Support > pats[j].Support
		}
		if len(pats[i].Steps) != len(pats[j].Steps) {
			return len(pats[i].Steps) > len(pats[j].Steps)
		}
		return strings.Join(pats[i].Steps, sep) < strings.Join(pats[j].Steps, sep)
	})

	if len(pats) > opts.TopN {
		pats = pats[:opts.TopN]
	}
	return pats
}

// keepMaximal drops any pattern that is a contiguous infix of a strictly longer
// pattern with equal support (it carries no extra information).
func keepMaximal(pats []Pattern) []Pattern {
	out := pats[:0:0]
	for i, p := range pats {
		dominated := false
		for j, q := range pats {
			if i == j || len(q.Steps) <= len(p.Steps) || q.Support != p.Support {
				continue
			}
			if containsRun(q.Steps, p.Steps) {
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, p)
		}
	}
	return out
}

// containsRun reports whether needle appears as a contiguous run within hay.
func containsRun(hay, needle []string) bool {
	if len(needle) == 0 || len(needle) > len(hay) {
		return false
	}
	for i := 0; i+len(needle) <= len(hay); i++ {
		match := true
		for k := range needle {
			if hay[i+k] != needle[k] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
