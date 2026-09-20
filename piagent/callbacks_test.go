package piagent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adktool "google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/memory"
)

// namedTool is the minimum an after-tool callback needs from a tool: its name.
type namedTool struct {
	adktool.Tool
	name string
}

func (n namedTool) Name() string { return n.name }

// recordingCallback returns a callback that appends its name to log and hands
// back result.
func recordingCallback(log *[]string, name string, result map[string]any, err error) llmagent.AfterToolCallback {
	return func(_ adkagent.Context, _ adktool.Tool, _, _ map[string]any, _ error) (map[string]any, error) {
		*log = append(*log, name)
		return result, err
	}
}

func TestComposeAfterToolRunsEveryCallback(t *testing.T) {
	// This is the regression that motivates the whole package: handed to ADK
	// as a slice, only the first of these would run.
	var log []string
	cb := composeAfterTool([]llmagent.AfterToolCallback{
		recordingCallback(&log, "first", map[string]any{"n": 1}, nil),
		recordingCallback(&log, "second", map[string]any{"n": 2}, nil),
		recordingCallback(&log, "third", map[string]any{"n": 3}, nil),
	})

	got, err := cb(nil, namedTool{name: "read"}, nil, map[string]any{"n": 0}, nil)
	if err != nil {
		t.Fatalf("composed callback: %v", err)
	}
	if len(log) != 3 {
		t.Fatalf("ran %v, want all three callbacks", log)
	}
	if got["n"] != 3 {
		t.Errorf("result = %v, want the last callback's value", got)
	}
}

func TestComposeAfterToolFeedsResultsForward(t *testing.T) {
	var seen []any
	observe := func(_ adkagent.Context, _ adktool.Tool, _, result map[string]any, _ error) (map[string]any, error) {
		seen = append(seen, result["n"])
		return map[string]any{"n": result["n"].(int) + 1}, nil
	}
	cb := composeAfterTool([]llmagent.AfterToolCallback{observe, observe})

	got, err := cb(nil, namedTool{name: "read"}, nil, map[string]any{"n": 0}, nil)
	if err != nil {
		t.Fatalf("composed callback: %v", err)
	}
	if len(seen) != 2 || seen[0] != 0 || seen[1] != 1 {
		t.Errorf("callbacks saw %v, want each to see the previous one's output", seen)
	}
	if got["n"] != 2 {
		t.Errorf("result = %v, want 2", got)
	}
}

func TestComposeAfterToolNilMeansUnchanged(t *testing.T) {
	var log []string
	cb := composeAfterTool([]llmagent.AfterToolCallback{
		recordingCallback(&log, "observer-a", nil, nil),
		recordingCallback(&log, "observer-b", nil, nil),
	})

	got, err := cb(nil, namedTool{name: "read"}, nil, map[string]any{"n": 7}, nil)
	if err != nil {
		t.Fatalf("composed callback: %v", err)
	}
	if got != nil {
		t.Errorf("result = %v, want nil so ADK keeps the tool's own result", got)
	}
	if len(log) != 2 {
		t.Errorf("ran %v, want both observers", log)
	}
}

func TestComposeAfterToolAbortsOnError(t *testing.T) {
	var log []string
	want := errors.New("policy violation")
	cb := composeAfterTool([]llmagent.AfterToolCallback{
		recordingCallback(&log, "first", nil, nil),
		recordingCallback(&log, "blocker", nil, want),
		recordingCallback(&log, "never", nil, nil),
	})

	got, err := cb(nil, namedTool{name: "bash"}, nil, map[string]any{}, nil)
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if got != nil {
		t.Errorf("result = %v, want nil on abort", got)
	}
	if strings.Join(log, ",") != "first,blocker" {
		t.Errorf("ran %v, want the chain to stop at the failing callback", log)
	}
}

func TestComposeAfterToolEmptyChain(t *testing.T) {
	if cb := composeAfterTool(nil); cb != nil {
		t.Error("composeAfterTool(nil) should return nil, not an empty wrapper")
	}
	if cb := composeAfterTool([]llmagent.AfterToolCallback{nil, nil}); cb != nil {
		t.Error("a chain of nils should compose to nil")
	}
}

// sessionCtx is an adkagent.Context whose session ID a test controls.
//
// Embedding the interface gets the rest of the method set for free; only
// SessionID is ever read by the callback under test, so leaving the embedded
// interface nil is safe here.
type sessionCtx struct {
	adkagent.Context
	id string
}

func (c sessionCtx) SessionID() string { return c.id }

// newCollectingWorker returns a started worker that records into got, plus a
// stop function that drains it. The compressor is a passthrough so a test can
// assert on attribution without spawning a real compression subagent.
func newCollectingWorker(t *testing.T, got *[]memory.RawObservation) (*memory.Worker, func()) {
	t.Helper()
	w := memory.NewWorker(recordingStoreFor{into: got}, passthroughCompressor{}, 64)
	w.Start(t.Context())
	return w, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := w.Shutdown(ctx); err != nil {
			t.Fatalf("worker shutdown: %v", err)
		}
	}
}

// passthroughCompressor turns a raw observation into a stored one without an
// LLM call, preserving the fields attribution is asserted on.
type passthroughCompressor struct{}

