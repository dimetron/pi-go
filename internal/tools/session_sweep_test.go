package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeEvents drops an events.jsonl into a fresh session directory under root.
func writeEvents(t *testing.T, root, id string, lines ...string) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o600); err != nil {
		t.Fatalf("write events: %v", err)
	}
}

func TestAbortDetail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"unrelated error", "provider timed out", ""},
		{"bare abort", "agent loop aborted: model repeated a 106-character phrase 12 times",
			"model repeated a 106-character phrase 12 times"},
		{"prefixed", "run failed: agent loop aborted: identical tool call \"read\" repeated 10 times",
			"identical tool call \"read\" repeated 10 times"},
		{"trimmed to 120", "agent loop aborted: " + strings.Repeat("x", 200), strings.Repeat("x", 120)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := abortDetail(tt.in); got != tt.want {
				t.Fatalf("abortDetail(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestAccumulateSweepFile(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("z", oversizeChars+1000)
	writeEvents(t, root, "s1",
		`{"content":{"parts":[{"functionCall":{"name":"read"}}]},"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20}}`,
		`{"content":{"parts":[{"functionResponse":{"name":"read","response":{"content":"`+big+`"}}}]}}`,
		`{"content":{"parts":[{"functionResponse":{"name":"read","response":{"content":"`+big+`"}}}]}}`,
		`{"content":{"parts":[{"functionResponse":{"name":"ripgrep","response":{"error":"boom"}}}]}}`,
		`{"errorMessage":"agent loop aborted: model repeated a 106-character phrase 12 times"}`,
	)

	totals := newSweepTotals()
	accumulateSweepFile(totals, filepath.Join(root, "s1", "events.jsonl"))

	if totals.PromptTokens != 100 || totals.OutputTokens != 20 {
		t.Fatalf("usage = prompt %d output %d, want 100/20", totals.PromptTokens, totals.OutputTokens)
	}
	if got := totals.tool("read").Calls; got != 1 {
		t.Fatalf("read calls = %d, want 1", got)
	}
	if got := totals.tool("ripgrep").Errors; got != 1 {
		t.Fatalf("ripgrep errors = %d, want 1 (an \"error\" key in the response body)", got)
	}
	if totals.tool("read").Oversized == 0 {
		t.Fatal("oversized read result was not counted")
	}
	// The second identical response is the duplicate; the first is not.
	if totals.DupBytes == 0 {
		t.Fatal("identical result repeated in one session was not counted as a duplicate")
	}
	if n := totalAborts(totals); n != 1 {
		t.Fatalf("aborts = %d, want 1", n)
	}
}

// TestAccumulateSweepFileDuplicatesArePerSession pins the scope of dedup
// accounting: the same result in two different sessions is not a duplicate,
// because nothing showed it to the model twice in one context.
func TestAccumulateSweepFileDuplicatesArePerSession(t *testing.T) {
	root := t.TempDir()
	line := `{"content":{"parts":[{"functionResponse":{"name":"read","response":{"content":"same"}}}]}}`
	writeEvents(t, root, "a", line)
	writeEvents(t, root, "b", line)

	totals := newSweepTotals()
	accumulateSweepFile(totals, filepath.Join(root, "a", "events.jsonl"))
	accumulateSweepFile(totals, filepath.Join(root, "b", "events.jsonl"))

	if totals.DupBytes != 0 {
		t.Fatalf("DupBytes = %d, want 0 — cross-session repeats are not duplicates", totals.DupBytes)
	}
}

func TestAccumulateSweepFileMissingPath(t *testing.T) {
	totals := newSweepTotals()
	accumulateSweepFile(totals, filepath.Join(t.TempDir(), "nope", "events.jsonl"))
	if totals.ToolBytes != 0 || len(totals.Tools) != 0 {
		t.Fatal("a missing file should contribute nothing, not panic or invent totals")
	}
}

func TestSessionStatsReportIncludesSweepSections(t *testing.T) {
	root := t.TempDir()
	writeEvents(t, root, "s1",
		`{"content":{"parts":[{"functionCall":{"name":"read"}}]},"usageMetadata":{"promptTokenCount":8000,"candidatesTokenCount":40}}`,
		`{"errorMessage":"agent loop aborted: identical tool call \"read\" repeated 10 times"}`,
	)
	// os.ReadDir skips nothing here, but the cutoff does — keep mtime fresh.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(root, "s1", "events.jsonl"), now, now)

	out, err := SessionStats(SessionStatsInput{Hours: 24, SessionDir: root, All: true})
	if err != nil {
		t.Fatalf("SessionStats: %v", err)
	}
	for _, want := range []string{"## Failures", "## Token waste", "## Token spend", "## Pipeline health"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("report missing %q section:\n%s", want, out.Content)
		}
	}
	if out.AbortedRuns != 1 {
		t.Errorf("AbortedRuns = %d, want 1", out.AbortedRuns)
	}
	if out.PromptTokens != 8000 {
		t.Errorf("PromptTokens = %d, want 8000 (provider-reported, not estimated)", out.PromptTokens)
	}
}

func TestThousands(t *testing.T) {
	tests := map[int]string{0: "0", 42: "42", 999: "999", 1000: "1,000",
		12345: "12,345", 1234567: "1,234,567"}
	for in, want := range tests {
		if got := thousands(in); got != want {
			t.Errorf("thousands(%d) = %q, want %q", in, got, want)
		}
	}
}
