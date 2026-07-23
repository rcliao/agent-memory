//go:build ORT && cgo

package embedding

import "testing"

// hugot permits only one active ORT session per process. The embedder and
// reranker must therefore resolve to the SAME session — separate sessions
// made the second consumer fail with "another session is currently active"
// and silently disabled the reranker.
func TestORTSessionIsShared(t *testing.T) {
	embedderSess, err := makeEmbedderSession()
	if err != nil {
		t.Skipf("ORT session unavailable (set GHOST_ONNXRUNTIME_PATH): %v", err)
	}
	rerankerSess, err := makeRerankerSession()
	if err != nil {
		t.Fatalf("reranker session errored after embedder session succeeded: %v", err)
	}
	if embedderSess != rerankerSess {
		t.Fatalf("embedder and reranker got different ORT sessions: %p vs %p", embedderSess, rerankerSess)
	}
	// destroySession must be a no-op on the shared session: a second
	// consumer initialized later would otherwise inherit a dead runtime.
	destroySession(embedderSess)
	again, err := makeRerankerSession()
	if err != nil || again != embedderSess {
		t.Fatalf("session unusable after destroySession: sess=%p err=%v", again, err)
	}
}
