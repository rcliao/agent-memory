package store

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rcliao/ghost/internal/model"
)

// ── Which member of a near-duplicate cluster should represent it? ─────
//
// Collapsing a duplicate cluster to one member is only as good as the member
// chosen. This eval exists because the choice is not obvious and every
// available signal is biased in a way ghost has already measured elsewhere:
//
//   - access_count counts every INJECTION, not every usefulness. A memory that
//     gets picked as representative is injected more, which raises its
//     access_count, which makes it the representative again. Choosing by
//     exposure closes a feedback loop on the wrong signal.
//   - content length rewards verbosity. Session summaries run ~221 tokens
//     against ~24 for a crisp fact, so "longest" systematically prefers the
//     chattiest restatement over the clearest one.
//   - importance is author-set and, on a real store, mostly left at its
//     default — it cannot discriminate inside a cluster where every member was
//     written by the same routine.
//   - recency is the strongest single signal, because duplicates accumulate as
//     RESTATEMENTS over time and the newest restatement reflects what is
//     currently true. But taken alone it hands the cluster to whatever junk
//     fragment happened to be written last.
//
// The rules are measured against scenarios that each punish a different naive
// choice, and a rule only wins by passing all of them.
//
// THE RESULT THAT RESHAPED THE DESIGN: the first two versions of the rule tried
// to pick a winner in every case, and both failed the `verbosity` scenario.
// Measuring pairwise similarity showed why — those two memories are 0.100
// alike. They are not a duplicate cluster, and no choice of representative is
// correct, because collapsing them would discard a genuinely different memory.
// So the rule's first job is to decide whether the group is a cluster at all,
// and only then to choose within it. Declining is a valid, and sometimes the
// only correct, answer.

// clusterMember is one restatement in a duplicate cluster.
type clusterMember struct {
	key         string
	content     string
	ageDays     int // how long ago it was written
	importance  float64
	accessCount int
	pinned      bool
}

type dedupScenario struct {
	name string
	why  string // what this scenario punishes
	// wantCollapse is false when the members are not really duplicates, so the
	// correct behaviour is to keep all of them.
	wantCollapse bool
	members      []clusterMember
	wantKey      string
}

func dedupScenarios() []dedupScenario {
	// Synthetic office/ops content — this ships in a public repo and must never
	// carry real personal or family data.
	return []dedupScenario{
		{
			name:         "drifting-restatement",
			why:          "the fact changed; an older restatement is now simply wrong",
			wantCollapse: true,
			members: []clusterMember{
				{key: "standup-1", content: "The team standup is at 09:30 in the small room.", ageDays: 90, importance: 0.5, accessCount: 40},
				{key: "standup-2", content: "Standup runs at 09:30, small room, fifteen minutes.", ageDays: 60, importance: 0.5, accessCount: 22},
				{key: "standup-3", content: "We hold the standup at 09:30 as usual in the small room.", ageDays: 30, importance: 0.5, accessCount: 9},
				{key: "standup-4", content: "Standup moved to 10:15 and is now in the big room upstairs.", ageDays: 2, importance: 0.5, accessCount: 1},
			},
			wantKey: "standup-4",
		},
		{
			name:         "junk-tail",
			why:          "the newest member is a contentless fragment, so pure recency loses",
			wantCollapse: true,
			members: []clusterMember{
				{key: "vpn-1", content: "The VPN profile has to be re-imported after any laptop OS upgrade.", ageDays: 40, importance: 0.5, accessCount: 12},
				{key: "vpn-2", content: "Remember to re-import the VPN profile whenever the laptop OS is upgraded.", ageDays: 20, importance: 0.5, accessCount: 6},
				{key: "vpn-3", content: "VPN: noted.", ageDays: 1, importance: 0.5, accessCount: 0},
			},
			wantKey: "vpn-2",
		},
		{
			name:         "exposure-bias",
			why:          "the most-injected member is the oldest one; access_count would keep it alive forever",
			wantCollapse: true,
			members: []clusterMember{
				{key: "oncall-1", content: "Escalation goes to the platform rota first, then the duty manager.", ageDays: 120, importance: 0.5, accessCount: 300},
				{key: "oncall-2", content: "Escalation now goes straight to the duty manager; the platform rota was retired.", ageDays: 5, importance: 0.5, accessCount: 2},
			},
			wantKey: "oncall-2",
		},
		{
			// Measured jaccard(release-1, release-2) = 0.100. A session summary
			// that merely MENTIONS the release process is not a restatement of
			// the rule about release notes. Collapsing here loses information,
			// so the only correct answer is to decline.
			name:         "not-a-cluster",
			why:          "members share a topic but not a claim; collapsing would discard real content",
			wantCollapse: false,
			members: []clusterMember{
				{key: "release-1", content: "Release notes must be published before the announcement goes out.", ageDays: 10, importance: 0.5, accessCount: 5},
				{key: "release-2", content: "Session summary: discussed the release process at some length, touched on notes, " +
					"the announcement, who signs off, whether the template still applies, and various other points raised " +
					"during the meeting which were not fully resolved and will be picked up again next time.", ageDays: 3, importance: 0.5, accessCount: 1},
			},
		},
		{
			name:         "protected-wins",
			why:          "a pinned member is a deliberate choice and must outrank a fresher casual restatement",
			wantCollapse: true,
			members: []clusterMember{
				{key: "badge-1", content: "Building access requires the photo badge, not the temporary paper pass.", ageDays: 50, importance: 0.9, accessCount: 30, pinned: true},
				{key: "badge-2", content: "I think you can get in with the paper pass too.", ageDays: 1, importance: 0.5, accessCount: 0},
			},
			wantKey: "badge-1",
		},
	}
}

