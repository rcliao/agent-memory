# Provenance as encoding context: who, where, and the memory graph

2026-08-12. Extends `source-identity-design.md` (phase 1: person aliases and the boost) into the fuller architecture.
Companions: `provenance-first-class.md`, `../provenance-and-memory.md`.

## Problem

Provenance today records only WHO — `source_user` and `source_kind`.
Two gaps follow.

First, WHERE is missing.
A coding agent serves one user across many projects; the project is the load-bearing encoding context there, not the person.
Our own data shows the cost: the `agent:claude-code` namespace carries zero provenance, and memories born in different repos mingle indistinguishably in one store.
The Source Monitoring Framework says context — spatial, social — is part of source, so who-only provenance is the borrowed concept left half-implemented.

Second, identity is a dangling string.
A canonical id like `mami` names a person, but nothing in the store IS that person.
Facts about her are scattered rows findable only by text search.
The graph cannot traverse from a memory to what the agent knows about its source.
Asking "what do I know about mami" has no structural answer.

Affected: every multi-project coding-agent integration (scope), both family agents' entity recall (identity), and the owner's proposal to make provenance graph-native, which needs a decision.

## Goals / Non-Goals

Goals:

- Record encoding context — who AND where — at write time, mechanically, by the writer.
- Identities and places become addressable memory nodes; canonical ids resolve to nodes, not bare strings.
- Retrieval can traverse from a memory to its source entity and back — "what do I know about mami" becomes graph expansion, not text search.
- Reuse existing concepts: memories, edges, aliases. No parallel provenance subsystem.

Non-Goals:

- No provenance edge on every mechanical write (see Option B — hub degeneracy).
- No LLM inference of who or where — declared and mechanical only, as always.
- No rewriting or migrating the shipped `source_user`/`source_kind` columns or their data.
- No numeric confidence; the ladder stays ordinal.

## Proposed Design

Three layers, cheapest first.
The record stays in-row; the entities become memories; the edges stay selective.

**Layer 1 — the record (columns).**
The immutable facts of the encoding event stay denormalized on the memory row: `source_user`, `source_kind`, and NEW `source_scope`.
Scope is a caller-defined place id — `project:ghost` for a coding agent, `chat:<id>` for a messenger agent.
Rationale for staying in-row: provenance must survive graph lifecycle (edge decay, merge, consolidation) untouched, and rendering must not cost a join per packed memory.

**Layer 2 — the entities (memories).**
This is the owner's recursion, adopted: a canonical id is the KEY of an identity memory node in the same namespace.
`person:mami` is a memory holding her standing facts; `project:ghost` can be one too.
The `source_aliases` table (phase 1) now resolves variants to a node key instead of a bare string.
The person is a memory; provenance points at it.
Nothing new is invented — the node is put, retrieved, pinned, and edged like any memory.

**Layer 3 — the edges (selective).**
A NEW `about` relation links a curated fact to its entity node: `health-mami-canonical --about--> person:mami`.
Authored at curation time — by agents or consolidation — NOT mechanically per exchange.
Expansion policy: accompany, one hop, both directions.
When a mami fact surfaces, her node can travel with it; when her node seeds, her key facts expand from it.

The division of labor: columns answer "where did THIS memory come from" (total coverage, zero cost); edges answer "what does the store know about this entity" (curated coverage, graph traversal).
The graph view of provenance is derivable from the columns at any time, because the record is total.

Retrieval gains one parameter: `ForScope`, symmetric to `ForUser`, resolved through the same alias table, boost-never-filter, applied before the MinScore floor like its sibling.
Same red-first eval discipline: the boost eval must fail without the mechanism.

## Data Flow

