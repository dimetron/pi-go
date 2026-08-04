package palace

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

const (
	// DefaultOllamaURL is where a local Ollama daemon listens by default.
	DefaultOllamaURL = "http://localhost:11434"

	// DefaultOllamaEmbedModel is the embedding model used when none is configured.
	//
	// embeddinggemma over all-minilm is a deliberate quality trade. Measured on
	// 80 real drawers from this project's palace (query = first 100 chars,
	// document = the remainder):
	//
	//	model            dim   recall@1   MRR     chunks/sec
	//	all-minilm       384     45.0%    0.584      261
	//	embeddinggemma   768     70.0%    0.798       82
	//
	// Mining is a one-off cost paid per changed chunk; retrieval quality is paid
	// on every search. 3x slower indexing for a 56% relative gain in recall@1 is
	// worth it. Note the 768 dimensions double embedding storage and double the
	// cost of any full scan over them.
	DefaultOllamaEmbedModel = "embeddinggemma"

	// ollamaEmbedBatchSize is how many chunks are sent per /api/embed call.
	//
	// Unrelated to embedBatchSize, which is 8 only because gomlx's simplego
	// backend balloons in memory on larger batches. Ollama has no such problem
	// and throughput climbs with batch size (measured, embeddinggemma: 41
	// chunks/sec at 8, 72 at 64, 74 at 256, 82 at 512).
	ollamaEmbedBatchSize = 512

	// ollamaProbeTimeout bounds the reachability check. It is a loopback request
	// to a daemon that is either up or not; waiting longer helps nobody.
	ollamaProbeTimeout = 3 * time.Second
)

// ErrOllamaUnavailable is returned when the Ollama daemon cannot be reached or
// does not have the requested embedding model. Callers that can degrade (the
// agent) should warn and continue; callers that cannot (mining) should abort
// with a message that says how to fix it.
var ErrOllamaUnavailable = errors.New("ollama unavailable")

// ollamaEmbedder embeds text through a local Ollama daemon.
type ollamaEmbedder struct {
	client *api.Client
	model  string
}

// NewOllamaEmbedder connects to an Ollama daemon and verifies the embedding
// model is present. Both checks happen here rather than on first Embed so that
// misconfiguration surfaces at startup, where it can be reported usefully,
// instead of midway through a mining run.
func NewOllamaEmbedder(baseURL, model string) (Embedder, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultOllamaURL
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultOllamaEmbedModel
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("palace: bad ollama url %q: %w", baseURL, err)
	}

	// A generous client timeout, with the short deadline applied per-request via
	// context below. A large embed batch legitimately takes minutes; only the
	// reachability probe should fail fast.
	client := api.NewClient(u, &http.Client{Timeout: 10 * time.Minute})

	ctx, cancel := context.WithTimeout(context.Background(), ollamaProbeTimeout)
	defer cancel()

	models, err := client.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot reach daemon at %s: %w", ErrOllamaUnavailable, baseURL, err)
	}
	if !hasOllamaModel(models, model) {
		return nil, fmt.Errorf("%w: model %q is not pulled on %s", ErrOllamaUnavailable, model, baseURL)
	}

	return &ollamaEmbedder{client: client, model: model}, nil
}

// hasOllamaModel reports whether name is in the daemon's model list, tolerating
// the ":latest" suffix Ollama adds to unqualified names.
func hasOllamaModel(list *api.ListResponse, name string) bool {
	if list == nil {
		return false
	}
	want := strings.TrimSuffix(name, ":latest")
	for _, m := range list.Models {
		if strings.TrimSuffix(m.Name, ":latest") == want {
			return true
		}
	}
	return false
}

// Embed sends texts to Ollama in batches and returns their vectors in order.
func (o *ollamaEmbedder) Embed(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += ollamaEmbedBatchSize {
		end := min(start+ollamaEmbedBatchSize, len(texts))

		resp, err := o.client.Embed(context.Background(), &api.EmbedRequest{
			Model: o.model,
			Input: texts[start:end],
		})
		if err != nil {
			return nil, fmt.Errorf("palace: ollama embed: %w", err)
		}
		if got, want := len(resp.Embeddings), end-start; got != want {
			return nil, fmt.Errorf("palace: ollama returned %d embeddings for %d inputs", got, want)
		}
		out = append(out, resp.Embeddings...)
	}
	return out, nil
}

// Close is a no-op: the daemon owns the model's lifetime, not this process.
func (o *ollamaEmbedder) Close() {}
