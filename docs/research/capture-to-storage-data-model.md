# Capture → storage: the data model, and where identity dies

2026-08-08. Companion to `provenance-first-class.md`, which set the position;
this is the code-level survey (shell + ghost) of every write path — what each
one KNOWS at capture time versus what it STORES — so the provenance wiring can
be designed against the real pipeline instead of an imagined one.

## The one-sentence finding

Shell resolves every speaker to a configured identity at the edge
(`user_labels`: Telegram user ID → display label, `senderLabel()` in
`internal/telegram/handler.go`) and then **loses it at the write boundary**:
the majority write path is `LogExchange(ctx, chatID, userMsg, response)` —
no sender parameter — and the stored content begins with the literal role
word `"User:"`. Identity survives only as prompt text (the `[From: ...]`
tag), which is exactly the wrong place: visible to the model, invisible to
the store.

## Write-path inventory

Shell (`internal/memory/memory.go` unless noted), in rough volume order:

| path | trigger | knows at capture | writes today | correct provenance | missing |
|---|---|---|---|---|---|
| `LogExchange` | every interactive turn | chatID, user text, reply; sender resolvable at bridge | `exchange-<ts>`, episodic, sensory, `chat:` tag | `source_user=<sender>`, `stated` | sender param end-to-end |
| `capture.Capture()` (ghost pkg, called by LogExchange) | same turn | full text; has `SpeakerFilter` already | distilled candidates, own tags | inherit from the turn: `<sender>`/`stated` | provenance fields on `Candidate` |
| `SummarizeExchanges` (B1 distiller + consolidate) | session rotation | the exchanges it summarizes (mixed speakers) | session summaries + `contains` parents, semantic | `observed` when about a person, `self` for ops; `source_user` only when one speaker dominates | derived-provenance rule |
| `Remember` / `RememberMedia` | user says "remember this" | chatID; sender resolvable | semantic memory | `<sender>`/`stated` — the purest case | sender param |
| `ParseMemoryDirectives` / `StoreDirectiveTagged` | agent reply contains a directive | agent authored it, about the conversation | tagged memory | `self`, or `observed` + `source_user` when it records a person-fact | split rule + sender |
| `StoreHeartbeatLearning` / `StoreBehavioralLearning` / `LogHygieneOutcome` / `StoreIdentity` | heartbeat, self-audit | agent-authored | learning rows | `self` | one-line change each |
| `StoreReviewerLearning` | evolve reviewer | external process | learning row | `peer` (or `self`; decide once) | one-line change |
| `CorrectMemory` | correction flow | rewrites whole value | full `Put` | should carry origin | migrate to `PatchMemory` — carry-over + CAS + drift-free for free |
| `SeedCapability` / `SeedNamespace` | provisioning | operator action | seeds | empty (unknown) or `self` | decide once |
| RPC `POST /memory` (`internal/rpc/server.go`, backs `shell-remember`) | agent skill call | action, tags (incl. `via:<agent>` on sync imports) | memory | `via:` present ⇒ `peer` + originating agent; else agent-declared | `source_user`/`source_kind` on `MemoryRequest` + skill flags |
| MCP `ghost_put` | agent tool call | whatever the agent knows | agent-set fields | agent-declared per #113 guidance | shipped; adoption is the census |
| `topic/registry.go` | sticky-pointer machinery | mechanical | topic rows | `self` | one-line change |

Ghost-side surfaces are ready: `PutParams.SourceUser/SourceKind` (#113),
`PatchMemory` carry-over, `List` filter. The gap is entirely in shell's
plumbing — which the position doc predicted: mechanical writers are the
majority of writes, and they are where first-class is won or lost.

## The design: thread the label, not the text

1. **One new parameter, threaded once.** The bridge already computes
   `senderLabel(msg.From)` for the `[From:]` tag. Pass that resolved label
   (plus, where useful, the stable Telegram user ID) into the memory calls a
   turn makes: `LogExchange(ctx, chatID, sender, userMsg, response)`,
   `Remember(ctx, chatID, sender, content)`. No new identity system — the
   `user_labels` map IS the identity system; this only stops discarding its
   output.
2. **`capture.Candidate` gains `SourceUser`/`SourceKind`** (ghost, small):
   candidates distilled from the user's own lines (`SpeakerFilter` already
   exists for exactly this split) are `<sender>`/`stated`; agent-line
   candidates are `self`.
3. **Derived writes get a derivation rule, not fake precision.** A summary is
   the agent's account: `self` for ops content, `observed` + `source_user`
   only when the summarized exchanges have a single dominant speaker.
   Mixed-speaker summaries leave `source_user` empty — unknown stays
   unknown; a guessed origin is worse than none.
4. **`MemoryRequest` gains `source_user`/`source_kind`;** the sync path maps
   `via:<agent>` ⇒ `source_kind=peer` mechanically, preserving the imported
   memory's own `source_user` so "mami said it, umbreon relayed it" survives
   the hop (PROV would call this delegation; two fields express it fine).
5. **`CorrectMemory` migrates to `PatchMemory`** — the one refactor that pays
   three times: provenance carry-over, CAS, and drift-free edits replace a
   whole-value rewrite.
6. **Group chats stay honest.** A turn has exactly one sender; provenance is
   that sender's. Cross-speaker inference ("mami agreed with what papi
   said") is agent judgment and belongs in agent-authored writes, not in
   mechanical capture.

## Eval hook

Per-path coverage census, same instrument as edge adoption: fraction of new
writes per path carrying provenance, with a mislabel spot-check. Shell-side
unit assertions per path (each write function's test asserts the fields) keep
regressions structural rather than statistical. Target after wiring: every
mechanical path at 100% by construction — only the MCP path depends on agent
behaviour, and that one is the guidance eval's job.

## What this deliberately does not do

No LLM speaker inference anywhere (the sticky-pointer lesson: per-turn LLM
classification measured catastrophic in this exact codebase); no new identity
store; no numeric confidence; no retroactive backfill of the existing ~12k
memories — unknown origin stays unknown.
