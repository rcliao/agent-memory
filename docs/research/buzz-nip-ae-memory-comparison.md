# buzz `mem` (NIP-AE) vs ghost — a bounded design read

2026-08-08. This is the "one bounded design read of NIP-AE for ghost's benefit"
called for by shell's `PLAN-BUZZ-DECISION.md`. The adoption question is closed
(declined, recorded there); this document only extracts what is worth stealing.
Method: no buzz source is available locally, so everything below is observed
from the CLI surface of the shipped binary (`buzz mem`, `buzz pack`) and from
probing `pack validate` with deliberately wrong inputs until the schema fell
out. Rust crate paths embedded in the binary (`crates/buzz-persona/...`,
`crates/buzz-cli/src/commands/mem.rs`) corroborate the layout.

## What buzz `mem` is

A relay-backed key–value store for agent "engrams": `ls / get / hash / set /
patch / rm`, addressed by slug, scoped by owner/agent pubkey, deletion via
tombstone (`rm`, refused for the protected `core` slug). Errors are a JSON
envelope `{"error", "message", "retryable"}` with a **dedicated exit code (5)
for write conflicts**.

What it is **not**: there is no search, no ranking, no relevance, no lifecycle
or decay, no graph, no token-budget assembly. The entire ghost retrieval stack
has no buzz counterpart. The two systems barely overlap — buzz's strength is
**write discipline**, ghost's is everything that happens after the write. That
is what makes the steal list short and sharp.

## The steal list, in priority order

### S1 — Compare-and-swap writes (high)

`buzz mem patch --base-hash <sha256>` refuses to apply if the slug changed
since the hash was captured (`buzz mem hash <slug>` before editing; the hash is
over the exact UTF-8 bytes, not normalized lines). `--no-base-hash` exists and
its help text says plainly that it is unsafe under concurrency.

Ghost has no equivalent. `Put` is last-write-wins with version history: two
well-behaved writers can both `Get` v3 and both `Put`; the second lands as v5
and silently buries the first's edit in history. This is not hypothetical —
ghost's multi-writer reality is the daemon (mechanical capture, reflect,
consolidate), the MCP agent tools, the CLI, and heartbeats, all against the
same store, sometimes the same keys (session summaries, identity documents).
The #91 deferred-transaction fix addressed DB-level lost writes; app-level
read-modify-write interleaving is still unguarded.

Ghost-shaped design: `PutParams.BaseVersion int` — version is already tracked
and monotonic, so CAS on version is cheaper than hashing and gives the same
guarantee. Zero means "no check" (today's behaviour, fully
backwards-compatible). On mismatch, a typed conflict error the caller can
distinguish from failure. CLI `--base-version`, MCP `base_version` optional
param. Eval-first red test: two interleaved writers; the stale writer's `Put`
must fail instead of silently superseding.

### S2 — Context-verified partial edits (medium-high)

`mem patch` applies a unified diff and "refuses hunks whose context doesn't
match the current value verbatim". For LLM agents this does two jobs at once:
it is the CAS story above, and it prevents **paraphrase drift** — an agent
editing one line of a memory today must rewrite the whole value, and whole-value
rewrites mutate the untouched text. Restatement accumulation is a measured
ghost disease; byte-exact preservation of unedited content attacks it at the
write path.

`ghost patch` would be a new CLI subcommand and MCP tool → needs human
approval under CONVENTIONS.md before building.

### S3–S5 — Hygiene riders (low cost, take alongside S1/S2)

- **Empty-write guard**: `set` rejects a zero-byte stdin read unless
  `--allow-empty` — prevents silent data loss from a broken upstream pipeline.
- **`--dry-run`** on patch: echoes the patch plus the resulting hash without
  writing — a natural propose→review→apply loop for agent edits.
- **Error taxonomy**: machine-readable category + `retryable` flag + dedicated
  conflict code. Ghost MCP errors are strings; a typed conflict category is
  what lets an agent retry intelligently rather than pattern-match messages.

## `pack` vs shell's identity layers

A persona pack is a Claude-plugin-shaped directory, validated entirely locally:

```
.plugin/plugin.json     {id, name, version, description, personas: ["personas/x.md"]}
personas/x.md           YAML frontmatter (name, display_name required;
                        description; trigger/thread/broadcast defaults)
                        + body = system prompt
instructions.md, .mcp.json, skills.plugin   (optional, same plugin idiom)
```

`pack validate` walks the whole structure and fails with precise,
machine-readable errors (missing manifest → missing `id` → zero personas →
missing frontmatter → missing `display_name` — each surfaced separately when
probed). `pack inspect` renders the effective config: triggers, reply policy,
prompt size.