1. A writer puts a memory with `source_user` and NEW `source_scope` — the Claude Code hook passes the project dir's declared scope id; shell passes the chat scope; stored verbatim by the store, unchanged otherwise.
2. NEW: the integration seeds scope aliases at startup (worktree paths and checkout variants map to one `project:` id), alongside the phase-1 person aliases, via `SetSourceAlias`.
3. NEW: curation (agent or consolidate) authors `about` edges from durable person- or project-facts to their entity node — never from raw exchanges.
4. CHANGED: the context assembler resolves `ForUser` and NEW `ForScope` through the resolver and boosts matching candidates before the MinScore floor.
5. CHANGED: edge expansion treats `about` per its policy — an entity node accompanies its surfacing facts one hop in either direction.
6. The renderer receives `source_user`, `source_kind`, and NEW `source_scope` verbatim in `ContextMemory`; it may append scope only when foreign to the current one.

Steps 1 and 4–6 run per turn; steps 2–3 are rare declarative writes.

## Components

- Scope column (`internal/store` migrations, crud): third provenance field — NEW.
- Resolver (`canonicalSource()`, phase 1): unchanged mechanism, now also resolving scopes — CHANGED.
- `ForScope` boost (`internal/store/context.go`): sibling of `ForUser` — NEW.
- `about` relation (`internal/store/edge_policy.go`): accompany policy, hop 1 — NEW.
- Entity node convention (`docs/integration-guide.md` + MCP guidance): key format, pinning advice — NEW.
- Claude Code hook + shell (separate PRs): pass scope, seed scope aliases — NEW.

## Data Model

```dbml
Table memories {
  ns           text [not null]          // unchanged; lookup by (ns, key)
  key          text [not null]          // entity nodes are ordinary rows: key "person:mami"
  source_user  text [null]              // unchanged (shipped)
  source_kind  text [null]              // unchanged (shipped)
  source_scope text [null]              // NEW: place id of the encoding event, verbatim
  // ...other fields unchanged
}

Table source_aliases {
  ns         text [not null]            // phase 1 table, unchanged shape; lookup by (ns, alias)
  alias      text [not null, note: 'COLLATE NOCASE']
  canonical  text [not null]            // CHANGED meaning: by convention the KEY of an entity memory
  created_at text [not null]
  indexes {
    (ns, alias) [pk]
  }
}

Table memory_edges {
  from_ns   text [not null]             // unchanged table; lookup by (from, to, relation)
  from_key  text [not null]
  to_ns     text [not null]
  to_key    text [not null]
  relation  text [not null]             // NEW value: 'about' (fact -> entity node)
  weight    real [not null]
  // ...unchanged
}

// Ref: source_aliases.canonical -> memories.key is CONVENTION, not enforced:
// a canonical id may predate its entity node, and resolution must not fail on absence.
// Table provenance_events — still deliberately does NOT exist (flip condition
// recorded in provenance-first-class.md: real source events get a side table then).
```

Compatibility: rows with empty `source_scope` behave exactly as today; unaliased scopes resolve to themselves; the shipped columns and their data are untouched.

## Interfaces

CHANGED — `PutParams` gains `SourceScope string` (stored verbatim, nullable); MCP `ghost_put` gains `source_scope` with guidance naming the scope convention.
Writes `memories.source_scope`; used by step 1.

NEW — `ContextParams.ForScope string` / MCP `ghost_context.for_scope`: alias-resolved match on `source_scope`, multiplicative boost before MinScore, empty = byte-identical behavior.
Used by step 4; reads `source_aliases`, `memories`.

CHANGED — `ContextMemory` gains `source_scope` (verbatim) through all packing paths.
Used by step 6.

CHANGED — `ghost source alias` (phase 1 CLI): documentation only; scopes use the same command.
Used by step 2.

NEW — edge relation `about` accepted by `ghost edge` / `ghost_edge` with policy accompany/hop-1.
Used by steps 3 and 5; reads and writes `memory_edges`.

Unchanged — `ListParams.SourceUser` filtering; a `SourceScope` filter follows the same pattern if needed.

## Options Considered

### Option A — columns only (add source_scope, skip the entity layer)

Pros:

- Smallest change; phase-1 machinery plus one column and one boost.
- Total coverage by construction on mechanical paths.

