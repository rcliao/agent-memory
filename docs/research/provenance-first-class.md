# Provenance as a first-class citizen — prior art and position

2026-08-08. Follow-up to the August landscape RPI (`agent-memory-landscape-2026-08.md`,
steal-list tier 1) and to ghost PR #113, which shipped `source_user` +
`source_kind`. This note records the prior-art validation of that design and
puts forward the position the owner set: **provenance is a first-class part of
the memory model, not a bolt-on field pair** — with what that means concretely.

## Prior art: who implements provenance, and how

Four independent designs, three shipping and one formal standard, converge on
the same shape ghost adopted:

**Zep/Graphiti** — the closest working analog. Messages carry `role` (the
person's actual name) and `role_type` (`human`/`ai`/`tool`); the speaker
becomes a graph entity, and every extracted fact links to its source episode
via `MENTIONS` edges. Their one documented provenance failure is
wrong-speaker attribution — it happens inside LLM extraction, the step ghost
does not have. Ghost inverts the risk: the writing agent declares provenance,
so the failure mode is mislabeling, which the adoption census spot-checks.

**MemOS** — the most formal. MemCube metadata carries `user_id`, `source`,
timestamps, and "origin signatures (e.g., user input, inference output)" —
literally ghost's `stated` vs `observed` — plus a dedicated Provenance API.
MemOS adds numeric reliability indicators, which ghost rejects (below).

**Platform-native (ChatGPT / Claude / Gemini)** — all three converged on an
explicit-vs-ambient split: what the user deliberately said versus what the
system inferred. The industry-converged minimal kind axis, validated three
times at consumer scale.

**W3C PROV / PROV-AGENT** — the standard underneath. Memory = Entity,
`source_user` = `wasAttributedTo` a human agent, `source_kind` = the Activity
type collapsed to an enum, `peer` ≈ `actedOnBehalfOf` delegation. Collapsing
the Activity triple into one field is the standard pragmatic simplification.

| ghost choice | validated by |
|---|---|
| `source_user` as first-class person field | Zep (`role` = name), MemOS (`user_id`), PROV (`wasAttributedTo`) |
| `stated` vs `observed` | MemOS origin signatures; all three platforms' explicit/ambient |
| `self` / `peer` | Zep `role_type`; `peer` is ghost's multi-agent addition, PROV-shaped |
| provenance survives patch | PROV: attribution is a historical record, immutable by construction |
| chat ≠ provenance (channel stays in tags) | Graphiti separates the episode (ingestion event) from the speaker |
| writer-declared, not extracted | inverts Zep's known wrong-speaker failure class |

## The position: first-class, not bolt-on

A bolt-on field is something a few callers set and nothing reads. First-class
means the memory model treats "who this came from and how" the way it treats
content and tier — present on every write, visible on every read, and load-
bearing in the subsystems where origin changes the right answer. Concretely,
in adoption order:

1. **Every writer populates it — mechanical writers included.** Agent tool
   calls are the minority of writes; capture, distillation and session
   summaries are the majority, and they *know* the answer (transcripts carry
   speaker roles). Distilled user facts are `stated`/`observed` with the
   speaker's name; session summaries are `self`; sync imports are `peer`.
   This is shell-side integration work and it is where first-class is won or
   lost: provenance coverage of new writes is the adoption metric.
2. **Render attribution where it matters.** Assembled context should let the
   agent see "mami said" vs "I inferred" without a second lookup — a
   rendering change, no scoring change.
3. **The authority ladder joins the conflict machinery.** `stated` outranks
   `observed` for the same person — the provenance sibling of
   never-stale-without-fresh, entering through its red-baseline eval first
   (inference-in-context must never appear without the person's own statement
   when the two conflict), then a narrow boost scoped to same-person
   conflicts, like the reserve class — never a global weight.
4. **Lifecycle reads origin.** Ambient (`observed`) captures should need a
   retrieval hit to reach ltm; `stated` facts about a person decay last. This
   is how the 76%-dead-weight disease becomes attributable and then
   fixable surgically instead of by blanket policy.
5. **Forensics.** Quarantine-by-source and cascade-demote-by-source turn
   MINJA-shaped poisoning from invisible to a one-filter cleanup; the
   phase-2 poisoning eval asserts it.

## One gap accepted, one rejected

- **Accepted (closable later):** no source-event pointer. Graphiti links every
  fact to its originating episode; ghost records who but not which exchange.
  `chat:` tags partially cover it; an optional `source_ref` (exchange or
  session key) closes it cheaply when a concrete need appears.
- **Rejected (deliberately):** numeric reliability/confidence scores (MemOS).
  Ghost's ladder is ordinal — `stated` > `observed` — and a confidence number
  would be the store computing a semantic judgment, which is the line ghost
  does not cross. Recorded as a rejection, not an omission.

## Sequencing

Adoption before authority: the census (fraction of new people-facts carrying
provenance, with a mislabel spot-check — the edge-adoption lesson) and the
guidance-eval scenario come first; the authority boost waits until the fields
are demonstrably populated and its red-baseline eval exists. An authority rule
over empty fields would measure nothing.

## Sources

- https://help.getzep.com/graphiti/core-concepts/adding-episodes
- https://help.getzep.com/v2/memory
- https://arxiv.org/html/2501.13956v1 (Zep temporal KG)
- https://arxiv.org/html/2507.03724v2 (MemOS)
- https://blog.lqhl.me/exploring-ai-memory-architectures-part-2-memos-framework
- https://arxiv.org/pdf/2508.02866 (PROV-AGENT)
- https://www.w3.org/TR/prov-dm/ (W3C PROV-DM)
