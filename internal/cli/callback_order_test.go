package cli

import (
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent/llmagent"
	adktool "google.golang.org/adk/v2/tool"

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

// safeInvoke runs one after-tool callback, reporting false if it panicked or
// errored. Several callbacks in the assembled chain require a live invocation
// context — OTEL tracing reads trace state off it (internal/extension/hooks.go)
// — and panic on a nil context. They are not part of the ordering under test, so
// they are skipped rather than allowed to abort the test.
func safeInvoke(cb llmagent.AfterToolCallback, tool adktool.Tool, args, result map[string]any) (out map[string]any, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			out, ok = nil, false
		}
	}()
	next, err := cb(nil, tool, args, result, nil)
	if err != nil {
		return nil, false
	}
	return next, true
}

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

	// Run the assembled chain as the agent loop does, skipping the callbacks that
	// need a live context.
	runChain := func(result map[string]any) map[string]any {
		out := result
		for _, cb := range base.afterTool {
			next, ok := safeInvoke(cb, tool, args, out)
			if !ok || next == nil {
				continue
			}
			out = next
		}
		return out
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