// buildCluster materialises a scenario and returns its members as stored.
func buildCluster(t *testing.T, sc dedupScenario) []model.Memory {
	t.Helper()
	ctx := context.Background()
	s := newTestStore(t)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	for _, m := range sc.members {
		s.SetClock(func() time.Time { return base.AddDate(0, 0, -m.ageDays) })
		if _, err := s.Put(ctx, PutParams{NS: "agent:dedup", Key: m.key, Content: m.content,
			Kind: "semantic", Tier: "ltm", Importance: m.importance, Pinned: m.pinned}); err != nil {
			t.Fatal(err)
		}
		// access_count is not settable through Put — it is a retrieval-side
		// counter — so the scenario writes it directly.
		if _, err := s.db.ExecContext(ctx,
			`UPDATE memories SET access_count = ? WHERE ns = ? AND key = ? AND deleted_at IS NULL`,
			m.accessCount, "agent:dedup", m.key); err != nil {
			t.Fatal(err)
		}
	}
	s.SetClock(func() time.Time { return base })

	var out []model.Memory
	for _, m := range sc.members {
		got, err := s.Get(ctx, GetParams{NS: "agent:dedup", Key: m.key})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("member %q vanished", m.key)
		}
		out = append(out, got[0])
	}
	return out
}

// ── Candidate rules ──────────────────────────────────────────────

// repRule picks a representative. collapse=false means "these are not
// duplicates; keep them all".
type repRule struct {
	name string
	pick func([]model.Memory) (rep model.Memory, collapse bool)
}

func pickBy(ms []model.Memory, better func(a, b model.Memory) bool) model.Memory {
	best := ms[0]
	for _, m := range ms[1:] {
		if better(m, best) {
			best = m
		}
	}
	return best
}

func newest(a, b model.Memory) bool { return a.CreatedAt.After(b.CreatedAt) }

// cohesionFloor is the minimum token-Jaccard a member must reach with at least
// one peer to count as a restatement of it.
//
// An earlier version of this rule used a length floor relative to the cluster
// median, on the theory that the thing to exclude was a too-short fragment.
// That was a proxy for the wrong property and it failed the verbosity case,
// where the disqualifying member is far too LONG. Measured pairwise similarity
// shows one clean separation instead:
//
//	genuine restatements       0.27 - 0.57
//	"VPN: noted."              0.111   <- lowest in its cluster
//	rambling session summary   0.100   <- lowest in its cluster
//
// Both bad candidates fail the same way: they do not actually resemble the
// members they were grouped with. So the gate is cohesion, not size — no
// verbosity bias in either direction, and no absolute character count. 0.15
// sits in the empty band between the two populations.
//
// This is the same property that should have kept them out of the cluster in
// the first place. GetSimilarClusters groups by connected component, which
// chains unrelated memories together — measured: a 1,311-member component at
// 0.0117 density on the live pika store. Choosing a representative cannot
// rescue a badly formed cluster; it can only decline to be misled by one.
const cohesionFloor = 0.15

// peerCohesion returns each member's highest token-Jaccard against any other
// member of the cluster.
func peerCohesion(ms []model.Memory) []float64 {
	toks := make([]map[string]bool, len(ms))
	for i, m := range ms {
		toks[i] = contentTokenSet(m.Content)
	}
	out := make([]float64, len(ms))
	for i := range ms {
		for j := range ms {
			if i == j {
				continue
			}
			if s := jaccard(toks[i], toks[j]); s > out[i] {
				out[i] = s
			}
		}
	}
	return out
}

