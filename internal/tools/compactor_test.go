package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// ANSI & hard truncation
// ---------------------------------------------------------------------------

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
		{"negative limit", "text", -1, false},
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

func TestHardTruncateLines_ZeroLimit(t *testing.T) {
	_, applied := hardTruncateLines("line1\nline2", 0)
	if applied {
		t.Error("zero limit should not truncate")
	}
}

func TestHardTruncateLines_UnderLimit(t *testing.T) {
	_, applied := hardTruncateLines("line1\nline2", 100)
	if applied {
		t.Error("should not truncate when under limit")
	}
}

// ---------------------------------------------------------------------------
// Command detection
// ---------------------------------------------------------------------------

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
	positives := []string{"go test ./...", "pytest -v", "npm test", "jest --watch", "cargo test"}
	for _, cmd := range positives {
		if !isTestCommand(cmd) {
			t.Errorf("should detect %q as test command", cmd)
		}
	}
	if isTestCommand("go build ./...") {
		t.Error("should not detect go build as test")
	}
}

func TestIsBuildCommand(t *testing.T) {
	positives := []string{"go build ./...", "make all", "cargo build", "npm run build", "gcc -o main", "g++ main.cpp"}
	for _, cmd := range positives {
		if !isBuildCommand(cmd) {
			t.Errorf("should detect %q as build command", cmd)
		}
	}
	if isBuildCommand("go test ./...") {
		t.Error("should not detect go test as build")
	}
}

func TestIsGitCommand(t *testing.T) {
	if !isGitCommand("git status") {
		t.Error("should detect git command")
	}
	if !isGitCommand("  git diff") {
		t.Error("should detect git with leading spaces")
	}
	if isGitCommand("echo git") {
		t.Error("should not detect non-git command")
	}
}

func TestIsLinterCommand(t *testing.T) {
	positives := []string{"golangci-lint run", "eslint src/", "pylint module", "flake8 .", "cargo clippy"}
	for _, cmd := range positives {
		if !isLinterCommand(cmd) {
			t.Errorf("should detect %q as linter command", cmd)
		}
	}
	if isLinterCommand("go build ./...") {
		t.Error("should not detect go build as linter")
	}
}

// ---------------------------------------------------------------------------
// Test output aggregation
// ---------------------------------------------------------------------------

func TestAggregateTestOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

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

func TestAggregateTestOutput_TooShort(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := aggregateTestOutput("short output", cfg)
	if applied {
		t.Error("should not apply to short output")
	}
}

func TestAggregateTestOutput_NoParseable(t *testing.T) {
	cfg := DefaultCompactorConfig()
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "some random output")
	}
	_, applied := aggregateTestOutput(strings.Join(lines, "\n"), cfg)
	if applied {
		t.Error("should not apply when no test patterns found")
	}
}

// ---------------------------------------------------------------------------
// Build output filtering
// ---------------------------------------------------------------------------

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

func TestFilterBuildOutput_TooShort(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := filterBuildOutput("short", cfg)
	if applied {
		t.Error("should not apply to short output")
	}
}

// ---------------------------------------------------------------------------
// Linter aggregation
// ---------------------------------------------------------------------------

func TestAggregateLinterOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, "main.go:1:5: some warning")
	}
	for i := 0; i < 30; i++ {
		lines = append(lines, "util.go:2:3: another issue")
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

func TestAggregateLinterOutput_TooShort(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := aggregateLinterOutput("short", cfg)
	if applied {
		t.Error("should not apply to short output")
	}
}

// ---------------------------------------------------------------------------
// Git text compaction
// ---------------------------------------------------------------------------

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

func TestCompactGitDiffText_Short(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := compactGitDiffText("short diff", cfg)
	if applied {
		t.Error("should not compact short diff")
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

func TestCompactGitLogText_Short(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := compactGitLogText("commit abc\nAuthor: test\nshort log", cfg)
	if applied {
		t.Error("should not compact short log")
	}
}

func TestCompactGitStatusText(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	lines = append(lines, "On branch main")
	lines = append(lines, "Changes not staged for commit:")
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("M  file%d.go", i))
	}

	input := strings.Join(lines, "\n")
	got, applied := compactGitStatusText(input, cfg)

	if !applied {
		t.Fatal("compactGitStatusText should have applied")
	}
	if !strings.Contains(got, "more files") {
		t.Error("should indicate truncated files")
	}
	if len(got) >= len(input) {
		t.Error("compacted output should be shorter")
	}
}

func TestCompactGitStatusText_Short(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := compactGitStatusText("M file.go", cfg)
	if applied {
		t.Error("should not compact short status")
	}
}

func TestCompactGitBashOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

	// Test git diff routing
	var diffLines []string
	diffLines = append(diffLines, "diff --git a/a.go b/a.go")
	diffLines = append(diffLines, "@@ -1,5 +1,7 @@")
	for i := 0; i < 200; i++ {
		diffLines = append(diffLines, "+line")
	}
	diffInput := strings.Join(diffLines, "\n")
	_, applied := compactGitBashOutput(diffInput, "git diff", cfg)
	if !applied {
		t.Error("should compact git diff output")
	}

	// Test git status routing
	var statusLines []string
	statusLines = append(statusLines, "On branch main")
	for i := 0; i < 50; i++ {
		statusLines = append(statusLines, fmt.Sprintf("M  file%d.go", i))
	}
	statusInput := strings.Join(statusLines, "\n")
	_, applied = compactGitBashOutput(statusInput, "git status", cfg)
	if !applied {
		t.Error("should compact git status output")
	}

	// Test unknown git subcommand
	_, applied = compactGitBashOutput("some output", "git branch", cfg)
	if applied {
		t.Error("should not compact unknown git subcommand")
	}
}

