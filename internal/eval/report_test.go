package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// fullReport is a report with every optional section populated, so a render
// test exercises the whole document rather than one branch of it.
func fullReport() *RunReport {
	return &RunReport{
		Metadata: ReportMetadata{
			Spec:       "eval-orchestrator",
			Mode:       "parallel",
			Model:      "test-model",
			Binary:     "/tmp/pi",
			GitHead:    "abcdef1234567890",
			BaseRef:    "eval/base",
			BaseCommit: "1234567890abcdef",
			Timestamp:  time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
			Duration:   "4m12s",
		},
		Outcome: RunOutcome{
			FinalPhase: "failed",
			Retries:    1,
			Reason:     "**Merge failed**\nconflict in add.go",
			GateResults: []GateResult{
				{Name: "test", Command: "go test ./...", Passed: true},
				{Name: "vet", Command: "go vet ./...", Passed: false},
			},
			GoldenCheck: []GoldenFile{
				{Name: "go.mod", Match: true},
				{Name: "add.go", Match: false, Diff: "first difference at line 3\n- 3| a\n+ 3| b\n"},
				{Name: "add_test.go", Match: false, Error: "produced file missing: no such file"},
			},
			GoldenPass:    false,
			BaselineRef:   "eval/golden",
			BaselineCheck: []GoldenFile{{Name: "add.go", Match: true}},
			BaselinePass:  true,
		},
		Trajectory: TrajectoryMetrics{
			Sessions: []SessionSummary{
				{SessionID: "260810-1601-abcde-12345", AgentName: "pi-go", Model: "test-model", Depth: 0, Steps: 10, ToolCalls: 8, SubagentRefs: 1, Duration: "2m"},
				{SessionID: "short", AgentName: "task", Depth: 1, Steps: 4, ToolCalls: 3},
			},
			TotalSteps:       14,
			TotalToolCalls:   11,
			NestedAgentCalls: 1,
			MaxDepth:         1,
		},
		Concurrency: ConcurrencyMetrics{
			PoolBudget:      4,
			WorkerBudget:    2,
			MaxRunning:      3,
			MeanRunning:     1.5,
			AgentsSeen:      3,
			ParallelOverlap: 0.5,
			Samples: []ConcurrencySample{
				{Running: 0}, {Running: 2}, {Running: 3}, {Running: 1},
			},
		},
		Tools: ToolsMetrics{
			TotalCalls:   11,
			TotalResults: 9,
			Wasted:       2,
			Duplicates:   3,
			ByTool: map[string]ToolStats{
				"write":    {Calls: 1, Results: 1},
				"bash":     {Calls: 6, Results: 5, Errors: 2, Wasted: 1, Duplicates: 3, AvgResultBytes: 120, AvgLatencyMs: 400},
				"read":     {Calls: 3, Results: 3},
				"edit":     {Calls: 1, Results: 0, Wasted: 1},
				"subagent": {Calls: 1, Results: 1},
			},
		},
		Tokens: TokenMetrics{
			PromptTokens:     1000,
			CompletionTokens: 200,
			CachedTokens:     50,
			TotalTokens:      1200,
			CostUSD:          0.0123,
			Sessions: []SessionTokenUsage{
				{SessionID: "260810-1601-abcde-12345", PromptTokens: 900, CompletionTokens: 180},
				{SessionID: "short", PromptTokens: 100, CompletionTokens: 20},
			},
		},
	}
}

func TestRenderMarkdown_FullReport(t *testing.T) {
	md := RenderMarkdown(fullReport())

	for _, want := range []string{
		"# Eval run: eval-orchestrator",
		"**mode**: parallel",
		"**git head**: `abcdef123456`", // truncated to 12
		"**base ref**: `eval/base` (`1234567890ab`)",
		"**final phase**: `failed`",
		"conflict in add.go",
		"| test | `go test ./...` | ✓ |",
		"| vet | `go vet ./...` | ✗ |",
		"**Golden check**: FAIL",
		"`add.go`: mismatch",
		"`add_test.go`: mismatch (produced file missing: no such file)",
		"**baseline ref**: `eval/golden`",
		"**Baseline check**: PASS",
		"**max nesting depth**: 1",
		"`260810-1601-…`", // session id truncated to 12 + ellipsis
		"| `short` | task | — |",
		"**worker budget** (nested): 2",
		"**parallel overlap** (fraction of samples with >1 running): 0.50",
		"**duplicate calls**: 3",
		"**cached tokens**: 50",
		"**total tokens**: 1200",
		"**estimated cost**: $0.0123",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown is missing %q\n---\n%s", want, md)
		}
	}

	if !utf8.ValidString(md) {
		t.Error("rendered markdown is not valid UTF-8")
	}
}

