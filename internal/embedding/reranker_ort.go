//go:build ORT && cgo

package embedding

import (
	"github.com/knights-analytics/hugot"
)

// makeRerankerSession returns the shared ONNX Runtime session (see
// ort_shared.go — hugot allows only one ORT session per process, and the
// embedder needs it too). Requires a system-installed onnxruntime shared
// library:
//
//	# macOS
//	brew install onnxruntime
//	# point Ghost at the DIRECTORY containing libonnxruntime.dylib
//	export GHOST_ONNXRUNTIME_PATH=/opt/homebrew/lib
//
//	# Linux (Debian/Ubuntu)
//	# install libonnxruntime-dev or download from https://github.com/microsoft/onnxruntime/releases
//	export GHOST_ONNXRUNTIME_PATH=/usr/lib/x86_64-linux-gnu
//
// (Note: WithOnnxLibraryPath takes a directory, not a file path.)
//
// Intra-op thread count defaults to runtime.NumCPU() so forward passes use
// all cores. Override via GHOST_ORT_INTRA_THREADS / GHOST_ORT_INTER_THREADS.
func makeRerankerSession() (*hugot.Session, error) {
	return sharedORTSession()
}

const rerankerBackendName = "onnxruntime"
