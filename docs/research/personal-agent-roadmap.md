# Ghost as an LLM-Free Personal-Agent Memory Tool — Roadmap

> Status: Proposal (2026-07-10). Derived from a 5-angle research pass (write path,
> read path, shell production usage, SOTA survey, live-DB health) + synthesis.
> All findings are grounded in code (`file:line`) and read-only queries against the
> live 285MB `~/.ghost/memory.db`.

## Thesis

Ghost already has **SOTA-competitive, fully LLM-free retrieval** (LongMemEval_S
R@5 0.908 / MRR 0.827; beats published BM25/Contriever) and a cognitively-grounded
substrate (kind-aware scoring, typed weighted edges, Atkinson–Shiffrin tiers,
pure-Go entity/TF-IDF extractors). "Best LLM-free **personal-agent** memory" is
won on three axes that the public benchmarks never measure:

1. **A capture/learning loop that is actually LLM-free.** Today automatic capture
   is 100% `claude -p` in `hooks/ghost-stop.sh:100` — it cannot run without an API
   key, which directly contradicts the differentiator.
2. **Personalization-aware ranking** that surfaces what proved useful to *this*
   user and suppresses stale/contradicted facts. Today `utility_count` is captured
   but never scored, same-day facts are structurally buried, and the associative
   graph is inert.
3. **Lifecycle & forgetting that reclaims space** and keeps durable preferences hot.

Most of the work is **wiring dormant primitives into the hot path**, not new
invention. The retrieval hot path must stay LLM-free; LLM extraction may remain an
optional out-of-band quality tier.

## Headline evidence

| Finding | Evidence |
|---|---|
| Auto-capture requires an LLM | `hooks/ghost-stop.sh:100` pipes transcript to `claude -p`; no heuristic `capture` in CLI or library |
| `utility_count` scored nowhere | `computeContextScore` (context.go:526) ignores it; 35% of live memories carry a non-zero value |
| Same-day facts buried | today's episodic ≈ `0.595 × 0.1 (sensory) × 2.5 = 0.149` vs a 60-day ltm summary `0.475`; maps to real `inject_irrelevant` recall misses |
| Edge graph inert | **205 edges over 6,345 memories**; 97% isolated islands; zero contradicts/depends_on/refines |
| Superseded bloat | ~38% of live pikamini store is superseded-but-live dead rows (one key has **50 near-identical live copies**); GC never prunes them |
| Procedural memory thin | only 6.7% (297/4404); behavioral-tagged capture path is dead (0 rows) |
| Importance flat | 78% of live memories sit at exactly 0.5; importance is caller-declared, never self-assigned |
| No personal-agent eval | every harness (LongMemEval/LoCoMo/HaluMem) tests public conversational QA, not preference/procedural/decision/freshness recall |

## Strategic themes

1. **LLM-free capture & learning loop** (the write side) — the differentiator.
2. **Personalization-aware ranking** — make captured-but-dead signals load-bearing.
3. **Associative graph activation** — densify edges so spreading activation fires.
4. **Lifecycle & storage health** — utility-driven forgetting that reclaims space.
5. **Measurement for personalization** — a personal-agent eval, or we tune blind.

## Roadmap (prioritized)

Legend: effort S/M/L/XL · impact low/med/high/transformative · all **LLM-free** unless noted.

### Tier 0 — Foundation (do first; gates the tuning work)

- **P0. Personal-agent eval harness** — ✅ **Shipped (2026-07-10).** `TestEvalPersonal`
  (`internal/store/eval_personal.go` + `_test.go`), 9 scenarios / 6 slices, JSON report via
  `GHOST_PERSONAL_EVAL_OUT`. Baseline documented in `docs/eval.md`; already flags the
  freshness-update ranking gap (fresh-outranks-stale 0/1). `TestEvalPersonal*` over a
  live-DB-shaped synthetic corpus, scored LLM-free by key/tag match, in the existing
  JSON report (no new tables). Slices: same-day-fact-recall, preference-recall,
  procedural-recall, decision-recall, freshness-suppression, contradiction-surfacing.
  Touches: `internal/store/eval_personal.go` (new), `eval_test.go`, `docs/eval.md`.

### Tier 1 — Quick wins (S, high value, no approvals needed)

- **Q1. Same-day recall scoring fix** — S, high. Apply `SessionScope` boost as a floor
  or *before* the tier multiplier (not a multiply on a 0.1 base) so a today's fact
  outranks a 60-day summary. Add a regression test. `internal/store/context.go`.
- **Q2. Utility into ranking** — 🟡 **Partial: opt-in shipped (2026-07-10, `GHOST_UTILITY_WEIGHT`).**
  `computeContextScore` now blends `utility_count/access_count × w` (default 0=off); utility-recall
  eval slice + knob test added. Also fixed a pre-existing nondeterministic context tie-break (now
  breaks ties by priority then key). Remaining: search.go re-sort blend + tighten the co-retrieval
  increment (edge.go:493) from +1-for-all to token-overlap-gated.