// ---------------------------------------------------------------------------
// Search grouping
// ---------------------------------------------------------------------------

func TestGroupSearchOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()

	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "fileA.go:1: match content")
	}
	for i := 0; i < 50; i++ {
		lines = append(lines, "fileB.go:2: other content")
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

func TestGroupSearchOutput_TooShort(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := groupSearchOutput("fileA.go:1: match", cfg)
	if applied {
		t.Error("should not group short output")
	}
}

// ---------------------------------------------------------------------------
// Source code filtering
// ---------------------------------------------------------------------------

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

func TestFilterSourceCode_Aggressive(t *testing.T) {
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "// line comment")
		lines = append(lines, "# python comment")
		lines = append(lines, "/* block start")
		lines = append(lines, "   block middle */")
		lines = append(lines, "func foo() {}")
		lines = append(lines, "")
		lines = append(lines, "")
	}

	input := strings.Join(lines, "\n")
	got, applied := filterSourceCode(input, "aggressive")

	if !applied {
		t.Fatal("filterSourceCode aggressive should have applied")
	}
	if strings.Contains(got, "// line comment") {
		t.Error("aggressive mode should remove line comments")
	}
	if strings.Contains(got, "# python comment") {
		t.Error("aggressive mode should remove hash comments")
	}
	if len(got) >= len(input) {
		t.Error("filtered output should be shorter")
	}
}

func TestFilterSourceCode_TooShort(t *testing.T) {
	_, applied := filterSourceCode("// short\nfunc main() {}", "aggressive")
	if applied {
		t.Error("should not filter short files")
	}
}

// ---------------------------------------------------------------------------
// Smart truncation
// ---------------------------------------------------------------------------

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

func TestSmartTruncate_UnderLimit(t *testing.T) {
	cfg := DefaultCompactorConfig()
	_, applied := smartTruncate("short", cfg)
	if applied {
		t.Error("should not truncate when under limit")
	}
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

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

func TestCompactMetrics_Empty(t *testing.T) {
	m := NewCompactMetrics()
	stats := m.FormatStats()
	if !strings.Contains(stats, "No compaction records") {
		t.Error("empty metrics should say no records")
	}
}

func TestCompactMetrics_Save(t *testing.T) {
	m := NewCompactMetrics()
	m.Record([]string{"ansi"}, 1000, 500, "bash")

	dir := t.TempDir()
	err := m.Save(dir)
	if err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "compactor-metrics.json"))
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if !strings.Contains(string(data), "bash") {
		t.Error("saved file should contain tool name")
	}
	if !strings.Contains(string(data), "summary") {
		t.Error("saved file should contain summary")
	}
}

