#!/usr/bin/env bash
# Ghost memory capture — Stop hook, LLM-FREE tier.
#
# A drop-in alternative to ghost-stop.sh that captures memories from the session
# transcript using `ghost capture` — deterministic heuristics (entity salience +
# intent classifiers), NO `claude -p`, no API key required. Runs offline and
# instantly.
#
# Two-tier note: this is the always-on mechanical tier. If you also want the
# higher-fidelity LLM extraction, keep ghost-stop.sh as well (it dedups against
# what this captures). Both converge on the same `ghost put`/dedup path.
#
# Configure via environment variables:
#   GHOST_BIN           — path to ghost binary (default: ghost on PATH)
#   GHOST_AGENT_NS      — agent namespace (default: agent:claude-code)
#   GHOST_CAPTURE_MIN   — min salience 0..1 (default: 0.4, stricter than the lib default)
#   GHOST_CAPTURE_MAX   — max memories per session (default: 8)
#   GHOST_CAPTURE_SPEAKER — restrict to one speaker, e.g. "user" (default: all)
set -uo pipefail

GHOST="${GHOST_BIN:-ghost}"
AGENT_NS="${GHOST_AGENT_NS:-agent:claude-code}"
MIN_SALIENCE="${GHOST_CAPTURE_MIN:-0.4}"
MAX_CAP="${GHOST_CAPTURE_MAX:-8}"
SPEAKER="${GHOST_CAPTURE_SPEAKER:-}"
DEBUG_LOG="/tmp/ghost-stop-heuristic-debug.log"

trap 'echo "ghost-stop-heuristic.sh failed at line $LINENO" >&2; echo "Error at line $LINENO" >> "$DEBUG_LOG"' ERR

HOOK_INPUT=$(cat)
echo "=== $(date -Iseconds) ===" >> "$DEBUG_LOG"

SESSION_ID=$(echo "$HOOK_INPUT" | jq -r '.session_id // empty' 2>/dev/null)
CWD=$(echo "$HOOK_INPUT" | jq -r '.cwd // empty' 2>/dev/null)
PROJECT_NAME=$(basename "${CWD:-unknown}")
DATE=$(date +%Y-%m-%d)
SESSION_TAG="session:${SESSION_ID:-$(date +%s)}"

TRANSCRIPT_PATH=$(echo "$HOOK_INPUT" | jq -r '.transcript_path // empty' 2>/dev/null)
if [ -z "$TRANSCRIPT_PATH" ] && [ -n "$SESSION_ID" ]; then
  TRANSCRIPT_PATH=$(find ~/.claude/projects -name "${SESSION_ID}*.jsonl" 2>/dev/null | head -1)
fi
if [ -z "$TRANSCRIPT_PATH" ] || [ ! -f "$TRANSCRIPT_PATH" ]; then
  echo "No readable transcript found, skipping" >> "$DEBUG_LOG"
  exit 0
fi

# Debounce + delta-only processing: the Stop hook fires after EVERY assistant
# turn. Without a watermark, each fire re-reads the same transcript tail and
# re-captures the same content (observed as key suffix variants like css/css-a
# and resurrection of deleted memories). Only process lines added since the
# last capture for this session, and only once enough new lines accumulate.
GROWTH_THRESHOLD="${GHOST_CAPTURE_GROWTH:-40}"
STATE_DIR="/tmp/ghost-stop-heuristic-state"
mkdir -p "$STATE_DIR" 2>/dev/null || true
STATE_FILE="$STATE_DIR/${SESSION_ID:-nosession}.lines"
CUR_LINES=$(wc -l < "$TRANSCRIPT_PATH" 2>/dev/null | tr -d ' '); CUR_LINES=${CUR_LINES:-0}
LAST_LINES=$(cat "$STATE_FILE" 2>/dev/null || echo 0); LAST_LINES=${LAST_LINES:-0}
if [ "$((CUR_LINES - LAST_LINES))" -lt "$GROWTH_THRESHOLD" ]; then
  echo "Debounce: $((CUR_LINES - LAST_LINES)) new lines (<$GROWTH_THRESHOLD), skipping" >> "$DEBUG_LOG"
  exit 0
fi
# Advance the watermark immediately so overlapping Stop fires don't all proceed.
echo "$CUR_LINES" > "$STATE_FILE" 2>/dev/null || true

