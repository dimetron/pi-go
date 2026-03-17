package tools

import (
	"strings"
	"testing"
)

func TestStripAnsi(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		applied bool
	}{
		{"no ansi", "hello world", "hello world", false},
		{"color codes", "\x1b[31mred\x1b[0m text", "red text", true},
		{"bold", "\x1b[1mbold\x1b[0m", "bold", true},
		{"cursor", "\x1b[2Jclear", "clear", true},
		{"mixed", "start \x1b[32mgreen\x1b[0m end", "start green end", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, applied := stripAnsi(tt.input)
			if got != tt.want {
				t.Errorf("stripAnsi() got = %q, want %q", got, tt.want)
			}
			if applied != tt.applied {
				t.Errorf("stripAnsi() applied = %v, want %v", applied, tt.applied)
			}
		})
	}
}

func TestHardTruncate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxChars int
		applied  bool
	}{
		{"under limit", "short", 100, false},
		{"at limit", "12345", 5, false},
		{"over limit", "1234567890", 5, true},
		{"zero limit", "text", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, applied := hardTruncate(tt.input, tt.maxChars)
			if applied != tt.applied {
				t.Errorf("hardTruncate() applied = %v, want %v", applied, tt.applied)
			}
			if applied && !strings.HasSuffix(got, "... (output truncated)") {
				t.Errorf("hardTruncate() should end with truncation marker")
			}
		})
	}
}

func TestHardTruncateLines(t *testing.T) {
	input := strings.Join(make([]string, 500), "\n") // 500 empty lines
	got, applied := hardTruncateLines(input, 100)
	if !applied {
		t.Error("hardTruncateLines() should have applied")
	}
	lineCount := strings.Count(got, "\n")
	if lineCount > 101 { // 100 lines + truncation message
		t.Errorf("hardTruncateLines() got %d lines, want <= 101", lineCount)
	}
}

func TestDetectCommand(t *testing.T) {
	tests := []struct {
		args map[string]any
		want string
	}{
		{nil, ""},
		{map[string]any{}, ""},
		{map[string]any{"command": "go test ./..."}, "go test ./..."},
		{map[string]any{"command": 42}, ""},
	}

	for _, tt := range tests {
		got := detectCommand(tt.args)
		if got != tt.want {
			t.Errorf("detectCommand(%v) = %q, want %q", tt.args, got, tt.want)
		}
	}
}

func TestIsTestCommand(t *testing.T) {
	if !isTestCommand("go test ./...") {
		t.Error("should detect go test")
	}
	if !isTestCommand("pytest -v") {
		t.Error("should detect pytest")
	}
	if isTestCommand("go build ./...") {
		t.Error("should not detect go build as test")
	}
}

func TestIsBuildCommand(t *testing.T) {
	if !isBuildCommand("go build ./...") {
		t.Error("should detect go build")
	}
	if !isBuildCommand("make all") {
		t.Error("should detect make")
	}
	if isBuildCommand("go test ./...") {
		t.Error("should not detect go test as build")
	}
}

func TestIsGitCommand(t *testing.T) {
	if !isGitCommand("git status") {
		t.Error("should detect git command")
	}
	if isGitCommand("echo git") {
		t.Error("should not detect non-git command")
	}
}

func TestIsLinterCommand(t *testing.T) {
	if !isLinterCommand("golangci-lint run") {
		t.Error("should detect golangci-lint")
	}
	if isLinterCommand("go build ./...") {
		t.Error("should not detect go build as linter")
	}
}

func TestAggregateTestOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

	// Generate realistic Go test output
	var lines []string
	lines = append(lines, "=== RUN   TestFoo")
	lines = append(lines, "--- PASS: TestFoo (0.00s)")
	lines = append(lines, "=== RUN   TestBar")
	lines = append(lines, "--- FAIL: TestBar (0.01s)")
	lines = append(lines, "    bar_test.go:15: expected 42, got 0")
	lines = append(lines, "    bar_test.go:16: more detail here")
	for i := 0; i < 50; i++ {
		lines = append(lines, "=== RUN   TestGen"+strings.Repeat("x", i))
		lines = append(lines, "--- PASS: TestGen"+strings.Repeat("x", i)+" (0.00s)")
	}
	lines = append(lines, "ok  \tpkg/foo\t0.5s")
	lines = append(lines, "FAIL\tpkg/bar\t0.1s")

	input := strings.Join(lines, "\n")
	got, applied := aggregateTestOutput(input, cfg)

	if !applied {
		t.Fatal("aggregateTestOutput should have applied")
	}
	if !strings.Contains(got, "Test Summary:") {
		t.Error("should contain test summary")
	}
	if !strings.Contains(got, "FAIL=1") {
		t.Error("should show failure count")
	}
	if len(got) >= len(input) {
		t.Error("compacted output should be shorter")
	}
}

func TestFilterBuildOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "compiling package foo...")
	}
	lines = append(lines, "main.go:10:5: undefined: foo")
	lines = append(lines, "  more context about the error")
	lines = append(lines, "FAIL\tbuild failed")

	input := strings.Join(lines, "\n")
	got, applied := filterBuildOutput(input, cfg)

	if !applied {
		t.Fatal("filterBuildOutput should have applied")
	}
	if !strings.Contains(got, "undefined: foo") {
		t.Error("should preserve error lines")
	}
	if len(got) >= len(input) {
		t.Error("compacted output should be shorter")
	}
}

func TestAggregateLinterOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "main.go:"+strings.Repeat("1", 1)+":5: some warning")
	}
	for i := 0; i < 30; i++ {
		lines = append(lines, "util.go:"+strings.Repeat("2", 1)+":3: another issue")
	}

	input := strings.Join(lines, "\n")
	got, applied := aggregateLinterOutput(input, cfg)

	if !applied {
		t.Fatal("aggregateLinterOutput should have applied")
	}
	if !strings.Contains(got, "issues") {
		t.Error("should contain issue counts")
	}
	if len(got) >= len(input) {
		t.Error("compacted output should be shorter")
	}
}

func TestCompactGitDiffText(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	lines = append(lines, "diff --git a/main.go b/main.go")
	lines = append(lines, "--- a/main.go")
	lines = append(lines, "+++ b/main.go")
	lines = append(lines, "@@ -1,5 +1,7 @@")
	for i := 0; i < 200; i++ {
		lines = append(lines, "+added line "+strings.Repeat("x", i%10))
	}

	input := strings.Join(lines, "\n")
	got, applied := compactGitDiffText(input, cfg)

	if !applied {
		t.Fatal("compactGitDiffText should have applied")
	}
	if !strings.Contains(got, "diff --git") {
		t.Error("should preserve file header")
	}
	if len(got) >= len(input) {
		t.Error("compacted output should be shorter")
	}
}

func TestCompactGitLogText(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "commit abc123"+strings.Repeat("f", i%5))
		lines = append(lines, "Author: test")
		lines = append(lines, "Date: 2026-01-01")
		lines = append(lines, "")
		lines = append(lines, "    commit message "+strings.Repeat("x", i))
		lines = append(lines, "")
	}

	input := strings.Join(lines, "\n")
	got, applied := compactGitLogText(input, cfg)

	if !applied {
		t.Fatal("compactGitLogText should have applied")
	}
	if len(got) >= len(input) {
		t.Error("compacted output should be shorter")
	}
}

func TestGroupSearchOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "fileA.go:"+strings.Repeat("1", 1)+": match content")
	}
	for i := 0; i < 50; i++ {
		lines = append(lines, "fileB.go:"+strings.Repeat("2", 1)+": other content")
	}

	input := strings.Join(lines, "\n")
	got, applied := groupSearchOutput(input, cfg)

	if !applied {
		t.Fatal("groupSearchOutput should have applied")
	}
	if !strings.Contains(got, "matches") {
		t.Error("should contain match counts")
	}
}

func TestFilterSourceCode(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "// this is a comment")
		lines = append(lines, "func foo() {}")
		lines = append(lines, "")
		lines = append(lines, "")
	}

	input := strings.Join(lines, "\n")
	got, applied := filterSourceCode(input, "minimal")

	if !applied {
		t.Fatal("filterSourceCode should have applied")
	}
	if len(got) >= len(input) {
		t.Error("filtered output should be shorter")
	}
}

