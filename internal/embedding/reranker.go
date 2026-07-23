package embedding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

// RerankResult holds a reranked document with its score.
type RerankResult struct {
	Index int     // original position in the input slice
	Score float32 // relevance score (0-1, higher = more relevant)
}

// Reranker re-scores documents against a query using a cross-encoder model.
type Reranker interface {
	Rerank(ctx context.Context, query string, documents []string) ([]RerankResult, error)
}

// LocalReranker runs a cross-encoder model locally via hugot.
type LocalReranker struct {
	modelsDir string
	modelName string
	hfName    string

	mu       sync.Mutex
	session  *hugot.Session
	pipeline *pipelines.CrossEncoderPipeline
	initErr  error
	inited   bool
}

const (
	defaultRerankerModel = "cross-encoder/ms-marco-MiniLM-L-6-v2"
	defaultRerankerName  = "ms-marco-MiniLM-L-6-v2"
)

// NewLocalReranker creates a local cross-encoder reranker.
// The model is downloaded on first use to ~/.ghost/models/.
func NewLocalReranker() *LocalReranker {
	dir := os.Getenv("GHOST_MODELS_DIR")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".ghost", "models")
	}
	modelName := os.Getenv("GHOST_RERANKER_MODEL")
	hfName := defaultRerankerModel
	if modelName == "" {
		modelName = defaultRerankerName
	} else {
		// If user set a custom model name, use it as HF name too
		hfName = modelName
	}
	return &LocalReranker{modelsDir: dir, modelName: modelName, hfName: hfName}
}

func (r *LocalReranker) init() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.inited {
		return r.initErr
	}
	r.inited = true

	if err := os.MkdirAll(r.modelsDir, 0o755); err != nil {
		r.initErr = fmt.Errorf("create models dir: %w", err)
		return r.initErr
	}

	modelDirName := strings.ReplaceAll(r.hfName, "/", "_")
	modelPath := filepath.Join(r.modelsDir, modelDirName)
	onnxPath := filepath.Join(modelPath, "model.onnx")
	if _, err := os.Stat(onnxPath); os.IsNotExist(err) {
		opts := hugot.NewDownloadOptions()
		opts.OnnxFilePath = "onnx/model.onnx"
		downloaded, err := hugot.DownloadModel(r.hfName, r.modelsDir, opts)
		if err != nil {
			r.initErr = fmt.Errorf("download reranker %s: %w", r.hfName, err)
			return r.initErr
		}
		modelPath = downloaded
	}

	session, err := makeRerankerSession()
	if err != nil {
		r.initErr = fmt.Errorf("create session: %w", err)
		return r.initErr
	}
	r.session = session

	config := hugot.CrossEncoderConfig{
		ModelPath:    modelPath,
		Name:         "ghost-reranker",
		OnnxFilename: "model.onnx",
	}
	pipeline, err := hugot.NewPipeline[*pipelines.CrossEncoderPipeline](session, config)
	if err != nil {
		destroySession(session)
		r.initErr = fmt.Errorf("create reranker pipeline: %w", err)
		return r.initErr
	}
	r.pipeline = pipeline

	return nil
}

func (r *LocalReranker) Rerank(ctx context.Context, query string, documents []string) (results []RerankResult, err error) {
	if len(documents) == 0 {
		return nil, nil
	}

	if initErr := r.init(); initErr != nil {
		return nil, initErr
	}

	defer func() {
		if rec := recover(); rec != nil {
			results = nil
			err = fmt.Errorf("reranker panic: %v", rec)
		}
	}()

	output, err := r.pipeline.RunPipeline(query, documents)
	if err != nil {
		return nil, fmt.Errorf("rerank: %w", err)
	}

	results = make([]RerankResult, len(output.Results))
	for i, r := range output.Results {
		results[i] = RerankResult{
			Index: r.Index,
			Score: r.Score,
		}
	}
	return results, nil
}

// Close releases resources.
func (r *LocalReranker) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.session != nil {
		destroySession(r.session)
		r.session = nil
	}
}

// rerankerModelOnDisk reports whether the cross-encoder model is already
// downloaded, so "auto" mode can use it without ever touching the network.
func rerankerModelOnDisk() bool {
	dir := os.Getenv("GHOST_MODELS_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".ghost", "models")
	}
	hfName := os.Getenv("GHOST_RERANKER_MODEL")
	if hfName == "" {
		hfName = defaultRerankerModel
	}
	modelDir := strings.ReplaceAll(hfName, "/", "_")
	_, err := os.Stat(filepath.Join(dir, modelDir, "model.onnx"))
	return err == nil
}

// NewRerankerFromEnv creates a reranker from environment variables.
//   - GHOST_RERANKER=local  — always on; downloads the model on first use.
//   - GHOST_RERANKER=none/0 — off.
//   - unset or "auto"       — on ONLY when the model is already on disk:
//     users who have run the reranker once (and deployments that pre-fetch
//     the model) get the full two-stage stack by default, while a fresh
//     install stays fully offline and never phones the network implicitly.
//     Measured with the cross-encoder: personal eval meanMRR 0.790→0.861,
//     MemoryAgentBench single-hop hit@5 0.91→0.99 on top of embeddings.
func NewRerankerFromEnv() Reranker {
	switch os.Getenv("GHOST_RERANKER") {
	case "local":
		return NewLocalReranker()
	case "none", "0":
		return nil
	case "", "auto":
		if rerankerModelOnDisk() {
			return NewLocalReranker()
		}
		return nil
	default:
		return nil
	}
}