The analogue is shell's identity layers (charter/personality/lore as pinned
sets), which have **no validation step** — the daemon logs layer hashes at
startup but nothing checks shape, and a malformed or missing layer degrades
silently. Stealable, shell-side: a `shell identity validate` against a small
manifest, and possibly a portable persona-export format for backup/regroup.
Not ghost's job — ghost deliberately does not interpret identity taxonomies.

## What ghost should not take

- **Relay/event-log/signatures**: both shell daemons hold the key, so signing
  buys nothing (recorded in PLAN-BUZZ-DECISION). Ghost's local SQLite gets
  locality, latency, and privacy that a relay model gives up.
- **Flat slug namespace**: ghost's ns/key/tags/tiers is strictly richer.
- **Tombstone-only protection**: ghost's protected-memory contract (pinned +
  locked bits with lifecycle immunity) is already stronger than a single
  protected `core` slug.

## Verdict

Adopt nothing wholesale; steal the write discipline. S1 (CAS on version) is
the one change with a real, present failure mode behind it and near-zero cost.
S2 is the interesting one long-term because it attacks restatement drift at
the write path, but it needs a new-subcommand approval. S3–S5 ride along.
Pack-validate is a shell backlog item, not a ghost change.

---

## Part 2 — the scoping and task model behind `mem` (2026-08-08)

The first pass compared the storage surface. Probing the binary's validation
strings answers the deeper question: where did buzz's complexity GO, if not
into retrieval?

### Memory is a few governed documents, not a corpus

Slugs are `core` or `mem/<name>` (`[a-z0-9][a-z0-9_-]*`, ≤64 bytes). Each slug
is explicitly "a single virtual file" — `mem patch` rejects multi-file diffs —
and `core` carries a mandatory `profile` field and cannot be tombstoned. So
NIP-AE's mental model is a handful of markdown-ish documents per agent, edited
by diff under CAS: the CLAUDE.md model, not a searchable memory store. There
is no retrieval question because the entire memory is small enough to load.

### Scope is cryptographic, three ways at once

Every engram event is structurally validated on the relay side:

- signed by the **agent** pubkey (`pubkey != expected_agent` rejection),
- addressed to the **owner** via p-tag (`p tag != expected_owner`),
- slug bound to the addressable d-tag by re-derivation
  (`body slug does not re-derive to d tag`).

So the unit of scope is the (owner, agent, slug) triple, and none of the three
can be spoofed. Ghost's equivalent is a namespace string plus filesystem
permissions; shell hardens it socially with one SQLite file per agent.

### The capability split is the interesting part

`BUZZ_AUTH_TAG` (NIP-OA) makes agent-vs-owner a property of the signature, not
of convention: an agent "signs as itself, so it can only ever satisfy the self
path (target == signer), not the owner-of-agent path." Owner actions —
archiving an identity (NIP-IA kinds 9035/9036), approving an agent update —
are unforgeable by construction. Agent creation and edits flow through
`agents draft-create / draft-update`, which open a prefilled form in the
OWNER's desktop: agents propose, the owner approves.

Ghost's analogue is the `locked` tag + `--unlock`, which is caller-side
discipline; shell's analogue is that papi owns the config files and reviews
evolve changes. Same pattern, enforced at different layers.

### Task design: workflows with human gates, not an agent task store

There is no per-agent task memory. Tasks live as channel-scoped YAML
`workflows` with `runs` and explicit human `approve`/`deny` steps, plus a
`feed` with a `needs_action` type. Shell already evaluated and declined this
(`PLAN-BUZZ-DECISION.md`): the queue ledger covers it, and adopting the
workflow framing risked rebuilding the `agent.task` kind retired 2026-08-07.

### What this changes about the verdict

Nothing about adoption, but it sharpens the comparison: **buzz spent its
sophistication on WHO may write — ghost spends it on WHAT to retrieve.** The
two systems are complementary answers to different halves of agent memory.
Three residual notes:

1. The (owner, agent) pair as a first-class data-model concept formalizes what
   shell does socially. For a single-household deployment with both daemons
   holding the key, the cryptography buys nothing — but the *shape* (agent
   proposes, owner approves identity changes) independently validates shell's
   evolve-review pattern.
2. `core` ≈ ghost's pinned+locked contract; ghost's is richer (lifecycle
   immunity, retained version history), so nothing to take.
3. The virtual-file model is the strongest argument yet for S2: buzz gets
   clean diff-based editing *because* it treats a memory as a document. Ghost
   treats memories as opaque values; a `ghost patch` would import the
   ergonomics without importing the architecture.
