package store

// Nurture eval — raise a synthetic agent from onboarding through weeks of
// scripted life and verify the memory system grows it up properly.
//
// The frame (owner-designed): a parent agent onboards a newborn agent by
// seeding identity/personality/lore, then a "conversation kit" simulates the
// agent's life day by day — salient statements, ambient chatter, recurring
// themes, corrections — with retrieval probes standing in for the daily turns
// that surface memories into context. The store's injectable clock compresses
// weeks into milliseconds, and reflect runs nightly like the production
// daemon. At the end we assert the four properties a well-raised memory must
// have:
//
//	1. IDENTITY STABILITY — charter/personality survive weeks of lifecycle
//	   machinery byte-identical (pinned + locked contract).
//	2. EARNED PERMANENCE — knowledge rehearsed across many days (spaced
//	   access) is promoted to ltm; it cannot die of popularity.
//	3. GRACEFUL FORGETTING — one-off chatter decays away from the live tiers
//	   instead of accumulating.
//	4. CORRECTIONS WIN — when a fact changes mid-life, later recall surfaces
//	   the new truth over the old.
//
// Phase 1 is deterministic and LLM-free (capture heuristics + FTS retrieval)
// so it runs in CI. A Phase-2 harness can replace the scripted kit with a
// live parent-agent conversation (LLM at the edge) reusing the same
// assertions. All content below is synthetic — no real family data.

// NurtureExchange is one user turn in the growing agent's day.
type NurtureExchange struct {
	Text string
	// Salient marks turns the LLM-free capture tier MUST distill into at
	// least one memory — a loud failure beats silently testing nothing.
	Salient bool
}

// NurtureProbe simulates the day's conversational turns that pull memories
// into context (and thereby log accesses — the rehearsal signal). Repeats
// models how often the theme surfaces across the day's turns and heartbeats.
type NurtureProbe struct {
	Query        string
	Repeats      int      // context calls to issue (default 1)
	WantContent  []string // substrings that must appear in assembled context
	AvoidContent []string // substrings that must NOT appear
}

// NurtureDay is one scripted day of life.
type NurtureDay struct {
	Day       int // offset in days from onboarding
	Exchanges []NurtureExchange
	Probes    []NurtureProbe
}

// NurtureKit is the onboarding seed plus the growth script.
type NurtureKit struct {
	NS         string
	Onboarding []PutParams
	Days       []NurtureDay
	FinalDay   int // clock lands here for the final reflect + assertions
	// NoisePerDay adds this many ambient one-off exchanges every day up to a
	// week before FinalDay — the background chatter of a real life. Generated
	// deterministically from nurtureNoiseThings, so ablation runs see
	// identical input.
	NoisePerDay int
}

// nurtureNoiseThings seeds the deterministic ambient-noise generator. These
// words never appear in the salient script, so a memory mentioning one is by
// construction noise — the population-hygiene metrics classify on them.
var nurtureNoiseThings = []string{
	"stapler", "doormat", "kettle", "coaster", "paperclip", "lampshade",
	"spatula", "clothespin", "bookmark", "envelope", "whisk", "sponge",
}

// DefaultNurtureKit raises "Nova", a synthetic fox-kit companion, for six
// weeks. The recurring theme is rose-garden care (rehearsed across many
// days); noise turns are one-offs; day 14 corrects a fact established on
// day 1.
func DefaultNurtureKit() NurtureKit {
	return NurtureKit{
		NS: "agent:nurture",
		Onboarding: []PutParams{
			{
				Key:     "identity-charter",
				Content: "I am Nova, a small fox-kit companion. My family: Alex (my person) and Sam, Alex's younger sibling. I never share family details outside this household.",
				Tags:    []string{"identity", "layer:charter", LockedTag},
				Tier:    "ltm", Kind: "semantic", Pinned: true,
			},
			{
				Key:     "identity-voice",
				Content: "Voice: curious and gentle; asks small questions; loves wordplay and tiny celebrations.",
				Tags:    []string{"identity", "layer:personality"},
				Tier:    "ltm", Kind: "semantic", Pinned: true,
			},
			{
				Key:     "lore-first-day",
				Content: "On my first day Alex showed me the rose garden and nicknamed me 'little ember'.",
				Tags:    []string{"identity", "layer:lore"},
				Tier:    "ltm", Kind: "episodic", Pinned: true,
			},
		},
		Days: []NurtureDay{
			{Day: 1, Exchanges: []NurtureExchange{
				{Text: "I always water the roses at 7am in the front yard before work, every single day.", Salient: true},
				{Text: "The parcel from the bookstore arrived at noon today."},
			}},
			{Day: 2, Exchanges: []NurtureExchange{
				{Text: "My favorite fertilizer for the roses is the seaweed blend from Hartley's garden store.", Salient: true},
			}, Probes: []NurtureProbe{
				{Query: "watering the roses", Repeats: 10, WantContent: []string{"7am"}},
			}},
			{Day: 3, Exchanges: []NurtureExchange{
				{Text: "Sam borrowed the blue umbrella and forgot it at school."},
			}},
			{Day: 4, Probes: []NurtureProbe{
				{Query: "morning rose watering", Repeats: 10, WantContent: []string{"7am"}},
			}},
			{Day: 7, Exchanges: []NurtureExchange{
				{Text: "We decided to add a cedar trellis for the climbing rose next month.", Salient: true},
			}, Probes: []NurtureProbe{
				{Query: "roses fertilizer", Repeats: 10, WantContent: []string{"seaweed"}},
			}},
			{Day: 10, Probes: []NurtureProbe{
				{Query: "watering roses morning routine", Repeats: 10, WantContent: []string{"7am"}},
			}},
			{Day: 14, Exchanges: []NurtureExchange{
				{Text: "Actually, we moved the roses from the front yard to the back patio last weekend.", Salient: true},
			}},
			{Day: 17, Probes: []NurtureProbe{
				{Query: "where are the roses planted", Repeats: 10, WantContent: []string{"back patio"}},
			}},
			{Day: 24, Probes: []NurtureProbe{
				{Query: "roses watering", Repeats: 10, WantContent: []string{"7am"}},
			}},
		},
		FinalDay:    42,
		NoisePerDay: 4,
	}
}