- **Q3. Storage compaction** — ✅ **Shipped (2026-07-11).** `ghost gc --purge-deleted <age|all>
  [--vacuum]` hard-removes soft-deleted memories + their orphaned chunks/edges/links/files, then
  optionally VACUUMs. Dry-run supported; live memories never touched (retrieval joins MAX(version)).
  Concrete-store method (no Store-interface change). On the live DB copy: **249MB → 164MB** (2,117
  rows + 15,096 chunks reclaimed). `internal/store/sqlite_gc.go`, `internal/cli/gc.go`.
- **Q4. Dedup threshold reconcile + `date:` tag on write** — S, med. Fix 0.82-in-code
  vs 0.92-in-help mismatch, expose `GHOST_DEDUP_THRESHOLD`; auto-stamp
  `date:YYYY-MM-DD` at write to activate the dead `SessionScope` tag half.

### Tier 2 — Core mechanisms (M/L)

- **C1. LLM-free capture path** — ✅ **Shipped (2026-07-10).** `internal/capture` + `ghost
  capture` (two-tier: heuristic default, `--json` LLM tier) + `hooks/ghost-stop-heuristic.sh`.
  New `internal/capture` package:
  `entity.Extract` + `ExtractTopics` for salience, regex intent classifiers
  (preference/correction/decision/gotcha/imperative) ported from shell's
  `write_verify.go`/`recall_verify.go`, Mem0-style ADD/UPDATE/NOOP via embedding-dedup,
  kind inference, mechanical importance = novelty + cue-weight + emphasis. `ghost
  capture` CLI + `ghost-stop-heuristic.sh` hook. Keep `claude -p` as optional tier.
- **C2. Densify the edge graph** — 🟡 **Partial: backfill shipped (2026-07-10).** `ghost
  reflect --relink` exposes the multi-signal linker (cosine OR shared entities OR topic
  overlap) — works even with no embeddings (entity/topic), idempotent, folded into `reflect`
  (no new subcommand / interface change). Fixes the live DB's isolated islands on demand.
  Remaining: promote multi-signal linking into the hot-path `autoLinkEdges` default (carries
  benchmark A/B risk → gate it). — M, high. Promote the entity+topic multi-signal
  linker (dormant in `BenchBuildEdges`, sqlite_crud.go:565) into hot-path
  `autoLinkEdges`; widen candidate window beyond `LIMIT 50`; add `ghost relink`
  (or `reflect --relink`) to backfill. `internal/store/edge.go`.
- **C3. Write-time salience heuristic** — M, high. Mechanical importance scorer in
  Put/capture; promote logging-pattern turns to stm/0.55–0.7, keep chatter at
  sensory/0.3. Expose `PutParams.Salience`. Depends on C1 + P0.
- **C4. Freshness / supersede / contradiction down-weight** — ✅ **DEFAULT-ON (2026-07-11).**
  Shipped opt-in, then A/B on the personal eval showed a net win (meanMRR 0.745→0.790) so
  flipped to default (disable with `GHOST_FRESHNESS=0`). Never touches the Search benchmark path. Put-time supersede detection (`internal/store/freshness.go`):
  change-cue + entity overlap → auto `contradicts` edge (new→old) + importance demotion; closes the
  eval's freshness gap (0→1) with the knob on, default OFF pending a public-suite A/B. Remaining:
  scoring-side staleness penalty + optional bi-temporal `valid_from/valid_to`. — M, high. Auto-create
  contradicts/supersedes edges on Put when near-topic + changed-value or negation
  cues; penalize FROM-side-of-contradicts and older same-topic memories in scoring.
  Optional bi-temporal `valid_from/valid_to` (needs schema ALTER). Depends on C2 + P0.
- **C5. Procedural workflow induction (mechanical AWM)** — ✅ **Shipped (2026-07-11).**
  `internal/procedure` (frequent contiguous-sequence miner with maximal-pattern filtering)
  + `ghost mine-procedures` CLI: reads usage sessions from stdin, stores high-support routines
  as procedural memories with support-scaled importance. Pure counting, no LLM. Remaining:
  auto-feed from transcripts/episodic memories (currently caller-supplied sequences). Offline
  `ghost mine-procedures`: frequent-sequence/n-gram mining over command/tool-call
  sequences → procedural memories with frequency-scaled importance. Depends on C1 + P0.
- **C6. Near-dup content guard on Put** — S/M, med. Cosine-compare new content vs
  current latest version before versioning; bump access instead of churning a 51st copy.

### Tier 3 — Deeper (M, needs decisions)

- **D1. Native recall-coverage check + self-heal + MinScore defaults** — M, med. Port
  `recall_verify` coverage into `Context`; auto-escalate a targeted search on missed
  salient tokens; set sensible shell `MinScore/MinSpread`; raise 38% budget fill.
- **D2. Utility-driven, rehearsal-aware lifecycle (SM-2)** — ✅ **Shipped (2026-07-11).** Added
  `ease` column (schema ALTER, approved); reflect recomputes `ease = 1 + 0.2·utility_count` (cap 4)
  and the stale-LTM demote threshold scales by ease, so proven-useful memories resist idle demotion.
  Tests: shield/cap/dry-run/unit. Motivated by live finding: umbreon 96% / pika 60% dormant from
  clock-based demotion. Per-memory
  ease/interval (schema ALTER); demote on low-utility-AND-stale, not bare 7-day age;
  decay `access_count` over time.
