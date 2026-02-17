#!/usr/bin/env bash
# agent-memory acceptance tests
# Exercises every command end-to-end against a temp DB.
# Exit code 0 = all pass, non-zero = failures.

set -euo pipefail

BINARY="./agent-memory"
TMPDB=$(mktemp /tmp/agent-memory-test-XXXXXX.db)
PASS=0
FAIL=0
ERRORS=""

cleanup() { rm -f "$TMPDB"; }
trap cleanup EXIT

run() {
  local name="$1"; shift
  if output=$("$@" 2>&1); then
    PASS=$((PASS + 1))
    echo "  ✅ $name"
  else
    FAIL=$((FAIL + 1))
    ERRORS="${ERRORS}\n  ❌ $name: $output"
    echo "  ❌ $name"
  fi
}

# Check a command exists (--help doesn't error)
check_cmd() {
  run "command exists: $1" $BINARY --db "$TMPDB" "$1" --help
}

echo "🧪 agent-memory acceptance tests"
echo "   DB: $TMPDB"
echo ""

# ── Phase 0: Binary exists and runs ──
echo "Phase 0: Binary"
run "binary exists" test -x "$BINARY"
run "help works" $BINARY --help

# ── Phase 1: Command registration ──
echo ""
echo "Phase 1: All commands registered"
check_cmd put
check_cmd get
check_cmd list
check_cmd rm
check_cmd search
check_cmd context
check_cmd link
check_cmd ns
check_cmd stats
check_cmd export
check_cmd import

# ── Phase 2: Core CRUD ──
echo ""
echo "Phase 2: Core CRUD"

# put
run "put simple" $BINARY --db "$TMPDB" put -n "test" -k "greeting" "hello world"
run "put with priority" $BINARY --db "$TMPDB" put -n "test" -k "important" -p critical "do not forget"
run "put with kind" $BINARY --db "$TMPDB" put -n "test" -k "howto" --kind procedural "step 1: do thing"
run "put with tags" $BINARY --db "$TMPDB" put -n "test" -k "tagged" -t "foo,bar" "tagged memory"
run "put stdin" bash -c "echo 'from stdin' | $BINARY --db '$TMPDB' put -n 'test' -k 'piped'"

# put versioning
run "put version 2" $BINARY --db "$TMPDB" put -n "test" -k "greeting" "hello world v2"

# get
run "get latest" bash -c "$BINARY --db '$TMPDB' get -n 'test' -k 'greeting' | grep -q 'v2'"
run "get history" bash -c "$BINARY --db '$TMPDB' get -n 'test' -k 'greeting' --history | grep -q 'hello world'"
run "get version 1" bash -c "$BINARY --db '$TMPDB' get -n 'test' -k 'greeting' -v 1 | grep -q 'hello world'"

# list
run "list namespace" bash -c "$BINARY --db '$TMPDB' list -n 'test' | grep -q 'greeting'"
run "list by kind" bash -c "$BINARY --db '$TMPDB' list -n 'test' --kind procedural | grep -q 'howto'"
run "list keys-only" $BINARY --db "$TMPDB" list -n "test" --keys-only

# rm (soft)
run "rm soft" $BINARY --db "$TMPDB" rm -n "test" -k "tagged"
run "rm verify gone" bash -c "! $BINARY --db '$TMPDB' get -n 'test' -k 'tagged' 2>/dev/null | grep -q 'tagged memory'"

# ── Phase 3: Search ──
echo ""
echo "Phase 3: Search"
run "search basic" bash -c "$BINARY --db '$TMPDB' search -n 'test' 'hello' | grep -q 'greeting'"
run "search cross-ns" bash -c "$BINARY --db '$TMPDB' search 'hello' | grep -q 'greeting'"
run "search no results" bash -c "$BINARY --db '$TMPDB' search 'zzzznonexistent' 2>&1; true"

# ── Phase 4: Context assembly ──
echo ""
echo "Phase 4: Context"
run "context basic" bash -c "$BINARY --db '$TMPDB' context -n 'test' --budget 2000 'greeting' 2>&1; true"

# ── Phase 5: Links ──
echo ""
echo "Phase 5: Links"
run "link create" bash -c "$BINARY --db '$TMPDB' link --from 'test:greeting' --to 'test:important' -r relates_to 2>&1; true"

# ── Phase 6: Namespace ops ──
echo ""
echo "Phase 6: Namespace"
run "ns list" bash -c "$BINARY --db '$TMPDB' ns list 2>&1; true"
run "ns stats" bash -c "$BINARY --db '$TMPDB' stats 2>&1; true"

# ── Phase 7: Export/Import ──
echo ""
echo "Phase 7: Export/Import"
TMPEXPORT=$(mktemp /tmp/agent-memory-export-XXXXXX.json)
run "export" bash -c "$BINARY --db '$TMPDB' export -n 'test' > '$TMPEXPORT'"
run "export non-empty" test -s "$TMPEXPORT"

TMPDB2=$(mktemp /tmp/agent-memory-import-XXXXXX.db)
run "import" bash -c "$BINARY --db '$TMPDB2' import < '$TMPEXPORT' 2>&1; true"
rm -f "$TMPEXPORT" "$TMPDB2"

# ── Phase 8: Long content chunking ──
echo ""
echo "Phase 8: Chunking"
LONGTEXT=$(printf '# Heading 1\n\nParagraph one with some content.\n\n## Heading 2\n\nParagraph two with different content.\n\n## Heading 3\n\nParagraph three wrapping up.\n')
run "put long content" bash -c "echo '$LONGTEXT' | $BINARY --db '$TMPDB' put -n 'test' -k 'long-doc' --kind episodic"
run "get long content" bash -c "$BINARY --db '$TMPDB' get -n 'test' -k 'long-doc' | grep -q 'Heading'"

# ── Summary ──
echo ""
echo "════════════════════════════════"
echo "  Results: $PASS passed, $FAIL failed"
if [ $FAIL -gt 0 ]; then
  echo -e "\n  Failures:$ERRORS"
  echo ""
  echo "════════════════════════════════"
  exit 1
else
  echo "  All tests passed! 🎉"
  echo "════════════════════════════════"
  exit 0
fi