func (passthroughCompressor) CompressObservation(_ context.Context, raw memory.RawObservation) (*memory.Observation, error) {
	return &memory.Observation{
		SessionID: raw.SessionID,
		Project:   raw.Project,
		Title:     raw.ToolName,
		Type:      memory.TypeChange,
		ToolName:  raw.ToolName,
		CreatedAt: raw.Timestamp,
	}, nil
}

// recordingStoreFor is a memory.Store that collects inserted observations. The
// callback under test only needs InsertObservation; the rest of the interface is
// unreachable through it, so embedding keeps the stub honest about that.
type recordingStoreFor struct {
	memory.Store
	into *[]memory.RawObservation
}

func (s recordingStoreFor) InsertObservation(_ context.Context, obs *memory.Observation) error {
	*s.into = append(*s.into, memory.RawObservation{
		SessionID: obs.SessionID,
		Project:   obs.Project,
		ToolName:  obs.ToolName,
	})
	return nil
}

func TestMemoryObservationCallback(t *testing.T) {
	cfg := config.Config{Memory: &config.MemoryConfig{ExcludedTools: []string{"bash"}}}

	t.Run("skips failed and excluded calls, attributes by context session", func(t *testing.T) {
		var got []memory.RawObservation
		worker, stop := newCollectingWorker(t, &got)
		cb := memoryObservationCallback(worker, cfg, "/project")

		ctx := sessionCtx{id: "s1"}
		for _, tc := range []struct {
			name    string
			ctx     adkagent.Context
			tool    string
			toolErr error
		}{
			{"no session", sessionCtx{id: ""}, "read", nil},
			{"failed tool call", ctx, "read", errors.New("nope")},
			{"excluded tool", ctx, "bash", nil},
			{"recorded", ctx, "read", nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				out, err := cb(tc.ctx, namedTool{name: tc.tool}, map[string]any{"a": 1}, map[string]any{"b": 2}, tc.toolErr)
				if err != nil {
					t.Fatalf("callback: %v", err)
				}
				// The callback only observes; it must never rewrite the result.
				if out != nil {
					t.Errorf("result = %v, want nil", out)
				}
			})
		}

		stop()
		if len(got) != 1 {
			t.Fatalf("recorded %d observations, want 1 (only the successful non-excluded call)", len(got))
		}
		if got[0].SessionID != "s1" {
			t.Errorf("SessionID = %q, want s1 — attribution must come from the call context", got[0].SessionID)
		}
		if got[0].ToolName != "read" {
			t.Errorf("ToolName = %q, want read", got[0].ToolName)
		}
	})

	// The regression this guards: the session ID used to be read from a single
	// shared variable captured at build time, so interleaved sessions could have
	// one session's tool calls recorded under another's ID.
	t.Run("interleaved sessions keep their own attribution", func(t *testing.T) {
		var got []memory.RawObservation
		worker, stop := newCollectingWorker(t, &got)
		cb := memoryObservationCallback(worker, config.Config{}, "/project")

		a := sessionCtx{id: "session-a"}
		b := sessionCtx{id: "session-b"}

		for i := 0; i < 3; i++ {
			if _, err := cb(a, namedTool{name: "read"}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := cb(b, namedTool{name: "edit"}, nil, nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		stop()

		if len(got) != 6 {
			t.Fatalf("recorded %d observations, want 6", len(got))
		}
		for i, o := range got {
			want := "session-a"
			if i%2 == 1 {
				want = "session-b"
			}
			if o.SessionID != want {
				t.Errorf("observation %d SessionID = %q, want %q — sessions bled into each other",
					i, o.SessionID, want)
			}
		}
	})
}

func TestBuildCallbacksComposesAfterToolIntoOne(t *testing.T) {
	isolate(t)
	before, after := buildCallbacks(callbackDeps{
		cfg:      config.Config{},
		provider: "anthropic",
		opts:     defaultOptions(),
	})

	if len(after.tool) != 1 {
		t.Errorf("after-tool callbacks = %d, want exactly 1 composed callback", len(after.tool))
	}
	if len(before.model) == 0 {
		t.Error("before-model callbacks are empty; tracing and image reading should be wired")
	}
}

func TestBuildCallbacksIncludesEmbedderCallbacks(t *testing.T) {
	isolate(t)
	var ran []string
	o := defaultOptions()
	o.afterTool = []llmagent.AfterToolCallback{recordingCallback(&ran, "embedder", nil, nil)}
	o.beforeTool = []llmagent.BeforeToolCallback{
		func(_ adkagent.Context, _ adktool.Tool, _ map[string]any) (map[string]any, error) { return nil, nil },
	}
	o.beforeModel = []llmagent.BeforeModelCallback{nil}
	o.afterModel = []llmagent.AfterModelCallback{nil}

	before, after := buildCallbacks(callbackDeps{
		cfg:  config.Config{},
		opts: o,
	})
	if len(before.tool) == 0 {
		t.Error("before-tool callbacks are empty; the embedder's was dropped")
	}
	if len(after.model) == 0 {
		t.Error("after-model callbacks are empty; the embedder's was dropped")
	}
	// That the embedder's after-tool callback actually runs is proven by a
	// real turn in TestEmbedderAfterToolCallbackSeesEveryToolCall; here the
	// chain is folded into one entry, so counting is all this can assert.
	if len(after.tool) != 1 {
		t.Errorf("after-tool callbacks = %d, want exactly 1 composed callback", len(after.tool))
	}
}
