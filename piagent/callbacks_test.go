package piagent

import (
	"errors"
	"strings"
	"testing"

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

func TestMemoryObservationCallback(t *testing.T) {
	worker := memory.NewWorker(nil, nil, 8)
	cfg := config.Config{Memory: &config.MemoryConfig{ExcludedTools: []string{"bash"}}}

	sessionID := ""
	cb := memoryObservationCallback(worker, cfg, "/project", &sessionID)

	tests := []struct {
		name    string
		session string
		tool    string
		toolErr error
	}{
		{"no session yet", "", "read", nil},
		{"failed tool call", "s1", "read", errors.New("nope")},
		{"excluded tool", "s1", "bash", nil},
		{"recorded", "s1", "read", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionID = tt.session
			got, err := cb(nil, namedTool{name: tt.tool}, map[string]any{"a": 1}, map[string]any{"b": 2}, tt.toolErr)
			if err != nil {
				t.Fatalf("callback: %v", err)
			}
			// The callback only observes; it must never rewrite the result.
			if got != nil {
				t.Errorf("result = %v, want nil", got)
			}
		})
	}
}

func TestBuildCallbacksComposesAfterToolIntoOne(t *testing.T) {
	isolate(t)
	before, after := buildCallbacks(callbackDeps{
		cfg:       config.Config{},
		provider:  "anthropic",
		sessionID: new(string),
		opts:      defaultOptions(),
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
		cfg:       config.Config{},
		sessionID: new(string),
		opts:      o,
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
