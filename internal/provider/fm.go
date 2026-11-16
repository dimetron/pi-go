package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
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

// fmContextWindow is the token budget `fm serve` enforces on the whole request,
// measured against a running server rather than taken from a spec sheet.
//
// The server names the number itself when it rejects a request ("Provided 8,910
// tokens, but the maximum allowed is 8,192"), and bisecting a plain-text prompt
// agrees: the largest accepted request was 32,812 characters of prose, which is
// 8,203 tokens at the calibrated rate in fmCharsPerToken.
//
// What fits, measured: the on-device model, a short prompt, and all 40 of pi's
// tool declarations — 7,749 tokens of the 8,192, and it answers with tool calls.
// What does not fit: pi-go's default system prompt, 13,376 tokens by itself, so
// the coding agent's turn can never be sent here. That is the conclusion the
// numbers force, and fmMeasureRequest is what turns it into a message instead of
// a 400.
const fmContextWindow int64 = 8192

// fmCharsPerToken converts the rendered request into tokens.
//
// 4 is the calibrated value, checked against the server's own token counts for
// four requests rather than assumed. Measured wire characters per server-reported
// token: 4.20, 4.19, 4.41 and 4.01. Divisors of 3 and 5 were both tried and both
// misclassified real requests — 3 refused a 40-tool request the server accepts
// (predicting 10,840 tokens against an actual 7,749), and 5 passed a request the
// server rejects (6,910 against an actual 8,249).
//
// At 4 the only disagreement is a no-tools prompt of 32,812 characters: the
// server accepts it at exactly 8,192, and this estimate calls it 8,203. That errs
// toward refusing, which is the direction to be wrong in — the alternative is a
// round trip that ends in "Provided 8,910 tokens, but the maximum allowed is
// 8,192" with nothing the user can act on.
const fmCharsPerToken = 4

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
// anything past 8192 is rejected. That is smaller than pi-go's own default
// system prompt, so the request is checked against it before it is sent rather
// than being answered with an opaque rejection; see fmMeasureRequest.
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

// fmAlwaysStreamingModel rewrites each request into the shape `fm serve`
// accepts, then forces the streaming code path regardless of what the caller
// asked for.
//
// Two independent defects are fixed here, both measured against a running
// server rather than inferred from documentation.
//
// # Streaming
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
// Forcing the streaming path avoids the mismatch instead of rewriting the wire:
// it is the shape this server always produces, and the streaming accumulator
// already yields a final non-partial response, so a caller that asked for one
// response still gets one.
//
// # Tool schemas and the context window
//
// fm compiles every tool schema into a Swift GenerationSchema and rejects the
// whole request — 400, naming no field — if any one of them uses a keyword
// outside that model. pi's own tool set uses several, so *every* tool-using fm
// turn failed. fmSanitizeTools rewrites them; the rejected keywords and the
// measurements behind them are documented there.
//
// The window is the second wall: 8192 tokens. Sanitized, the tool declarations
// fit (7,749 tokens with a short prompt, and the model answers). pi-go's default
// system prompt does not — 13,376 on its own — so the coding agent's turn can
// never be sent here, however the schemas are rewritten. A request that cannot
// fit is refused with a message naming the limit, the per-part breakdown, and
// the largest part, instead of spending a round trip to be told "400 Bad
// Request" with nothing to act on.
type fmAlwaysStreamingModel struct {
	model.LLM
}

