//go:build ORT && cgo

package embedding

import (
	"github.com/knights-analytics/hugot"
)

// makeEmbedderSession uses the ONNX Runtime backend for the embedder
// forward pass. The session is shared with the reranker (see
// ort_shared.go) because hugot allows only one ORT session per process.
// Thread counts are configured on the shared session via
// GHOST_ORT_INTRA_THREADS / GHOST_ORT_INTER_THREADS; the same
// onnxruntime + libtokenizers install covers both consumers.
func makeEmbedderSession() (*hugot.Session, error) {
	return sharedORTSession()
}
