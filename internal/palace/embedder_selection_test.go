package palace

import (
	"testing"
)

// stubEmbedder stands in for a local (non-Ollama) embedder, which is the only
// property the selection helpers below actually branch on.
type stubEmbedder struct {
	closed bool
}

func (s *stubEmbedder) Embed(texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{0.1, 0.2, 0.3}
	}
	return out, nil
}

func (s *stubEmbedder) Close() { s.closed = true }

func TestWithOllamaEmbedder(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		model     string
		wantURL   string
		wantModel string
	}{
		{
			name:      "explicit URL and model are used",
			baseURL:   "http://ollama.internal:11434",
			model:     "nomic-embed-text",
			wantURL:   "http://ollama.internal:11434",
			wantModel: "nomic-embed-text",
		},
		{
			name:      "empty arguments keep the defaults",
			baseURL:   "",
			model:     "",
			wantURL:   DefaultOllamaURL,
			wantModel: DefaultOllamaEmbedModel,
		},
		{
			name:      "an empty URL keeps the default but the model still applies",
			baseURL:   "",
			model:     "custom-model",
			wantURL:   DefaultOllamaURL,
			wantModel: "custom-model",
		},
		{
			name:      "an empty model keeps the default but the URL still applies",
			baseURL:   "http://elsewhere:1234",
			model:     "",
			wantURL:   "http://elsewhere:1234",
			wantModel: DefaultOllamaEmbedModel,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := PalaceConfig{OllamaURL: DefaultOllamaURL, OllamaModel: DefaultOllamaEmbedModel}
			WithOllamaEmbedder(tc.baseURL, tc.model)(&cfg)

			if !cfg.UseOllama {
				t.Error("WithOllamaEmbedder did not set UseOllama")
			}
			if cfg.OllamaURL != tc.wantURL {
				t.Errorf("OllamaURL = %q, want %q", cfg.OllamaURL, tc.wantURL)
			}
			if cfg.OllamaModel != tc.wantModel {
				t.Errorf("OllamaModel = %q, want %q", cfg.OllamaModel, tc.wantModel)
			}
		})
	}
}

func TestBatchSizeFor(t *testing.T) {
	// Ollama batches server-side and takes a much larger batch than the local
	// pipeline, which is bounded by graph compilation.
	if got := batchSizeFor(&ollamaEmbedder{model: "test"}); got != ollamaEmbedBatchSize {
		t.Errorf("batchSizeFor(ollama) = %d, want %d", got, ollamaEmbedBatchSize)
	}
	if got := batchSizeFor(&stubEmbedder{}); got != embedBatchSize {
		t.Errorf("batchSizeFor(local) = %d, want %d", got, embedBatchSize)
	}
}

func TestEmbedderName(t *testing.T) {
	if got, want := embedderName(&ollamaEmbedder{model: "nomic-embed-text"}), "ollama/nomic-embed-text"; got != want {
		t.Errorf("embedderName(ollama) = %q, want %q", got, want)
	}
	if got := embedderName(&stubEmbedder{}); got != backendName {
		t.Errorf("embedderName(local) = %q, want %q", got, backendName)
	}
}

// TestEmbedderPool_OllamaIsSharedNotCloned is the guard for a correctness bug,
// not a performance one: building extra *local* embedders alongside an Ollama
// one mixes 768- and 384-dimension vectors into a single wing, where cosine
// similarity silently returns 0 for every mismatched pair.
func TestEmbedderPool_OllamaIsSharedNotCloned(t *testing.T) {
	shared := &ollamaEmbedder{model: "test"}
	p := &Palace{embedder: shared}

	embs, cleanup := embedderPool(p, 8)
	defer cleanup()

	if len(embs) != 1 {
		t.Fatalf("embedderPool returned %d embedders for Ollama, want 1 shared instance", len(embs))
	}
	if embs[0] != Embedder(shared) {
		t.Error("embedderPool did not reuse the palace's own Ollama embedder")
	}
}

func TestEmbedderPool_OllamaCleanupDoesNotCloseTheSharedEmbedder(t *testing.T) {
	// Worker 0 always reuses the palace's embedder, whose lifetime the palace
	// owns — closing it here would break the palace after a mine run.
	shared := &ollamaEmbedder{model: "test"}
	p := &Palace{embedder: shared}

	_, cleanup := embedderPool(p, 4)
	cleanup() // must be a no-op, and must not panic
}

func TestEmbedderPool_SingleWorkerReusesPalaceEmbedder(t *testing.T) {
	local := &stubEmbedder{}
	p := &Palace{embedder: local, config: PalaceConfig{ModelPath: t.TempDir()}}

	embs, cleanup := embedderPool(p, 1)
	defer cleanup()

	if len(embs) != 1 {
		t.Fatalf("embedderPool(workers=1) returned %d embedders, want 1", len(embs))
	}
	if embs[0] != Embedder(local) {
		t.Error("the single-worker case must reuse the palace's embedder rather than allocating")
	}

	cleanup()
	if local.closed {
		t.Error("cleanup closed the palace's own embedder")
	}
}
