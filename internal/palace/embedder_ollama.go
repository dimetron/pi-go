package palace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
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

	// ollamaEmbedRetries is how many times one batch is attempted before the
	// error is returned to the caller. Three covers a runner restart (which
	// takes a second or two) without turning a genuinely dead daemon into a
	// long stall — the non-transient path returns on the first attempt anyway.
	ollamaEmbedRetries = 3

	// ollamaMinEmbedBatch is the floor for the adaptive batch size. Below this
	// the per-request overhead dominates and a failure is no longer plausibly
	// about payload size, so there is nothing left to learn by halving again.
	ollamaMinEmbedBatch = 16
)

// ollamaEmbedRetryDelay is the base for linear backoff between attempts. A var
// rather than a const so tests can shrink it; nothing in production writes it.
var ollamaEmbedRetryDelay = 2 * time.Second

// ErrOllamaUnavailable is returned when the Ollama daemon cannot be reached or
// does not have the requested embedding model. Callers that can degrade (the
// agent) should warn and continue; callers that cannot (mining) should abort
// with a message that says how to fix it.
var ErrOllamaUnavailable = errors.New("ollama unavailable")

// ollamaEmbedder embeds text through a local Ollama daemon.
//
// Embed is serialized by mu so the daemon only ever sees one in-flight embed
// request from this process. The api.Client itself is concurrency-safe, so this
// is not about protecting the client — it is about protecting the daemon's
// model runner.
//
// Ollama fronts a runner subprocess on an ephemeral port and forwards
// /tokenize and /embed to it. Overlapping large batches make it evict or
// restart that runner mid-request, which surfaces here as
// "read tcp ...: connection reset by peer" against a port nobody configured.
// The callers are genuinely concurrent — mining embeds from its worker pool
// while drawer_service embeds on every search and add — so the serialization
// has to live at the one point they all funnel through, which is here.
type ollamaEmbedder struct {
	client *api.Client
	model  string

	// mu serializes Embed across every caller in the process, and guards batch.
	mu sync.Mutex

	// batch is the number of texts sent per /api/embed call. It starts at
	// ollamaEmbedBatchSize and halves, permanently, whenever a batch fails its
	// retries — see Embed. Sticky because the limit being discovered is a
	// property of the model and the machine, not of one request: retrying the
	// same oversized batch just kills the runner again and drops another 512
	// chunks on the floor.
	batch int
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
//
// Serialized process-wide: see the note on ollamaEmbedder. A mining run holds
// this lock for the length of one batch, so a concurrent search waits rather
// than racing the model runner. Batches are seconds, not minutes, so the wait
// is bounded in practice.
func (o *ollamaEmbedder) Embed(texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	if o.batch <= 0 {
		o.batch = ollamaEmbedBatchSize
	}

	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); {
		end := min(start+o.batch, len(texts))

		resp, err := o.embedBatchWithRetry(texts[start:end])
		if err != nil {
			// A batch that will not go through after retries is usually too
			// large: embeddinggemma's runner resets the connection partway
			// through tokenizing a big payload. Halve and retry the same span
			// rather than giving up on it — dropping the batch would store
			// these chunks with nil vectors, which is invisible until every
			// search over them silently misses.
			if o.shrinkLocked() {
				continue
			}
			return nil, err
		}
		if got, want := len(resp.Embeddings), end-start; got != want {
			return nil, fmt.Errorf("palace: ollama returned %d embeddings for %d inputs", got, want)
		}
		out = append(out, resp.Embeddings...)
		start = end
	}
	return out, nil
}

// shrinkLocked halves the batch size, reporting whether there was room to do so.
// Caller must hold mu.
func (o *ollamaEmbedder) shrinkLocked() bool {
	if o.batch <= ollamaMinEmbedBatch {
		return false
	}
	o.batch = max(o.batch/2, ollamaMinEmbedBatch)
	slog.Warn("palace: reducing ollama embed batch size after repeated transport errors",
		"batch", o.batch, "model", o.model)
	return true
}

// embedBatchWithRetry sends one batch, retrying transient transport failures.
//
// Serializing calls stops this process from causing a runner restart, but it
// cannot stop one: Ollama also evicts models on its own idle timer and under
// memory pressure, and a batch in flight when that happens dies with a reset
// connection. Without a retry the caller drops the whole batch — and in mining
// that means up to ollamaEmbedBatchSize chunks are stored with nil vectors,
// which is invisible until every semantic search over them silently misses.
//
// Embedding is a pure function of its input, so retrying is always safe. Only
// transport-shaped failures are retried; a bad model name or a malformed
// request fails the same way every time and should surface immediately.
func (o *ollamaEmbedder) embedBatchWithRetry(batch []string) (*api.EmbedResponse, error) {
	var lastErr error
	for attempt := range ollamaEmbedRetries {
		if attempt > 0 {
			// Linear backoff. The runner needs time to come back up, and a
			// tight retry would just meet the same closed socket.
			time.Sleep(time.Duration(attempt) * ollamaEmbedRetryDelay)
			slog.Warn("palace: retrying ollama embed batch after transport error",
				"attempt", attempt+1, "of", ollamaEmbedRetries, "size", len(batch), "error", lastErr)
		}

		resp, err := o.client.Embed(context.Background(), &api.EmbedRequest{
			Model: o.model,
			Input: batch,
		})
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isTransientOllamaErr(err) {
			return nil, fmt.Errorf("palace: ollama embed: %w", err)
		}
	}
	return nil, fmt.Errorf("palace: ollama embed failed after %d attempts: %w", ollamaEmbedRetries, lastErr)
}

// isTransientOllamaErr reports whether err looks like the daemon or its model
// runner went away mid-request, rather than a request that will never succeed.
func isTransientOllamaErr(err error) bool {
	if err == nil {
		return false
	}
	// A dropped runner surfaces as a reset/closed connection or a truncated
	// body. net.Error covers timeouts; the string checks cover the syscall-level
	// resets that the ollama api client wraps into plain errors.
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	s := err.Error()
	for _, frag := range []string{
		"connection reset by peer",
		"broken pipe",
		"unexpected EOF",
		"EOF",
		"connection refused",
		"server closed",
	} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
}

// Close is a no-op: the daemon owns the model's lifetime, not this process.
func (o *ollamaEmbedder) Close() {}
