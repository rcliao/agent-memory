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

# Soft-delete (recoverable)
agent-memory rm -n "user:prefs" -k "old-thing"

# Hard-delete all versions (permanent)
agent-memory rm -n "user:prefs" -k "old-thing" --all-versions --hard
```

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

## Chunking

Long content is automatically split into chunks for future search indexing. Chunks are internal — you always get back full memory content.

## Dependencies

- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) — Pure Go SQLite (no CGo)
- [github.com/oklog/ulid/v2](https://github.com/oklog/ulid) — ULID generation
- [github.com/spf13/cobra](https://github.com/spf13/cobra) — CLI framework

## License

MIT
