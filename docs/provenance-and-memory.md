# Provenance and Memory

The science behind ghost's provenance design — what we borrowed from how human
memory handles sources, and the one place we deliberately do the opposite,
because human source memory is famously bad and a database does not have to be.

Companion to `cognitive-inspirations.md` (tiers, kinds, decay, consolidation)
and the research pair `research/provenance-first-class.md` (prior art and
position) / `research/capture-to-storage-data-model.md` (the write-path
survey). This document is the conceptual grounding.

---

## The Source Monitoring Framework → `source_user` + `source_kind`

The central account of how humans track where knowledge came from is the
**Source Monitoring Framework** (Johnson, Hashtroudi & Lindsay, 1993). Its key
claim: the brain does **not** store a source tag alongside a memory. Source is
*reconstructed at retrieval* — inferred from qualities of the trace
(perceptual detail, emotional tone, cognitive operations) using heuristic
judgment. You do not *remember* that mami told you; you *decide*, at recall
time, that this memory feels like something mami told you.

The framework distinguishes three monitoring problems, and they map exactly
onto ghost's `source_kind` vocabulary:

| Source monitoring problem | Human question | Ghost field value |
|---|---|---|
| External source monitoring | *Which person told me this?* | `source_user` + `stated` |
| Reality monitoring (Johnson & Raye, 1981) | *Did I perceive this or imagine/infer it?* | `stated`/`observed` vs `self` |
| Internal source monitoring | *Did I say this or only think it?* | `self` |
| — (no clean human analog) | *Did another agent relay it?* | `peer` |

**Where we deliberately diverge — the load-bearing decision:** because source
is reconstructed rather than stored, human source memory fails constantly and
predictably. Ghost inverts the architecture: **provenance is recorded at
encoding, by the writer, while the source is still known** — and never
reconstructed. The write paths thread the already-resolved speaker label into
the store at the moment of capture; nothing ever guesses a source afterwards
(`provKindFor("")` returns empty — an unknown origin stays unknown forever).
This is the same pattern as ghost's other divergences: borrow the taxonomy,
refuse the failure mode.

---

## Source Amnesia and Misattribution → why the tag is immutable

Schacter's "seven sins of memory" (2001) catalogs what goes wrong when source
is reconstructed. Three sins are provenance failures:

- **Source amnesia** — retaining the fact, losing where it came from. This was
  ghost's literal shipped state before 2026-08-09: shell resolved every
  speaker at the edge (`user_labels`) and discarded the label one function
  call later. The store had classic source amnesia *by construction*.
- **Misattribution** — assigning a memory to the wrong source. Zep/Graphiti's
  one documented provenance failure is exactly this (wrong-speaker
  attribution inside LLM extraction). Ghost avoids the whole class by having
  the writer declare source rather than a model infer it — inheriting the
  smaller sibling risk, mislabeling, which the adoption census spot-checks.
