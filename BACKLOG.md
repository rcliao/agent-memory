# Ghost Backlog

## PRIORITY QUEUE (owner-ranked 2026-07-14, by our real use cases)

Use cases driving the ranking: (a) family agents carrying irreplaceable personas,
(b) image-first daily-life conversations on the densest chat, (c) Chinese/CJK
queries, (d) novel-topic chatter over-injecting filler, (e) bounded turn latency.

1. **Identity foundation — REDESIGNED then shipped (PR #37, 2026-07-14).**
   First cut implemented a `layer:charter|personality|lore` subsystem in the
   store; owner challenged it ("good foundation, not new systems per use
   case"), and a framework survey confirmed: identity taxonomies live in the
   caller everywhere (character cards, ElizaOS, CLAUDE.md); the only
   storage-layer guardrail anyone ships is Letta's per-block `read_only`.
   Reworked to two GENERAL properties, no identity subsystem (`locked.go`):
   (a) **pinned contract completed** — pinned was documented decay-exempt but
   leaked: stale-GC, low-utility prune, supersede demotion, merge absorption,
   and version purge could all touch pinned memories; all closed (these were
   bugs, proven by the eval failing on main); (b) **blessed `locked` tag** =
   read-only bit: overwriting a live locked memory requires `PutParams.Unlock`
   (CLI `--unlock`), same lifecycle immunity as pinned even unpinned; TTL
   rejected on both. Boundary rule: ghost = substrate (store/forget/reflect +
   two safety bits enforced absolutely); shell = mind (layer taxonomy as its
   own tags, pin/lock policy, budgets, render+hash, rate limits, promotion
   judgment). Eval: `eval_identity_test.go` — persona-shaped fixture over the
   general contract. Identity evolution preserved by design: deliberate
   versioned writes only, never maintenance side effects (drift-literature
   distinction: authorized trajectory vs compositional drift; MemGhost-class
   attacks on always-loaded memory blocked by `locked`). Shell-side remaining:
   tag its ~35 identity keys (pin+lock charter), grouping/render/hash,
   lore-overflow via `ghost consolidate`.
2. **Vision memory Phase 1 — SHIPPED (shell 073a2e5, 2026-07-14, deployed)**:
   media-notes now write ghost memories (episodic, stm, chat+media+photo tags,
   FileParam refs to archived images) at the bridge capture point; caption
   template densified (subject/scene/visible text/people/event). Photos are
   retrievable through the full memory stack. Monitor via agentic review on
   photo turns; Phases 2–3 (SigLIP cross-modal, EXIF filters) stay queued below.
3. **Lifecycle quality (owner-framed 2026-07-14): ghost's identity contribution
   IS the pipeline.** First monitoring round produced two measured diagnoses
   and two shipped fixes:
   - **Phantom churn FIXED**: no-op tier changes (already-dormant re-demoted
     ~2.7k+1.9k/cycle across agents) no longer fire — live `demoted` dropped
     2738→1. Remaining churn class: dedup re-archiving the same clusters
     (deduped=47/171 every cycle) — same fix shape, queued.
   - **Promotion starvation ROOT-CAUSED + FIXED (spaced access)**: access_count
     counts every context injection (hot rows: 10k–83k accesses, utility ≤1),
     so the low-utility prune (priority 90) executed every promotion candidate
     before the promote rule (priority 50) saw it — promoted=0 in production,
     ~2,497 of pika's 5,218 dormant = prune casualties incl. real behavioral
     memories; explains umbreon's 96%-dormant/62-ltm degenerate shape. Fix:
     lifecycle rules read the access LOG, not the counter — promote needs >=3
     distinct access days (spaced rehearsal), prune spares spaced memories +
     72h grace to earn spacing; no-log rows keep legacy behavior. Proven by
     eval_lifecycle_spaced_test.go (red on main).
   - Still open: (a) shell sends REAL utility feedback (grounded-recall
     telemetry → utility-inc) — the proper signal; (b) selective restore of
     prune casualties (shell data decision, SQL in handoff); (c) cluster
     stability over time as the promotion-proposal signal (TESSERA); (d) shell
     consolidation contract — digests born with `contains` edges; (e) dedup
     churn skip; (f) ACT-R re-match on matured logs.
4. **Store-integrity asks from shell wiring (2026-07-14)**: (a) **version
   handshake** — a stale ghost binary bypassed lock enforcement (library-level)
   against a newer-contract DB; store a contract version (user_version), old
   binaries refuse writes when DB is newer. (b) **Gate RemoveTag/RenameTag** on
   `locked` (tag removal currently un-gated — the one bypass left). (c) FTS
   trigger migration SHIPPED (see below).
5. **#7 vector-path flooding**: embedded flooding eval scenario first, then the
   query-relative/hybrid gate tuned against it. Confirmed on live traffic
   nightly; damage bounded (extra injected filler).
6. **#4 compaction_suggested threshold** — cheap fix, kills the ~183/day reflect
   storm pressure.
7. **#9/#10 query quality** (shell-side distilled chat-arm query; distinctiveness-
   weighted terms) — eval case required before tuning.
8. **Vision Phases 2–3** (SigLIP cross-modal, EXIF filters) — after identity; gated
   on darwin/arm64 hugot ImageMode smoke test and whether Phase-1 captions prove
   insufficient in agentic reviews.
9. Ops hygiene when convenient: #5 namespace validation, #6 soft-delete purge.

Passive: rerank quality canary runs until ~7/17 (recall_grounded before/after);
ACT-R re-match once access logs mature; multi-hop research thread.

---

Ranked by user-facing impact. Sourced from the shell evolve-loop's production
review of ghost (handoff 2026-07-13, `~/.shell/evolve-reviews/`) plus this
repo's own measured findings. Statuses: `open | in-progress | done`.

| # | Item | Status | Notes |
|---|------|--------|-------|
| 1 | **Make the reranker affordable before re-enabling in shell.** Re-entry criteria (REVISED 2026-07-13 after second rollback): **<2s per query ON THE LARGEST CHAT** (the primary user's densest chat pool — months of daily-life memories — is both the worst case and the main workload; average-chat benchmarks hide exactly that user) AND **a demonstrated `recall_grounded` lift**. | in-progress (rolled back ×2) | First re-entry attempt: adaptive skip fixed (scale-free RRF ratio) + window 10 measured 1.6s/1.9s on broad-namespace queries — but live turns ran **6–8.5s** (a sparse DM ran 0.6s vs 8s on the densest chat, identical config). Refined diagnosis from shell rollback: TOP_N caps what the reranker *returns*; the cost lives in *scoring*, which scales with the candidate pool the chat surfaces. Fix direction: **cap candidates before rerank — vector-cut to ~30, rerank those**. CORRECTED flow analysis: InjectContext makes exactly 2 concurrent Context calls (chat-scoped + cross-chat) on the modern profile path — the sparse-vs-dense gap is window FILL + chunk depth, not call count. KEY BUG FOUND: the 3s fetch budget is porous — rerankMaxP never checks ctx.Err() between cross-encoder calls, so cancellation cannot stop an in-flight rerank (8s turns with zero degraded warnings). (a)+(b) SHIPPED 2026-07-13: rerankMaxP scores per-doc with ctx checks between (partial head reranked, tail keeps fused order); TestLatencyBenchLargestChat replicates shell's 2-concurrent-call turn against the real chat pool. Measured on the densest chat, reranker on: budget=3s → turns 3.5-3.8s (was 8s unbounded); budget=1.5s → 2.1s; overshoot bounded ≤1s (one in-flight inference per fetch). Eval battery byte-identical. **RE-ENTRY #3 LIVE (2026-07-13 15:45)** under a REVISED criterion from the owner: bounded worst case beats raw speed — occasional ~4s (even 8s) is acceptable; the failure class is unbounded/minutes-long stalls, which the deadline fix eliminates structurally. Config: reranker on, window 10, 3s fetch budget → worst turn ≈ 4s hard cap, top 4-7 injected docs cross-encoder-cleaned. First live datapoint: heartbeat inject 5.8s→3.3s. Monitoring at ≥5s (deadline-failure signal). Optional quality follow-ups: (c) pre-rerank vector cut, (d) shell skips rerank on heartbeat/synthetic turns (also stops access-log/edge pollution), (e) config-driven fetch budget (const 3s today) for fuller coverage where tolerated. |
| 2 | **V2-H6: embeddings as BLOBs** — every vector query JSON-decodes ~58k float arrays from a 551MB DB. | **done** (2026-07-13) | Version-prefixed float32 blobs in the same column (no schema change — SQLite stores BLOBs as-is under TEXT affinity); decoder reads both formats; one-time in-place migration. With the two-phase FTS fix: production Search p50 836ms→**218ms**, p95 271ms — under the 300ms target. All eval gates byte-identical. |
| 3 | **ANN index** — vector retrieval is a full namespace scan per query. | deferred | BLOBs made the 58k-vector scan cheap enough (~150ms of a 218ms p50); revisit at ~200k+ chunks. |
| 4 | **V2-H7: `compaction_suggested` always true** — the `>500 active memories` namespace threshold fires permanently on real DBs (6k+ memories), driving the shell's ~183/day reflect storm. Make the threshold proportional, rate-limited, or state-aware (suggest only when reflect would actually change something). | open | `internal/store/context.go` (countActiveMemories check). Cheap fix, large operational win. |
| 5 | **Namespace validation on put** — reject or warn on unknown/never-seen namespaces. The dead-pin drift stranded a critical behavioral rule for a week because a pinned memory sat in a namespace nothing reads. | open | Warn-by-default (hard-reject breaks bulk import); `--strict-ns` or config allowlist for enforcement. |
| 6 | **V2-H5: purge 7.6k soft-deleted rows** (~98MB of dead weight scanned by every query on the live DB). | open | `ghost gc --purge-deleted all --vacuum` exists — this is an ops runbook item plus possibly an auto-purge age default in reflect/gc. |
| 7 | **Ghost half of recall relevance** — umbreonmini's 31% `inject_irrelevant`: context injection surfaces memories the turn doesn't need. Levers: `GHOST_MIN_SCORE` floor (shipped, deployed), MinSpread flat-noise cut, and per-turn query quality (shell-side). | open | Measure via shell's owner-eval before/after the 2026-07-12 deploy (MinScore=0.3 + reranker may already have moved this). |
| 8 | **Provenance-aware retrieval (H26)** — shell now reliably writes `chat:<id>` tags on every memory; add a retrieval-side audience filter (exclude/boost by chat scope) to complete the privacy-scoping story. | open | Tag filtering exists in Search/Context; what's missing is a *negative* scope (exclude other chats' private memories by default when a chat scope is active). Design needed: opt-in flag vs profile config. |

## Nurture eval — agent-upbringing e2e framework (SHIPPED 2026-07-14)
Owner-designed frame: onboard a newborn agent (charter/personality/lore seeds),
run a scripted "conversation kit" through weeks of compressed life (injectable
clock, capture distillation, retrieval probes = daily rehearsal, nightly
reflect), assert the memory GROWS UP right. Four properties in
`eval_nurture{,_test}.go`: IdentityStability (byte-identical after 6 weeks),
EarnedPermanence (spaced rehearsal → ltm), GracefulForgetting (one-offs decay,
sensory dies), CorrectionWins (day-14 correction outranks the stale fact).
All green on the current stack — and the first run already caught two things:
- **Supersede entity gap — FIXED (2026-07-15, CORRECT disposition)**:
  detectSupersede gains CJK change cues (其實是/而不是/記錯/搞錯/改成/換成…),
  a distinctive-term-overlap fallback for common-noun facts (entities count
  double, tokens are the floor), sensory-tier exclusion, and RECONSOLIDATION —
  the correction inherits the stale fact's importance before demoting it, so
  an unrehearsed correction can't rank below the rehearsed fact it replaces.
  TestEvalNurtureSupersedeFired flipped from logged diagnostic to assertion;
  TestEvalSupersedeCJKCorrection encodes the real raspberry/cranberry case.
- **Unrehearsed corrections fade below the score floor within ~4 weeks** on
  the FTS-only path (paraphrase queries can't reach them; term queries can).
  The embedded/reranked variant of this kit (GHOST_PERSONAL_EMBED pattern)
  is the eval to build before tuning correction-memory ranking (#12).
**Ablation harness added (2026-07-14)** — same childhood, machinery removed,
measured deltas under 4-noise-exchanges/day pressure (TestEvalNurtureAblation):
| condition | probes | promoted | live pop (wk1→6) |
|---|---|---|---|
| full | 6/6 | 2 | 6→7 (bounded) |
| no-reflect | 6/6 | 0 | 39→153 (linear hoarding, 146 sensory) |
| no-distill | 0/6 | 0 | 3 (nothing recallable) |
Measured story: distillation IS recallability (write path); reflect IS
evolution + hygiene (promotion only exists with it; population stays bounded
instead of growing linearly) and costs zero recall on this kit. Identity
intact under every condition. Honest caveat: at this kit size retrieval
survives hoarding (no-reflect still 6/6) — reflect's recall value should
emerge at production scale/noise; scale-up + noise-with-intent (fake
preferences that capture distills, pressuring stm not just sensory) are the
next knobs.

**Parenting kit added (2026-07-14)** — the shaping loop, measured
(TestEvalNurtureParenting, 7 weeks, 6 noise/day with every 3rd phrased so
capture STORES it): (1) **behavior change** = repeated correction → the
conduct rule is in context when the situation recurs (6/6 probes; the store's
whole contribution to behavior — the model does the rest); (2) **personality
development** = spaced encouragement earns ltm (promoted=6), one-offs don't;
(3) **transience** = "just for today" instruction never becomes a trait (no
late-context leak, never ltm); (4) **signal over noise** = stored noise: 0 in
ltm, 0 probe contaminations, live noise below live signal. Findings: (a)
capture's intent patterns are the write-path gate — phrasings outside them
("My preference stays the same...") distill nothing; production corrections
also arrive in many forms → capture pattern coverage is a measurable axis;
(b) **restatement splits rehearsal**: the same rule in different words = N
sibling memories, each with too little spaced access to promote alone —
semantic aggregation (embedded dedup/clusters) is what should reunify them;
the embedded kit variant will measure that.

Phase 2 (queued): LLM-at-edge parent-agent harness — a real model plays the
parent running onboarding + freeform growth conversations against a live shell
agent, judged on behavior (does Nova ACT like Nova at week 6?); reuses these
same four assertions as the rubric. Shell-side, cost-gated like e2e_bench.

## Write path — "the soul's ear" workstream (opened 2026-07-14, owner-directed)
The nurture evals proved the write path is the narrowest gate: what capture
misses never exists downstream. Eval-first plan, measurement SHIPPED:
- **Capture-coverage corpus (in repo, TestEvalCaptureCoverage)**: measured
  baseline **14/29 (48%)** — English 14/23 (~61%), **Chinese 0/6**: the
  primary user's language never matches intent cues (all English word-boundary
  regexes); CJK capture rides only on named entities. Chatter false-positive
  guard = hard zero (EN+CJK). Floors lock in gains; gap flags flip as fixed.
- **Pattern widening SHIPPED (2026-07-14)**: corpus 14/29 → **29/29** — CJK
  alternations on every classifier (比較喜歡/以後/不對/過敏/決定/記得/不要再…),
  English gaps closed (I'd rather / my preference / please always / going
  forward / that's not right / let's go with / never share / don't send),
  new `boundary` intent for standing instructions (semantic kind), fact
  pattern widened to multi-word subjects. Chatter FPs still zero (EN+CJK).
  Deployed to both daemons — mami's Chinese preferences/corrections/allergies
  now distill on every turn.
- **Triage v1 REINFORCE SHIPPED (2026-07-14)**: Dedup-enabled puts that
  paraphrase a live memory (exact / cosine>=0.82 / strict bidirectional
  distinctive-term overlap with stopword filtering in LLM-free mode) now
  STRENGTHEN it — rehearsal access logged (spaced-promotion fuel) +
  importance max-merge — instead of creating a sibling. Sensory tier
  excluded as a target (nurture kits caught distillates being swallowed by
  their own raw exchange — false-merge class fixed same hour). Parenting
  kit: siblings collapse (live 14->12), promotion intact. Remaining triage
  dispositions (CORRECT with standing inheritance, REFINE) still open:
  classify each incoming candidate against the live store into
  REINFORCE (high distinctive-term/cosine overlap, same polarity → rehearsal
  access + importance nudge on the EXISTING memory instead of a sibling —
  makes repetition aggregate into spaced promotion by construction) /
  CORRECT (subject overlap + negation/change-cue → supersede, new memory
  inherits old standing) / REFINE (moderate overlap + new terms → version or
  refines edge) / NEW / DISCARD. Signals all deterministic: IDF term overlap,
  entity overlap, cosine when embedded, capture intent class, change-cue re.
  Eval: triage-disposition corpus (pairs → expected disposition) + parenting
  kit post-fix (sibling collapse; promotion via restatement without probe
  tuning).
- Later: shell tier-1 [remember:] side-channel (Candidate JSON via the
  existing `ghost capture --json` commit path — media-note pattern
  generalized); provenance/trust tags on write (MemGhost write-side defense);
  correction-class durability (with supersede fallback).

## Behavior render bench (2026-07-15) — memory→behavior measured ghost-side
Owner question: rules in memory visibly don't change behavior (browser-tool
rule ignored in production). Before shell builds anything, ghost's
TestBehaviorRenderBench (env-gated GHOST_BEHAVIOR_BENCH=1; mechanical
compliance grading — length/price/secrecy/format; haiku, n=12/condition)
measured three renderings of the SAME store:
| condition | compliance |
|---|---|
| flat pinned wall (today) | 7/12 — short-answers rule obeyed 0/4 |
| imperative "Standing rules" section | **10/12** |
| section + per-turn trigger reminder | 8/12 (no gain; one "violation" was a clarifying question — grader N/A case) |
**Verdict**: ship the standing-rules render shell-side (cheap, +25pt);
DEFER the trigger machinery (no measured gain at this scale). Grader
refinements before finer claims: length-threshold sensitivity,
clarifying-question = N/A. Phase-2 nurture rubric seeds from this bench
(same rules, mechanical grading, grow samples / swap models). Nurture
Phase-1 addition still worthwhile: RuleSalience metrics (rank/position of
matching rule at probe time).

## Retrieval-quality findings (2026-07-13/14 live-query reviews)
- **Graded relevance + relevance gate SHIPPED (eval-first)**: the flat "any FTS match = 0.5 relevance" let recency float topically-empty filler over the floor. New: term-match-graded relevance (CJK-aware) + a gate that scales the composite down when relevance <0.45 — recency/importance are tie-breakers among relevant candidates, not substitutes. Proven by the new `flooding` eval slice: pre-fix FAIL (4 injected for a novel topic), post-fix 14/14. Side-win: freshness-update slice 0.14→**1.00** (fresh fact finally rank-0); personal meanMRR 0.790→**0.821**.
- **NEXT EVAL CASE (defined by the live review, not yet modeled)**: vector-path flooding — live memories carry embeddings, and weak-but-real cosine (~0.3–0.45 semantic adjacency: meal memos vs a supplement question) bypasses the term-grading path. The FTS-only eval cannot see this; the embedded personal-eval variant (GHOST_PERSONAL_EMBED=1) needs a flooding scenario, then the gate curve gets tuned against it. This is the eval-catches-next-improvement loop working as designed.
- **MinSpread flat-noise filter: measured NO-GO as deployed default** — flat distributions are ambiguous on this workload (equally-relevant meal-memo walls score as flat as filler walls; at 0.15 it trimmed the best query and missed the worst). `GHOST_MIN_SPREAD` exists as an off-by-default knob.
- **CJK FTS gap (real, measured)**: unicode61 tokenizes contiguous CJK runs as single tokens — 拖鞋 present in 7 chunks matches 0 in FTS (only punctuation-delimited terms like 鴻禧菇 index standalone, 185/197). Chinese recall currently rides the LIKE + vector channels; LIKE has no term scoring (created_at order only). **Next measured experiment: trigram-tokenized FTS** (index size + English-behavior tradeoffs need the full eval battery + latency bench).
- Chat-scoped arm floods topic-divergent queries with the chat's dominant content class (meal memos); cross arm carries the topical answers. Structural improvement candidates: per-arm retrieval policy in shell, or topic-gate on the chat arm.

## NEXT UP: Identity layers — charter / personality / lore as first-class memory (handoff 2026-07-13)
Source: `~/.shell/evolve-reviews/ghost-identity-handoff-20260713.md` (shell V2-H42 "identity constitution" needs ghost primitives). Agents keep whole personas as one undifferentiated pinned blob (~20 keys / ~3.6k tokens and growing, no mutation rules). Six asks, priority order:

1. **Layer designation** — `layer:charter|personality|lore` (blessed tag or field) respected by retrieval, reflect, and render; one-time curation pass migrates existing identity-tagged keys.
2. **Per-layer mutation policy enforced in the store**: charter writes require an explicit override flag and are immune to reflect/consolidate/dedupe; personality always versioned with version history + one-call revert (+ rate-limit metadata); lore unrestricted but budgeted.
3. **Pinned-snapshot token budget with consolidate-on-overflow**: charter+personality always render; only lore competes; overflow consolidates (summary + contains edges — the shipped redesign machinery) instead of growing or dropping.
4. **Stable per-layer render + hash** (API/CLI) so shell can diff L0 across generations (`identity_stability` eval dimension) and attribute prompt-fingerprint changes to a layer.
5. **Promotion proposals from consolidation** — recurring lore patterns emit a PROPOSAL artifact shell can poll; never auto-promote to personality.
6. **Identity key versions never GC'd/pruned** — version history is the rollback store.

Constraints: LLM-free; validate against halumem/longmemeval/e2e + the in-repo battery; do not break `memory.SystemPrompt` frozen-per-generation snapshot contract; two separate agent DBs; keep deadline-aware rerank intact (quality canary until ~7/17). Definition-of-done in the handoff file (charter write w/o flag fails; zero automatic charter/personality mutations in reflect logs; budgeted render with expandable overflow summary; stable layer hashes; ≥1 real promotion proposal within a week).

Note the design synergy: (3) is packing substitution applied to the pinned snapshot; (5) is the reflect/cluster machinery emitting artifacts; (2)/(6) extend the supersede/versioning model. Most primitives shipped this weekend — this workstream is largely composition + policy enforcement.

## QUEUED: Vision memory — photos as first-class memories (design reviewed 2026-07-14)
Owner use case: image-first conversations (~60 image-referencing replies/month, multi-day photo troubleshooting threads); today photo content survives only as whatever text the agent happened to write ("(photo)" exchanges). Shell V2-H19 increments 1–3 already shipped (archive + ledger + [media-note] descriptions + Channel B) — **the missing link is ghost-side**.

**Phase 1 — connect the pipes (small, high value, no new models):** shell `ghost_put`s each media-note as a memory (kind episodic, chat tag, `FileRef` → archived photo path — FileRef already exists in the model) with a documented dense-caption template (subject, scene, text-in-image, people-as-named, event context) + EXIF timestamp. The VLM already sees the image at turn time — captions cost zero extra inference. Photos instantly become searchable through the full tuned stack (graded relevance, rerank, CJK). This is Mem0's production pattern with a better caption author. Research: captions WIN for personal-event recall (Visual Lifelog Retrieval, arXiv 2510.04010).

**Phase 2 — true cross-modal (ghost, ahead of the field):** SigLIP2-base-patch16-224 q8 ONNX (vision+text towers, dim 768, ~100ms/image CPU ingest) via hugot's `WithImageMode()` (already in our hugot version). Image vectors in the existing blob codec **with a new version byte tagging the embedding space** (0x02 = siglip — cross-space cosine silently corrupts otherwise); query-time: SigLIP text tower (NOT MiniLM — different space) → fourth retrieval channel fused via RRF. Nobody in the Letta/Mem0/Zep class does local cross-modal today. Research: embeddings win visual queries (~72% R@10, PhotoBench arXiv 2603.01493) but collapse on metadata queries — hence:

**Phase 3 — structured filters + rejection:** EXIF time/GPS as query filters (PhotoBench: pure embeddings lose 50–60pts there), calibrated no-result floor (users misremember), thumbnails (~256px WebP, in-DB blob <30KB) for context display + original-loss survival. Privacy: originals stay outside the DB; `ghost export` ships refs+captions+thumbs only by default; GPS excluded from default context render.

**Risks (from research):** hugot ImageMode needs a darwin/arm64 smoke test before committing; SigLIP sigmoid scores need calibration (different scale from cosine); pin fixed-res checkpoint (NaFlex variant does not export to ONNX). **Eval:** PhotoBench as the external bench candidate; photo-recall cases in the personal eval + the agentic review ("that thing I photographed last month").

Ordering: Phase 1 can ship independently and immediately (mostly shell + a caption template); Phases 2–3 queue behind identity layers.

## Review-findings disposition (2026-07-14, owner-directed)
- **#8 legacy digest class — DONE**: all remaining edge-less auto/exchange-summaries demoted to dormant (44 more on the primary store; class now fully archived). Verified: the collagen-thread query's summary wall replaced by the SR-collagen bulls-eye + live thread exchanges.
- **#7 vector-path flooding — TUNING INSIGHT (measured)**: absolute cosine CANNOT separate filler from hits in this space — novel-topic filler 0.48–0.62, real topical hits 0.60–0.63 (overlap), real social hits 0.27–0.41 (BELOW filler). Any fixed threshold kills good answers before bad. The gate must be query-relative (distance from the query's own distribution) or hybrid (cosine AND term evidence). Build the embedded flooding eval scenario first, tune against it. Monitoring meanwhile.
- **#9 chat-arm query construction — shell design item**: send a distilled query (salient terms / current topic) for the chat-scoped fetch instead of the raw hedged message; owner flagged as interesting. Shell-side.
- **#10 long-query dilution — design note**: weight query terms by distinctiveness (document-frequency over the namespace — rare terms like 膠原蛋白 dominate hedges like 不太確定). LLM-free, needs an eval case first.
- **#11 recency contamination — RESOLVED**: the measured case (smart-home cluster on a coffee question) now injects zero contaminants under gate+rerank. Residual same-thread recency flavor is acceptable current-context behavior. Off the list.

## Summary-node redesign SHIPPED (2026-07-14)
The four-rule redesign is live: (1) children are first-class — unconditional
parent→child suppression removed; (2) **liveness-scaled retrieval rights** —
direct-matched summaries score ×(1−activeChildren/total): defer while children
live, inherit searchability as they age out (replaces the flat 0.25);
(3) **packing substitution** — under budget pressure, 3+ co-retrieved children
of one parent are replaced by the parent carrying `summary_of` drill-down keys
(`ghost expand` recovers detail) — compression proportional to pressure;
(4) fan-out caps keep archive digests out of all of it. Proven by
TestEvalPackingSubstitution (budget-shrink: roomy=specifics, tight=summary+keys)
and TestEvalSummaryLivenessRights (dormant children → summary retrievable).
Remaining shell-side: consolidation must go through ghost consolidate so
digests are born with contains edges (the legacy edge-less exchange-summaries
stay governed only by the relevance gate until then).

## Recently completed (2026-07-12 session, PRs #18–24)
Bi-temporal validity · deterministic evals (injectable clock) · reranker unlock
+ temporal window anchoring · expand-then-rerank · lifecycle eval (FAMA) +
supersede guard + change-cue fix · MemoryAgentBench CR harness · entity-bridge
hop2 (measured NO-GO) · porter stemming · CI on both repos. Measured state:
personal 0.861/0.917, MAB sh_6k 0.99 hit@5 (emb+rerank), LongMemEval_S
0.9083/0.8277 (default path).

## Standing research threads
- Compositional multi-hop (~0.22 hit@5 on MAB in every config): needs
  ingest-time relation extraction feeding the edge graph, or iterative
  retrieval. The one precisely-scoped open retrieval problem.
- ACT-R re-match once live access logs (shipping since 2026-07-12) accumulate
  a few weeks of history.
- Write-path eval (HaluMem operation-level) and poisoning-resistance eval —
  next eval additions from the 2026-07-12 benchmark research.
