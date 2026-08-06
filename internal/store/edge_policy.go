package store

// ── Per-relation expansion policy ────────────────────────────────
//
// Spreading activation used to traverse OUTGOING edges only: the SQL filtered
// `WHERE from_id = ?`, so a seed could only reach memories it pointed at.
//
// That silently disabled the relation it mattered most for. A correction is
// naturally written `correction --contradicts--> stale` — the new memory
// refutes the old one — which is an INCOMING edge at the stale memory. So when
// the stale fact was the seed (the common case: it is older, better
// established, often pinned), the correction was unreachable. Measured by
// TestEvalGraphPullThrough: flipping that one edge's direction changed the
// result from NO EFFECT to PULLED THROUGH, with everything else identical.
//
// Direction is a property of the RELATION, not of the graph, so the rule lives
// here in the application layer rather than in SQL — it is a table you can read
// and unit-test, and adding a relation means adding a row. The query fetches
// both directions (both `from_id` and `to_id` are indexed; measured max
// in-degree 141 vs out-degree 125 on a 37,698-edge production store, so this
// is an indexed lookup over tens of rows per seed) and the policy decides what
// to follow.

// edgeDirections says which way spreading activation may travel along a
// relation. Outgoing means "seed points at neighbour"; Incoming means
// "neighbour points at seed".
type edgeDirections struct {
	Outgoing bool
	Incoming bool
	// Priority orders expansion slots when a seed has more edges than
	// MaxEdgesPerSeed. Higher wins.
	//
	// This exists because ordering by weight alone silently starved the typed
	// relations. Auto-linked `relates_to` edges take raw cosine as their
	// weight, clamped to [0.5, 1.0] (see autoLinkEdges), so near-duplicate
	// siblings routinely sit at 0.95-0.99 — above `contradicts`, whose
	// semantic default is 0.90. A seed surrounded by near-copies therefore
	// spent all five of its expansion slots on "this is similar to that" and
	// never followed the one edge that said "this REFUTES that". Measured:
	// with ten near-duplicate fillers present, an incoming contradicts edge
	// produced no effect at any budget; with priority applied it pulls
	// through. A meaning-bearing relation should not lose a slot to a
	// similarity score.
	Priority int
}

// edgeExpansionPolicies is deliberately conservative: incoming traversal is
// enabled only where the relation is genuinely symmetric for RETRIEVAL
// purposes, so this cannot flood context with reverse-direction passengers.
//
//   - relates_to  — symmetric by construction (auto-linked on cosine similarity;
//     which side happened to be written first carries no meaning).
//   - contradicts — mutual: if A refutes B then seeing either without the other
//     is exactly the failure this graph exists to prevent.
//   - refines     — a refinement should surface when the thing it refines is
//     found; that is the entire point of recording it.
//
// Everything else stays outgoing-only, preserving today's behaviour:
//
//   - contains    — parent→child drives child suppression during packing.
//     Traversing child→parent here would collide with the separate
//     parent-boosting path, so it is left alone deliberately.
//   - depends_on  — finding a dependent should pull its prerequisite. The
//     reverse (pull every dependent of a prerequisite) fans out badly.
//   - caused_by / prevents / implies — same asymmetry: one cause can have many
//     effects, and pulling all of them in is noise, not context.
//   - merged_into — an audit trail, never traversed (weight 0, also excluded
//     in SQL).
var edgeExpansionPolicies = map[string]edgeDirections{
	// A contradiction is the one thing an agent must never miss, so it takes
	// a slot ahead of everything else.
	"contradicts": {Outgoing: true, Incoming: true, Priority: 3},
	// Relations that carry explicit meaning, curated or inferred.
	"refines":    {Outgoing: true, Incoming: true, Priority: 2},
	"depends_on": {Outgoing: true, Priority: 2},
	"caused_by":  {Outgoing: true, Priority: 2},
	"prevents":   {Outgoing: true, Priority: 2},
	"implies":    {Outgoing: true, Priority: 2},
	"contains":   {Outgoing: true, Priority: 2},
	// Generic similarity, auto-linked in bulk. Last in line by design: it is
	// the most numerous relation and the least informative.
	"relates_to":  {Outgoing: true, Incoming: true, Priority: 1},
	"merged_into": {},
}

// expansionDirectionsFor returns the policy for a relation. Unknown relations
// default to outgoing-only — the pre-existing behaviour — so a relation added
// without a policy row degrades to what ghost did before rather than silently
// gaining reverse traversal.
func expansionDirectionsFor(rel string) edgeDirections {
	if d, ok := edgeExpansionPolicies[rel]; ok {
		return d
	}
	return edgeDirections{Outgoing: true, Priority: 1}
}

// neighborForSeed returns the far end of an edge relative to seedID, and
// whether the policy permits travelling that way at all. A self-loop returns
// ok=false so callers do not have to guard separately.
func neighborForSeed(e Edge, seedID string) (neighborID string, ok bool) {
	if e.FromID == e.ToID {
		return "", false
	}
	dirs := expansionDirectionsFor(e.Rel)
	switch seedID {
	case e.FromID:
		return e.ToID, dirs.Outgoing
	case e.ToID:
		return e.FromID, dirs.Incoming
	}
	return "", false
}

// expansionSeed is a starting point for spreading activation: a memory ID and
// the score its neighbours' propagation is scaled from.
type expansionSeed struct {
	id    string
	score float64
}
