# Source identity: canonical person ids for provenance

2026-08-11. Follows `provenance-first-class.md` (position), `capture-to-storage-data-model.md` (write-path survey), and `../provenance-and-memory.md` (cognitive grounding).

## Problem

Provenance records who a memory came from, but `source_user` is a free string.
Every writer invents its own spelling.
The day-1 census (2026-08-10) found one person under four labels.
The variants: the configured display label, the short given name, the lowercased name, and a nickname form.
Nineteen of 86 `stated` rows carry a variant — including the most safety-relevant curated canonical.

Exact-string matching therefore fails where it matters most.
The interlocutor boost cannot join a person's conversation to their own curated facts.
The `List --source-user` filter misses variant rows.
The future authority ladder has no reliable notion of "same person" to gate on.

The census also found the boost itself was never implemented.
`forUserBoost` exists only in PR #116's commit message, and the gating eval passes on main without it — a false green.
Its corpus never forces a contested choice, so the gate never required the mechanism.

Affected: both family agents' person-fit retrieval, the authority mechanism this workstream builds toward, and any future source management.
Today a label merge is impossible without rewriting memory rows, which the provenance design forbids.

## Goals / Non-Goals

Goals:

- One person resolves to one canonical id at every provenance read point, whichever declared spelling a writer used.
- The interlocutor boost exists, is canonical-aware, and is gated by an eval that fails without it.
- Merging or renaming a source is a reversible metadata operation with zero memory-row rewrites.
- Works for every writer — including the agent-authored MCP path that caller-side rules cannot reach.

Non-Goals:

- No inferred identity: ghost never decides two labels are the same person; equivalence is always declared.
- No entity registry: no person metadata, no cross-namespace sharing.
- No backfill or rewriting of stored `source_user` values.
- Not here: the namespace-leak fix and the authority mechanism — separate changes; authority additionally waits for this normalization.

## Proposed Design

Source identity becomes ghost's job, on the bookkeeping side of ghost's judgment/bookkeeping line.
A declared alias table maps spellings onto canonical ids.
Resolution is exact, case-insensitive lookup — never inference.

Writes stay verbatim forever.
`Put` stores exactly the declared string; a mislabel remains visible as evidence.
This is the suggestibility principle from `provenance-and-memory.md`: attribution is a historical record.

Declarations are reversible.
A merge is an alias-row insert; an unmerge is a delete.
`canonical(x)` is one step: the matching row's canonical, else `x` itself.
Chains are rejected at declaration, in both directions.
An alias may not point at a declared alias.
An alias string may not equal an existing canonical — that would split one person across two ids.

Resolution happens at read, at the points that care.
The boost — built red-first, since it does not exist today — applies `score *= 1.8` when `canonical(memory.source_user) == canonical(params.ForUser)`.
It applies BEFORE the MinScore floor, stated explicitly: an undocumented interaction with `GHOST_MIN_SCORE=0.3` is how the #103 incident happened.
Rendering stays verbatim; unifying display names is the caller's choice.

`COLLATE NOCASE` is ASCII-only.
The observed fragmentation is Latin-script variants, and CJK labels do not case-fold, so both are covered.

Approvals (CONVENTIONS.md — new table, Store interface change, new CLI subcommand): owner, 2026-08-10.

## Data Flow

1. A writer (daemon, MCP agent, skill, sync peer) puts a memory with whatever `source_user` label it has — unchanged; stored verbatim by the store.
2. NEW: shell's seeder, at daemon startup, declares aliases from its `user_labels` config to the store via `SetSourceAlias` (canonical short id per person; display label and name token as aliases).
3. NEW: the operator declares further aliases with the `ghost source` CLI when the census surfaces variants; each declaration is one row, reversible by `ghost source rm`.
4. CHANGED: the context assembler, scoring candidates for `Context(ForUser)`, resolves both sides through the resolver and multiplies matching candidates by 1.8 before applying the MinScore floor.
5. CHANGED: the list filter resolves `ListParams.SourceUser` the same way, so `--source-user mami` returns variant-labeled rows.
6. The renderer (shell) receives `source_user` verbatim in `ContextMemory` and prefixes attribution — unchanged.

Steps 4 and 5 run per-query and independently; steps 2 and 3 are rare writes.
The resolver is not greppable as a role name yet: it lands as `canonicalSource()` in `internal/store`.

## Components

- Resolver (`canonicalSource()`, `internal/store`): one-step alias lookup — NEW.
- Alias CRUD (`internal/store/sqlite*.go`): table + Store methods — NEW.
- Boost (`internal/store/context.go` scoring): applies the multiplier — NEW.
- `ghost source` command (`internal/cli`): declaration surface — NEW.
- MCP guidance (`internal/mcpserver`): names canonical ids — CHANGED.
- Seeder (shell repo, separate PR): startup declarations from `user_labels` — NEW.

## Data Model

```dbml
Table source_aliases {
  ns         text [not null]            // NEW table; lookup by (ns, alias)
  alias      text [not null, note: 'COLLATE NOCASE']
  canonical  text [not null]
  created_at text [not null]
  indexes {
    (ns, alias) [pk]
  }
}

Table memories {
  ns          text [not null]           // unchanged; lookup by (ns, key)
  key         text [not null]
  source_user text [null]               // unchanged: stays verbatim, never rewritten
  source_kind text [null]               // unchanged: stated | observed | self | peer
  // ...other fields unchanged
}

// Ref: source_aliases.canonical is a plain string, NOT a foreign key —
// convention, not enforced. A canonical id has no row of its own;
// the table is a mapping, not an entity registry.
// Table source_entities — deliberately does NOT exist: person facts
// (birthdays, relationships) are memories, not schema.
```

