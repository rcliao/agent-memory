package store

// Personal-agent evaluation corpus and scenarios.
//
// The public benchmarks (LongMemEval, LoCoMo, HaluMem) measure conversational
// QA recall. They do NOT measure what a *personal* agent needs: recalling the
// user's stated preferences, their procedures ("how I deploy"), decisions
// ("what did we choose"), same-day facts, and — critically — NOT resurfacing a
// fact that a newer one has superseded or contradicted.
//
// This corpus models a small personal store and PersonalCases enumerates
// queries with known-relevant keys, scored LLM-free by key match (see
// eval_personal_test.go). Runs FTS-only like the other in-repo TestEval*
// scenarios, so content shares surface terms with its query.

// PersonalNS is the namespace used by the personal-agent eval corpus.
const PersonalNS = "agent:personal"

// PersonalSeedCorpus returns memories shaped like a real personal-agent store:
// durable preferences/procedures/decisions (ltm), fresh same-day facts (stm,
// low importance, backdated a few hours), stale distractors, and
// supersede/contradiction pairs.
func PersonalSeedCorpus() []SeedMemory {
	return []SeedMemory{
		// ── Preferences (durable, semantic, ltm) ──────────────────────────
		{NS: PersonalNS, Key: "pref-go-format", Kind: "semantic", Tier: "ltm", Priority: "high", Importance: 0.8,
			Content: "I prefer to format Go code with gofmt, and I use tabs not spaces."},
		{NS: PersonalNS, Key: "pref-editor", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.7,
			Content: "My editor is Neovim; I use Telescope for fuzzy file finding."},
		{NS: PersonalNS, Key: "pref-commit", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.7,
			Content: "I prefer small focused commits and always open a pull request rather than pushing to main."},

		// ── Procedures (how the user works, procedural, ltm) ──────────────
		{NS: PersonalNS, Key: "proc-deploy", Kind: "procedural", Tier: "ltm", Priority: "high", Importance: 0.8,
			Content: "To deploy the service, run make build and then push a git tag; ArgoCD deploys it."},
		{NS: PersonalNS, Key: "proc-tests", Kind: "procedural", Tier: "ltm", Priority: "normal", Importance: 0.7,
			Content: "To run the tests, run go test ./... from the repo root before committing."},

		// ── Decisions with rationale (semantic, ltm) ──────────────────────
		{NS: PersonalNS, Key: "decision-db", Kind: "semantic", Tier: "ltm", Priority: "high", Importance: 0.8,
			Content: "We decided to use Postgres as the database for the metadata store because we need transactions."},
		{NS: PersonalNS, Key: "decision-queue", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.7,
			Content: "We chose NATS instead of Kafka for the event queue to keep the deployment simple."},

		// ── Same-day facts (fresh, stm, low importance, hours old) ────────
		{NS: PersonalNS, Key: "today-parking", Kind: "episodic", Tier: "stm", Priority: "normal", Importance: 0.4,
			BackdateHours: 2, Content: "Today I parked the car in lot B on level 3."},
		{NS: PersonalNS, Key: "today-errand", Kind: "episodic", Tier: "stm", Priority: "normal", Importance: 0.4,
			BackdateHours: 3, Content: "I need to pick up the dry cleaning after 5pm today."},

		// ── Stale distractor sharing terms with a same-day fact ───────────
		{NS: PersonalNS, Key: "parking-usual", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.6,
			BackdateHours: 1440, Content: "I usually keep the car in the office garage on lot A."},

		// ── Supersede / knowledge-update pair (bundler) ───────────────────
		{NS: PersonalNS, Key: "old-bundler", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.6,
			BackdateHours: 720, Content: "We use Webpack to bundle the frontend assets."},
		{NS: PersonalNS, Key: "new-bundler", Kind: "semantic", Tier: "stm", Priority: "normal", Importance: 0.5,
			BackdateHours: 2, Content: "We now use Vite instead of Webpack to bundle the frontend."},

		// ── Contradiction pair (auth). A contradicts edge is created in the
		//    test so force-include can be exercised. ───────────────────────
		{NS: PersonalNS, Key: "auth-old", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.6,
			BackdateHours: 1000, Content: "The API uses API keys for authentication."},
		{NS: PersonalNS, Key: "auth-new", Kind: "semantic", Tier: "stm", Priority: "high", Importance: 0.6,
			BackdateHours: 4, Content: "The API now uses OAuth2 bearer tokens for authentication instead of API keys."},

		// ── Utility pair: same relevance, different proven usefulness ─────
		{NS: PersonalNS, Key: "util-high", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.5,
			AccessCount: 25, UtilityCount: 20, Content: "Deploys always go through the staging pipeline first."},
		{NS: PersonalNS, Key: "util-low", Kind: "semantic", Tier: "ltm", Priority: "normal", Importance: 0.5,
			AccessCount: 25, UtilityCount: 0, Content: "Deploys occasionally touch the staging pipeline setup."},

		// ── Noise so retrieval is not trivial ─────────────────────────────
		{NS: PersonalNS, Key: "noise-standup", Kind: "episodic", Tier: "ltm", Priority: "low", Importance: 0.3,
			Content: "The team standup meeting is at 10am on weekdays."},
		{NS: PersonalNS, Key: "noise-hike", Kind: "episodic", Tier: "ltm", Priority: "low", Importance: 0.3,
			Content: "We hiked Mount Tam last weekend; the weather was clear."},
	}
}

