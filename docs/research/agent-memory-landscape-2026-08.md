# Ghost Landscape Comparison & Eval Roadmap — August 2026

**Date:** 2026-08-08 · **Basis:** verified findings across five research clusters (per-claim verdicts applied; corrected claims used in corrected form, unverifiable specifics dropped) + two in-repo eval audits · **Prior:** `docs/research/agent-memory-landscape-2026-07.md`

---

## 1. Positioning at a glance

| Dimension | ghost | mem0 | Letta | Zep/Graphiti | platform-native (Anthropic/OpenAI/Google) |
|---|---|---|---|---|---|
| **Write discipline** | Verbatim, LLM-free; CAS (BaseVersion) + context-verified diff patch | One LLM extraction call per add; ADD-only since April 2026; dedup is MD5 exact-hash | Agent edits git-backed markdown; replace is a substring check, replace-all footgun; last-write-wins on blocks | Full LLM ingestion pipeline: extraction, entity resolution, edge invalidation per write | Model-initiated file ops (Anthropic) or opaque platform auto-extraction (ChatGPT/Gemini) |
| **Correction handling** | Versioned supersede + contradicts force-include + refutation budget reserve; staleness 6/6 | None in OSS — contradictions accumulate; #4896 closed as not planned | Agent must notice and edit; dreaming pass may consolidate later | LLM invalidation, per-edge validity intervals, stale facts rewritten to past tense | Delete-and-retell (ChatGPT bio can update in place; Gemini: correct-in-chat only) |
| **Graph** | Typed edges, per-relation expansion policy, free in the single binary | Deleted from OSS (~4000 lines), Platform-paywalled; OSS "entity matching" is a search signal, explicitly not a graph substitute | None — filetree hierarchy as structure | The core product: bi-temporal typed knowledge graph with edge_type_map ontology | None |
| **Retrieval quality** | FTS5→vector→RRF→cross-encoder→composite→edge expansion; semantic MRR 0.833, multi-hop 0.54 search-only | 3-signal hybrid (vector/BM25/entity), no reranker, BM25+entities hardcoded English (#4884) | grep + iterative agent search; 74.0% LoCoMo with GPT-4o mini | Same fusion family + 5 pluggable rerankers; read path LLM-free | Flat injection (ChatGPT timestamped list) or agent-initiated file reads (Anthropic); no ranking |
| **Temporal** | Bitemporal shipped but un-evaled; recency-by-kind scoring | Claimed +29.6 temporal (vendor-run, end-to-end) | git history only | Per-edge valid_at/invalid_at; temporal +38.4% on LongMemEval | Timestamps in injected list; no as-of capability |
| **Write-quality gatekeeping** | Structural only — 146/146 mislabelled contradicts edges show the gap | Same LLM is writer and gatekeeper; no verification step | None built-in; developer discipline + optional dreaming | Specialized invalidation/dedup prompts over similarity-narrowed candidates | Opaque extraction filters, nothing published |
| **Eval rigor** | ~26 deterministic suites + LongMemEval_S/LoCoMo+; production-config parity is the named gap (two subsystems shipped dead while suites passed — Section 7 phase 1) | Vendor-run numbers, methodology publicly disputed | One benchmark blog + a sound methodology critique | Published per-type breakdowns including its own regression (94.6→80.4) | None published; Anthropic has a qualitative safety bar only |
| **Cost / latency / privacy** | Free, local, zero LLM calls, one SQLite file | LLM per write; Platform $19–$249/mo, retrieval quotas 10x scarcer than add quotas | Meters active agents + tool-execution seconds — continuous billing for always-on fleets | Graph DB server + LLM per write; hosted sub-200ms claimed | Subscription-bundled; data platform-held (Gemini: reviewed chats retained up to 3 years post-deletion) |

---

## 2. Where ghost leads

- **Staleness handling, measured.** Never-stale-without-fresh 6/6 at all budgets, refutation budget reserve, dormant-refuter resurrection. Memora/FAMA found reuse of invalidated memories endemic across 6 memory agents and 4 LLMs — ghost's strongest property is the field's weakest, though ghost has no externally comparable number yet.
- **Never silently deletes.** mem0's old resolver "quietly issues a DELETE, and the fact is gone from every future search() call" (documented third-party failure). Ghost's soft-delete + locked/pinned + contradicts force-include prevents this class by construction, and it is checkable in evals.
- **Concurrency.** CAS via BaseVersion with typed ErrVersionConflict and proven gapless version chains, vs Letta's documented last-write-wins and its git-merge story that needs an LLM for real conflicts.
- **Edit mechanics.** Ghost patch (unified diff, verbatim context, uniqueness, dry-run, empty-result guard) strictly exceeds Letta's core_memory_replace (substring-exists check, replace-all, no dry-run) — verified against Letta source.
- **Local-first graph.** mem0 paywalled its graph; ghost's typed edges with per-relation expansion stay in the free binary. Graphiti's graph requires a database server plus an LLM API per write.
- **Multilingual.** mem0's BM25 and entity extraction are hardcoded to English spaCy (#4884, silent failure on CJK); ghost runs multilingual MiniLM with a cross-lingual eval suite.
- **Lifecycle hygiene built in.** TTL/GC, tier decay, soft-delete, pinned/locked, path safety — everything on Anthropic's memory-tool "security considerations" wishlist that developers are told to hand-roll, ghost ships as store features.
- **Audit surface.** Every memory inspectable, versioned, CAS-editable. Gemini exposes no individual learned memories at all; ChatGPT's ambient profile is uninspectable (reverse-engineered, GDPR concerns flagged).
- **Extraction hallucination structurally absent.** HaluMem locates the field's dominant error source in LLM extraction/updating (accuracy <62% across all tested systems; correct-update <50%). Ghost stores what the agent hands it verbatim — that failure stage does not exist in the store.
- **Eval rigor.** Deterministic production-config suites vs Cognee's self-admitted 24-question LLM-judged runs mixing vendor-supplied numbers; no platform publishes retrieval-quality benchmarks at all (negative claim: none found).

## 3. Where ghost lags

- **Write-quality curation — the competitive gap.** Ghost validates structure, not meaning: 146/146 contradicts edges mislabelled in production. Graphiti does the same judgment as a specialized ingestion prompt over a similarity-narrowed candidate set; TrustMem trains the update policy against a transition verifier (corruptions −79.1%); SAGE shows the confident dedup/novelty band needs no LLM at all. Ghost currently has no curation assist of any kind — the strategic finding is that ghost already matches Graphiti's LLM-free read path, and the whole gap is write-path curation.
- **Restatement glut.** 871 near-identical pairs on the main store. Graphiti runs a write-time dedup funnel (embedding + full-text + LLM verdict); A-Mem retroactively folds restatements into existing notes; SAGE auto-NOOPs dense-region writes. Ghost's dedup-representative machinery exists but nothing runs at put time.
- **Promotion signal.** 76% of writes reach ltm without ever matching a query; promotion counts injections. "When to Forget" (read in full) proposes outcome-keyed Memory Worth counters — utility, not age/capacity — and its micro-experiment even uses ghost's exact default embedder. TRUSTMEM's placement insight: validation belongs at the promotion/consolidation boundary, not put time.
- **Per-fact temporal validity.** Graphiti expresses "this was true from March to June" per edge; ghost's bitemporal is per-memory versioning and cannot say it. Caller-supplied dates would close this without an LLM.
- **Navigation / progressive disclosure.** Letta keeps the filetree + frontmatter descriptions always in the system prompt; claude.ai gives a categorized, user-editable memory view. Ghost has no cheap always-present overview of what the store contains.
- **Provenance.** MemOS makes provenance a first-class MemCube field; ChatGPT/Claude/Gemini all separate explicit from ambient writes; MINJA shows query-only poisoning works precisely when origin is unrecorded. Ghost records no originating user/chat/agent, so its 76%-junk disease cannot be attributed and a poisoned memory is forensically indistinguishable.
- **Multi-hop.** 0.54 search-only recall. mem0 claims +23.1 multi-hop (vendor-run, not comparable); HippoRAG 2 claims +7% associative. Ghost's answer (curated typed edges reach full recall) depends on the write quality that is currently the bottleneck.
- **Scale evidence.** Largest in-repo eval corpus: 543 memories; the live store's diseases (hairball, glut) are scale pathologies. mem0 and Cognee both report on BEAM at 1M–10M tokens; ghost measures nothing about degradation as the store grows.
- **Ephemeral mode.** All three platforms converged on an incognito surface with no memory write; ghost has no store-level equivalent.
- **Poisoning defenses.** OWASP Agent Memory Guard ships five LLM-free detectors, quarantine dispositions, and integrity baselines (metrics project-claimed, secondary source); ghost has zero of them despite a multi-user Telegram deployment that is exactly MINJA's setting.
- **Conflict resolution as a scored competency.** MemoryAgentBench's ICLR version names the fourth competency Conflict Resolution (not "selective forgetting" as earlier drafts) — which is precisely where ghost's production write path fails, a weaker story than the July framing.

---

## 4. Changed since July

- **mem0's "quiet retreat" became a shipped algorithm.** July inferred retreat from the ADD-only prompt; the April 2026 v3 is now confirmed: single-pass ADD-only, agent facts stored with equal weight, semantic conflict resolution formally abandoned in OSS (#4896 closed as not planned; "Latest truth wins" documented but not implemented), and all external graph drivers deleted (~4000 lines) with graph memory paywalled. OSS gained "entity matching" — a third retrieval signal, explicitly not a graph replacement.
- **Letta abandoned the specialized-memory-tool category.** March 2026 pivot confirmed: memory moves to bash-over-git-backed markdown (MemFS/Context Repositories), sleep-time agents become client-side subagents, legacy memory tools announced for removal (tool rules deprecated, not removed), letta-ai/letta README labeled the legacy server. This is the opposite bet from ghost's MCP surface — and Letta's own rationale (training-distribution tools win) argues for keeping ghost's verbs shaped like familiar primitives, which ghost patch already does.
- **The LoCoMo dispute timeline in the July report was wrong.** Corrected sequence: Zep claimed ~84% → Mem0's paper reported Zep at 65.99% (the number the three harness errors explain: speaker roles, timestamp plumbing, sequential search) → Zep's rerun claims 75.14% → Mem0's counter-issue then re-corrected Zep's own 84% pipeline to 58.44% (adversarial questions in numerator but not denominator). The lesson survives reinforced: 58.44→84 on the same system is pure harness.
- **Category convergence is now explicit.** Both heavyweights store cheaply and let the agent judge — validating ghost's founding bet — but both pair it with an offline LLM consolidation pass (mem0 Platform sells "Dream (Memory Consolidation)"; Letta runs dreaming). The pass didn't disappear; it moved behind the paywall or into subagents. That pass is what ghost's measured write-quality diseases say is missing.
- **Platform-native memory surveyed for the first time.** All three platforms converged on three surfaces (explicit facts / ambient personalization / incognito), and none publishes retrieval benchmarks. Anthropic's memory tool is a pure protocol with a documented injected MEMORY PROTOCOL string; claude.ai's current product is categorized individual entries (the "single summary document" framing from the Sept-2025 rollout is stale).
- **A new write-gate literature appeared.** ConsistencyGate (July 2026, provenance entailment at admission), SAGE (May 2026, vMF density gate — confident ADD/NOOP decisions provably LLM-free), TrustMem (transition verifier + RL-trained update policy, corrected description). SAGE is the most ghost-compatible result of the cycle.
- **Memory security became a named field.** OWASP Agent Memory Guard (June 1, 2026) as the ASI06 reference implementation; MINJA query-only poisoning (defenses discussed in the paper and shown weak — not absent, as first reported); a formal origin-bound-authority proposal (single-author, weigh accordingly).
- **Ghost's own position moved.** Since July: CAS + typed conflicts, ghost patch, per-relation expansion policy, refutation budget reserve, pinned-seed expansion fix (PRs #93–97) — much of the instrumentation the July report asked for is now built; what remains is proving it under production configuration (Section 7).
- **Benchmark map updated.** TEMPO (per-step gold documents, LLM-judge-free — best-matched external benchmark for an LLM-free pipeline), TiMem (52.2% context reduction on LoCoMo via time-bucketed hierarchy), MemConflict (July rejected importing its corpus; the new use — agent-labelled vs oracle-labelled edge A/B — is a different, measurement-only proposal).

---

## 5. Steal list 2026-08

All adaptations keep the LLM-free core: LLM work belongs to the calling agent or env-gated evals, never the retrieval path.

### Tier 1 — attacks a measured disease directly

1. **Put-time candidate surfacing with constrained judgment.** From mem0 (retrieve-top-k-then-decide shape) + Graphiti (invalidation prompt over narrowed candidates). ghost_put returns top-k similar existing memories plus temporally-overlapping contradiction candidates with a structured checklist; the agent answers yes/no per pair instead of free-labelling edges. Constrained binary judgment vs the open-ended labelling that produced 146/146 mislabels. Effort: medium.
2. **vMF density novelty gate.** From SAGE. Score each put against store geometry using ghost's local MiniLM embeddings; auto-NOOP or auto-link confident restatements, return the uncertain band to the calling agent (ghost's substitute for SAGE's LLM merge step). Attacks the 871-pair glut and the 76%-dead-weight simultaneously. Effort: medium.
3. **Utility-keyed lifecycle.** From "When to Forget" (as actually written: outcome-keyed Memory Worth counters) + TRUSTMEM's placement insight (gate at promotion, not put). Promote on matched-query count, not injection count; protect refuters and contradiction targets from decay — would have prevented the dormant-allergy failure. Effort: small–medium.
4. **Write provenance.** From MINJA (threat model), MemOS (MemCube provenance), and the platform explicit/ambient consensus. Record originating user/chat/agent per put plus an explicit-vs-ambient bit; ambient captures require a retrieval hit to reach ltm. Schema change — needs human approval per CONVENTIONS.md. Effort: small.

### Tier 2 — cheap, high-leverage

5. **Caller-supplied valid_at/invalid_at.** From Graphiti. Optional date fields on memories/edges (especially contradicts); the agent supplies dates, same division of labor as edge semantics. Effort: small.
6. **Anthropic error-string discipline + memory-protocol preamble.** From the Anthropic memory tool. Mimic the RL-trained str_replace error strings in ghost_patch failures; ship a recommended protocol preamble (check ghost_context first, record progress, assume interruption) with the MCP server instructions. Effort: small.
7. **Structural edge constraints.** From Graphiti's edge_type_map. Namespace-declarable rules like "contradicts requires overlapping tags / same kind", validated at edge-write time — structural, not semantic, so it fits ghost's contract. Effort: small.
8. **Namespace memory summary + memory tree.** From claude.ai (categorized audit surface) + Letta (filetree always in prompt). A pinned, human-editable per-namespace summary maintained via contains edges, plus a one-line-per-key tree as cheap navigation in context. Effort: medium.
9. **Timestamp-prefixed rendering + past-tense supersede convention.** From ChatGPT ("[date] fact" injection) + Graphiti (fact rewriting). Render dates in assembled context; document the convention that superseding patches rewrite old content to past tense. Zero LLM. Effort: small.
10. **Quarantine + integrity baselines.** From OWASP Agent Memory Guard. A held write state invisible to retrieval until reviewed (soft-delete in reverse); hash pinned/locked memories and alert on unexpected mutation; size-anomaly check complementing the flooding gate. Effort: small–medium.

### Tier 3 — worthwhile, not urgent

11. **Ephemeral session mode.** Platform consensus (all three ship incognito): session-scoped forced-sensory/no-write flag on the MCP surface. Effort: small.
12. **Cascade-invalidate by source conversation.** From Gemini's delete-the-chat model: one command demotes every memory tagged chat:<id> — the cleanest poisoning-recovery story surveyed. Effort: small.
13. **Restatement-cluster surfacing.** From A-Mem's memory evolution, LLM-free: high-similarity auto-links mark a cluster and surface it in curate/edge_candidates as a consolidation prompt. Effort: small.
14. **Transition audit log.** From TrustMem: record (prior state, operation, new state) so a nightly caller-side verifier can score coverage/preservation/faithfulness offline and file curation tasks. Effort: medium.
15. **Deletion contract document.** From Gemini's Privacy Hub (the honest enumeration, not the retention): one page defining soft-delete vs hard-delete vs version purge vs export files vs git history — the household handles family health data and has no such document. Effort: small.
16. **Time-bucketed consolidation + complexity-aware expansion.** From TiMem: day→week→month rollup candidates are deterministic even with caller-written summaries; gate expansion depth on seed-hit count. Effort: medium.
17. **Creator-bound unlock.** From the origin-bound-authority direction (single-author paper — verify before citing): locked overwrite rights tied to recorded creator, not a global --unlock. Effort: small, rides on item 4.
18. **Encryption decorator.** From OpenAI's EncryptedSession: at-rest encryption as a Store-interface wrapper, no SQLiteStore changes. Effort: medium.
19. **Positioning line: "deterministic, never silently deletes."** From the mem0 silent-DELETE story — the anti-pattern ghost's design prevents, checkable in evals. Effort: writing.

Eval-shaped steals (FAMA metric, HaluMem stage split, abstention, TEMPO, MemConflict A/B, iterative-search condition, per-type reporting) are placed in Section 7.

---

## 6. Rejection list

- **Any LLM in the store's write or read path** (mem0 extraction, Graphiti ingestion judgment, ConsistencyGate's K-query gate as-is, TrustMem's trained writer) — the core bet; interfaces are stolen above, mechanisms are not.
- **Git-backed file substrate (Letta MemFS)** — forfeits FTS5/vector/rerank retrieval and budgeted assembly for grep.
- **Small trained memory LLM (mem-agent)** — its weakest measured skill (clarification, 45.5%) is exactly the skill needed; evidence the bottleneck is field-wide, not solved by the small model.
- **LLM-arbitrated ADD/UPDATE/DELETE** — abandoned by both vendors in 2026; the silent-DELETE failure mode is documented.
- **DMR benchmark** — saturated (full-context baseline 98.0% with gpt-4o-mini).
- **Episode-mentions reranker (Graphiti)** — exposure-counting is ghost's measured disease; a frequency reranker subsidizes it.
- **Pluggable backend matrix (Cognee, Graphiti)** — the Kuzu deprecation shows the carrying cost; single-binary SQLite bet holds.
- **PPR / HippoRAG 2 adoption** — measured NO-GO on ghost's corpus; needs an LLM at index and query time. (One cheap derivative kept: a chunk-node vs memory-node granularity eval.)
- **Activation/parametric memory (MemOS scope)** — unreachable from outside the model; ghost is deliberately plaintext.
- **Replacing tiers with the survey taxonomy** — adopt the survey's actual vocabulary (forms/functions; dynamics = formation/evolution/retrieval; "multi-agent memory") in docs only; ghost's kind axis already implements the functions lens.
- **Vendor-supplied numbers in any published comparison** — Cognee's mixed-provenance charts are the negative example; re-run every competitor in the same harness or don't publish.
- **Chat-correction as the only audit surface (Gemini pattern)** — ghost's corrected-pins incident already proved chat-correction without store mutation silently fails.
- **BEAM-scale (10M-token) ambitions** — a regime the household workload never reaches; the scale envelope eval (7.3.3) covers the realistic range.

---

## 7. Eval expansion roadmap

### Phase 1 — production-parity mechanism (prerequisite for trusting anything else)

The twice-institutionalized failure (MinScore floor, dormancy rule: subsystems silently dead while all suites pass) has a root cause: no repo-visible production configuration.

1. **profiles/production.env + `make eval-prod` + drift check.** Check in the exact daemon env block (MIN_SCORE=0.3, RERANKER=local, RERANK_TOP_N=10, UTILITY_WEIGHT=0.5, multilingual L12 embedder), with comments citing the motivating incident per knob; daemon deploy sources it; env-gated test diffs it against the live daemon. Green: eval-prod passes. Red: any suite that only passes floor-less. Effort: small.
2. **Re-run the five core Context() suites (graph, staleness, refute-reserve, summary, relation-stress) under the prod profile, unmodified.** Every headline claim is currently certified under a configuration production never runs. Red criterion: any assertion that passes default but fails prod is a shipped-dead subsystem and blocks merge. Effort: medium.
3. **TestMinScoreEnvInheritance.** The env-inheritance branch production actually executes has near-zero coverage; post-incident suites test an equivalent value, not the code path. Green: env floor applies when param unset; explicit param wins; viaEdge exemption works through the env path; explicit-zero semantics decided and asserted. Effort: small.
4. **Subsystem-liveness canary (eval_liveness_test.go, prod profile only).** One fixture where every pipeline stage is necessary (edge-only-reachable fact, vector-only paraphrase, reranker-only decoy, dormant refuter, utility-lifted memory); assert per-stage contribution counters, not final contents. Green: every stage nonzero. Red: any zero counter regardless of final assertions — the generic detector both incidents argue for. Effort: medium.
5. **Prod-profile benchmark rerun.** LongMemEval_S and LoCoMo+ under TOP_N=10 + multilingual L12 (published numbers used TOP_N=20 and the English embedder; eval.md itself records rank-6–15 evidence loss at TOP_N=10). Add a "best-known config" vs "production config" column to docs/eval.md; ship the exact harness alongside any published number (the Zep/Mem0 dispute lesson). Red: >10% relative drop forces a promote-the-knob-or-document-the-number decision. Effort: large, one-time + cheap sentinel.
6. **Config-sensitivity net.** Deliberately break each key knob in CI and assert the suite catches it — the Cognee 16x-EM-from-tuning phenomenon (partly a metric artifact, but the direction holds) turned into a permanent regression net. Effort: small.

### Phase 2 — highest-disease-tie-in evals

1. **Derived-artifact staleness** (small/high). Arm A: correct a child under a contains-parent whose summary text still asserts v1; probe under parent substitution at tight budget. Arm B: restatement army — k=1..10 near-dup restatements of v1 vs one v2 correction, extending the 6/6 gate as a function of k. Green: no budget tier serves v1-only; gate holds through k=10. Red: any stale-only context — the compaction feature acting as a staleness amplifier, the "corrected pins" mechanism.
2. **Promotion precision** (small/high). Tag seeded memories query-earned vs exposure-only; 30 virtual days under production reflect. Green: ltm exposure-only rate ≤20%, zero query-earned facts pruned. Red: rate near the measured production 76%. This is the forgetting-precision gate FAMA (recall-side) cannot express, and the instrument for Steal 3.
3. **Abstention gating** (small/high). Un-skip HaluMem Memory Boundary (harness already in-repo) and report abstention-rate alongside R@5; plus a ~15-query discriminative absent-answer corpus. Green: boundary ≥ published Mem0/Zep numbers without recall regression; false-surface ≤1/15. Today's 3/5 false-surface shape is red. Directly guards the MinScore behavior that once silently broke, and the false-confidence→junk-write feeder of the 76% disease.
4. **Poisoning eval** (medium/high). Four scripted arms against a clean household-shaped store: fake supersede via the freshness change-cue, restatement flood (displacement curve), reserve capture via mislabelled contradicts/prevents, protected-target attacks. Green: pinned/locked surface truth 100%; a single fake supersede never leaves the false value as the only version in context; reserve junk ≤ 1:1 bound. No LLM anywhere. (MINJA-shaped; nothing in-repo today — zero grep hits.)
5. **Utility-weight matrix** (small/medium). {UTILITY_WEIGHT 0, 0.5} × {MIN_SCORE 0, 0.3} on a staleness fixture (high-utility stale vs low-utility fresh) and a floor-interaction fixture. Green: correction outranks stale in all four cells. Red: any cell where utility flips it — that cell is production today, since utility_count amplifies the corrupt exposure signal.
6. **Daemon-param parity helper** (small/medium). prodContextParams() = {Budget 2000, ExcludePinned true, MinScore unset so the env floor applies}; migrate the core suites; lint-test against inline daemon-shaped params. Guards the pinned-two-assembly-paths false-alarm class.

### Phase 3 — the rest

1. **Temporal state eval** (medium). Exercise the shipped-but-unmeasured bitemporal fields: as-of, before/after, and ordering-evidence probes over facts evolving through dated states. Green: version-selection MRR ≥0.5 with current-truth gates unregressed. Red: freshness down-weighting makes superseded versions unreachable for anchored-past queries — the one case where the staleness cure breaks the correct answer.
2. **Longitudinal answer stability** (small). Record top-1/top-3 per probe per epoch in the existing lifecycle sim; count unforced flips; near-dup arm with/without dedup-representative consolidation. Green: consolidation cuts unforced flips to 0. No external benchmark scores answer churn.
3. **Scale envelope** (large, GHOST_SCALE-gated). Synthetic 100k-memory store matching the production census (dup-cluster sizes, degree distribution, 96% relates_to); measure latency, planted-target recall, reserve integrity in the synthetic hairball, Reflect wall time and decision parity. Red: any semantic divergence from small-corpus behavior. (BEAM's lesson at ghost-realistic scale.)
4. **Namespace isolation invariants** (small, CI-default). Drive every automated mutation path over two namespaces with near-identical bait content; hard-fail on any cross-namespace edge, leaked private marker, cross-namespace dedup representative, or impure consolidation cluster. Design risk today, incident tomorrow — namespace is the household's only privacy boundary.
5. **External adoptions.**
   - **TEMPO** as third external anchor — per-step gold documents score ghost's LLM-free pipeline deterministically, unlike LoCoMo/LongMemEval.
   - **FAMA as a reported metric** in the staleness suite (internal delta only, per the July rejection of cross-system FAMA comparison) — makes ghost's rarest property citable.
   - **HaluMem stage-split schema** for write-path evals: extraction/update/QA scored separately, so "ghost is fine, the writer hallucinated" becomes a verdict.
   - **MemConflict agent-vs-oracle A/B**: run with agent-labelled vs oracle-labelled edges; the gap quantifies how much contradiction machinery is lost to write quality — turns the 146/146 anecdote into a measurement.
   - **LoCoMo event-summarization split** for ghost consolidate — first external evidence for or against the contains hierarchy.
   - **Iterative-search condition** (Letta's argument): score multi-hop with N sequential ghost_search calls — ghost's real deployment mode; may close part of the 0.54 gap with no new machinery.
   - **Incremental-ingestion re-runs** of staleness/dedup suites (MemoryAgentBench's delivery format) — batch loading hides order-dependent diseases, the MinScore-trap class.
   - **Per-question-type reporting** for LongMemEval (Zep's honest-regression practice) — the regression categories are where verbatim storage should structurally beat graph summarization.
   - **Memory-harm eval** (Anthropic's bar, formalized): corrected-then-stale sensitive facts; assert assembled context never reinforces the refuted claim — ghost already found this failure class in production.

---

## 8. Sources

**OSS heavyweights**
- https://mem0.ai/blog/state-of-ai-agent-memory-2026
- https://github.com/mem0ai/mem0/issues/4896
- https://github.com/mem0ai/mem0/issues/4884
- https://docs.mem0.ai/open-source/graph_memory/overview
- https://mem0.ai/pricing
- https://www.letta.com/blog/our-next-phase/
- https://www.letta.com/blog/context-repositories/
- https://docs.letta.com/guides/agents/architectures/sleeptime/
- https://www.letta.com/blog/benchmarking-ai-agent-memory/
- https://docs.letta.com/letta-code/pricing
- https://docs.letta.com/guides/core-concepts/memory/memory-blocks
- https://raw.githubusercontent.com/letta-ai/letta/main/letta/functions/function_sets/base.py
- https://dev.to/mukesh_13/mem0-auto-resolves-memory-conflicts-for-you-until-it-silently-deletes-one-you-still-need-4f4m

**Graph-centric**
- https://blog.getzep.com/beyond-static-knowledge-graphs/
- https://arxiv.org/html/2501.13956v1
- https://help.getzep.com/graphiti/core-concepts/custom-entity-and-edge-types
- https://blog.getzep.com/lies-damn-lies-statistics-is-mem0-really-sota-in-agent-memory/
- https://github.com/getzep/zep-papers/issues/5
- https://github.com/getzep/graphiti
- https://github.com/topoteretes/cognee
- https://www.cognee.ai/blog/deep-dives/ai-memory-evals-0825
- https://www.cognee.ai/blog/deep-dives/ai-memory-tools-evaluation
- https://arxiv.org/abs/2505.24478

**Platform-native**
- https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool
- https://support.claude.com/en/articles/11817273-use-claude-s-chat-search-and-memory-to-build-on-previous-context
- https://claude.com/blog/memory
- https://help.openai.com/en/articles/8590148-memory-faq (verified via official-article excerpts; direct fetch 403s)
- https://embracethered.com/blog/posts/2025/chatgpt-how-does-chat-history-memory-preferences-work/ (point-in-time reverse engineering)
- https://openai.github.io/openai-agents-python/sessions/
- https://support.google.com/gemini/answer/16598469
- https://support.google.com/gemini/answer/13594961

**Research frontier & write discipline**
- https://arxiv.org/abs/2502.14802 (HippoRAG 2)
- https://arxiv.org/abs/2502.12110 (A-Mem)
- https://arxiv.org/abs/2507.03724 (MemOS)
- https://huggingface.co/blog/driaforall/mem-agent
- https://arxiv.org/abs/2511.03506 (HaluMem)
- https://arxiv.org/abs/2507.05257 (MemoryAgentBench; ICLR version per HUST-AI-HYZ repo)
- https://arxiv.org/abs/2410.10813 (LongMemEval)
- https://arxiv.org/abs/2402.17753 (LoCoMo)
- https://arxiv.org/abs/2604.20006 (Memora/FAMA)
- https://arxiv.org/pdf/2601.09523v1 (TEMPO)
- https://arxiv.org/abs/2601.02845 (TiMem)
- https://arxiv.org/html/2606.25161v1 (TrustMem)
- https://arxiv.org/abs/2512.13564 (Dec 2025 survey)
- https://arxiv.org/pdf/2605.20926 (MemConflict)
- https://arxiv.org/html/2504.19413v1 (mem0 paper)
- https://arxiv.org/abs/2607.22962 (ConsistencyGate)
- https://arxiv.org/abs/2605.30711 (SAGE)
- https://arxiv.org/abs/2503.03704 (MINJA)
- https://kiteworks.substack.com/p/ai-agent-memory-poisoning-owasp-defense (secondary; metrics project-claimed)
- https://arxiv.org/pdf/2606.24322 (origin-bound authority; single-author)
- https://arxiv.org/pdf/2604.12007 (When to Forget)