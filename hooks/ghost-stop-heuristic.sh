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

# Flatten the JSONL transcript into plain "Speaker: text" lines. Handles both
# string content and Claude Code's block-array content; skips tool noise.
TRANSCRIPT_TEXT=$(jq -r '
  select(.type=="user" or .type=="assistant")
  | .type as $sp
  | (.message.content // .content) as $c
  | (if ($c|type)=="string" then $c
     elif ($c|type)=="array" then ($c | map(select(.type=="text") | .text) | join(" "))
     else "" end) as $t
  | select(($t|length) > 0)
  | "\($sp[0:1]|ascii_upcase)\($sp[1:]): \($t)"
' "$TRANSCRIPT_PATH" 2>> "$DEBUG_LOG" | tail -400)

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
  "${SPEAKER_ARG[@]}" \
  >> "$DEBUG_LOG" 2>&1 || echo "capture failed (non-fatal)" >> "$DEBUG_LOG"

# Lightweight reflect so new memories flow through the lifecycle. Silent on error.
"$GHOST" reflect --ns "$AGENT_NS" >> "$DEBUG_LOG" 2>&1 || echo "reflect failed (non-fatal)" >> "$DEBUG_LOG"

echo "Done" >> "$DEBUG_LOG"