func TestSmartTruncate(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.MaxLines = 50

	var lines []string
	for i := 0; i < 200; i++ {
		if i%20 == 0 {
			lines = append(lines, "error: something went wrong")
		} else {
			lines = append(lines, "normal output line "+strings.Repeat("x", i%10))
		}
	}

	input := strings.Join(lines, "\n")
	got, applied := smartTruncate(input, cfg)

	if !applied {
		t.Fatal("smartTruncate should have applied")
	}
	if strings.Count(got, "\n") > 60 { // some tolerance
		t.Errorf("smartTruncate should limit to ~50 lines, got %d", strings.Count(got, "\n"))
	}
	if !strings.Contains(got, "error:") {
		t.Error("smartTruncate should preserve error lines")
	}
}

func TestCompactMetrics(t *testing.T) {
	m := NewCompactMetrics()

	m.Record([]string{"ansi", "test-aggregate"}, 12400, 850, "bash")
	m.Record([]string{"ansi"}, 5000, 4800, "read")

	s := m.Summary()
	if s.TotalOrig != 17400 {
		t.Errorf("TotalOrig = %d, want 17400", s.TotalOrig)
	}
	if s.TotalComp != 5650 {
		t.Errorf("TotalComp = %d, want 5650", s.TotalComp)
	}
	if len(s.ByTool) != 2 {
		t.Errorf("ByTool has %d entries, want 2", len(s.ByTool))
	}

	stats := m.FormatStats()
	if !strings.Contains(stats, "RTK Compactor Stats") {
		t.Error("FormatStats should contain header")
	}
	if !strings.Contains(stats, "bash") {
		t.Error("FormatStats should contain tool name")
	}
}

func TestDefaultCompactorConfig(t *testing.T) {
	cfg := DefaultCompactorConfig()
	if !cfg.Enabled {
		t.Error("default config should be enabled")
	}
	if cfg.MaxChars != 24000 {
		t.Errorf("MaxChars = %d, want 24000", cfg.MaxChars)
	}
	if cfg.MaxLines != 440 {
		t.Errorf("MaxLines = %d, want 440", cfg.MaxLines)
	}
}

func TestCompactToolResult_UnknownTool(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := compactToolResult("unknown_tool", nil, nil, cfg)
	if result != nil {
		t.Error("unknown tool should return nil")
	}
}

func TestRunStage_PanicRecovery(t *testing.T) {
	var techniques []string
	input := "test input"

	// This should recover from panic without crashing
	output := runStage(input, &techniques, "panicking", func(s string) (string, bool) {
		panic("test panic")
	})

	if output != input {
		t.Error("runStage should return original input on panic")
	}
}

func TestApplyCompaction_BashOutput(t *testing.T) {
	result := map[string]any{"stdout": "original", "stderr": ""}
	cr := &CompactResult{Output: "compacted"}
	applyCompaction(result, cr)
	if result["stdout"] != "compacted" {
		t.Errorf("stdout = %v, want 'compacted'", result["stdout"])
	}
}

func TestApplyCompaction_ReadOutput(t *testing.T) {
	result := map[string]any{"content": "original"}
	cr := &CompactResult{Output: "compacted"}
	applyCompaction(result, cr)
	if result["content"] != "compacted" {
		t.Errorf("content = %v, want 'compacted'", result["content"])
	}
}

func TestApplyCompaction_NilResult(t *testing.T) {
	applyCompaction(nil, nil) // should not panic
	applyCompaction(map[string]any{}, nil) // should not panic
}

func TestCompactBash_EmptyOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := compactBash(map[string]any{"stdout": "", "stderr": ""}, nil, cfg)
	if result != nil {
		t.Error("empty output should return nil")
	}
}

func TestDedup(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b"}
	got := dedup(input)
	if len(got) != 3 {
		t.Errorf("dedup() = %v, want 3 unique elements", got)
	}
}
