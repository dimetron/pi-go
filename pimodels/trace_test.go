package pimodels_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/pimodels"
)

// sinkCollector is a trace sink that records what it was handed. Callbacks
// arrive on the request goroutine, so a mutex keeps the race detector honest.
type sinkCollector struct {
	mu      sync.Mutex
	entries []pimodels.TraceEntry
}

func (c *sinkCollector) fn() func(pimodels.TraceEntry) {
	return func(e pimodels.TraceEntry) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.entries = append(c.entries, e)
	}
}

func (c *sinkCollector) snapshot() []pimodels.TraceEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]pimodels.TraceEntry(nil), c.entries...)
}

// openAIServer stands in for any OpenAI-compatible endpoint.
func openAIServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m",` +
			`"choices":[{"index":0,"message":{"role":"assistant","content":"hi"},` +
			`"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,` +
			`"total_tokens":2}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// runTurn drives one request through the client so the transport has something
// to capture. Any error is tolerated: what is under test is the trace, not the
// completion, and a fake endpoint is not guaranteed to satisfy the SDK.
func runTurn(ctx context.Context, t *testing.T, m pimodels.Model) {
	t.Helper()
	for _, err := range m.GenerateContent(ctx, &model.LLMRequest{
		Contents: []*genai.Content{{
			Role:  "user",
			Parts: []*genai.Part{{Text: "hello"}},
		}},
	}, false) {
		_ = err
	}
}

// TestTraceSinkCapturesWithoutGlobalEnable is the whole point of the option.
//
// pi-go's --trace-http arrangement needs httplog.SetEnabled(true) AND a sink
// installed globally before a single byte is captured. An embedder sets neither
// — and a library must not, because the global sink is one slot shared by the
// whole process. So a client with a sink has to capture on its own, with global
// capture off.
func TestTraceSinkCapturesWithoutGlobalEnable(t *testing.T) {
	srv := openAIServer(t)
	ctx := context.Background()

	var got sinkCollector
	m, err := pimodels.New(ctx, "gpt-5.6-luna",
		pimodels.WithBaseURL(srv.URL),
		pimodels.WithAPIKey("test-key"),
		pimodels.WithTraceSink(got.fn()),
	)
	if err != nil {
		t.Fatalf("pimodels.New: %v", err)
	}

	runTurn(ctx, t, m)

	entries := got.snapshot()
	if len(entries) == 0 {
		t.Fatal("no trace entries reached the sink — a client with a sink must " +
			"capture without httplog.SetEnabled(true), which no embedder calls")
	}

	var sawRequest, sawResponse bool
	for _, e := range entries {
		switch e.Direction {
		case "request":
			sawRequest = true
			if e.Method != http.MethodPost {
				t.Errorf("request entry Method = %q, want POST", e.Method)
			}
			if !strings.Contains(e.URL, srv.URL) {
				t.Errorf("request entry URL = %q, want it to contain %q", e.URL, srv.URL)
			}
			// The API key is supplied as a bearer token; Redact must have
			// masked it before the sink saw it.
			for _, vs := range e.Headers {
				for _, v := range vs {
					if strings.Contains(v, "test-key") {
						t.Errorf("sink received an unmasked credential in header: %q", v)
					}
				}
			}
		case "response":
			sawResponse = true
			if e.Status != http.StatusOK {
				t.Errorf("response entry Status = %d, want 200", e.Status)
			}
		}
	}
	if !sawRequest || !sawResponse {
		t.Errorf("entries = %+v; want at least one request and one response", entries)
	}
}

// TestTraceSinkIsPerClient pins the property that made a field on the options
// the right shape: the global sink is a single slot that SetSink replaces, so
// two clients must be able to trace independently.
func TestTraceSinkIsPerClient(t *testing.T) {
	ctx := context.Background()
	var a, b sinkCollector

	ma, err := pimodels.New(ctx, "gpt-5.6-luna",
		pimodels.WithBaseURL(openAIServer(t).URL),
		pimodels.WithAPIKey("k"), pimodels.WithTraceSink(a.fn()))
	if err != nil {
		t.Fatalf("pimodels.New (a): %v", err)
	}
	mb, err := pimodels.New(ctx, "gpt-5.6-luna",
		pimodels.WithBaseURL(openAIServer(t).URL),
		pimodels.WithAPIKey("k"), pimodels.WithTraceSink(b.fn()))
	if err != nil {
		t.Fatalf("pimodels.New (b): %v", err)
	}
	// Built only to have a second live client; a turn on it is not needed,
	// since the property is that a's request never reaches b's sink.
	_ = mb

	runTurn(ctx, t, ma)
	if n := len(b.snapshot()); n != 0 {
		t.Errorf("client b's sink received %d entries from client a's request; "+
			"sinks must not be shared", n)
	}
	if len(a.snapshot()) == 0 {
		t.Error("client a's sink received nothing from its own request")
	}
}

// TestNoTraceSinkCapturesNothing is the control: without the option, an
// embedder gets nothing, which is what makes the option meaningful.
func TestNoTraceSinkCapturesNothing(t *testing.T) {
	ctx := context.Background()
	// A process-global sink would otherwise leak in from another test and make
	// this pass for the wrong reason. Nothing here installs one, and the
	// default is capture-off.
	m, err := pimodels.New(ctx, "gpt-5.6-luna",
		pimodels.WithBaseURL(openAIServer(t).URL),
		pimodels.WithAPIKey("k"))
	if err != nil {
		t.Fatalf("pimodels.New: %v", err)
	}
	// Nothing to assert on the sink — the point is that building without one
	// works and the turn completes. The absence of a panic or a nil deref
	// through traceTransport is the assertion.
	runTurn(ctx, t, m)
}

// TestTraceEntryFieldsReachable guards the alias. A consumer outside this
// module can only use the callback if every field it needs is nameable through
// pimodels, so a field rename in httplog has to fail here rather than silently
// break embedders.
func TestTraceEntryFieldsReachable(t *testing.T) {
	e := pimodels.TraceEntry{
		Exchange:      1,
		Direction:     "request",
		Method:        "POST",
		URL:           "https://example.test/v1",
		Proto:         "HTTP/2.0",
		Status:        200,
		Headers:       map[string][]string{"x": {"y"}},
		Body:          "{}",
		BodyTruncated: false,
		Err:           "",
	}
	if e.Direction != "request" || e.Status != 200 || e.Body != "{}" {
		t.Fatalf("TraceEntry fields not addressable as expected: %+v", e)
	}
	if e.Duration.Milliseconds() != 0 {
		t.Fatal("TraceEntry.Duration not addressable")
	}
}

// TestTraceMaxBodyMatchesTransport pins the documented cap to the real one, so
// the number in the README cannot drift from the transport's behavior.
func TestTraceMaxBodyMatchesTransport(t *testing.T) {
	if got := pimodels.TraceMaxBody(); got != 1<<20 {
		t.Errorf("TraceMaxBody() = %d, want %d", got, 1<<20)
	}
}
