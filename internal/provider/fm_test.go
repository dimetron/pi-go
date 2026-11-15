package provider

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"
)

func TestResolveFoundationModelsPrefix(t *testing.T) {
	tests := []struct {
		name     string
		modelArg string
		wantProv string
		wantMod  string
	}{
		{"system", "fm/system", "fm", "system"},
		{"pcc", "fm/pcc", "fm", "pcc"},
		{"uppercase prefix and model", "FM/System", "fm", "System"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, err := Resolve(tc.modelArg)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", tc.modelArg, err)
			}
			if info.Provider != tc.wantProv {
				t.Errorf("Provider = %q, want %q", info.Provider, tc.wantProv)
			}
			if info.Model != tc.wantMod {
				t.Errorf("Model = %q, want %q", info.Model, tc.wantMod)
			}
			// Not Custom: a bare fm/ name must still get the default endpoint and
			// the lazy server start, both of which a Custom mark would suppress.
			if info.Custom {
				t.Error("Custom = true, want false for a plain fm/ model")
			}
		})
	}
}

// TestResolveFoundationModelsBareNameRejected pins the reason the prefix is
// mandatory. "system" and "pcc" are ordinary words; if Resolve accepted them,
// a user typing --model system would silently be routed to a loopback port
// instead of seeing an unknown-model error.
func TestResolveFoundationModelsBareNameRejected(t *testing.T) {
	for _, name := range []string{"system", "pcc"} {
		if _, err := Resolve(name); err == nil {
			t.Errorf("Resolve(%q) succeeded, want an unknown-model error", name)
		}
	}
}

// TestFoundationModelContextWindow pins the 8192-token ceiling.
//
// This is the number auto-compaction keys off. A missing entry returns 0, which
// disables compaction and lets a session grow until the server rejects it —
// the window here is smaller than pi-go's own default system prompt, so getting
// it wrong fails on the very first turn rather than eventually.
func TestFoundationModelContextWindow(t *testing.T) {
	for _, name := range []string{"system", "pcc"} {
		if got := ContextWindowSizeFor("fm", name); got != 8192 {
			t.Errorf("ContextWindowSizeFor(\"fm\", %q) = %d, want 8192", name, got)
		}
	}
}

func TestNewFoundationModelsUnknownModel(t *testing.T) {
	_, err := NewFoundationModels(context.Background(), "gpt-5.2", "", "", nil)
	if err == nil {
		t.Fatal("NewFoundationModels accepted a non-fm model, want an error")
	}
	if !strings.Contains(err.Error(), "unknown Foundation Models model") {
		t.Errorf("error = %q, want it to name the unknown model", err)
	}
}

// TestNewFoundationModelsAppliesDefaults checks the two defaults the fm path
// depends on: the loopback endpoint, and a reply cap that fits the model's real
// budget rather than the 64000 the OpenAI-compatible default would claim.
func TestNewFoundationModelsAppliesDefaults(t *testing.T) {
	opts := &LLMOptions{}
	llm, err := NewFoundationModels(context.Background(), "system", "", "", opts)
	if err != nil {
		t.Fatalf("NewFoundationModels: %v", err)
	}
	if llm == nil {
		t.Fatal("NewFoundationModels returned a nil model")
	}
	if opts.MaxOutputTokens != fmMaxOutputTokens {
		t.Errorf("MaxOutputTokens = %d, want %d", opts.MaxOutputTokens, fmMaxOutputTokens)
	}

	// An explicit cap must survive: a caller that knows better is not overridden.
	explicit := &LLMOptions{MaxOutputTokens: 512}
	if _, err := NewFoundationModels(context.Background(), "system", "", "", explicit); err != nil {
		t.Fatalf("NewFoundationModels: %v", err)
	}
	if explicit.MaxOutputTokens != 512 {
		t.Errorf("MaxOutputTokens = %d, want the caller's 512", explicit.MaxOutputTokens)
	}
}

func TestFoundationModelBaseURL(t *testing.T) {
	if got := FoundationModelBaseURL(""); got != fmDefaultBaseURL {
		t.Errorf("default = %q, want %q", got, fmDefaultBaseURL)
	}
	if got := FoundationModelBaseURL("http://127.0.0.1:9999"); got != "http://127.0.0.1:9999" {
		t.Errorf("explicit = %q, want it passed through", got)
	}
}

// recordingLLM captures the stream flag it was called with, so the wrapper's
// behavior is observed rather than assumed.
type recordingLLM struct {
	gotStream *bool
	replies   []string
}

func (r *recordingLLM) Name() string { return "recording" }

func (r *recordingLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if r.gotStream != nil {
			*r.gotStream = stream
		}
		for _, text := range r.replies {
			yield(&model.LLMResponse{
				Content: &genai.Content{Role: "model", Parts: []*genai.Part{{Text: text}}},
			}, nil)
		}
	}
}