Cons:

- Identity stays a dangling string; "what do I know about mami" remains text search.
- The alias table resolves to nothing addressable; entity facts stay scattered.

Rejected: delivers scope but abandons the graph half of the owner's insight.

### Option B — fully graph-native: provenance IS edges, on every memory

Pros:

- One concept — memories and edges — with no denormalized provenance fields at all.
- Maximally recursive: the person node's own provenance works identically.
- Closest to W3C PROV's spirit (attribution as first-class graph structure).

Cons:

- Hub degeneracy: one person node would accumulate tens of thousands of edges — the measured clusters hairball (a 1,311-member component) at far greater scale, dominating expansion budgets.
- Lifecycle collision: edges decay and strengthen by design; provenance is immutable — an edge class exempt from all edge dynamics is a column wearing an edge costume.
- Write amplification on the hottest path (every exchange), plus a join per rendered memory.
- Migration: throws away shipped, populated columns and a day-one census baseline.
- Agent-authored relation misuse is a measured phenomenon (146/146 mislabels); provenance correctness would inherit that surface.

Rejected as the total model — but its entity-node core is adopted in the chosen design.

### Option C — hybrid: record in-row, entities as memories, edges selective (chosen)

Pros:

- Keeps the total, immutable, zero-cost record where it is; adds the graph where the graph earns it.
- Entity nodes reuse put/pin/edge machinery — the recursion without the hubs.
- Derivable: a full provenance graph can be materialized from columns later if ever needed.

Cons:

- Two places express "who": column (always) and edge (when curated) — divergence is possible and needs a census check.
- `about` coverage depends on curation actually happening.

### Option D — separate provenance/event tables

Pros:

- Room for source events (exchange pointers, corroboration) with clean relational shape.

Cons:

- Rejected twice already; the flip condition (real source events) has not arrived.

Rejected: unchanged from `provenance-first-class.md`.

## Risks

- Entity nodes become dumping grounds or blow the pin budget (a known tight budget).
  Mitigated: nodes hold a summary, not every fact; facts stay separate rows linked `about`.
- Over-authored `about` edges recreate the hub problem gradually.
  Mitigated: policy caps expansion at one hop; the edge census already watches relation quality; flooding gate precedent applies.
- Column/edge divergence (edge says mami, column says a variant).
  Mitigated: both resolve through one alias table; census spot-checks.
- Scope ids fragment across worktrees and renames.
  Mitigated: that is what scope aliases are for; seeded mechanically by the integration.

## Definition of Done

Phased; each phase is its own PR with its own red eval first.

- [automated] Phase 1 (unchanged, in review): `source-identity-design.md` DoD.
- [automated] Phase 2 — scope: red `ForScope` eval fails on main, passes after; scope-alias resolution tests; unset `ForScope` byte-identity; Claude Code hook writes `source_scope` on session memories (asserted in hook test).
- [automated] Phase 3 — entities: `about` policy tests; expansion eval where "what do I know about person X" pulls `about`-linked facts through Context, red-first against text search baseline.
- [manual] Production A/B on snapshots: same-project query prefers same-scope memories at a contested budget.
- [manual] Census after 1 week: scope coverage per write path; `about` edge quality spot-check.
- Out of this handoff: authority mechanism (c); namespace-leak fix; any backfill (still never).

## Unresolved Questions

- [blocking] Entity node key convention: `person:mami` / `project:ghost` prefixed keys, or bare canonical ids as keys?
  Prefixes prevent collisions with existing keys and make nodes greppable; owner picks.
- [blocking] Are entity nodes pinned by default?
  Pinning guarantees presence but the pin budget is already contended; accompany-expansion may suffice.
- [non-blocking] Should the renderer show scope always, or only when foreign to the current scope?
- [non-blocking] Do `about` edges participate in co-retrieval strengthening, or hold fixed weight?
- [non-blocking] Does `chat:<id>` tagging eventually retire in favor of `source_scope`, or serve both roles indefinitely?
