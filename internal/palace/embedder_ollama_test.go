package palace

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOllama serves the two endpoints NewOllamaEmbedder and Embed use.
func fakeOllama(t *testing.T, models []string, dim int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		list := make([]map[string]any, 0, len(models))
		for _, m := range models {
			list = append(list, map[string]any{"name": m})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": list})
	})

	mux.HandleFunc("/api/embed", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		vecs := make([][]float32, len(req.Input))
		for i := range vecs {
			vecs[i] = make([]float32, dim)
			vecs[i][0] = float32(i + 1)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vecs})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNewOllamaEmbedder_UnreachableDaemon(t *testing.T) {
	// Port 1 is reserved and never listening.
	_, err := NewOllamaEmbedder("http://127.0.0.1:1", "embeddinggemma")
	if !errors.Is(err, ErrOllamaUnavailable) {
		t.Fatalf("err = %v, want ErrOllamaUnavailable", err)
	}
}

func TestNewOllamaEmbedder_ModelNotPulled(t *testing.T) {
	srv := fakeOllama(t, []string{"llama3:latest"}, 4)

	_, err := NewOllamaEmbedder(srv.URL, "embeddinggemma")
	if !errors.Is(err, ErrOllamaUnavailable) {
		t.Fatalf("err = %v, want ErrOllamaUnavailable", err)
	}
	if !strings.Contains(err.Error(), "not pulled") {
		t.Errorf("error should say the model is missing, got: %v", err)
	}
}

// Ollama reports unqualified names with a ":latest" suffix, so a configured
// "embeddinggemma" has to match a listed "embeddinggemma:latest".
func TestNewOllamaEmbedder_MatchesLatestSuffix(t *testing.T) {
	srv := fakeOllama(t, []string{"embeddinggemma:latest"}, 4)

	if _, err := NewOllamaEmbedder(srv.URL, "embeddinggemma"); err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}
}

func TestOllamaEmbedder_EmbedPreservesOrderAcrossBatches(t *testing.T) {
	srv := fakeOllama(t, []string{"embeddinggemma"}, 3)
	e, err := NewOllamaEmbedder(srv.URL, "embeddinggemma")
	if err != nil {
		t.Fatalf("NewOllamaEmbedder: %v", err)
	}
	defer e.Close()

	// More than one batch so the append path is exercised.
	n := ollamaEmbedBatchSize + 7
	texts := make([]string, n)
	for i := range texts {
		texts[i] = "chunk"
	}

	vecs, err := e.Embed(texts)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != n {
		t.Fatalf("got %d vectors, want %d", len(vecs), n)
	}
	// The stub marks each vector with its index within its batch; the first
	// element of the second batch must restart at 1, proving batches were not
	// silently dropped or concatenated wrongly.
	if vecs[0][0] != 1 {
		t.Errorf("first vector marker = %v, want 1", vecs[0][0])
	}
	if vecs[ollamaEmbedBatchSize][0] != 1 {
		t.Errorf("second batch did not start a new request: %v", vecs[ollamaEmbedBatchSize][0])
	}
}

func TestOllamaEmbedder_EmbedEmptyInput(t *testing.T) {
	srv := fakeOllama(t, []string{"embeddinggemma"}, 3)
	e, _ := NewOllamaEmbedder(srv.URL, "embeddinggemma")
	defer e.Close()

	vecs, err := e.Embed(nil)
	if err != nil || vecs != nil {
		t.Errorf("Embed(nil) = %v, %v; want nil, nil", vecs, err)
	}
}

func TestNewOllamaEmbedder_DefaultsApplied(t *testing.T) {
	// An empty model name must resolve to the default rather than querying "".
	srv := fakeOllama(t, []string{DefaultOllamaEmbedModel}, 4)
	if _, err := NewOllamaEmbedder(srv.URL, ""); err != nil {
		t.Fatalf("empty model should default to %s: %v", DefaultOllamaEmbedModel, err)
	}
}

func TestEmbedderAvailability_ReportsOllamaFailure(t *testing.T) {
	cfg := DefaultConfig()
	cfg.OllamaURL = "http://127.0.0.1:1"

	err := EmbedderAvailability(cfg)
	if !errors.Is(err, ErrOllamaUnavailable) {
		t.Fatalf("err = %v, want ErrOllamaUnavailable", err)
	}
}

func TestEmbedderAvailability_LocalBackendWithoutModel(t *testing.T) {
	cfg := DefaultConfig()
	cfg.UseOllama = false
	cfg.ModelPath = ""

	if err := EmbedderAvailability(cfg); err == nil {
		t.Error("expected an error when no local model is configured")
	}
}
