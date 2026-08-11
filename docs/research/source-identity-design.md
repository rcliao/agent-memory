# Source identity: canonical person ids for provenance

2026-08-10 (restructured 2026-08-11 to the design-doc review template).
Follows `provenance-first-class.md` (position), `capture-to-storage-data-model.md`
(write-path survey), and `../provenance-and-memory.md` (cognitive grounding).

## Problem

Provenance answers *who* a memory came from — but `source_user` is a free
string, and every writer invents its own spelling. The day-1 production
census (2026-08-10) found one person recorded under four labels: the full
configured display label, the short given name, the same name lowercased,
and a label-with-one-nickname variant. 19 of 86 `stated` rows carry a
variant, including the most safety-relevant curated canonical in the store.

Exact-string matching therefore fails precisely where it matters: the
interlocutor boost cannot join a person's conversation to their own curated
facts, the `List --source-user` filter misses variant rows, and the future
authority ladder (stated-beats-observed *for the same person*) has no
reliable notion of "same person" to gate on.

The census also found the boost itself was never implemented: `forUserBoost`
exists only in PR #116's commit message, no code applies it, and the gating
eval passes on main anyway — its corpus never forces a contested choice, so
the gate never required the mechanism. The subsystem was born dead behind a
false-green eval.

Affected: both family agents' retrieval quality (person-fit answers), the
authority mechanism this whole workstream builds toward, and any future
source management (merging duplicate identities, renaming a person). Today a
label merge is impossible without rewriting memory rows, which the
provenance design forbids — attribution is a historical record.

## Goals / Non-Goals

Goals:

- One person resolves to one canonical id at every provenance read point,
  regardless of which declared spelling a writer used.
- The interlocutor boost actually exists, is canonical-aware, and is gated
  by an eval that fails without it.
- Merging or renaming a source is a reversible metadata operation with zero
  memory-row rewrites.
- Works for every writer, including the agent-authored MCP path that caller
  side rules cannot reach.

Non-Goals:

- No inferred identity: ghost never decides two labels are the same person;
  equivalence is always declared.
- No entity registry: no person metadata, no cross-namespace sharing.
- No backfill or rewriting of stored `source_user` values.
- The namespace-leak fix (agent writes to an undeclared namespace) and the
  authority mechanism itself — separate changes; authority additionally
  waits for this normalization.

## Proposed Design

Source identity becomes ghost's job, on the bookkeeping side of ghost's
judgment/bookkeeping line: a **declared alias table**, resolved by exact
(case-insensitive) lookup, never by inference.

```sql
CREATE TABLE IF NOT EXISTS source_aliases (
    ns         TEXT NOT NULL,
    alias      TEXT NOT NULL COLLATE NOCASE,
    canonical  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (ns, alias)
);
```

The flow, end to end:

```
write (verbatim, forever)          declare (reversible)        read (resolved)
Put{source_user:"<any label>"} --> ghost source alias X        Context(ForUser) boost
   never normalized, never    <--     --canonical mami   -->   List --source-user
   rewritten                       shell seeds from            (rendering stays
                                   user_labels at startup       verbatim)
```

- **Write — verbatim.** `Put` stores exactly the declared string. A mislabel
  stays visible as evidence; a correction is an alias declaration, not a row
  rewrite (the suggestibility principle from `provenance-and-memory.md`).
- **Declare — once, reversibly.** Canonical ids are short stable strings
  (e.g. `mami`); aliases map spellings onto them. A merge is an alias-row
  insert; an unmerge is a delete. Resolution is one step: `canonical(x)` =
  the matching row's canonical, else `x` itself. Chains are rejected at
  declaration in both directions — an alias may not point at a string that
  is itself a declared alias, and an alias string may not equal an existing
  row's canonical (which would split one person across two ids).
- **Resolve — at read, three points.** (1) The interlocutor boost — to be
  implemented, red-first, since it does not exist today — applies
  `score *= 1.8` when `canonical(memory.source_user) ==
  canonical(params.ForUser)`, BEFORE the MinScore floor (stated explicitly;
  an undocumented interaction with `GHOST_MIN_SCORE=0.3` is how the #103
  incident happened). (2) `List --source-user` matches through aliases.
  (3) Rendering stays verbatim; unifying display names is the caller's
  choice.
- **Interfaces.** Store: `SetSourceAlias` / `ListSourceAliases` /
  `DeleteSourceAlias`; `ForUser` and `ListParams.SourceUser` become
  alias-aware internally, signatures unchanged. CLI: `ghost source alias
  <alias> --canonical <id>`, `ghost source list`, `ghost source rm`. MCP:
  guidance names the canonical ids; no new tool yet. Shell (separate PR):
  seeds aliases from `user_labels` at startup and passes canonical ids as
  `ForUser` / mechanical `source_user`.