# Flatten ONLY the new JSONL lines into plain "Speaker: text" lines. Handles
# both string content and Claude Code's block-array content; skips tool noise.
# CRITICAL: strip ghost's own injected memory blocks ([Ghost Memory ...] ...
# [End Ghost Memory]) — retrieval-hook output arrives as user-turn text, so
# without this the capture tier eats ghost's own injections and re-stores them
# as new memories (a self-feedback loop that even resurrects deleted junk).
TRANSCRIPT_TEXT=$(tail -n "+$((LAST_LINES + 1))" "$TRANSCRIPT_PATH" | jq -r '
  select(.type=="user" or .type=="assistant")
  | .type as $sp
  | (.message.content // .content) as $c
  | (if ($c|type)=="string" then $c
     elif ($c|type)=="array" then ($c | map(select(.type=="text") | .text) | join(" "))
     else "" end) as $t
  | select(($t|length) > 0)
  | "\($sp[0:1]|ascii_upcase)\($sp[1:]): \($t | gsub("[\\n\\r]+"; " "))"
' 2>> "$DEBUG_LOG" | awk '
  /\[Ghost Memory[^]]*\]/ {ghost=1}
  ghost && /\[End Ghost Memory\]/ {ghost=0; next}
  !ghost
' | perl -pe '
  # Strip harness machinery that the transcript delivers inside USER turns.
  # These are not user speech, but the salience scorer rates them like prose
  # and stored them as memories (keys: local-command-caveat-..., task-
  # notification-..., auto-summary-..., always/default from skill docs).
  # Substring-strip rather than drop the turn: a single user message often
  # carries boilerplate AND the real prompt (e.g. a /model caveat followed by
  # the actual question), and dropping it wholesale would lose real content.
  s{<local-command-caveat>.*?</local-command-caveat>}{ }g;
  s{<local-command-stdout>.*?</local-command-stdout>}{ }g;
  s{<command-(name|message|args)>.*?</command-\1>}{ }g;
  s{<command-(name|message|args)>[^<]*}{ }g;
  s{<task-notification.*?</task-notification>}{ }g;
  s{<system-reminder>.*?</system-reminder>}{ }g;
  s{<summary>.*?</summary>}{ }g;
  s{Caveat: The messages below were generated by the user while running local commands[^<]*?unless the user explicitly asks you to\.}{ }g;
  # Hook-injected additionalContext (Zero plugin notice, ghost memory hints).
  s{Zero is available to you \(the Zero plugin is installed\).*?search Zero first\.}{ }g;
  s{\*\*Memory hygiene\*\*.*?utility signal\.}{ }g;
  s{\s{2,}}{ }g;
' | awk '
  # Drop turns that are pure automation: scheduled/cron prompts re-fire
  # verbatim forever, so capturing them mints a memory that then gets served
  # back into every session (observed: the monitor prompt became a top-
  # retrieved "memory"). Also drop turns left empty by the strip above.
  { line = $0; sub(/^[A-Za-z]+: /, "", line) }
  line ~ /^Run ~\// { next }
  line ~ /ghost-monitor\.sh/ { next }
  length(line) < 25 { next }
  { print }
' | tail -400)

if [ -z "$(echo "$TRANSCRIPT_TEXT" | tr -d '[:space:]')" ]; then
  echo "No text extracted from transcript, skipping" >> "$DEBUG_LOG"
  exit 0
fi

SPEAKER_ARG=()
[ -n "$SPEAKER" ] && SPEAKER_ARG=(--speaker "$SPEAKER")

echo "Capturing from $(echo "$TRANSCRIPT_TEXT" | wc -l | tr -d ' ') lines (ns=$AGENT_NS)" >> "$DEBUG_LOG"

echo "$TRANSCRIPT_TEXT" | "$GHOST" capture \
  -n "$AGENT_NS" \
  --source "$SESSION_TAG" \
  -t "project:${PROJECT_NAME},date:${DATE}" \
  --min-salience "$MIN_SALIENCE" \
  --max "$MAX_CAP" \
  ${SPEAKER_ARG[@]+"${SPEAKER_ARG[@]}"} \
  >> "$DEBUG_LOG" 2>&1 || echo "capture failed (non-fatal)" >> "$DEBUG_LOG"

# Lightweight reflect so new memories flow through the lifecycle. Silent on error.
"$GHOST" reflect --ns "$AGENT_NS" >> "$DEBUG_LOG" 2>&1 || echo "reflect failed (non-fatal)" >> "$DEBUG_LOG"

echo "Done" >> "$DEBUG_LOG"