func TestCompactMetrics_SaveEmpty(t *testing.T) {
	m := NewCompactMetrics()
	err := m.Save(t.TempDir())
	if err != nil {
		t.Fatalf("Save() empty should not error: %v", err)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{500, "500B"},
		{1024, "1.0KB"},
		{2048, "2.0KB"},
		{1048576, "1.0MB"},
		{2097152, "2.0MB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.n)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Core router and helpers
// ---------------------------------------------------------------------------

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

func TestCompactToolResult_AllTools(t *testing.T) {
	cfg := DefaultCompactorConfig()

	// Unknown tool
	if compactToolResult("unknown_tool", nil, nil, cfg) != nil {
		t.Error("unknown tool should return nil")
	}

	// Bash with large test output
	bashResult := map[string]any{
		"stdout": generateLargeTestOutput(),
		"stderr": "",
	}
	bashArgs := map[string]any{"command": "go test ./..."}
	cr := compactToolResult("bash", bashArgs, bashResult, cfg)
	if cr == nil {
		t.Fatal("bash compaction should return result for large test output")
	}
	if cr.CompSize >= cr.OrigSize {
		t.Error("compacted size should be smaller")
	}

	// Read with large content
	readResult := map[string]any{"content": generateLargeSourceCode()}
	cr = compactToolResult("read", nil, readResult, cfg)
	if cr == nil {
		t.Fatal("read compaction should return result for large source code")
	}

	// Grep with many matches — built from the real GrepOutput struct, not a
	// synthetic "output" map (the shape that hid the defect).
	cr = compactToolResult("ripgrep", nil, buildProdResult(t, largeGrepOutput(400)), cfg)
	if cr == nil {
		t.Fatal("ripgrep compaction should return a result for large real output")
	}

	// Find with many files
	cr = compactToolResult("find", nil, buildProdResult(t, largeFindOutput(400)), cfg)
	if cr == nil {
		t.Fatal("find compaction should return a result for large real output")
	}

	// Tree carries its listing in "tree", not "files".
	treeResult := buildProdResult(t, TreeOutput{Tree: generateLargeFindOutput(), Dirs: 3, Files: 9})
	cr = compactToolResult("tree", nil, treeResult, cfg)
	if cr == nil {
		t.Fatal("tree compaction should return a result for large real output")
	}

	// ls was registered with no pipeline at all before this change.
	entries := make([]LsEntry, 400)
	for i := range entries {
		entries[i] = LsEntry{Name: fmt.Sprintf("e%d.go", i), Size: int64(i)}
	}
	cr = compactToolResult("ls", nil, buildProdResult(t, LsOutput{Entries: entries, TotalEntries: 400}), cfg)
	if cr == nil {
		t.Fatal("ls compaction should return a result — it had no route before")
	}
}

func TestRunStage_PanicRecovery(t *testing.T) {
	var techniques []string
	input := "test input"

	output := runStage(input, &techniques, "panicking", func(s string) (string, bool) {
		panic("test panic")
	})

	if output != input {
		t.Error("runStage should return original input on panic")
	}
}

func TestRunStage_Applied(t *testing.T) {
	var techniques []string
	output := runStage("hello", &techniques, "upper", func(s string) (string, bool) {
		return strings.ToUpper(s), true
	})
	if output != "HELLO" {
		t.Errorf("got %q, want HELLO", output)
	}
	if len(techniques) != 1 || techniques[0] != "upper" {
		t.Errorf("techniques = %v, want [upper]", techniques)
	}
}

func TestRunStage_NotApplied(t *testing.T) {
	var techniques []string
	output := runStage("hello", &techniques, "noop", func(s string) (string, bool) {
		return s, false
	})
	if output != "hello" {
		t.Errorf("got %q, want hello", output)
	}
	if len(techniques) != 0 {
		t.Errorf("techniques should be empty, got %v", techniques)
	}
}

// ---------------------------------------------------------------------------
// applyCompaction
// ---------------------------------------------------------------------------

func TestApplyCompaction_WritesOnlyItsOwnKey(t *testing.T) {
	// Each pipeline names the field it read. applyCompaction must write exactly
	// that key and nothing else — the old fixed probe order (stdout → content →
	// output → diff) meant a correct pipeline could write to the wrong field.
	for _, tc := range []struct {
		name string
		key  string
		val  any
	}{
		{"bash", "stdout", "compacted"},
		{"read", "content", "compacted"},
		{"git-file-diff", "diff", "compacted"},
		{"grep", "matches", []any{map[string]any{"file": "a.go"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := map[string]any{tc.key: "original"}
			wrote := applyCompaction(result, &CompactResult{
				Writes: []CompactWrite{{Key: tc.key, Value: tc.val}},
			})
			if !wrote {
				t.Fatal("applyCompaction reported no write")
			}
			got, _ := json.Marshal(result[tc.key])
			want, _ := json.Marshal(tc.val)
			if string(got) != string(want) {
				t.Errorf("%s = %s, want %s", tc.key, got, want)
			}
		})
	}
}

func TestApplyCompaction_DoesNotGuessAField(t *testing.T) {
	// A write naming a key must not touch any other field. The old
	// implementation probed result/data as a fallback and wrote there, which is
	// how a correct pipeline could still land its output in the wrong place.
	result := map[string]any{"data": "original", "result": "original"}
	applyCompaction(result, &CompactResult{
		Writes: []CompactWrite{{Key: "matches", Value: "compacted"}},
	})
	if result["data"] != "original" || result["result"] != "original" {
		t.Errorf("fallback fields were modified: %v", result)
	}
	if result["matches"] != "compacted" {
		t.Errorf("matches = %v, want the named key written", result["matches"])
	}
}

func TestApplyCompaction_WritesOmittedKeys(t *testing.T) {
	// `truncated` carries omitempty, so a complete result has no such key.
	// The compactor must still be able to set it, or a capped list would read
	// as complete.
	result := map[string]any{"matches": []any{1, 2, 3}}
	applyCompaction(result, &CompactResult{
		Writes: []CompactWrite{{Key: "truncated", Value: true}},
	})
	if result["truncated"] != true {
		t.Errorf("truncated = %v, want true (omitempty key must be writable)", result["truncated"])
	}
}

func TestApplyCompaction_ReportsFalseWhenNothingWritten(t *testing.T) {
	if applyCompaction(map[string]any{"a": 1}, nil) {
		t.Error("nil CompactResult should report no write")
	}
	if applyCompaction(nil, &CompactResult{Writes: []CompactWrite{{Key: "a", Value: 2}}}) {
		t.Error("nil result map should report no write")
	}
	if applyCompaction(map[string]any{"a": 1}, &CompactResult{}) {
		t.Error("empty Writes should report no write")
	}
}

func TestApplyCompaction_PreservesFieldType(t *testing.T) {
	// ADK round-trips results through JSON, so a slice arrives as []any. Replacing
	// it with a string would change the shape the TUI summaries and the model see.
	orig := []any{map[string]any{"file": "a.go", "line": float64(1)}}
	result := map[string]any{"matches": orig}
	applyCompaction(result, &CompactResult{
		Writes: []CompactWrite{{Key: "matches", Value: orig[:1]}},
	})
	if _, ok := result["matches"].([]any); !ok {
		t.Errorf("matches changed type: %T", result["matches"])
	}
}

func TestApplyCompaction_NilResult(t *testing.T) {
	applyCompaction(nil, nil)              // should not panic
	applyCompaction(map[string]any{}, nil) // should not panic
	applyCompaction(nil, &CompactResult{}) // should not panic
}

// ---------------------------------------------------------------------------
// Pipeline-level tests: compactBash
// ---------------------------------------------------------------------------

func TestCompactBash_EmptyOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := compactBash(map[string]any{"stdout": "", "stderr": ""}, nil, cfg)
	if result != nil {
		t.Error("empty output should return nil")
	}
}

func TestCompactBash_TestCommand(t *testing.T) {
	cfg := DefaultCompactorConfig()
	args := map[string]any{"command": "go test ./..."}
	result := map[string]any{
		"stdout": generateLargeTestOutput(),
		"stderr": "",
	}
	cr := compactBash(result, args, cfg)
	if cr == nil {
		t.Fatal("should compact large test output")
	}
	if cr.CompSize >= cr.OrigSize {
		t.Error("compacted should be smaller")
	}
	if len(cr.Techniques) == 0 {
		t.Error("should report applied techniques")
	}
}

func TestCompactBash_BuildCommand(t *testing.T) {
	cfg := DefaultCompactorConfig()
	args := map[string]any{"command": "go build ./..."}

	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, "compiling package foo...")
	}
	lines = append(lines, "main.go:10:5: undefined: bar")
	lines = append(lines, "FAIL\tbuild failed")

	result := map[string]any{"stdout": strings.Join(lines, "\n"), "stderr": ""}
	cr := compactBash(result, args, cfg)
	if cr == nil {
		t.Fatal("should compact large build output")
	}
}

func TestCompactBash_GitCommand(t *testing.T) {
	cfg := DefaultCompactorConfig()
	args := map[string]any{"command": "git diff HEAD~5"}

	var lines []string
	lines = append(lines, "diff --git a/a.go b/a.go")
	lines = append(lines, "@@ -1,5 +1,7 @@")
	for i := 0; i < 200; i++ {
		lines = append(lines, "+added line")
	}

	result := map[string]any{"stdout": strings.Join(lines, "\n"), "stderr": ""}
	cr := compactBash(result, args, cfg)
	if cr == nil {
		t.Fatal("should compact large git diff via bash")
	}
}

func TestCompactBash_LinterCommand(t *testing.T) {
	cfg := DefaultCompactorConfig()
	args := map[string]any{"command": "golangci-lint run"}

	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf("main.go:%d:5: some lint issue", i))
	}

	result := map[string]any{"stdout": strings.Join(lines, "\n"), "stderr": ""}
	cr := compactBash(result, args, cfg)
	if cr == nil {
		t.Fatal("should compact large linter output")
	}
}