- COLLATE NOCASE is ASCII-only; the observed fragmentation is Latin-script
  name variants, and CJK labels do not case-fold, so both are covered.

Approvals (CONVENTIONS.md — new table, Store interface change, new CLI
subcommand): owner, 2026-08-10.

## Options Considered

### Option A — caller-side canonicalization only (shell fixes its labels)

Pros:

- No ghost schema, interface, or CLI change at all.
- Fixes the majority mechanical write volume with one threaded parameter.

Cons:

- Cannot reach the path that caused the fragmentation: the agent free-typing
  names into `ghost_put`. Guidance alone already lost this bet once with
  edge semantics (146/146 mislabels).
- Every writer (daemon, skills, sync peers, future callers) must
  re-implement the same rule; one missed writer re-fragments the corpus.
- Existing variant rows stay unjoinable forever without rewrites.

Rejected: simpler, but it defends only the paths that were never the
problem.

### Option B — normalize at write, plus a migration command for old rows

Pros:

- Reads stay dumb and fast; no resolution logic in the retrieval path.
- The stored value is always already canonical.

Cons:

- Mutates provenance after the fact — the suggestibility failure mode the
  provenance doc explicitly refuses; a mislabel gets laundered into
  apparent correctness.
- A merge is a destructive batch rewrite; an unmerge is impossible.
- Late-declared aliases still require another migration pass each time.

Rejected: violates the verbatim/historical-record principle and makes
merges irreversible.

### Option C — declared alias table, resolve at read (chosen)

Pros:

- Catches every writer, including the untrusted one; retroactive the moment
  an alias is declared, with zero row rewrites.
- Merges are instant and reversible; the record stays the record.
- Stays mechanical: declared equivalence is bookkeeping, like tags — no
  inference, honoring the per-turn-LLM-classification lesson.

Cons:

- A join (or cached map) in the read path; new table + CLI surface to
  maintain.
- Depends on someone declaring aliases (mitigated: shell seeds
  mechanically; the census measures drift).

### Option D — full entity registry (person rows with metadata)

Pros:

- Room for future person attributes and relationships in one place.

Cons:

- Ghost does not need entities, only identity equivalence; person facts are
  memories, not schema.
- Scope creep toward a knowledge graph ghost deliberately is not.

Rejected: the mapping table is the smallest thing that solves the problem.

## Risks

- **Agents keep inventing new variants faster than anyone declares them.**
  Mitigated: mechanical writes become canonical at the source (shell PR),
  MCP guidance names the ids, and the adoption census measures the residue —
  which is now recoverable by declaration instead of unfixable.
- **A wrong alias declaration merges two people.** Mitigated: reversible by
  deleting the row; no stored data changed. Residual risk accepted.
- **Boost-before-MinScore lifts a junk memory over the floor.** Bounded: the
  1.8 multiplier applies only to the interlocutor's own facts, and the
  ordering is pinned by an explicit test rather than left emergent.
- **False-green recurrence** — the gate passing without the mechanism, as in
  #116. Mitigated: the plan's first commit is the eval proven red on main.

## Definition of Done

- [automated] Rebuilt `TestProvenanceInterlocutorBoost` demonstrably FAILS
  at the pre-implementation commit (red run recorded in the PR) and passes
  after the boost lands.
- [automated] New tests green under `make test -count=1`: boost across
  aliases, List filter across aliases, alias CRUD, chain rejection in both
  directions, case-insensitivity, boost-before-MinScore ordering, and
  unset-`ForUser` byte-identity.
- [automated] `go vet` clean; existing suites unaffected.
- [manual] Snapshot A/B on production data: a variant-labeled curated
  canonical flips from unboosted to boosted once the alias is declared.
- [manual] Two-step deploy verified (library + PATH binary, daemon inode
  changed).
- Out of this handoff: the shell seeding/canonical-id PR, the
  namespace-leak fix, and the authority mechanism — each its own change.

## Unresolved Questions

- **[blocking] Canonical id scheme.** Short household nicknames (`mami`,
  `papi`) or opaque stable ids (e.g. the messenger user id) with nicknames
  as aliases? Nicknames are readable in renders and logs; opaque ids
  survive nickname changes. Owner decides before implementation.
- **[non-blocking] Expose `canonical_source_user` in `ContextMemory`**
  alongside the verbatim field, so renderers can unify display without
  their own lookup. Can be settled during implementation.
- **[non-blocking] `ghost_source` MCP tool** for agent self-service
  declaration — only if the census shows guidance is not enough.