- **D3. Reranker in production** — L, high (open). Largest measured lever (+64% HaluMem
  MRR) is OFF: GO backend ~18.7s/query; fast ORT regresses via sigmoid clipping. Fix
  ORT normalization or run selective GO rerank on low-confidence queries only.

### Explicitly NOT doing

- **SPLADE / ColBERT** — XL effort, low impact. Trades the single-binary/pure-Go
  simplicity that *is* ghost's differentiator for a retrieval ceiling the personal-agent
  use case doesn't need. Ghost already hits R@5 0.908.

## Risks

- Scoring changes risk regressing the strong public numbers — gate every one behind an
  env knob and A/B on **both** the public suite and the new personal eval.
- Densifying edges risks spurious associations — corroborate with entity/topic, not
  cosine alone.
- LLM-free capture has lower fidelity than `claude -p` — ship as default, measure
  precision/recall, keep LLM as opt-in quality tier.
- Destructive compaction on live DBs — dry-run-first, opt-in, backed up.
- Schema ALTERs (bi-temporal, ease) need explicit sign-off per CONVENTIONS.md.

## Open decisions (see conversation)

1. Additive `ALTER ADD COLUMN` approval for (a) bi-temporal `valid_from/valid_to`,
   (b) spaced-repetition `ease/interval`.
2. New CLI subcommands: `ghost capture`, `ghost relink`, `ghost mine-procedures`,
   new `gc` flags — approve the set, or fold some into existing commands.
3. Capture strategy: replace `claude -p` as default, or two-tier (mechanical always-on
   + optional LLM pass)?
4. Dedup threshold: 0.92 (fewer false merges) vs 0.82 (more aggressive)?
5. One-time supervised compaction + relink backfill over the live DB (backed up), or
   forward-only on new data?

---

# Phase 2 — validated next iteration (2026-07-11)

Phase 1 built the LLM-free personal-memory substrate. Phase 2 was pressure-tested against
code + SOTA (workflow `wxvijqxfh`); **all four levers came back CONDITIONAL**. Decisions:
optimize for **both benchmark + live in sequence**, and stay **pure-Go only** (no CGo/ORT,
no vendored forks).

## Sequence

1. **B1 · same-day recall — GO.** Root cause: shell `LogExchange` writes every turn as
   `tier=sensory / importance 0.3`, and sensory is excluded from default search — the fact
   just stated is invisible to the next recall query. Pure-shell fix (reuse Phase-1 capture
   heuristics to emit a distilled higher-tier fact for salient turns; keep chatter at sensory).
   No ghost change, no schema, zero benchmark risk. Bundle **B4** (extend heartbeat
   `RunReflect` to also call `relink`/`mine-procedures`/`gc --purge-deleted`). Measure: shell
   grounded-recall rate over time.
2. **PPR edge-provenance ablation.** Before building PPR, test on the in-house multi-hop slice
   with entity/topic edges only vs cosine-only. Live graph is 29,042/29,138 cosine edges, so
   PPR may just echo dense retrieval. If cosine echoes dominate the lift → **PPR is NO-GO**,
   widen/selective rerank instead. (Reranker logit-diagnostic dropped — its fix needed ORT/fork,
   which pure-Go-only rules out.)
3. **Commit to the winner.** PPR behind `GHOST_PPR` (pure-Go power iteration, off by default) if
   the ablation shows entity-bridge edges carry it; otherwise pure-Go **selective reranking**
   (recalibrate `GHOST_RERANK_ADAPTIVE` so only low-confidence queries pay the ~18s cost).
4. **Bi-temporal validity** behind `GHOST_BITEMPORAL` — additive `valid_from/valid_to` (approved
   ALTER), point-in-time "as of" recall, hard-retire superseded facts (gated; false supersede
   risks Omission). Justify on the personal-agent capability, not HaluMem (n=17, saturated).
   Regression gate: LongMemEval_S R@5 0.908 / MRR 0.827 (this is a Search-path change).
5. **B3 feedback loop.** Grounded-recall → utility bump. Needs first: gate the existing
   direct-hit auto-bump (edge.go) to avoid double-count, and thread memory identity
   (`ContextMemory.ID` or `UtilityIncByKey` — Store-interface change, needs approval).
6. **B2 real-miss eval — DEFERRED** until grounded volume is sufficient (currently ~10 noisy misses).

## Corrections this research surfaced
- The docs' "ORT applies sigmoid" reranker story is **wrong** — hugot applies sigmoid
  unconditionally in both backends; the real issue is float32 tail-collapse tying MaxP maxima.
- PPR is not the slam-dunk it first appeared: a cosine-similarity graph doesn't give HippoRAG's
  entity-bridge win, and benchmarks build edges fresh per-question (the dense live graph ≠ eval graph).
