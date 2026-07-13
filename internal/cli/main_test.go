package cli

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Keep CLI tests deterministic and model-free regardless of what the
	// host machine has downloaded: the "auto" reranker default and local
	// embedder would otherwise switch on wherever the models exist.
	// Explicit env still wins (benchmark runs set these deliberately).
	if os.Getenv("GHOST_EMBED_PROVIDER") == "" {
		os.Setenv("GHOST_EMBED_PROVIDER", "none")
	}
	if os.Getenv("GHOST_RERANKER") == "" {
		os.Setenv("GHOST_RERANKER", "none")
	}
	os.Exit(m.Run())
}