Compatibility: rows whose `source_user` matches no alias resolve to themselves, so the ~12k pre-provenance and unaliased rows behave exactly as today.

## Interfaces

NEW — Store methods:
`SetSourceAlias(ctx, ns, alias, canonical) error` (rejects chain/reverse-collision, see Proposed Design);
`ListSourceAliases(ctx, ns) ([]SourceAlias, error)`;
`DeleteSourceAlias(ctx, ns, alias) error`.
Used by Data Flow steps 2–3; read and write `source_aliases`.

NEW — CLI:
`ghost source alias <alias> --canonical <id> --ns <ns>`, `ghost source list --ns <ns>`, `ghost source rm <alias> --ns <ns>`.
Errors: chain rejection, reverse collision, missing ns.
Used by step 3; wraps the Store methods.

CHANGED — `ContextParams.ForUser` (step 4): signature unchanged; matching becomes `canonical(source_user) == canonical(ForUser)`.
Before: dead parameter (no code consumed it).
After: alias-aware boost, applied before MinScore.
Reads `source_aliases` and `memories`.

CHANGED — `ListParams.SourceUser` (step 5): exact match → alias-aware match.

Unchanged shape — MCP `ghost_context.for_user`, `ghost_put.source_user`; guidance text gains the canonical-id instruction.

## Options Considered

### Option A — caller-side canonicalization only (shell fixes its labels)

Pros:

- No ghost schema, interface, or CLI change at all.
- Fixes the majority mechanical write volume with one threaded parameter.

Cons:

- Cannot reach the path that caused the fragmentation: the agent free-typing names into `ghost_put`. Guidance alone already lost this bet once (146/146 edge mislabels).
- Every writer must re-implement the rule; one missed writer re-fragments the corpus.
- Existing variant rows stay unjoinable forever without rewrites.

Rejected: simpler, but it defends only the paths that were never the problem.

### Option B — normalize at write, plus a migration command for old rows

Pros:

- Reads stay dumb and fast; no resolution logic in the retrieval path.
- The stored value is always already canonical.

Cons:

- Mutates provenance after the fact — the suggestibility failure mode the provenance doc refuses; a mislabel gets laundered into apparent correctness.
- A merge is a destructive batch rewrite; an unmerge is impossible.
- Late-declared aliases require another migration pass each time.

Rejected: violates the verbatim principle and makes merges irreversible.

### Option C — declared alias table, resolve at read (chosen)

Pros:

- Catches every writer, including the untrusted one; retroactive the moment an alias is declared, zero row rewrites.
- Merges are instant and reversible; the record stays the record.
- Stays mechanical: declared equivalence is bookkeeping, like tags — no inference.

Cons:

- A lookup (or cached map) in the read path; new table and CLI surface to maintain.
- Depends on someone declaring aliases (mitigated: shell seeds mechanically; the census measures drift).

### Option D — full entity registry (person rows with metadata)

Pros:

- Room for future person attributes and relationships in one place.

Cons:

- Ghost needs identity equivalence, not entities; person facts are memories, not schema.
- Scope creep toward a knowledge graph ghost deliberately is not.

Rejected: the mapping table is the smallest thing that solves the problem.

## Risks

- Agents invent variants faster than anyone declares them.
  Mitigated: mechanical writes become canonical at the source (shell PR), guidance names the ids, and the census measures the residue — now recoverable by declaration instead of unfixable.
- A wrong alias declaration merges two people.
  Mitigated: reversible by deleting the row; no stored data changed. Residual risk accepted.
- Boost-before-MinScore lifts a junk memory over the floor.
  Bounded: the 1.8 multiplier applies only to the interlocutor's own facts, and the ordering is pinned by an explicit test.
- False-green recurrence — the gate passing without the mechanism, as in #116.
  Mitigated: the first commit is the eval proven red on main.

## Definition of Done

- [automated] Rebuilt `TestProvenanceInterlocutorBoost` demonstrably FAILS at the pre-implementation commit (red run recorded in the PR) and passes after the boost lands.
- [automated] New tests green under `go test ./... -count=1`: boost across aliases, List filter across aliases, alias CRUD, chain rejection both directions, case-insensitivity, boost-before-MinScore ordering, unset-`ForUser` byte-identity.
- [automated] `go vet` clean; existing suites unaffected.
- [manual] Snapshot A/B on production data: a variant-labeled curated canonical flips from unboosted to boosted once its alias is declared.
- [manual] Two-step deploy verified (library + PATH binary, daemon inode changed).
- Out of this handoff: the shell seeding PR, the namespace-leak fix, and the authority mechanism — each its own change.

## Unresolved Questions

- [blocking] Canonical id scheme: short household nicknames (`mami`, `papi`) or opaque stable ids (e.g. messenger user id) with nicknames as aliases?
  Nicknames are readable in renders and logs; opaque ids survive nickname changes.
  Owner decides before implementation.
- [non-blocking] Expose `canonical_source_user` in `ContextMemory` alongside the verbatim field, so renderers can unify display without their own lookup.
- [non-blocking] A `ghost_source` MCP tool for agent self-service declaration — only if the census shows guidance is not enough.
