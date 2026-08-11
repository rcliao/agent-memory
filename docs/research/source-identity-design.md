# Source identity: canonical person ids for provenance

2026-08-10. Design one-pager. Follows `provenance-first-class.md` (position),
`capture-to-storage-data-model.md` (write-path survey), and
`../provenance-and-memory.md` (cognitive grounding). Motivated by the day-1
production census in the Evidence appendix, which found two defects: person
labels fragment across writers, and the interlocutor boost that was supposed
to consume them was never actually implemented.

## Intent

Provenance answers *who* a memory came from — but "who" is currently a free
string, and every writer invents its own spelling. The day-1 census found one
person recorded under four labels: the full configured display label, the
short given name, the same name lowercased, and a label-with-one-nickname
variant. Exact-string matching therefore fails precisely on the curated
canonical memories (written by the agent, which free-types names) — the
memories the authority ladder exists to protect.

The fix cannot live in the caller. Shell can canonicalize the paths it
controls, but the fragmentation came from the path it does not: the agent
typing names into `ghost_put`. Any caller-side rule must be re-implemented by
every writer (daemon, MCP agent, skill scripts, sync peers), and the writer
that caused the problem is the one that cannot be trusted to follow it —
guidance alone already lost this bet once with edge semantics.

So source identity becomes ghost's job, with one constraint that keeps it on
the right side of ghost's judgment/bookkeeping line: **ghost never infers
that two labels are the same person.** Equivalence is *declared* (by the
operator or agent) into an alias table; ghost only resolves through declared
rows with exact (case-insensitive) lookup. Declared mapping is bookkeeping,
like tags. Inference would be judgment — and the per-turn-LLM-classification
lesson from shell (catastrophic in production, replaced by a mechanical
pointer) applies verbatim.

Prior art agrees the source is an entity, not a string: W3C PROV models
agents as first-class identities (`wasAttributedTo` points at an agent, not a
label), and Zep/Graphiti resolve speakers to user entities before attributing
facts.

## Components

- `internal/store/` — new `source_aliases` table, resolution helper, the
  (actually implemented this time) `forUserBoost`, alias-aware `List` filter,
  alias CRUD on the `Store` interface.
- `internal/cli/` — new `ghost source` subcommand (alias management).
- `internal/mcpserver/` — guidance naming the canonical ids; `for_user`
  unchanged in shape.
- shell (separate PR) — seed aliases from `user_labels` at startup; pass the
  canonical short id as `ForUser` and as `source_user` on mechanical writes.

Approvals (CONVENTIONS.md): new table, Store interface change, and new CLI
subcommand approved by owner 2026-08-10.

## Data flow

1. **Write — verbatim, forever.** Mami tells the agent a preference; the
   daemon writes the memory with whatever label it has. The agent later
   writes a curated canonical and free-types the short name. Both strings are
   stored exactly as declared. `Put` never rewrites, normalizes, or resolves
   `source_user` — attribution is a historical record (the suggestibility
   principle from `provenance-and-memory.md`), and a mislabel stays visible
   as evidence rather than being laundered into correctness.
2. **Declare — once, reversibly.** The operator (or shell at startup, from
   its `user_labels` config) declares: canonical id `mami`, aliases = the
   long display label, the given name, known variants. A merge is inserting
   an alias row; an unmerge is deleting it. No memory row changes in either
   direction.
3. **Resolve — at read, at the three points that care.**
   - *Interlocutor boost*: a candidate gets `score *= forUserBoost` when
     `canonical(memory.source_user) == canonical(params.ForUser)`. The boost
     fires for the curated canonical written under the short name in a
     conversation keyed by the long label — retroactively, the moment the
     alias is declared.
   - *List filter*: `--source-user mami` matches all declared variants.
   - *Rendering*: `ContextMemory.source_user` stays verbatim (the record is
     the record). Unifying display names is the caller's choice; ghost may
     later expose `canonical_source_user` alongside, but not in this change.
4. **Guide — so declaration stays ahead of drift.** MCP guidance tells the
   agent the canonical ids exist and to use them; the census measures
   compliance. But unlike the label-hygiene status quo, a variant that slips
   through is now recoverable by declaration instead of unfixable.

## Data model

```sql
CREATE TABLE IF NOT EXISTS source_aliases (
    ns        TEXT NOT NULL,
    alias     TEXT NOT NULL COLLATE NOCASE,
    canonical TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (ns, alias)
);
```

- Scoped per namespace: agents evolve independently; identity vocabularies
  do too. No cross-namespace sharing.
- Resolution is **one step**: `canonical(x)` = the row's `canonical` if an
  alias row matches `x` case-insensitively, else `x` itself. Chains are
  prevented at declaration time: inserting an alias whose canonical is
  itself a declared alias is rejected with a suggestion to point at the
  flattened target; symmetrically, declaring an alias whose *alias* string is
  already in use as some row's canonical is rejected too — otherwise `mami`
  could resolve to a new id while older rows' variants still resolve to
  `mami`, splitting one person across two canonicals. (COLLATE NOCASE is
  ASCII-only; that covers the observed
  fragmentation, which is Latin-script name variants. CJK labels do not
  case-fold and are unaffected.)
