package palace

import (
	"fmt"
	"math"
	"runtime"
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

// Embedder wraps a hugot session for in-process text embedding.
type Embedder struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
}

// NewEmbedder creates an Embedder backed by hugot's pure-Go (GoMLX) runtime.
func NewEmbedder(modelPath string) (*Embedder, error) {
	session, err := hugot.NewGoSession()
	if err != nil {
		return nil, fmt.Errorf("create hugot session: %w", err)
	}

	config := hugot.FeatureExtractionConfig{
		ModelPath: modelPath,
		Name:      "palace-embedder",
	}
	pipeline, err := hugot.NewPipeline(session, config)
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("create embedding pipeline: %w", err)
	}

	return &Embedder{session: session, pipeline: pipeline}, nil
}

// Embed returns embedding vectors for the given texts.
// Inputs exceeding maxTokenLength (128) tokens are truncated to stay within
// GoMLX sequence bucket limits and prevent bucket mismatch panics during
// graph compilation.
func (e *Embedder) Embed(texts []string) ([][]float32, error) {
	// Truncate texts to prevent GoMLX graph compilation failures from
	// bucket mismatches. BERT tokenization produces ~1 token per 3-4 chars,
	// so 128 tokens * 4 chars/token = 512 chars is a safe limit.
	for i := range texts {
		if len(texts[i]) > maxCharLength {
			texts[i] = texts[i][:maxCharLength]
		}
	}

	result, err := e.pipeline.RunPipeline(texts)
	if err != nil {
		return nil, fmt.Errorf("run embedding pipeline: %w", err)
	}
	return result.Embeddings, nil
}

// Close destroys the hugot session and releases resources.
func (e *Embedder) Close() {
	if e.session != nil {
		_ = e.session.Destroy()
	}
}

// DownloadModel fetches the all-MiniLM-L6-v2 model to dest and returns the local path.
func DownloadModel(dest string, onnxFilePath string) (string, error) {
	opts := hugot.NewDownloadOptions()
	if onnxFilePath != "" {
		opts.OnnxFilePath = onnxFilePath
	}
	return hugot.DownloadModel(
		"sentence-transformers/all-MiniLM-L6-v2",
		dest,
		opts,
	)
}

// DetectPlatformOnnxFile returns the recommended ONNX file for the current platform.
func DetectPlatformOnnxFile() string {
	// Detect ARM64 (Apple Silicon)
	if runtime.GOARCH == "arm64" {
		return "onnx/model_qint8_arm64.onnx"
	}
	// For x86_64, use the base model (AVX512 variants need CPU feature probing)
	if runtime.GOARCH == "amd64" {
		return "onnx/model.onnx"
	}
	// Fallback to base model
	return "onnx/model.onnx"
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