- **Suggestibility** — post-event information overwriting the original
  attribution. Ghost's defense is that `ghost patch` carries provenance over
  unchanged: **a correction edits content, not origin**. The memory of what
  mami said, lightly reworded, is still a memory of what mami said. Only a
  fresh `put` — a new act of authorship — sets new provenance. This mirrors
  the archival principle (and W3C PROV's) that attribution is a historical
  record, not a mutable attribute.

---

## The Misinformation Effect → the authority ladder

Loftus's misinformation studies (1974 onward) show the canonical failure:
post-event *suggestion* blends into memory until the witness can no longer
distinguish what they saw from what they were told, and confidence does not
track accuracy. The general form: **without source tags, all information is
flattened to one authority level, and rehearsal — not origin — decides what
wins.**

Ghost reproduced this failure in production before provenance existed. The
dairy incident: an agent's own invented scoring system ("pt points"), stored
and rehearsed across days, outranked the person's direct statement about her
own body — because in context, an inference and a first-person statement
looked identical, and the inference was better rehearsed. That is the
misinformation effect with the roles reversed: the agent's suggestion
contaminated its model of the person.

The authority ladder is the countermeasure, ordinal by design:

```
stated  >  observed  >  self  ≈  peer
(the person said it > the agent inferred it > the agent's note > relayed)
```

It enters retrieval in three graduated steps (see
`research/provenance-first-class.md` addendum): rendering first — `[mami,
stated]` vs `[inferred]` prefixes hand the ladder to the one component that
can already reason about testimony, the LLM; then the interlocutor boost;
then, gated behind an adoption census and a red-baseline eval
(`TestProvenanceAuthorityBaseline`), the mechanical rule
*inference-never-without-statement* at conflict points only — never as a
global weight.

**Where we diverge from the psychology:** humans cannot choose to keep
inference and testimony separate — encoding blends them. Ghost keeps them
separate at zero cost and lets the *reader* apply the ladder. The store
records; the agent judges. This is the same division of labor as the rest of
ghost: semantic judgment belongs to the caller, bookkeeping to the store.

---

## Testimony and Secondhand Knowledge → `peer`

Epistemology distinguishes knowledge by acquaintance, by inference, and by
testimony (Coady, 1992). Testimony is not degraded knowledge — most of what
anyone knows arrives through it — but it carries a different justification
structure: its warrant depends on the chain, not just the content.

`peer` is ghost's testimony marker, and the sync path preserves the chain:
a memory relayed from another agent arrives as `source_kind=peer` (set
mechanically from the `via:<agent>` tag — transport is recorded where the
transport is known, not declared) while the original `source_user` survives
the hop. "Mami said it; umbreon relayed it" keeps both facts. W3C PROV would
express this as `wasAttributedTo` plus `actedOnBehalfOf`; two denormalized
fields express the same thing for a household-sized graph.

For a family of deliberately independent agents, this is the epistemics the
architecture wanted all along: *what I witnessed*, *what I concluded*, and
*what my brother told me* are three different kinds of knowing, and now they
are three different rows.

---

## Cryptomnesia → attribution rendering

Cryptomnesia — honest unintentional plagiarism — is generating an idea you
actually absorbed from someone else, because the content survived while the
source tag decayed (Brown & Murphy, 1989). The agent version: asserting a
user's preference back to them as if it were the agent's own insight, or —
the harmful direction — treating an absorbed inference as if the person had
confirmed it.

The defense is cheap: **render the source at use time**. Injected context now
prefixes attribution, so at the moment of generation the model sees not just
the fact but its warrant. Human memory cannot do this — retrieval does not
come with citations. A store can, for the cost of eleven characters.

---

## What provenance is *not*, in this design

Three tempting extensions were considered and rejected, recorded here so they
are decisions rather than omissions:

- **No numeric confidence** (MemOS carries reliability scores). The ladder is
  ordinal. A confidence number would be the store computing a semantic
  judgment — the line ghost's whole design refuses to cross. Confidence
  reasoning belongs to the agent, at read time, with the tags visible.
- **No reconstruction/backfill.** ~12k pre-provenance memories stay unknown
  forever. Inferring their sources now would reintroduce, in one batch, the
  exact reconstruction-at-retrieval failure the design exists to avoid.
- **No separate provenance table.** Systems whose facts are *extracted* need
  relational provenance (one Graphiti episode yields many facts). Ghost
  stores verbatim what the agent hands it — the write model is 1:1 and the
  schema follows the write model. Flip condition recorded in the research
  addendum: if source *events* (exchange pointers, corroboration) become
  real, they get an event table then, with the head fields staying in-row.

---

## Summary: borrowed vs. diverged

| Concept | Source | What ghost did |
|---|---|---|
| Source monitoring taxonomy | Johnson, Hashtroudi & Lindsay 1993 | Adopted as the `source_kind` vocabulary (stated/observed/self, + peer) |
| Source stored vs reconstructed | same | **Inverted**: recorded at encoding by the writer; never reconstructed, never guessed |
| Source amnesia / misattribution / suggestibility | Schacter 2001 | Amnesia fixed at the write boundary; misattribution avoided by writer declaration; suggestibility blocked by patch carry-over |
| Misinformation effect | Loftus 1974+ | The dairy incident is the in-house replication; the ordinal authority ladder is the countermeasure, applied by the reader |
| Testimony epistemology | Coady 1992 | `peer` + origin-person preservation across relays |
| Cryptomnesia | Brown & Murphy 1989 | Attribution rendered at use time — retrieval with citations |
| Reliability scoring | MemOS 2025 | **Rejected** — ordinal ladder only; confidence is the agent's job |

The through-line matches the rest of ghost: human memory is the map, not the
spec. Where the cognitive machinery is adaptive (tiers, decay, consolidation,
spreading activation), ghost imitates it. Where it is a documented failure
mode — and source memory is among the best-documented failure modes in the
entire literature — ghost does the opposite, because a store that *can*
remember where everything came from has no excuse not to.