func TestCompactBash_StderrFallback(t *testing.T) {
	cfg := DefaultCompactorConfig()
	// When stdout is empty but stderr has content, compactBash still returns nil
	// because the pipeline works on stdout (which remains empty). The stderr is
	// only used for the initial empty check. This tests that the function doesn't panic.
	largeStderr := strings.Repeat("error line\n", 1000)
	result := map[string]any{"stdout": "", "stderr": largeStderr}
	// Should not panic, result may or may not be nil depending on pipeline savings
	compactBash(result, nil, cfg)
}

func TestCompactBash_SmallOutput(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := map[string]any{"stdout": "small output", "stderr": ""}
	cr := compactBash(result, nil, cfg)
	if cr != nil {
		t.Error("small output should not be compacted (no savings)")
	}
}

func TestCompactBash_AnsiStripping(t *testing.T) {
	cfg := DefaultCompactorConfig()
	// Large output with ANSI codes
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, "\x1b[31merror: something\x1b[0m")
	}
	result := map[string]any{"stdout": strings.Join(lines, "\n"), "stderr": ""}
	cr := compactBash(result, nil, cfg)
	if cr == nil {
		t.Fatal("should compact large ANSI output")
	}
	if !strings.Contains(strings.Join(cr.Techniques, ","), "ansi") {
		t.Error("should report ansi technique")
	}
}

// ---------------------------------------------------------------------------
// Pipeline-level tests: compactRead
// ---------------------------------------------------------------------------

func TestCompactRead_Empty(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cr := compactRead(map[string]any{"content": ""}, nil, cfg)
	if cr != nil {
		t.Error("empty content should return nil")
	}
}

func TestCompactRead_LargeSourceCode(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.SourceCodeFiltering = "minimal"
	result := map[string]any{"content": generateLargeSourceCode()}
	cr := compactRead(result, nil, cfg)
	if cr == nil {
		t.Fatal("should compact large source code")
	}
	if cr.CompSize >= cr.OrigSize {
		t.Error("compacted should be smaller")
	}
}