// TestFMAlwaysStreamingForcesStream is the regression guard for the bug that
// made every non-streaming fm call fail with
//
//	expected destination type of 'string' or '[]byte' for responses with
//	content-type 'text/event-stream'
//
// fm streams unless the request says stream:false, and the OpenAI SDK never
// sends that field, so the non-streaming path could never work against it.
func TestFMAlwaysStreamingForcesStream(t *testing.T) {
	var gotStream bool
	inner := &recordingLLM{gotStream: &gotStream, replies: []string{"WIRED"}}
	wrapped := fmAlwaysStreamingModel{LLM: inner}

	req := &model.LLMRequest{
		Contents: []*genai.Content{{Role: "user", Parts: []*genai.Part{{Text: "hi"}}}},
	}

	// Called with stream=false, which is what the memory summarizer and every
	// non-TUI caller does.
	var texts []string
	for resp, err := range wrapped.GenerateContent(context.Background(), req, false) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		if resp.Content != nil {
			for _, p := range resp.Content.Parts {
				texts = append(texts, p.Text)
			}
		}
	}

	if !gotStream {
		t.Fatal("inner model was called with stream=false; fm requires streaming")
	}
	if len(texts) != 1 || texts[0] != "WIRED" {
		t.Errorf("responses = %v, want the inner reply preserved", texts)
	}
}

// TestEnsureFoundationModelsServer drives the start-if-absent decision through
// the injectable hook, with no real fm involved.
func TestEnsureFoundationModelsServer(t *testing.T) {
	orig := ensureHook
	t.Cleanup(func() { ensureHook = orig })

	closedChan := func(err error) <-chan error {
		ch := make(chan error, 1)
		ch <- err
		close(ch)
		return ch
	}

	tests := []struct {
		name         string
		baseURL      string
		probeResults []bool // consumed one per call; last value repeats
		launchErr    error
		launchChan   <-chan error
		wantErr      bool
		wantLaunch   bool
		errContains  string
	}{
		{
			name:         "already running is left alone",
			probeResults: []bool{true},
			wantErr:      false,
			wantLaunch:   false,
		},
		{
			name:         "absent server is started",
			probeResults: []bool{false, true},
			launchChan:   closedChan(nil),
			wantErr:      false,
			wantLaunch:   true,
		},
		{
			name:         "fm not installed reports the launch error",
			probeResults: []bool{false},
			launchErr:    errors.New("fm not found in PATH"),
			wantErr:      true,
			wantLaunch:   true,
			errContains:  "fm not found in PATH",
		},
		{
			// A custom endpoint is probed, never launched: fm's port arguments are
			// the user's business.
			name:         "custom base URL is not launched",
			baseURL:      "http://127.0.0.1:9999",
			probeResults: []bool{false},
			wantErr:      true,
			wantLaunch:   false,
			errContains:  "start it with `fm serve`",
		},
		{
			name:         "exiting without serving is an error",
			probeResults: []bool{false, false},
			launchChan:   closedChan(nil),
			wantErr:      true,
			wantLaunch:   true,
			errContains:  "exited while starting",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			probes := 0
			launched := false

			ensureHook = struct {
				probe   func(string) bool
				launch  func() (<-chan error, error)
				timeout time.Duration
			}{
				probe: func(string) bool {
					idx := probes
					if idx >= len(tc.probeResults) {
						idx = len(tc.probeResults) - 1
					}
					probes++
					return tc.probeResults[idx]
				},
				launch: func() (<-chan error, error) {
					launched = true
					if tc.launchErr != nil {
						return nil, tc.launchErr
					}
					return tc.launchChan, nil
				},
				// Short enough that the "exits without serving" case does not wait
				// the real 8s, long enough for a few 50ms polls.
				timeout: 200 * time.Millisecond,
			}

			err := EnsureFoundationModelsServer(tc.baseURL)
			if tc.wantErr && err == nil {
				t.Fatal("want an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.errContains)
			}
			if launched != tc.wantLaunch {
				t.Errorf("launched = %v, want %v", launched, tc.wantLaunch)
			}
		})
	}
}

// TestEnsureFoundationModelsServerTimesOut covers the case where the process
// starts, never serves, and never exits: a server that cannot start must not
// block a turn forever.
func TestEnsureFoundationModelsServerTimesOut(t *testing.T) {
	orig := ensureHook
	t.Cleanup(func() { ensureHook = orig })

	never := make(chan error) // never written, never closed
	ensureHook.probe = func(string) bool { return false }
	ensureHook.launch = func() (<-chan error, error) { return never, nil }
	ensureHook.timeout = 120 * time.Millisecond

	start := time.Now()
	err := EnsureFoundationModelsServer("")
	if err == nil {
		t.Fatal("want a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("error = %q, want it to report the timeout", err.Error())
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s; the injected timeout should bound it", elapsed)
	}
}