// PersonalCase is one personal-agent retrieval scenario.
type PersonalCase struct {
	Name     string
	Category string
	Query    string
	// Relevant keys that should be retrieved (MRR is computed over these).
	Relevant []string
	// UseContext runs Context() (tier/recency/edge-aware) instead of Search().
	UseContext bool
	// PreferOver, when set to {fresh, stale}, records whether `fresh` ranks at
	// or above `stale` — the ranking signal a personal agent needs (recent/valid
	// over old). Recorded as a metric; not yet a hard gate (pending scoring work).
	PreferOver [2]string
	// MustInclude keys that must ALL be present (e.g. a contradicting fact that
	// must be force-surfaced).
	MustInclude []string
}

// PersonalCases returns the personal-agent eval scenarios.
func PersonalCases() []PersonalCase {
	return []PersonalCase{
		{Name: "format-go", Category: "preference-recall", Query: "how do I format my Go code", Relevant: []string{"pref-go-format"}},
		{Name: "editor", Category: "preference-recall", Query: "what editor do I use", Relevant: []string{"pref-editor"}},
		{Name: "deploy", Category: "procedural-recall", Query: "how do I deploy the service", Relevant: []string{"proc-deploy"}},
		{Name: "run-tests", Category: "procedural-recall", Query: "how do I run the tests", Relevant: []string{"proc-tests"}},
		{Name: "database", Category: "decision-recall", Query: "what database did we choose for the metadata store", Relevant: []string{"decision-db"}},
		{Name: "event-queue", Category: "decision-recall", Query: "what did we choose for the event queue", Relevant: []string{"decision-queue"}},

		{Name: "same-day-parking", Category: "same-day-recall", Query: "where is my car parked", UseContext: true,
			Relevant: []string{"today-parking"}, PreferOver: [2]string{"today-parking", "parking-usual"}},

		{Name: "bundler-update", Category: "freshness-update", Query: "what do we use to bundle the frontend", UseContext: true,
			Relevant: []string{"new-bundler"}, PreferOver: [2]string{"new-bundler", "old-bundler"}},

		{Name: "auth-contradiction", Category: "contradiction-surfacing", Query: "how does the API do authentication", UseContext: true,
			Relevant: []string{"auth-new"}, MustInclude: []string{"auth-new", "auth-old"}},

		{Name: "utility-deploy", Category: "utility-recall", Query: "how do deploys use the staging pipeline", UseContext: true,
			Relevant: []string{"util-high"}, PreferOver: [2]string{"util-high", "util-low"}},
	}
}