func TestCompactRead_WithAnsi(t *testing.T) {
	cfg := DefaultCompactorConfig()
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, "\x1b[32mfunc foo() {}\x1b[0m")
	}
	result := map[string]any{"content": strings.Join(lines, "\n")}
	cr := compactRead(result, nil, cfg)
	if cr == nil {
		t.Fatal("should compact large content with ANSI")
	}
}

func TestCompactRead_SmallContent(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := map[string]any{"content": "small file content"}
	cr := compactRead(result, nil, cfg)
	if cr != nil {
		t.Error("small content should not be compacted")
	}
}

// ---------------------------------------------------------------------------
// Pipeline-level tests: compactGrep / compactFind / compactTree
// ---------------------------------------------------------------------------

func TestCompactGrep_Empty(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cr := compactGrep(map[string]any{"output": ""}, nil, cfg)
	if cr != nil {
		t.Error("empty output should return nil")
	}
}

// buildProdResult mirrors ADK's functiontool.Run: a typed output struct is
// json.Marshal'd and json.Unmarshal'd into map[string]any (adk v2.4.0
// internal/typeutil/convert.go, ConvertToWithJSONSchema). Tests must build
// results this way: the old tests fed synthetic maps with an "output" key that
// no tool emits, which is exactly why seven pipelines shipped green and dead.
func buildProdResult(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func largeGrepOutput(n int) GrepOutput {
	matches := make([]GrepMatch, n)
	for i := range matches {
		matches[i] = GrepMatch{File: fmt.Sprintf("f%d.go", i), Line: i + 1, Content: strings.Repeat("b", 40)}
	}
	return GrepOutput{Matches: matches, TotalMatches: n}
}

func largeFindOutput(n int) FindOutput {
	files := make([]string, n)
	for i := range files {
		files[i] = fmt.Sprintf("dir/file%d.go", i)
	}
	return FindOutput{Files: files, TotalFiles: n}
}

func TestCompactGrep_RealStructCompacts(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := buildProdResult(t, largeGrepOutput(400))
	cr := compactGrep(result, nil, cfg)
	if cr == nil {
		t.Fatal("should compact large grep output built from GrepOutput")
	}
	if cr.CompSize >= cr.OrigSize {
		t.Errorf("compacted %d not smaller than %d", cr.CompSize, cr.OrigSize)
	}
	if len(cr.Writes) == 0 || cr.Writes[0].Key != "matches" {
		t.Fatalf("writes = %+v, want a write to matches", cr.Writes)
	}
}

func TestCompactGrep_PreservesTotalAndMarksTruncated(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := buildProdResult(t, largeGrepOutput(400))
	cr := compactGrep(result, nil, cfg)
	if cr == nil {
		t.Fatal("expected compaction")
	}
	applyCompaction(result, cr)

	// total_matches must stay the true, pre-cap count — an rtk invariant
	// (search.rs, test_grep_overflow_uses_uncapped_total).
	if got := result["total_matches"]; got != float64(400) {
		t.Errorf("total_matches = %v, want 400 (the uncapped total)", got)
	}
	if got := result["truncated"]; got != true {
		t.Errorf("truncated = %v, want true so a partial list is not read as complete", got)
	}
	matches, _ := result["matches"].([]any)
	if len(matches) != cfg.MaxSearchTotal {
		t.Errorf("matches kept %d, want %d", len(matches), cfg.MaxSearchTotal)
	}
}

func TestCompactGrep_NoOpWhenUnderLimit(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := buildProdResult(t, largeGrepOutput(5))
	if cr := compactGrep(result, nil, cfg); cr != nil {
		t.Error("a short match list must not be compacted")
	}
}

func TestCompactGrep_EmptyMatches(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := buildProdResult(t, GrepOutput{Matches: []GrepMatch{}, TotalMatches: 0})
	if cr := compactGrep(result, nil, cfg); cr != nil {
		t.Error("no matches should return nil")
	}
}

func TestCompactFind_RealStructCompacts(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := buildProdResult(t, largeFindOutput(400))
	cr := compactFind(result, nil, cfg)
	if cr == nil {
		t.Fatal("should compact large find output built from FindOutput")
	}
	applyCompaction(result, cr)
	if got := result["total_files"]; got != float64(400) {
		t.Errorf("total_files = %v, want 400", got)
	}
	files, _ := result["files"].([]any)
	if len(files) != cfg.MaxSearchTotal {
		t.Errorf("files kept %d, want %d", len(files), cfg.MaxSearchTotal)
	}
}

func TestCompactLs_RealStructCompacts(t *testing.T) {
	cfg := DefaultCompactorConfig()
	entries := make([]LsEntry, 400)
	for i := range entries {
		entries[i] = LsEntry{Name: fmt.Sprintf("e%d.go", i), Size: int64(i)}
	}
	result := buildProdResult(t, LsOutput{Entries: entries, TotalEntries: 400})
	cr := compactLs(result, nil, cfg)
	if cr == nil {
		t.Fatal("ls must compact — it had no pipeline at all before this change")
	}
	applyCompaction(result, cr)
	if got := result["total_entries"]; got != float64(400) {
		t.Errorf("total_entries = %v, want 400", got)
	}
}

func TestCompactTree_ReadsTreeFieldNotFiles(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.MaxChars = 200
	cfg.MaxLines = 10
	result := buildProdResult(t, TreeOutput{Tree: strings.Repeat("line\n", 400), Dirs: 3, Files: 9})
	cr := compactTree(result, nil, cfg)
	if cr == nil {
		t.Fatal("tree must compact from its own tree field")
	}
	if cr.Writes[0].Key != "tree" {
		t.Errorf("wrote %q, want tree", cr.Writes[0].Key)
	}
	// A find-shaped result (files field) must NOT be treated as tree output.
	if got := compactTree(buildProdResult(t, largeFindOutput(400)), nil, cfg); got != nil {
		t.Error("compactTree must not compact a files-only result")
	}
}

func TestCompactGitFileDiff_RealStructCompacts(t *testing.T) {
	cfg := DefaultCompactorConfig()
	diff := strings.Repeat("+added line\n", 3000)
	result := buildProdResult(t, GitFileDiffOutput{File: "x.go", Diff: diff, LinesAdded: 3000})
	cr := compactGitFileDiff(result, nil, cfg)
	if cr == nil {
		t.Fatal("git-file-diff must compact: its pipeline already read diff")
	}
	if cr.Writes[0].Key != "diff" {
		t.Errorf("wrote %q, want diff", cr.Writes[0].Key)
	}
}

func TestCompactGitOverview_CapsCommitsNotFileLists(t *testing.T) {
	cfg := DefaultCompactorConfig()
	commits := make([]string, 200)
	for i := range commits {
		commits[i] = fmt.Sprintf("commit %d", i)
	}
	staged := make([]string, 30)
	for i := range staged {
		staged[i] = fmt.Sprintf("staged%d.go", i)
	}
	result := buildProdResult(t, GitOverviewOutput{
		Branch: "main", RecentCommits: commits, StagedFiles: staged,
	})
	cr := compactGitOverview(result, nil, cfg)
	if cr == nil {
		t.Fatal("git-overview must cap its commit log")
	}
	applyCompaction(result, cr)

	got, _ := result["recent_commits"].([]any)
	if len(got) != cfg.MaxLogEntries {
		t.Errorf("recent_commits kept %d, want %d", len(got), cfg.MaxLogEntries)
	}
	// The dirty-file list is deliberately NOT capped: truncating it would hide
	// which files are dirty (rtk git_cmd.rs format_status_inner does the same).
	kept, _ := result["staged_files"].([]any)
	if len(kept) != len(staged) {
		t.Errorf("staged_files truncated to %d; dirty-file lists must stay complete", len(kept))
	}
	if result["branch"] != "main" {
		t.Errorf("branch = %v, want main (never cap the branch)", result["branch"])
	}
}

func TestCompactGitHunk_ReadsHunksNotDiff(t *testing.T) {
	cfg := DefaultCompactorConfig()
	hunks := make([]Hunk, 300)
	for i := range hunks {
		hunks[i] = Hunk{Header: "@@ -1,2 +1,2 @@", Content: strings.Repeat("c", 60), Added: 1, Removed: 1}
	}
	result := buildProdResult(t, GitHunkOutput{File: "x.go", Hunks: hunks, TotalHunks: 300})
	cr := compactGitHunk(result, nil, cfg)
	if cr == nil {
		t.Fatal("git-hunk must compact its hunks")
	}
	if cr.Writes[0].Key != "hunks" {
		t.Errorf("wrote %q, want hunks (git-hunk has no diff field)", cr.Writes[0].Key)
	}
	applyCompaction(result, cr)
	if got := result["total_hunks"]; got != float64(300) {
		t.Errorf("total_hunks = %v, want 300 (preserved)", got)
	}
}

// TestCompactorRouting_EveryRegisteredTool is the guard this bug needed: every
// registered tool that emits bulk output must have a pipeline, and the routing
// keys must be the names the registry actually produces. Sourcing the list from
// CoreTools is the point — a hand-written list is how `ls` stayed invisible and
// how `grep`/`ripgrep` were counted twice.
func TestCompactorRouting_EveryRegisteredTool(t *testing.T) {
	sb, err := NewSandbox(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	built, err := CoreTools(sb)
	if err != nil {
		t.Fatal(err)
	}

	// Tools whose output the compactor is responsible for. The search tool is
	// listed separately: it registers exactly one of "grep"/"ripgrep" depending
	// on whether rg is installed (grep.go:128-133), so requiring a specific one
	// here would fail on a host without rg.
	wantPipelines := map[string]bool{
		"bash": true, "read": true, "find": true,
		"tree": true, "ls": true, "git-file-diff": true,
		"git-overview": true, "git-hunk": true,
	}

	registered := map[string]bool{}
	for _, x := range built {
		registered[x.Name()] = true
		if _, isSearch := compactorPipelines[x.Name()]; !isSearch {
			if wantPipelines[x.Name()] {
				t.Errorf("registered tool %q has no compaction pipeline", x.Name())
			}
		}
	}

	for name := range wantPipelines {
		if !registered[name] {
			t.Errorf("expected %q among registered tools; registry changed shape", name)
		}
	}

	// Exactly one search spelling is registered, and both must route.
	searchRegistered := 0
	for _, name := range []string{"grep", "ripgrep"} {
		if registered[name] {
			searchRegistered++
		}
		if _, ok := compactorPipelines[name]; !ok {
			t.Errorf("route missing for %q", name)
		}
	}
	if searchRegistered != 1 {
		t.Errorf("expected exactly one of grep/ripgrep registered, got %d "+
			"(rgAvailable=%v)", searchRegistered, rgAvailable)
	}

	// Underscore spellings must NOT be the routing keys — they are what made the
	// git pipelines unreachable.
	for _, wrong := range []string{"git_file_diff", "git_overview", "git_hunk"} {
		if _, ok := compactorPipelines[wrong]; ok {
			t.Errorf("routing key %q is not a registered tool name", wrong)
		}
	}
}

// ---------------------------------------------------------------------------
// Dedup
// ---------------------------------------------------------------------------

func TestDedup(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b"}
	got := dedup(input)
	if len(got) != 3 {
		t.Errorf("dedup() = %v, want 3 unique elements", got)
	}
}

func TestDedup_Empty(t *testing.T) {
	got := dedup(nil)
	if len(got) != 0 {
		t.Error("dedup(nil) should return empty")
	}
}

// ---------------------------------------------------------------------------
// Test data generators
// ---------------------------------------------------------------------------

func generateLargeTestOutput() string {
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("=== RUN   TestFunc%d", i))
		if i%10 == 5 {
			lines = append(lines, fmt.Sprintf("--- FAIL: TestFunc%d (0.01s)", i))
			lines = append(lines, fmt.Sprintf("    file_test.go:%d: expected true, got false", i*10))
		} else {
			lines = append(lines, fmt.Sprintf("--- PASS: TestFunc%d (0.00s)", i))
		}
	}
	lines = append(lines, "ok  \tpkg/one\t0.5s")
	lines = append(lines, "FAIL\tpkg/two\t0.1s")
	return strings.Join(lines, "\n")
}

