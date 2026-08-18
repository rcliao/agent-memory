package store

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
)

// Subsystem liveness (the canary). Four production incidents share one
// shape: a retrieval stage dies silently — its errors swallowed or its
// output filtered — while every eval stays green because nothing measures
// whether the stage still CONTRIBUTES. (The MinScore floor killed edge
// candidates in production, #103; dormancy hid refuters, #104; a missed
// scan site disabled reflect's prune; #113 broke getMemoryByID and link
// expansion errored on every call for five days.)
//
// Two instruments, deliberately cheap:
//
//   - ContextResult.Stages: per-call counters of what each stage
//     contributed to the PACKED output, not merely what it attempted.
//     "edge candidates existed" was true during #103; "an edge candidate
//     got packed" was not — liveness is measured at the output.
//   - swallowed-error counters: the `continue`-on-error sites increment a
//     process-wide atomic; TestSubsystemLiveness asserts zero after
//     exercising the paths, and GHOST_DIAG=1 prints both instruments per
//     call in production.

// diagLinkExpansionErrors counts errors swallowed inside search-side link
// expansion (the #113 getMemoryByID incident's exact hiding place).
var diagLinkExpansionErrors atomic.Int64

// diagLinkExpansionAdded counts neighbours link expansion actually added.
var diagLinkExpansionAdded atomic.Int64

// DiagLinkExpansion returns (added, swallowedErrors) since process start.
// Test/diagnostic surface only.
func DiagLinkExpansion() (added, errors int64) {
	return diagLinkExpansionAdded.Load(), diagLinkExpansionErrors.Load()
}

// diagEnabled reports whether per-call stage logging is on (GHOST_DIAG=1).
func diagEnabled() bool { return os.Getenv("GHOST_DIAG") == "1" }

// logStages emits the per-call liveness line when GHOST_DIAG=1.
func logStages(stages map[string]int) {
	if !diagEnabled() || len(stages) == 0 {
		return
	}
	keys := make([]string, 0, len(stages))
	for k := range stages {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, stages[k]))
	}
	added, errs := DiagLinkExpansion()
	fmt.Fprintf(os.Stderr, "ghost: context stages %s link_expansion_added=%d link_expansion_errors=%d\n",
		strings.Join(parts, " "), added, errs)
}
