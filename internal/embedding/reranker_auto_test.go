package embedding

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRerankerAutoMode(t *testing.T) {
	// Point models dir at an empty temp dir: auto must stay off (no
	// implicit network fetch on fresh installs).
	t.Setenv("GHOST_MODELS_DIR", t.TempDir())
	t.Setenv("GHOST_RERANKER", "")
	if r := NewRerankerFromEnv(); r != nil {
		t.Errorf("auto mode enabled reranker with no model on disk")
	}
	t.Setenv("GHOST_RERANKER", "auto")
	if r := NewRerankerFromEnv(); r != nil {
		t.Errorf("explicit auto enabled reranker with no model on disk")
	}

	// Fake the model file: auto should now enable.
	dir := t.TempDir()
	modelDir := filepath.Join(dir, "cross-encoder_ms-marco-MiniLM-L-6-v2")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modelDir, "model.onnx"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GHOST_MODELS_DIR", dir)
	if r := NewRerankerFromEnv(); r == nil {
		t.Errorf("auto mode did not enable reranker with model on disk")
	}

	// none/0 always win.
	for _, v := range []string{"none", "0"} {
		t.Setenv("GHOST_RERANKER", v)
		if r := NewRerankerFromEnv(); r != nil {
			t.Errorf("GHOST_RERANKER=%s still enabled reranker", v)
		}
	}
}
