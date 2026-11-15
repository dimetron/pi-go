package provider

import (
	"context"
	"fmt"
	"iter"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
)

// fmDefaultBaseURL is where `fm serve` listens by default — Apple's Foundation
// Models CLI, which exposes an OpenAI-compatible Chat Completions API on
// loopback. The OpenAI client appends /v1 to it.
const fmDefaultBaseURL = "http://127.0.0.1:1976"

// fmModelSystem and fmModelPCC are the two model IDs `fm serve` advertises on
// /v1/models. "pcc" is Apple's Private Cloud Compute variant, which reports
// itself unavailable unless fm was started from the Terminal app; it is listed
// here anyway so the name resolves to a clear upstream error instead of
// "unknown model".
const (
	fmModelSystem = "system"
	fmModelPCC    = "pcc"
)

// fmMaxOutputTokens caps a reply on this provider.
//
// The model's own ceiling is small — a turn has to fit in the same 8K budget —
// so 64000, the default for the OpenAI-compatible paths, would describe a limit
// that does not exist here.
const fmMaxOutputTokens int64 = 4096

// fmHealthProbeTimeout bounds one reachability check. fm answers a loopback
// /health in single-digit milliseconds, so this only has to outlast a machine
// under load, not a network round trip.
const fmHealthProbeTimeout = 750 * time.Millisecond

// fmStartupTimeout bounds how long EnsureFoundationModelsServer waits for a
// server it launched itself. fm needs a few hundred milliseconds to load the
// on-device model and bind; a machine under memory pressure can take longer,
// but not indefinitely, and blocking a turn forever on a server that failed to
// start is worse than reporting that it is not there.
const fmStartupTimeout = 8 * time.Second

// NewFoundationModels creates a model.LLM backed by a local `fm serve`.
//
// No API key is required, or possible: fm listens on loopback and is bound to
// no credential. NewOpenAI substitutes a dummy key whenever a base URL is set,
// which is what lets this reuse the entire OpenAI-compatible path.
//
// The context window is 8192 tokens — Apple's on-device ceiling, measured
// against a running server: a request reporting 8096 prompt tokens succeeds and
// anything past 8192 is rejected with "Provided N tokens, but the maximum
// allowed is 8192". That is smaller than pi-go's own default system prompt, so
// this provider suits roles that pass no tools; the catalog entry in
// modeldata/context-windows.json keeps auto-compaction honest about the size.
func NewFoundationModels(ctx context.Context, modelName, apiKey, baseURL string, opts *LLMOptions) (model.LLM, error) {
	if !isFoundationModel(modelName) {
		return nil, fmt.Errorf("unknown Foundation Models model %q; known models: %s, %s",
			modelName, fmModelSystem, fmModelPCC)
	}
	if baseURL == "" {
		baseURL = fmDefaultBaseURL
	}
	if opts == nil {
		opts = &LLMOptions{}
	}
	if opts.MaxOutputTokens <= 0 {
		opts.MaxOutputTokens = fmMaxOutputTokens
	}
	llm, err := NewOpenAI(ctx, modelName, apiKey, baseURL, opts)
	if err != nil {
		return nil, fmt.Errorf("creating Foundation Models client: %w", err)
	}
	return fmAlwaysStreamingModel{LLM: llm}, nil
}

// fmAlwaysStreamingModel forces the streaming code path regardless of what the
// caller asked for.
//
// `fm serve` streams from /v1/chat/completions unless the request says
// `stream: false`, and the OpenAI SDK never sends that field — it is not part of
// ChatCompletionNewParams, because OpenAI's own API treats an absent field as
// non-streaming. So a non-streaming call sends no field at all, fm answers with
// Server-Sent Events, and the SDK fails before reading a token:
//
//	expected destination type of 'string' or '[]byte' for responses with
//	content-type 'text/event-stream' that is not 'application/json'
//
// Measured against `fm serve`, not inferred. Forcing the streaming path avoids
// the mismatch instead of rewriting the wire: it is the shape this server always
// produces, and the streaming accumulator already yields a final non-partial
// response, so a caller that asked for one response still gets one.
type fmAlwaysStreamingModel struct {
	model.LLM
}

func (m fmAlwaysStreamingModel) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return m.LLM.GenerateContent(ctx, req, true)
}