- A canonical id is just a string with no row of its own — the table is a
  mapping, not an entity registry. Person *facts* (birthdays, relationships)
  are memories, not schema.

## Interfaces

- Store: `SetSourceAlias(ctx, ns, alias, canonical)`,
  `ListSourceAliases(ctx, ns)`, `DeleteSourceAlias(ctx, ns, alias)`;
  `ContextParams.ForUser` and `ListParams.SourceUser` become alias-aware
  internally (signatures unchanged).
- CLI: `ghost source alias <alias> --canonical <id>`, `ghost source list`,
  `ghost source rm <alias>` — all ns-scoped via the existing `--ns` flag.
- MCP: no new tool yet (guidance first; a `ghost_source` tool ships only if
  the census shows agents need self-service declaration). `ghost_put`
  `source_user` description gains: use the canonical short id; known ids are
  listed in server instructions when configured.
- Shell: seeds aliases at startup (canonical short id per `user_labels`
  entry, aliases = the display label + the label's leading name token) and
  switches `ForUser`/mechanical `source_user` to the canonical id. Existing
  long-label rows keep working via the seeded aliases — no backfill.

## Plan (eval-first, one PR ghost-side + one PR shell-side)

1. **Make the boost eval red.** The day-1 census found `forUserBoost` exists
   only in the #116 commit message — no code applies it, and
   `TestProvenanceInterlocutorBoost` passes anyway because its corpus never
   actually forces a contested choice. Rebuild the corpus so two persons'
   facts are relevance-symmetric and the budget admits exactly one; assert
   the interlocutor's fact wins. Verify the test FAILS on main.
2. **Implement `forUserBoost` (1.8, multiplicative)** at candidate scoring,
   canonical-aware from day one. Green. Ordering is explicit and tested: the
   boost applies BEFORE the MinScore floor — the interlocutor's own sub-floor
   fact may be lifted over it, mirroring the `viaEdge`/`reserved` exemptions'
   spirit (#103's lesson: an unstated interaction with `GHOST_MIN_SCORE=0.3`
   is how retrieval subsystems die silently in production).
3. **Red alias evals**: `TestForUserBoostAcrossAliases` (stored under a
   variant, boosted under the canonical), `TestListFilterAcrossAliases`,
   alias CRUD + chain-rejection + case-insensitivity unit tests, and
   unset-`ForUser` byte-identity preserved.
4. **CLI + MCP guidance**, with tests on `cmd.OutOrStdout()`.
5. **Shell PR**: startup seeding + canonical `ForUser`/`source_user`;
   per-path provenance assertions updated to expect canonical ids.
6. **Verify on production data**: rerun the snapshot A/B from the census —
   the curated canonical stored under a name variant must flip from
   unboosted to boosted for the interlocutor's conversation. Two-step deploy
   (library + PATH binary), inode-verified.

Out of scope: fuzzy or automatic alias inference; cross-namespace entities;
person metadata; rewriting stored `source_user` values; the namespace-leak
fix for agent writes to undeclared namespaces (separate, smaller change);
authority mechanism (c), which stays gated behind the census and now also
behind this normalization (flipping the authority baseline before labels
join would gate on strings that don't match).

## Evidence appendix — day-1 production census (2026-08-10)

All personal data below is described generically; the corpus itself stays
out of this repo.

- **Capture works.** Agent-namespace writes since the provenance deploy:
  115/117 carry provenance on the primary agent (exchanges 50/50, hygiene
  12/12, media notes all `stated`); 21/21 on the second agent's daemon
  namespace. Mechanical paths at 100%, as designed.
- **Rendering works.** The active model transcript contains 50 injected
  memories prefixed `[<label>, stated]` — attribution reaches generation.
- **Labels fragment on the agent-authored path.** One person appears under
  four `source_user` spellings; 19 of 86 `stated` rows carry a variant that
  exact-match would miss, including the most safety-relevant curated
  canonical in the store.
- **The boost was never implemented.** `grep forUserBoost` over the #116
  diff matches only the commit message; `TestProvenanceInterlocutorBoost`
  passes on main (`-count=1`, verified 2026-08-10) with no boost code — a
  false-green: the eval's corpus resolves the "contested" slot by ordinary
  relevance, so it never required the mechanism it gates. Ship-time sibling
  of the silent-subsystem-death class (MinScore floor, dormancy skip,
  reflect-scan miss): a subsystem can be born dead, not just die later, when
  its gate can pass without it.
- **A/B on production-profile snapshots**: 4 realistic queries, budget 2000,
  `ForUser` set vs unset — identical admission 4/4 (consistent with the
  mechanism not existing; also, the live corpus is single-interlocutor-
  dominated, so even a working boost has little to disambiguate *today*).
  Fresh snapshot per arm (context assembly mutates access counts).
- **Namespace leak (separate fix):** one agent's MCP writes sometimes pass a
  shortened namespace explicitly; those memories are invisible to daemon
  context injection and half lack provenance.