func generateLargeSourceCode() string {
	var lines []string
	lines = append(lines, "package main")
	lines = append(lines, "")
	for i := 0; i < 200; i++ {
		lines = append(lines, "// This is a documentation comment for function")
		lines = append(lines, fmt.Sprintf("func function%d() {", i))
		lines = append(lines, "    // do something")
		lines = append(lines, fmt.Sprintf("    return %d", i))
		lines = append(lines, "}")
		lines = append(lines, "")
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func generateLargeSearchOutput() string {
	var lines []string
	for i := 0; i < 150; i++ {
		file := fmt.Sprintf("pkg/mod%d/file.go", i%5)
		lines = append(lines, fmt.Sprintf("%s:%d: matching content here", file, i))
	}
	return strings.Join(lines, "\n")
}

func generateLargeFindOutput() string {
	var lines []string
	for i := 0; i < 600; i++ {
		lines = append(lines, fmt.Sprintf("./pkg/module%d/file%d.go", i%10, i))
	}
	return strings.Join(lines, "\n")
}

func generateLargeDiff() string {
	var lines []string
	for f := 0; f < 5; f++ {
		lines = append(lines, fmt.Sprintf("diff --git a/file%d.go b/file%d.go", f, f))
		lines = append(lines, fmt.Sprintf("--- a/file%d.go", f))
		lines = append(lines, fmt.Sprintf("+++ b/file%d.go", f))
		lines = append(lines, "@@ -1,20 +1,30 @@")
		for i := 0; i < 40; i++ {
			lines = append(lines, "+added line content here")
		}
	}
	return strings.Join(lines, "\n")
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

func BenchmarkStripAnsi(b *testing.B) {
	// Build input with ANSI codes every few chars
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString("\x1b[31merror: something went wrong\x1b[0m\n")
	}
	input := sb.String()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		stripAnsi(input)
	}
}

func BenchmarkHardTruncate(b *testing.B) {
	input := strings.Repeat("x", 100000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hardTruncate(input, 24000)
	}
}

func BenchmarkHardTruncateLines(b *testing.B) {
	input := strings.Repeat("line content\n", 5000)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		hardTruncateLines(input, 440)
	}
}