// isFoundationModel reports whether name is one of the models `fm serve`
// serves.
//
// Matched exactly rather than by prefix like every other provider: the two IDs
// are fixed words as short as "system", and a prefix rule would accept anything
// that happened to start with one of them.
func isFoundationModel(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case fmModelSystem, fmModelPCC:
		return true
	}
	return false
}

// FoundationModelIDs returns the model IDs `fm serve` serves, in the order they
// are worth offering: the on-device model first, since "pcc" is unavailable
// unless fm was started from the Terminal app.
//
// Exported so the setup wizard offers names ValidateModel accepts rather than
// making the user recall that the model is called "system".
func FoundationModelIDs() []string {
	return []string{fmModelSystem, fmModelPCC}
}

// FoundationModelBaseURL returns the endpoint a model is served from: the
// explicit value when one was given, otherwise the default.
func FoundationModelBaseURL(baseURL string) string {
	if baseURL != "" {
		return baseURL
	}
	return fmDefaultBaseURL
}

// ensureHook lets a test replace the reachability probe and the launcher, so
// the start-if-absent decision is testable without a real fm on the machine.
//
// A package-level var rather than a field on LLMOptions: the wiring lives in the
// CLI and pimodels, and both would have to thread a hook through for no
// production benefit — the alternative, no test at all, is how a "start it if it
// is not running" path ends up never exercised.
var ensureHook = struct {
	probe   func(baseURL string) bool
	launch  func() (<-chan error, error)
	timeout time.Duration
}{
	probe:   fmReachable,
	launch:  launchFoundationModelsServer,
	timeout: fmStartupTimeout,
}

// EnsureFoundationModelsServer makes sure an `fm serve` is answering at
// baseURL, starting one if nothing is there yet.
//
// This is laziness, not supervision: a server already running is left alone, one
// started here is detached so it outlives pi, and nothing is ever stopped. A
// user who started `fm serve` themselves keeps their server; a user who did not
// gets one, once.
//
// Only the default endpoint is ever launched. A model pointed at an explicit
// --url is probed and reported, never started: fm's port and socket arguments
// are the user's business, and guessing them would mean spawning a server on a
// port nobody asked for.
func EnsureFoundationModelsServer(baseURL string) error {
	target := FoundationModelBaseURL(baseURL)
	if ensureHook.probe(target) {
		return nil
	}
	if baseURL != "" {
		return fmt.Errorf("no Foundation Models server is responding at %s; start it with `fm serve`", target)
	}

	started, err := ensureHook.launch()
	if err != nil {
		return fmt.Errorf("starting `fm serve`: %w", err)
	}

	deadline := time.NewTimer(ensureHook.timeout)
	defer deadline.Stop()
	// Poll rather than wait on the process alone: fm reports a bind failure by
	// exiting, but a healthy one prints its banner and says nothing more, so
	// readiness has to be observed from the outside either way.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if ensureHook.probe(target) {
				return nil
			}
		case exitErr, ok := <-started:
			if !ok {
				started = nil // closed with no verdict; keep polling until the deadline
				continue
			}
			// The process exited. One more probe, because a server that bound and
			// then exited — or that another process started meanwhile — may still
			// leave something answering.
			if ensureHook.probe(target) {
				return nil
			}
			if exitErr != nil {
				return fmt.Errorf("`fm serve` exited while starting: %w (log: %s)", exitErr, fmServerLogPath())
			}
			return fmt.Errorf("`fm serve` exited while starting; see %s", fmServerLogPath())
		case <-deadline.C:
			return fmt.Errorf("`fm serve` did not answer at %s within %s; see %s",
				target, ensureHook.timeout, fmServerLogPath())
		}
	}
}

// fmReachable reports whether something answers at baseURL's /health with a 200.
func fmReachable(baseURL string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), fmHealthProbeTimeout)
	defer cancel()

	url := strings.TrimRight(FoundationModelBaseURL(baseURL), "/")
	if !strings.HasSuffix(url, "/health") {
		url += "/health"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	client, err := BuildHTTPClient(nil, fmHealthProbeTimeout)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// fmServerLogPath is where a server started by EnsureFoundationModelsServer
// writes its output. Detached from the terminal by necessity — a write to stdout
// while the TUI holds the alternate screen would corrupt the display — so its
// diagnostics have to land somewhere a user can read afterwards.
func fmServerLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "pi-go-fm-serve.log")
	}
	return filepath.Join(home, ".pi-go", "fm-serve.log")
}
