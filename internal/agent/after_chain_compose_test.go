package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/tools"
)

// TestComposedAfterChainReachesCompactor is the end-to-end guard for the
// after-tool chain. It runs a REAL ADK agent whose after-tool chain is the one
// pi-go assembles — composed by extension.ComposeAfterToolChain — and asserts
// the compactor's reduction actually reaches the model.
//
// Before composition this could not hold. ADK's Flow.invokeAfterToolCallbacks
// (adk v2.4.0 internal/llminternal/base_flow.go:1436) returns at the first
// callback yielding a non-nil result, and the OTEL tracing callback is
// registered first and always returns the result map, so the chain stopped there
// and the compactor never ran. Verified directly: with a raw slice only the
// first callback fired and the mutation never reached the model.
func TestComposedAfterChainReachesCompactor(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("--- a/x.go\n+++ b/x.go\n@@ -1,3000 +1,3000 @@\n")
	for i := 0; i < 3000; i++ {
		b.WriteString("+added a long line of go source code content here\n")
	}
	writeFile(t, dir, "x.diff", b.String())

	cfg := tools.DefaultCompactorConfig()
	comp := tools.BuildCompactorCallback(cfg, tools.NewCompactMetrics())
	tracingLike := func(_ agent.Context, _ adktool.Tool, _, r map[string]any, _ error) (map[string]any, error) {
		return r, nil // non-nil, like OTEL tracing
	}
	chain := extension.ComposeAfterToolChain([]llmagent.AfterToolCallback{tracingLike, comp})

	llm := &toolCallingLLM{
		name:         "v",
		functionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": dir + "/x.diff"}},
		finalText:    "done",
	}
	ct, err := tools.CoreTools(testSandbox(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(Config{Model: llm, Tools: ct, Instruction: "t", AfterToolCallbacks: chain})
	if err != nil {
		t.Fatal(err)
	}
	sid, _, err := a.CreateSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	for ev, err := range a.Run(context.Background(), sid, "read") {
		if err != nil {
			t.Fatal(err)
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, p := range ev.Content.Parts {
			if p.FunctionResponse != nil {
				if s, ok := p.FunctionResponse.Response["content"].(string); ok {
					seen = s
				}
			}
		}
	}
	t.Logf("model saw %d bytes; compacted=%v", len(seen), strings.Contains(seen, "truncated"))
	if !strings.Contains(seen, "truncated") {
		t.Errorf("compactor did not reach the model through the composed chain")
	}
}

// writeFile writes a fixture file into dir.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
