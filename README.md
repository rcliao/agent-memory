# agent-memory

Persistent memory for AI agents. Text in, text out. SQLite-backed, single binary, no server.

Part of the [teeny-claw](https://github.com/rcliao/teeny-claw) constellation.

## Install

```bash
go install github.com/rcliao/agent-memory/cmd/agent-memory@latest
```

## Quick Start

```bash
# Store a memory
agent-memory put -n "user:prefs" -k "editor" "Prefers Neovim with Lazy plugin manager"

# Store with priority
agent-memory put -n "user:prefs" -k "allergies" -p critical "Allergic to peanuts"

# Store with TTL (auto-expires)
agent-memory put -n "session" -k "token" --ttl 24h "abc123"

# Pipe content from stdin
cat session-notes.md | agent-memory put -n "project:myapp" -k "session-2026-02-16" --kind episodic

# Retrieve latest version
agent-memory get -n "user:prefs" -k "editor"

# Get all versions
agent-memory get -n "user:prefs" -k "editor" --history

# Get specific version
agent-memory get -n "user:prefs" -k "editor" -v 1

# List all memories in a namespace
agent-memory list -n "user:prefs"

# List with filters
agent-memory list -n "project:myapp" --kind episodic --tags "deploy,infra"

# List keys only
agent-memory list -n "project:myapp" --keys-only

# Search memories
agent-memory search -n "user:prefs" "neovim"
agent-memory search "deploy"

# Database stats
agent-memory stats

# Export memories
agent-memory export -n "user:prefs" > backup.json

# Import memories
agent-memory import < backup.json

# Soft-delete (recoverable)
agent-memory rm -n "user:prefs" -k "old-thing"

# Hard-delete all versions (permanent)
agent-memory rm -n "user:prefs" -k "old-thing" --all-versions --hard
```

## Commands

| Command  | Description |
|----------|-------------|
| `put`    | Store a memory (positional arg or stdin) |
| `get`    | Retrieve a memory by namespace and key |
| `list`   | List memories with filters |
| `search` | Search memory content by keyword/substring |
| `rm`     | Soft-delete or hard-delete a memory |
| `stats`  | Show database statistics |
| `export` | Export memories as JSON |
| `import` | Import memories from JSON (stdin) |

## Storage

Database location (in order of precedence):
1. `--db` flag
2. `$AGENT_MEMORY_DB` environment variable
3. `~/.agent-memory/memory.db`

## Output

All output is JSON by default. Pipe to `jq` for pretty-printing:

```bash
agent-memory list -n "project:myapp" | jq .
```

## Versioning

Storing to an existing key creates a new version. Old versions are preserved:

```bash
agent-memory put -n "ns" -k "config" "version 1"
agent-memory put -n "ns" -k "config" "version 2"
agent-memory get -n "ns" -k "config"           # returns v2
agent-memory get -n "ns" -k "config" --history  # returns [v2, v1]
```

## TTL / Expiry

Memories can have a time-to-live. Expired memories are automatically filtered from `list`, `get`, and `search` results:

```bash
agent-memory put -n "session" -k "cache" --ttl 7d "temporary data"
agent-memory put -n "session" -k "token" --ttl 24h "short-lived"
```

Supported formats: `7d` (days), `24h` (hours), `30m` (minutes), `60s` (seconds).

## Chunking

Long content is automatically split into chunks for search indexing. Chunks are internal — you always get back full memory content. Search queries match across chunks too.

## Dependencies

- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — Pure Go SQLite (no CGo)
- [github.com/oklog/ulid/v2](https://github.com/oklog/ulid) — ULID generation
- [github.com/spf13/cobra](https://github.com/spf13/cobra) — CLI framework

## License

MIT
