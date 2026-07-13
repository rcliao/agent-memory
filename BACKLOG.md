# Ghost Backlog

Ranked by user-facing impact. Sourced from the shell evolve-loop's production
review of ghost (handoff 2026-07-13, `~/.shell/evolve-reviews/`) plus this
repo's own measured findings. Statuses: `open | in-progress | done`.

| # | Item | Status | Notes |
|---|------|--------|-------|
| 1 | **Make the reranker affordable before re-enabling in shell.** Re-entry criteria (REVISED 2026-07-13 after second rollback): **<2s per query ON THE LARGEST CHAT** (mami's chat-scoped pool — months of dense meal/health memories — is the worst case AND the primary user; average-chat benchmarks hide exactly her) AND **a demonstrated `recall_grounded` lift**. | in-progress (rolled back ×2) | First re-entry attempt: adaptive skip fixed (scale-free RRF ratio) + window 10 measured 1.6s/1.9s on broad-namespace queries — but live turns ran **6–8.5s** (papi's DM 0.6s vs mami 8s on identical config). Refined diagnosis from shell rollback: TOP_N caps what the reranker *returns*; the cost lives in *scoring*, which scales with the candidate pool the chat surfaces. Fix direction: **cap candidates before rerank — vector-cut to ~30, rerank those**. Also in play: shell's InjectContext makes 4 Context calls (2 unbudgeted); heartbeat turns pay full rerank. Ghost owes: (a) a largest-chat benchmark mode (chat-scoped Context against mami-shaped pools, not broad-ns averages), (b) the pre-rerank vector cut, (c) verify scoring cost is truly window-bounded in every path. |
| 2 | **V2-H6: embeddings as BLOBs** — every vector query JSON-decodes ~58k float arrays from a 551MB DB. | **done** (2026-07-13) | Version-prefixed float32 blobs in the same column (no schema change — SQLite stores BLOBs as-is under TEXT affinity); decoder reads both formats; one-time in-place migration. With the two-phase FTS fix: production Search p50 836ms→**218ms**, p95 271ms — under the 300ms target. All eval gates byte-identical. |
| 3 | **ANN index** — vector retrieval is a full namespace scan per query. | deferred | BLOBs made the 58k-vector scan cheap enough (~150ms of a 218ms p50); revisit at ~200k+ chunks. |
| 4 | **V2-H7: `compaction_suggested` always true** — the `>500 active memories` namespace threshold fires permanently on real DBs (6k+ memories), driving the shell's ~183/day reflect storm. Make the threshold proportional, rate-limited, or state-aware (suggest only when reflect would actually change something). | open | `internal/store/context.go` (countActiveMemories check). Cheap fix, large operational win. |
| 5 | **Namespace validation on put** — reject or warn on unknown/never-seen namespaces. The dead-pin drift stranded a critical behavioral rule for a week because a pinned memory sat in a namespace nothing reads. | open | Warn-by-default (hard-reject breaks bulk import); `--strict-ns` or config allowlist for enforcement. |
| 6 | **V2-H5: purge 7.6k soft-deleted rows** (~98MB of dead weight scanned by every query on the live DB). | open | `ghost gc --purge-deleted all --vacuum` exists — this is an ops runbook item plus possibly an auto-purge age default in reflect/gc. |
| 7 | **Ghost half of recall relevance** — umbreonmini's 31% `inject_irrelevant`: context injection surfaces memories the turn doesn't need. Levers: `GHOST_MIN_SCORE` floor (shipped, deployed), MinSpread flat-noise cut, and per-turn query quality (shell-side). | open | Measure via shell's owner-eval before/after the 2026-07-12 deploy (MinScore=0.3 + reranker may already have moved this). |
| 8 | **Provenance-aware retrieval (H26)** — shell now reliably writes `chat:<id>` tags on every memory; add a retrieval-side audience filter (exclude/boost by chat scope) to complete the privacy-scoping story. | open | Tag filtering exists in Search/Context; what's missing is a *negative* scope (exclude other chats' private memories by default when a chat scope is active). Design needed: opt-in flag vs profile config. |

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
