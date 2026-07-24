//go:build !ORT

package embedding

import "github.com/knights-analytics/hugot"

// RerankerBackend identifies the compiled inference backend. Logged at init
// so a plain rebuild that silently swaps ORT→simplego (measured 2026-07-23:
// ~200ms→~3s per inject) is visible in daemon logs instead of only in
// latency graphs.
const RerankerBackend = "simplego (single-threaded pure-Go)"

// makeRerankerSession creates a hugot session using the pure-Go simplego
// backend. Single-threaded — cross-encoder rerank cost is ~18s per call at
// rerank top-20 with 8-chunk docs on M-series CPU. Acceptable for slice
// benchmarks but not full-corpus runs.
//
// For full-corpus throughput, build with `-tags=ORT` (requires CGo and a
// system onnxruntime shared library) — see reranker_ort.go.
func makeRerankerSession() (*hugot.Session, error) {
	return hugot.NewGoSession()
}

// rerankerBackendName is exposed for diagnostics / build-config checks.
const rerankerBackendName = "go-simplego"