func (m fmAlwaysStreamingModel) GenerateContent(ctx context.Context, req *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	fmPrepareRequest(req)
	if budget := fmMeasureRequest(req); budget.total > fmContextWindow {
		return func(yield func(*model.LLMResponse, error) bool) {
			yield(nil, fmt.Errorf(
				"the request is roughly %d tokens, over the %d-token context window of `fm serve` "+
					"(%d for the system prompt, %d for the transcript, %d for tool declarations). "+
					"Foundation Models is an on-device model with an 8K window: %s",
				budget.total, fmContextWindow, budget.system, budget.contents, budget.tools,
				budget.advice()))
		}
	}
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

// fmPrepareRequest rewrites a request into the shape `fm serve` accepts: tool
// schemas limited to the JSON Schema subset it can compile.
//
// It mutates in place, which is what the caller wants — the same request is
// what any later inspection and the session log will show.
func fmPrepareRequest(req *model.LLMRequest) {
	if req == nil {
		return
	}
	fmSanitizeTools(req.Config)
}

// fmBudget is a request's estimated token spend, split into the parts a user can
// actually do something about.
//
// The split exists because the total alone misleads. The first version of this
// guard reported only a total and blamed the system prompt; against a request
// whose tools were the bulk of it, that sent the user to change the wrong thing.
type fmBudget struct {
	system   int64
	contents int64
	tools    int64
	total    int64
}

// advice names the largest part of the request, since that is what has to shrink
// for the request to fit.
func (b fmBudget) advice() string {
	switch {
	case b.tools >= b.system && b.tools >= b.contents:
		return "the tool declarations are the largest part, so run fm with a role that registers no tools " +
			"(or a smaller tool set), or use a different model for this session"
	case b.system >= b.contents:
		return "the system prompt is the largest part, so pass a short one with --system, or use a " +
			"different model for this session"
	default:
		return "the transcript is the largest part, so start a new session or compact this one, or use " +
			"a different model for this session"
	}
}

// fmMeasureRequest reports the token budget a request will spend at `fm serve`.
//
// The count covers everything the server counts — the system instruction, the
// transcript, and the tool declarations — because fm enforces one budget across
// all of them. Checking only the prompt would pass a request that the tool
// declarations alone push over the limit.
//
// It measures the *wire* shape rather than the genai structs, because that is
// what the server reads. Measuring the difference was not academic: counting only
// names, descriptions and parameter schemas under-reported a real 40-tool request
// by enough to let it through, and the server answered
//
//	500 Provided 8,910 tokens, but the maximum allowed is 8,192.
func fmMeasureRequest(req *model.LLMRequest) fmBudget {
	if req == nil {
		return fmBudget{}
	}
	var b fmBudget
	tokens := func(s string) int64 { return int64(len(s) / fmCharsPerToken) }

	if req.Config != nil {
		b.system = tokens(fmJSONString(fmWireSystemInstruction(req.Config.SystemInstruction)))
		if tools := fmWireTools(req.Config.Tools); len(tools) > 0 {
			b.tools = tokens(fmJSONString(tools))
		}
	}
	var contents strings.Builder
	for _, c := range req.Contents {
		contents.WriteString(fmJSONString(fmWireContent(c)))
	}
	b.contents = tokens(contents.String())
	b.total = b.system + b.contents + b.tools
	return b
}

// fmWireSystemInstruction renders the system instruction the way the OpenAI
// request carries it.
func fmWireSystemInstruction(c *genai.Content) any {
	if c == nil {
		return nil
	}
	return map[string]any{"role": "system", "content": fmContentText(c)}
}

// fmWireContent renders one transcript entry the way the request carries it.
func fmWireContent(c *genai.Content) any {
	if c == nil {
		return nil
	}
	role := c.Role
	if role == "" || role == "model" {
		role = "assistant"
	}
	return map[string]any{"role": role, "content": fmContentText(c)}
}

// fmContentText joins a content's parts into the text the server sees.
//
// Tool calls and their results are rendered with their payloads rather than
// dropped: a tool result can be tens of kilobytes, and it is exactly the part
// that blows the window.
func fmContentText(c *genai.Content) string {
	var b strings.Builder
	for _, p := range c.Parts {
		if p == nil {
			continue
		}
		b.WriteString(p.Text)
		if p.FunctionCall != nil {
			b.WriteString(p.FunctionCall.Name)
			b.WriteString(fmJSONString(p.FunctionCall.Args))
		}
		if p.FunctionResponse != nil {
			b.WriteString(p.FunctionResponse.Name)
			b.WriteString(fmJSONString(p.FunctionResponse.Response))
		}
	}
	return b.String()
}

// fmWireTools renders the tool declarations the way the OpenAI request carries
// them, wrapper included.
func fmWireTools(tools []*genai.Tool) []any {
	var out []any
	for _, t := range tools {
		if t == nil {
			continue
		}
		for _, fd := range t.FunctionDeclarations {
			if fd == nil {
				continue
			}
			params := fd.ParametersJsonSchema
			if params == nil {
				params = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			out = append(out, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        fd.Name,
					"description": fd.Description,
					"parameters":  params,
				},
			})
		}
	}
	return out
}

// fmJSONString encodes v, returning "" when it cannot be encoded — a value that
// will not marshal contributes nothing to a count anyway.
func fmJSONString(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