func BenchmarkAggregateTestOutput(b *testing.B) {
	input := generateLargeTestOutput()
	cfg := DefaultCompactorConfig()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		aggregateTestOutput(input, cfg)
	}
}

func BenchmarkFilterBuildOutput(b *testing.B) {
	var lines []string
	for i := 0; i < 500; i++ {
		lines = append(lines, "compiling package foo...")
	}
	lines = append(lines, "main.go:10:5: undefined: bar")
	input := strings.Join(lines, "\n")
	cfg := DefaultCompactorConfig()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		filterBuildOutput(input, cfg)
	}
}

func BenchmarkCompactGitDiffText(b *testing.B) {
	input := generateLargeDiff()
	cfg := DefaultCompactorConfig()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		compactGitDiffText(input, cfg)
	}
}

func BenchmarkGroupSearchOutput(b *testing.B) {
	input := generateLargeSearchOutput()
	cfg := DefaultCompactorConfig()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		groupSearchOutput(input, cfg)
	}
}

func BenchmarkSmartTruncate(b *testing.B) {
	cfg := DefaultCompactorConfig()
	cfg.MaxLines = 50
	var lines []string
	for i := 0; i < 2000; i++ {
		if i%20 == 0 {
			lines = append(lines, "error: something went wrong")
		} else {
			lines = append(lines, "normal output line here")
		}
	}
	input := strings.Join(lines, "\n")

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		smartTruncate(input, cfg)
	}
}

