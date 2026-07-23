//go:build ORT && cgo

package embedding

import (
	"os"
	"runtime"
	"strconv"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
)

var (
	ortSessionOnce sync.Once
	ortSession     *hugot.Session
	ortSessionErr  error
)

// sharedORTSession returns the process-wide ONNX Runtime session. hugot
// permits only one active ORT session per process, and the embedder and
// reranker each need one — so sharing is mandatory, not an optimization:
// with separate sessions, whichever consumer initialized second failed with
// "another session is currently active" and the reranker silently fell back
// to fused order on every search.
func sharedORTSession() (*hugot.Session, error) {
	ortSessionOnce.Do(func() {
		intra := runtime.NumCPU()
		if v := os.Getenv("GHOST_ORT_INTRA_THREADS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				intra = n
			}
		}
		inter := 1
		if v := os.Getenv("GHOST_ORT_INTER_THREADS"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				inter = n
			}
		}

		opts := []options.WithOption{
			options.WithIntraOpNumThreads(intra),
			options.WithInterOpNumThreads(inter),
		}
		if lib := os.Getenv("GHOST_ONNXRUNTIME_PATH"); lib != "" {
			opts = append(opts, options.WithOnnxLibraryPath(lib))
		}
		ortSession, ortSessionErr = hugot.NewORTSession(opts...)
	})
	return ortSession, ortSessionErr
}

// destroySession is a no-op on the ORT backend: the session is shared
// process-wide, so destroying it for one consumer would tear down the
// other's pipelines. It is released when the process exits.
func destroySession(*hugot.Session) {}