// The tool table ranges a map, which has no order. Without an explicit sort the
// same report renders differently on every call and two reports of one run
// cannot be diffed.
func TestRenderMarkdown_IsDeterministic(t *testing.T) {
	r := fullReport()
	first := RenderMarkdown(r)
	for i := range 100 {
		if got := RenderMarkdown(r); got != first {
			t.Fatalf("render %d differs from the first render\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}

	// And the order is the sorted one, not merely stable.
	rows := []string{"| `bash` |", "| `edit` |", "| `read` |", "| `subagent` |", "| `write` |"}
	prev := -1
	for _, row := range rows {
		at := strings.Index(first, row)
		if at < 0 {
			t.Fatalf("tool row %q missing from the table:\n%s", row, first)
		}
		if at < prev {
			t.Errorf("tool row %q is out of sorted order", row)
		}
		prev = at
	}
}

func TestSparkline(t *testing.T) {
	tests := []struct {
		name      string
		samples   []ConcurrencySample
		wantEmpty bool
		wantIn    string
	}{
		{name: "no samples", samples: nil, wantEmpty: true},
		{name: "all zero", samples: []ConcurrencySample{{Running: 0}, {Running: 0}}, wantIn: "(max 1, 2 samples)"},
		{name: "varied", samples: []ConcurrencySample{{Running: 0}, {Running: 1}, {Running: 2}, {Running: 4}}, wantIn: "(max 4, 4 samples)"},
		{name: "bucketed", samples: manySamples(500), wantIn: "(max 7, 500 samples)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sparkline(tt.samples)
			if tt.wantEmpty {
				if got != "" {
					t.Fatalf("sparkline = %q, want empty", got)
				}
				return
			}
			// The block characters are three bytes each in UTF-8. Indexing the
			// level string by byte emits a fragment of one and corrupts the
			// whole line, so validity is the assertion that matters most here.
			if !utf8.ValidString(got) {
				t.Errorf("sparkline is not valid UTF-8: %q", got)
			}
			if !strings.Contains(got, tt.wantIn) {
				t.Errorf("sparkline = %q, want it to contain %q", got, tt.wantIn)
			}
			bars, _, _ := strings.Cut(got, "  (")
			for _, r := range bars {
				if !strings.ContainsRune("▁▂▃▄▅▆▇█", r) {
					t.Errorf("sparkline contains %q, not a block character: %q", r, got)
				}
			}
			if n := utf8.RuneCountInString(bars); n > 100 {
				t.Errorf("sparkline is %d columns wide, want <= 100", n)
			}
		})
	}
}

func manySamples(n int) []ConcurrencySample {
	out := make([]ConcurrencySample, n)
	for i := range out {
		out[i] = ConcurrencySample{Running: i % 8}
	}
	return out
}

func TestWriteReport(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "reports") // MkdirAll must create it
	r := fullReport()

	jsonPath, mdPath, md, err := WriteReport(r, dir)
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}

	if want := "eval-eval-orchestrator-20260810-120000.json"; filepath.Base(jsonPath) != want {
		t.Errorf("json path = %q, want basename %q", jsonPath, want)
	}
	if want := "eval-eval-orchestrator-20260810-120000.md"; filepath.Base(mdPath) != want {
		t.Errorf("md path = %q, want basename %q", mdPath, want)
	}

	onDisk, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatalf("read md: %v", err)
	}
	if string(onDisk) != md {
		t.Error("returned markdown differs from the file written")
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	var round RunReport
	if err := json.Unmarshal(data, &round); err != nil {
		t.Fatalf("report JSON does not round-trip: %v", err)
	}
	if round.Metadata.Spec != r.Metadata.Spec || round.Tools.TotalCalls != r.Tools.TotalCalls {
		t.Errorf("round-tripped report = %+v, want it to match the original", round.Metadata)
	}
	if round.Judge != nil {
		t.Error("absent judge was serialized; the field should be omitted")
	}
}

func TestWriteReport_UnwritableDir(t *testing.T) {
	// A file where the report directory should be: MkdirAll must fail and the
	// error must be wrapped, not swallowed.
	base := t.TempDir()
	blocker := filepath.Join(base, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := WriteReport(fullReport(), filepath.Join(blocker, "out")); err == nil {
		t.Error("WriteReport succeeded with an unusable output dir")
	} else if !strings.Contains(err.Error(), "create report dir") {
		t.Errorf("error = %v, want it wrapped with the failing operation", err)
	}
}

func TestFirstDiff(t *testing.T) {
	tests := []struct {
		name       string
		want, got  string
		wantLineNo string
	}{
		{"differs mid-file", "a\nb\nc\n", "a\nX\nc\n", "line 2"},
		{"got is a prefix of want", "a\nb\nc\n", "a\nb\n", "line 3"},
		{"want is a prefix of got", "a\nb\n", "a\nb\nc\n", "line 3"},
		{"differs on the first line", "a\n", "z\n", "line 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := firstDiff([]byte(tt.want), []byte(tt.got))
			if !strings.Contains(got, tt.wantLineNo) {
				t.Errorf("firstDiff snippet = %q, want it to report %q", got, tt.wantLineNo)
			}
		})
	}

	// A long line is truncated so one minified file cannot flood the report.
	long := strings.Repeat("x", 500)
	snippet := firstDiff([]byte(long), []byte(strings.Repeat("y", 500)))
	for _, line := range strings.Split(snippet, "\n") {
		if len(line) > 120 {
			t.Errorf("diff snippet line is %d bytes, want it truncated: %q", len(line), line)
		}
	}
}

func TestShortIDAndShortHead(t *testing.T) {
	tests := []struct {
		name           string
		in             string
		wantID, wantHd string
	}{
		{"empty", "", "", ""},
		{"short", "abc", "abc", "abc"},
		{"exactly 12", "123456789012", "123456789012", "123456789012"},
		{"long", "260810-1601-abcde-12345", "260810-1601-…", "260810-1601-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shortID(tt.in); got != tt.wantID {
				t.Errorf("shortID(%q) = %q, want %q", tt.in, got, tt.wantID)
			}
			if got := shortHead(tt.in); got != tt.wantHd {
				t.Errorf("shortHead(%q) = %q, want %q", tt.in, got, tt.wantHd)
			}
		})
	}
}

func TestRenderMarkdown_NoGates(t *testing.T) {
	md := RenderMarkdown(&RunReport{})
	if !strings.Contains(md, "**gates**: none") {
		t.Errorf("a run with no gates should say so:\n%s", md)
	}
	if strings.Contains(md, "baseline ref") {
		t.Errorf("no baseline was configured, but the section rendered:\n%s", md)
	}
}