func BenchmarkFilterSourceCode(b *testing.B) {
	input := generateLargeSourceCode()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		filterSourceCode(input, "aggressive")
	}
}

func BenchmarkCompactBash_FullPipeline(b *testing.B) {
	cfg := DefaultCompactorConfig()
	args := map[string]any{"command": "go test ./..."}
	result := map[string]any{
		"stdout": generateLargeTestOutput(),
		"stderr": "",
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		compactBash(result, args, cfg)
	}
}

func BenchmarkCompactRead_FullPipeline(b *testing.B) {
	cfg := DefaultCompactorConfig()
	cfg.SourceCodeFiltering = "minimal"
	result := map[string]any{"content": generateLargeSourceCode()}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		compactRead(result, nil, cfg)
	}
}

func BenchmarkCompactGitFileDiff_FullPipeline(b *testing.B) {
	cfg := DefaultCompactorConfig()
	result := map[string]any{"diff": generateLargeDiff()}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		compactGitFileDiff(result, nil, cfg)
	}
}

// -------------------------------------------------------------------
// BuildCompactorCallback tests
// -------------------------------------------------------------------

func TestBuildCompactorCallback_Disabled(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.Enabled = false
	metrics := NewCompactMetrics()

	cb := BuildCompactorCallback(cfg, metrics)
	// When disabled, should return result unchanged
	result := map[string]any{"content": "test"}
	got, err := cb(nil, nil, nil, result, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["content"] != "test" {
		t.Errorf("expected result unchanged, got %v", got)
	}
}

func TestBuildCompactorCallback_WithError(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.Enabled = true
	metrics := NewCompactMetrics()

	cb := BuildCompactorCallback(cfg, metrics)
	// When there's an error, should return result unchanged
	result := map[string]any{"content": "test"}
	got, err := cb(nil, nil, nil, result, fmt.Errorf("some error"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["content"] != "test" {
		t.Errorf("expected result unchanged on error, got %v", got)
	}
}

func TestCompactToolResult_UnknownTool(t *testing.T) {
	cfg := DefaultCompactorConfig()
	result := compactToolResult("unknown_tool", nil, map[string]any{"content": "test"}, cfg)
	// Should return nil for unknown tools (no compaction)
	if result != nil {
		t.Errorf("expected nil for unknown tool, got %v", result)
	}
}

// fakeCompactTool implements tool.Tool for testing compactor callback.
type fakeCompactTool struct{ name string }

func (f *fakeCompactTool) Name() string        { return f.name }
func (f *fakeCompactTool) Description() string { return "" }
func (f *fakeCompactTool) IsLongRunning() bool { return false }

func TestBuildCompactorCallback_KnownToolCompacts(t *testing.T) {
	cfg := DefaultCompactorConfig()
	cfg.Enabled = true
	// Force aggressive truncation so bash output is compacted.
	cfg.MaxChars = 32
	cfg.MaxLines = 4

	metrics := NewCompactMetrics()
	cb := BuildCompactorCallback(cfg, metrics)

	// Build multi-line stdout that exceeds the configured limits.
	lines := make([]string, 200)
	for i := range lines {
		lines[i] = strings.Repeat("A", 32)
	}
	bigOutput := strings.Join(lines, "\n")

	result := map[string]any{
		"stdout":    bigOutput,
		"stderr":    "",
		"exit_code": 0,
	}
	args := map[string]any{"command": "echo hi"}

	got, err := cb(nil, &fakeCompactTool{name: "bash"}, args, result, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Something should have been compacted.
	if s, _ := got["stdout"].(string); len(s) >= len(bigOutput) {
		t.Errorf("expected compacted stdout, got size %d (was %d)", len(s), len(bigOutput))
	}
	summary := metrics.Summary()
	if summary.TotalOrig == 0 {
		t.Errorf("expected compactor metrics to record compaction, got empty summary")
	}
}

// TestCompact_NeverWorseGuard pins the rtk invariant that a filter must not
// emit more bytes than the original (tmp/rtk/src/core/guard.rs). A cap that
// saves nothing must return nil rather than rewrite the result.
func TestCompact_NeverWorseGuard(t *testing.T) {
	cfg := DefaultCompactorConfig()
	// A tiny payload: capping one match cannot beat the cost of adding
	// "truncated", so the pipeline must decline.
	small := buildProdResult(t, GrepOutput{
		Matches:      []GrepMatch{{File: "a.go", Line: 1, Content: "x"}},
		TotalMatches: 1,
	})
	cfg.MaxSearchTotal = 0 // force a cut of the single element
	if cr := compactGrep(small, nil, cfg); cr != nil && cr.CompSize >= cr.OrigSize {
		t.Errorf("guard failed: compSize %d >= origSize %d", cr.CompSize, cr.OrigSize)
	}
}
