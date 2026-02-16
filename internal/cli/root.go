// Package cli implements the agent-memory CLI commands.
package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/rcliao/agent-memory/internal/store"
	"github.com/spf13/cobra"
)

var (
	dbPath     string
	formatFlag string
)

// RootCmd is the top-level command.
var RootCmd = &cobra.Command{
	Use:   "agent-memory",
	Short: "Persistent memory for AI agents",
	Long:  "A tiny CLI for persistent agent memory. Text in, text out. SQLite-backed, single binary.",
}

func init() {
	RootCmd.PersistentFlags().StringVarP(&dbPath, "db", "d", "", "Database path (default: $AGENT_MEMORY_DB or ~/.agent-memory/memory.db)")
	RootCmd.PersistentFlags().StringVarP(&formatFlag, "format", "f", "json", "Output format: json or text")
}

func getDBPath() string {
	if dbPath != "" {
		return dbPath
	}
	if env := os.Getenv("AGENT_MEMORY_DB"); env != "" {
		return env
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agent-memory", "memory.db")
}

func openStore() (*store.SQLiteStore, error) {
	return store.NewSQLiteStore(getDBPath())
}

func exitErr(msg string, err error) {
	fmt.Fprintf(os.Stderr, "error: %s: %v\n", msg, err)
	os.Exit(1)
}
