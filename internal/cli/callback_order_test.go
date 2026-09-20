package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/toolconfirmation"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/tools"
)

// namedToolStub is a tool.Tool carrying only the registered name, so the
// compactor and deduper route on it the way they would in production.
type namedToolStub struct{ name string }

func (n *namedToolStub) Name() string        { return n.name }
func (n *namedToolStub) Description() string { return "" }
func (n *namedToolStub) IsLongRunning() bool { return false }

var _ adktool.Tool = (*namedToolStub)(nil)

// TestDeferredCallbacks_DedupSeesPreCompactionBytes is the integration guard for
// the callback order. It drives the real assembled chain over two results that
// differ only beyond the compaction cap, and asserts the model is NOT told the
// second is unchanged.
//
// This is the failure the order exists to prevent: compaction is lossy, so two
// different diffs truncate to the same head. If dedup hashes after the compactor
// it sees identical bytes and replaces the second result with "content is
// unchanged" — telling the model a file it changed did not change.
func TestDeferredCallbacks_DedupSeesPreCompactionBytes(t *testing.T) {
	resetGlobalFlags(t)
	sandbox, err := tools.NewSandbox(t.TempDir())
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })

	base := buildDeferredCallbacks(config.Config{}, "anthropic", sandbox, nil, nil)
	tool := &namedToolStub{name: "git-file-diff"}
	args := map[string]any{"file": "big.go"}

	// Long shared head (so the compacted result clears the dedup size floor),
	// then 4000 lines that differ between the two calls and sit past the cap.
	mkDiff := func(tag string) map[string]any {
		var b strings.Builder
		b.WriteString("diff --git a/big.go b/big.go\n--- a/big.go\n+++ b/big.go\n@@ -1,9000 +1,9000 @@\n")
		for i := 0; i < 300; i++ {
			b.WriteString("-shared line padded with body to add bytes\n+shared line padded with body to add bytes\n")
		}
		for i := 0; i < 4000; i++ {
			b.WriteString("-" + tag + " line\n+" + tag + " line\n")
		}
		return map[string]any{"file": "big.go", "diff": b.String(), "lines_added": 4300}
	}

	// Run the chain exactly as ADK does. Flow.invokeAfterToolCallbacks
	// (adk v2.4.0 internal/llminternal/base_flow.go:1436) returns at the FIRST
	// callback that yields a non-nil result. The chain is therefore handed to ADK
	// as a single composed callback: if it were passed as a slice, every stage
	// after the first non-nil one would be skipped — which is how dedup and the
	// compactor were dead in production.
	if len(base.afterTool) != 1 {
		t.Fatalf("after-tool chain has %d entries, want 1 composed callback; "+
			"ADK would stop at the first and skip the rest", len(base.afterTool))
	}
	// A real agent.Context is required: the OTEL tracing stage reads trace state
	// off the context and panics on nil. The composition is what makes every
	// stage reachable, so the context must be realistic.
	ctx := &cliToolCtx{Context: context.Background()}
	runChain := func(result map[string]any) map[string]any {
		next, err := base.afterTool[0](ctx, tool, args, result, nil)
		if err != nil || next == nil {
			return result // ADK keeps the tool's own result
		}
		return next
	}

	runChain(mkDiff("alpha"))
	second := runChain(mkDiff("bravo")) // genuinely different content

	got, _ := second["diff"].(string)
	if strings.Contains(got, "identical to the result") || strings.Contains(got, "content is unchanged") {
		t.Errorf("the assembled chain reported a CHANGED diff as unchanged; dedup is "+
			"hashing post-compaction bytes, so truncation made two different diffs "+
			"collide. Dedup must run before the compactor.\nresult: %q",
			truncateForErr(got, 200))
	}
	if !strings.Contains(got, "omitted") {
		t.Errorf("expected the diff to be compacted with a disclosure; got %q",
			truncateForErr(got, 200))
	}

	// The same chain must still elide a genuine, byte-identical repeat, so the
	// ordering fix cannot be satisfied by disabling dedup.
	third := runChain(mkDiff("charlie"))
	fourth := runChain(mkDiff("charlie"))
	f, _ := fourth["diff"].(string)
	_ = third
	if !strings.Contains(f, "identical to the result") {
		t.Errorf("an identical repeat was NOT elided; dedup stopped working after the "+
			"ordering change.\nresult: %q", truncateForErr(f, 200))
	}
}

// truncateForErr shortens a value for an error message.
func truncateForErr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// cliToolCtx is a minimal agent.Context. The composed chain includes the OTEL
// tracing stage, which reads trace state off the context and panics on nil, so
// tests that drive the real chain must supply one. Only the methods the chain
// touches are given real behavior; the rest satisfy the interface.
type cliToolCtx struct{ context.Context }

func (c *cliToolCtx) FunctionCallID() string         { return "fc-test" }
func (c *cliToolCtx) Actions() *session.EventActions { return nil }
func (c *cliToolCtx) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}
func (c *cliToolCtx) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }
func (c *cliToolCtx) RequestConfirmation(string, any) error                { return nil }
func (c *cliToolCtx) AgentName() string                                    { return "pi" }
func (c *cliToolCtx) ReadonlyState() session.ReadonlyState                 { return nil }
func (c *cliToolCtx) State() session.State                                 { return nil }
func (c *cliToolCtx) Artifacts() agent.Artifacts                           { return nil }
func (c *cliToolCtx) InvocationID() string                                 { return "inv-test" }
func (c *cliToolCtx) UserContent() *genai.Content                          { return nil }
func (c *cliToolCtx) AppName() string                                      { return "pi-go" }
func (c *cliToolCtx) Branch() string                                       { return "" }
func (c *cliToolCtx) SessionID() string                                    { return "s1" }
func (c *cliToolCtx) UserID() string                                       { return "local" }
func (c *cliToolCtx) Agent() agent.Agent                                   { return nil }
func (c *cliToolCtx) Memory() agent.Memory                                 { return nil }
func (c *cliToolCtx) Session() session.Session                             { return nil }
func (c *cliToolCtx) RunConfig() *agent.RunConfig                          { return nil }
func (c *cliToolCtx) EndInvocation()                                       {}
func (c *cliToolCtx) Ended() bool                                          { return false }
func (c *cliToolCtx) WithContext(ctx context.Context) agent.InvocationContext {
	return &cliToolCtx{Context: ctx}
}
func (c *cliToolCtx) IsolationScope() string { return "" }
func (c *cliToolCtx) ResumedInput(string) (any, bool) {
	return nil, false
}
func (c *cliToolCtx) WithICDelta(*agent.InvocationContextDelta) agent.InvocationContext {
	return c
}
func (c *cliToolCtx) Path() string                            { return "" }
func (c *cliToolCtx) RunID() string                           { return "" }
func (c *cliToolCtx) SubScheduler() agent.DynamicSubScheduler { return nil }
func (c *cliToolCtx) WithAgentContext(ctx context.Context) agent.Context {
	return &cliToolCtx{Context: ctx}
}
func (c *cliToolCtx) WithAgentTimeout(time.Duration) (agent.Context, context.CancelFunc) {
	return c, func() {}
}
func (c *cliToolCtx) WithAgentCancel() (agent.Context, context.CancelFunc) {
	return c, func() {}
}
func (c *cliToolCtx) OutputForAncestors() []string { return nil }
func (c *cliToolCtx) WithDelta(*agent.CommonContextDelta) agent.Context {
	return c
}

var _ agent.Context = (*cliToolCtx)(nil)