// pickRepresentative is the proposed rule:
//
//  1. Drop members that do not resemble any peer. A chained-in fragment or a
//     rambling log entry cannot speak for a group it was never part of.
//  2. If fewer than two survive, this was not a duplicate cluster — decline,
//     and keep everything. Collapsing a non-cluster destroys information, which
//     is strictly worse than leaving a duplicate in place.
//  3. A protected member wins. Pinned is a deliberate act, and the protected
//     contract already exempts it from every lifecycle path.
//  4. Otherwise the NEWEST survivor wins, because duplicates accumulate as
//     restatements and the latest restatement reflects current truth. This is
//     why the rule is not "highest importance": on a real store importance sits
//     at its default across every member the same routine wrote.
//
// Deliberately lexicographic rather than a weighted score. A weighted blend
// would need per-signal coefficients nothing in the corpus can calibrate, and
// would let a large access_count quietly outvote a protection bit.
func pickRepresentative(ms []model.Memory) (model.Memory, bool) {
	if len(ms) < 2 {
		return model.Memory{}, false
	}
	coh := peerCohesion(ms)
	var cohesive []model.Memory
	for i, m := range ms {
		if coh[i] >= cohesionFloor {
			cohesive = append(cohesive, m)
		}
	}
	if len(cohesive) < 2 {
		return model.Memory{}, false
	}

	var protected []model.Memory
	for _, m := range cohesive {
		if m.Pinned {
			protected = append(protected, m)
		}
	}
	if len(protected) > 0 {
		return pickBy(protected, newest), true
	}
	return pickBy(cohesive, newest), true
}

// alwaysCollapse adapts a single-signal rule, which has no notion of declining.
// That is precisely the failure the `not-a-cluster` scenario exposes.
func alwaysCollapse(better func(a, b model.Memory) bool) func([]model.Memory) (model.Memory, bool) {
	return func(ms []model.Memory) (model.Memory, bool) { return pickBy(ms, better), true }
}

func repRules() []repRule {
	return []repRule{
		{"newest", alwaysCollapse(newest)},
		{"most-accessed", alwaysCollapse(func(a, b model.Memory) bool { return a.AccessCount > b.AccessCount })},
		{"most-important", alwaysCollapse(func(a, b model.Memory) bool { return a.Importance > b.Importance })},
		{"longest", alwaysCollapse(func(a, b model.Memory) bool { return len(a.Content) > len(b.Content) })},
		{"cohesion-gated: protected>newest", pickRepresentative},
	}
}

func verdict(sc dedupScenario, rep model.Memory, collapse bool) (string, bool) {
	if !sc.wantCollapse {
		if !collapse {
			return "ok(kept all)", true
		}
		return fmt.Sprintf("FAIL(collapsed to %s)", rep.Key), false
	}
	if !collapse {
		return "FAIL(declined)", false
	}
	if rep.Key == sc.wantKey {
		return fmt.Sprintf("ok(%s)", rep.Key), true
	}
	return fmt.Sprintf("FAIL(%s)", rep.Key), false
}

// TestEvalDedupRepresentative scores every candidate rule against every
// scenario and reports the matrix, so the choice of representative is a
// measurement rather than an assertion.
func TestEvalDedupRepresentative(t *testing.T) {
	scenarios := dedupScenarios()
	built := make([][]model.Memory, len(scenarios))
	for i, sc := range scenarios {
		built[i] = buildCluster(t, sc)
	}

	var header strings.Builder
	header.WriteString(fmt.Sprintf("%-34s", "rule"))
	for _, sc := range scenarios {
		header.WriteString(fmt.Sprintf(" %-24s", sc.name))
	}
	header.WriteString(" score")
	t.Log(header.String())

	for _, r := range repRules() {
		var row strings.Builder
		row.WriteString(fmt.Sprintf("%-34s", r.name))
		correct := 0
		for i, sc := range scenarios {
			rep, collapse := r.pick(built[i])
			mark, ok := verdict(sc, rep, collapse)
			if ok {
				correct++
			}
			row.WriteString(fmt.Sprintf(" %-24s", mark))
		}
		row.WriteString(fmt.Sprintf(" %d/%d", correct, len(scenarios)))
		t.Log(row.String())
	}

	for _, sc := range scenarios {
		t.Logf("  %-22s punishes: %s", sc.name, sc.why)
	}
}

// TestDedupRepresentativeRule is the gate: the proposed rule must get every
// scenario right, and each naive rule must fail at least one. The second half
// matters as much as the first — if a single signal passed everything, the
// composite rule would be unjustified complexity.
func TestDedupRepresentativeRule(t *testing.T) {
	scenarios := dedupScenarios()
	built := make([][]model.Memory, len(scenarios))
	for i, sc := range scenarios {
		built[i] = buildCluster(t, sc)
	}

	for i, sc := range scenarios {
		rep, collapse := pickRepresentative(built[i])
		if _, ok := verdict(sc, rep, collapse); !ok {
			t.Errorf("%s: got rep=%q collapse=%v, want collapse=%v key=%q — scenario punishes: %s",
				sc.name, rep.Key, collapse, sc.wantCollapse, sc.wantKey, sc.why)
		}
	}

	for _, r := range repRules() {
		if r.name == "cohesion-gated: protected>newest" {
			continue
		}
		failed := false
		for i, sc := range scenarios {
			rep, collapse := r.pick(built[i])
			if _, ok := verdict(sc, rep, collapse); !ok {
				failed = true
				break
			}
		}
		if !failed {
			t.Errorf("naive rule %q passed every scenario — the corpus no longer "+
				"discriminates, so the composite rule is unjustified", r.name)
		}
	}
}
