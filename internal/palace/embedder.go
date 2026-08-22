package palace

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

const (
	// maxTokenLength is the maximum number of tokens the embedder will process.
	// This is set to 128 to match the tokenizer's truncation config (max_length=128)
	// and to stay within the GoMLX sequence bucket (128). Without truncation, inputs
	// exceeding 128 tokens cause GoMLX graph compilation failures due to bucket
	// mismatches between dynamic token lengths and fixed embedding weight tensors.
	maxTokenLength = 128

	// maxCharLength is the maximum character length to send to the embedder.
	// Calculated as: 128 tokens * ~4 chars/token = 512 chars.
	// Prevents token counts exceeding 128, which would cause GoMLX graph
	// compilation failures due to bucket mismatches.
	maxCharLength = maxTokenLength * 4
)

// Embedder produces embedding vectors for text.
//
// Two implementations exist: localEmbedder runs the model in-process through
// hugot, and ollamaEmbedder delegates to a local Ollama daemon. They are not
// interchangeable across a populated database — vectors from different models
// live in different spaces — so switching backends means re-indexing unless the
// underlying model is the same.
type Embedder interface {
	// Embed returns one vector per input text, in order.
	Embed(texts []string) ([][]float32, error)
	// Close releases any resources held by the embedder.
	Close()
}

// localEmbedder wraps a hugot session for in-process text embedding.
type localEmbedder struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
}

// NewEmbedder creates an Embedder on the compiled-in inference backend.
//
// The default build is pure Go (no cgo, no native libraries). Building with
// -tags ORT swaps in ONNX Runtime with CoreML, which runs on the Apple GPU and
// Neural Engine. Which backend is compiled in also decides which weights file to
// use — see platformOnnxFile — so the two must be chosen together.
func NewEmbedder(modelPath string) (Embedder, error) {
	session, err := newSession(context.Background())
	if err != nil {
		return nil, fmt.Errorf("create hugot session (%s): %w", backendName, err)
	}

	config := hugot.FeatureExtractionConfig{
		ModelPath: modelPath,
		Name:      "palace-embedder",
		// Pin the weights file. hugot otherwise requires the model directory to
		// hold exactly one .onnx and errors on ambiguity — so a cache holding both
		// the quantized and the fp32 model would fail to load. Naming it also
		// guarantees we get the variant this backend actually wants.
		OnnxFilename: OnnxModelFile(),
	}
	pipeline, err := hugot.NewPipeline(session, config)
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("create embedding pipeline: %w", err)
	}

	return &localEmbedder{session: session, pipeline: pipeline}, nil
}

// Embed returns embedding vectors for the given texts.
// Inputs exceeding maxTokenLength (128) tokens are truncated to stay within
// GoMLX sequence bucket limits and prevent bucket mismatch panics during
// graph compilation.
func (e *localEmbedder) Embed(texts []string) ([][]float32, error) {
	// Truncate texts to prevent GoMLX graph compilation failures from
	// bucket mismatches. BERT tokenization produces ~1 token per 3-4 chars,
	// so 128 tokens * 4 chars/token = 512 chars is a safe limit.
	for i := range texts {
		if len(texts[i]) > maxCharLength {
			texts[i] = texts[i][:maxCharLength]
		}
	}

	result, err := e.pipeline.RunPipeline(context.Background(), texts)
	if err != nil {
		return nil, fmt.Errorf("run embedding pipeline: %w", err)
	}
	return result.Embeddings, nil
}

// Close destroys the hugot session and releases resources.
func (e *localEmbedder) Close() {
	if e.session != nil {
		_ = e.session.Destroy()
	}
}

// hugotDownloadModel is the fetcher DownloadModel delegates to. It is a
// variable so tests can stand in a fake: the real one reaches Hugging Face,
// its parallel download has a data race the race detector flags, and leaving
// real weights behind makes every later palace test in the same package run
// real inference.
var hugotDownloadModel = hugot.DownloadModel

// DownloadModel fetches the all-MiniLM-L6-v2 model to dest and returns the local path.
func DownloadModel(dest string, onnxFilePath string) (string, error) {
	opts := hugot.NewDownloadOptions()
	if onnxFilePath != "" {
		opts.OnnxFilePath = onnxFilePath
	}
	return hugotDownloadModel(
		context.Background(),
		"sentence-transformers/all-MiniLM-L6-v2",
		dest,
		opts,
	)
}

// DetectPlatformOnnxFile returns the ONNX weights file to download for the
// compiled-in backend.
//
// The right answer depends on the backend, not just the platform, which is why
// this delegates to platformOnnxFile in the build-tagged file:
//
//   - pure Go (default): fp32. simplego has no int8 kernels and runs the
//     quantized model ~3x slower than fp32 (1.8 vs 5.6 chunks/sec, M2 Max).
//   - ORT + CoreML: int8 on arm64. ONNX Runtime has optimized ARM int8 kernels
//     and the Neural Engine is an int8 engine, so quantization is a real win.
//
// Picking the wrong pair silently costs a large multiple of runtime.
func DetectPlatformOnnxFile() string {
	return platformOnnxFile()
}

// OnnxModelFile is the bare weights filename the embedder loads.
func OnnxModelFile() string {
	return filepath.Base(platformOnnxFile())
}

// EmbedderBackend names the compiled-in inference backend.
func EmbedderBackend() string { return backendName }

// ModelReady reports whether modelDir already holds the weights this backend
// needs.
//
// A directory can exist and still be unusable: installs made before the fp32
// switch hold only model_qint8_arm64.onnx, which the pure-Go backend will load
// and then run ~3x slower. Checking for the *specific* file, rather than merely
// for the directory, is what lets callers repair such a cache instead of quietly
// using the wrong model forever. It also means switching backends re-downloads
// the variant that backend wants.
func ModelReady(modelDir string) bool {
	if modelDir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(modelDir, OnnxModelFile()))
	return err == nil && !info.IsDir() && info.Size() > 0
}

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns 0 if either vector has zero magnitude.
func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		ai, bi := float64(a[i]), float64(b[i])
		dot += ai * bi
		normA += ai * ai
		normB += bi * bi
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return float32(dot / denom)
}

// FindDuplicates returns candidates whose cosine similarity to embedding exceeds threshold.
func FindDuplicates(embedding []float32, candidates []EmbeddingRow, threshold float32) []DuplicateResult {
	var results []DuplicateResult
	for _, c := range candidates {
		sim := CosineSimilarity(embedding, c.Embedding)
		if sim >= threshold {
			results = append(results, DuplicateResult{
				ExistingID: c.DrawerID,
				Similarity: sim,
			})
		}
	}
	return results
}

// RankBySimilarity sorts candidates by cosine similarity to query descending and returns the top limit.
func RankBySimilarity(query []float32, candidates []EmbeddingRow, limit int) []ScoredResult {
	scored := make([]ScoredResult, 0, len(candidates))
	for _, c := range candidates {
		scored = append(scored, ScoredResult{
			DrawerID:   c.DrawerID,
			Similarity: CosineSimilarity(query, c.Embedding),
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Similarity > scored[j].Similarity
	})
	if limit > 0 && limit < len(scored) {
		scored = scored[:limit]
	}
	return scored
}
