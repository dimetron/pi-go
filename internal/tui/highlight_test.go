package tui

import (
	"strings"
	"testing"
)

// --- highlightCode ---

func TestHighlightCode_GoFile(t *testing.T) {
	code := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}"
	result := highlightCode(code, "main.go")

	// Should contain ANSI escape codes (chroma terminal256 output).
	if !strings.Contains(result, "\033[") {
		t.Error("expected ANSI escape codes in highlighted output")
	}
	// Should preserve all tokens.
	if !strings.Contains(result, "package") {
		t.Error("expected 'package' keyword in output")
	}
	if !strings.Contains(result, "main") {
		t.Error("expected 'main' in output")
	}
	if !strings.Contains(result, "fmt") {
		t.Error("expected 'fmt' in output")
	}
}

func TestHighlightCode_PythonFile(t *testing.T) {
	code := "def hello():\n    print('hi')"
	result := highlightCode(code, "script.py")

	if !strings.Contains(result, "\033[") {
		t.Error("expected ANSI escape codes for Python highlighting")
	}
	if !strings.Contains(result, "def") {
		t.Error("expected 'def' keyword preserved")
	}
}

func TestHighlightCode_UnknownExtension(t *testing.T) {
	code := "some plain text content"
	result := highlightCode(code, "file.xyz")

	// Should not crash; returns content (possibly with fallback styling).
	if !strings.Contains(result, "some plain text") {
		t.Error("expected content preserved for unknown extension")
	}
}

func TestHighlightCode_EmptyCode(t *testing.T) {
	result := highlightCode("", "main.go")
	// Should not crash on empty input.
	if result != "" {
		// Chroma may output trailing whitespace; just verify no crash.
		_ = result
	}
}

func TestHighlightCode_JSONFile(t *testing.T) {
	code := `{"key": "value", "num": 42}`
	result := highlightCode(code, "data.json")
	if !strings.Contains(result, "key") {
		t.Error("expected 'key' in highlighted JSON output")
	}
}

// --- highlightReadOutput ---

func TestHighlightReadOutput_BasicGoFile(t *testing.T) {
	lines := []string{
		"     1\tpackage main",
		"     2\t",
		"     3\tfunc main() {}",
	}
	result := highlightReadOutput(lines, "main.go")

	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(result))
	}
	// Each line should contain the line number.
	if !strings.Contains(result[0], "1") {
		t.Error("expected line number 1 in first line")
	}
	if !strings.Contains(result[2], "3") {
		t.Error("expected line number 3 in third line")
	}
	// Should contain ANSI codes from highlighting.
	if !strings.Contains(result[0], "\033[") {
		t.Error("expected ANSI escape codes in highlighted output")
	}
}

func TestHighlightReadOutput_NoTabSeparator(t *testing.T) {
	// Lines without tab separator (e.g. truncation messages).
	lines := []string{
		"     1\tpackage main",
		"... (truncated)",
	}
	result := highlightReadOutput(lines, "main.go")

	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	// Line without tab should still be present.
	if !strings.Contains(result[1], "truncated") {
		t.Error("expected truncation message preserved")
	}
}

func TestHighlightReadOutput_EmptyLines(t *testing.T) {
	lines := []string{}
	result := highlightReadOutput(lines, "main.go")
	if len(result) != 0 {
		t.Errorf("expected 0 lines for empty input, got %d", len(result))
	}
}

func TestHighlightReadOutput_PreservesLineNumberFormat(t *testing.T) {
	lines := []string{
		"    42\tx := 1",
		"    43\ty := 2",
	}
	result := highlightReadOutput(lines, "test.go")

	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	// Line numbers should be present (they get styled by lipgloss).
	if !strings.Contains(result[0], "42") {
		t.Error("expected line number 42 preserved")
	}
	if !strings.Contains(result[1], "43") {
		t.Error("expected line number 43 preserved")
	}
}

// --- highlightGrepOutput ---

func TestHighlightGrepOutput_BasicMatch(t *testing.T) {
	lines := []string{
		"main.go:10: func main() {}",
		"utils.go:25: var x = 42",
	}
	result := highlightGrepOutput(lines)

	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	// Should contain ANSI codes for styling.
	if !strings.Contains(result[0], "\033[") {
		t.Error("expected ANSI escape codes in grep output")
	}
	// Should preserve file name.
	if !strings.Contains(result[0], "main.go") {
		t.Error("expected 'main.go' in output")
	}
	// Should preserve line number.
	if !strings.Contains(result[0], "10") {
		t.Error("expected line number '10' in output")
	}
}

func TestHighlightGrepOutput_TruncationNote(t *testing.T) {
	lines := []string{
		"file.go:1: x := 1",
		"... (200 total matches, truncated)",
	}
	result := highlightGrepOutput(lines)

	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	if !strings.Contains(result[1], "truncated") {
		t.Error("expected truncation note preserved")
	}
}

func TestHighlightGrepOutput_EmptyLines(t *testing.T) {
	result := highlightGrepOutput([]string{})
	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d", len(result))
	}
}

func TestHighlightGrepOutput_NoColonLine(t *testing.T) {
	lines := []string{"just a line without colons"}
	result := highlightGrepOutput(lines)
	if len(result) != 1 {
		t.Fatalf("expected 1 line, got %d", len(result))
	}
	// Should not crash, render as dim text.
	if !strings.Contains(result[0], "just a line") {
		t.Error("expected content preserved for non-match line")
	}
}

func TestHighlightGrepOutput_SyntaxHighlightedContent(t *testing.T) {
	lines := []string{
		"main.go:5: func main() {}",
	}
	result := highlightGrepOutput(lines)

	// Content portion should have chroma highlighting (ANSI codes).
	if len(result) != 1 {
		t.Fatalf("expected 1 line, got %d", len(result))
	}
	// The output should contain styled content.
	if !strings.Contains(result[0], "\033[") {
		t.Error("expected ANSI codes for syntax highlighted Go code")
	}
}

func TestHighlightGrepOutput_PythonFile(t *testing.T) {
	lines := []string{
		"script.py:3: def hello():",
	}
	result := highlightGrepOutput(lines)
	if len(result) != 1 {
		t.Fatalf("expected 1 line, got %d", len(result))
	}
	if !strings.Contains(result[0], "script.py") {
		t.Error("expected file name preserved")
	}
}

// --- highlightFindOutput ---

func TestHighlightFindOutput_FileList(t *testing.T) {
	lines := []string{
		"internal/tools/read.go",
		"internal/tools/write.go",
		"internal/tui/tui.go",
	}
	result := highlightFindOutput(lines)

	if len(result) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(result))
	}
	// Should contain ANSI codes for coloring.
	for i, line := range result {
		if !strings.Contains(line, "\033[") {
			t.Errorf("line %d: expected ANSI codes for file path coloring", i)
		}
	}
	if !strings.Contains(result[0], "read.go") {
		t.Error("expected file path preserved")
	}
}

func TestHighlightFindOutput_WithDirectories(t *testing.T) {
	lines := []string{
		"src/",
		"src/main.go",
	}
	result := highlightFindOutput(lines)

	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	// Both should have ANSI codes but potentially different colors.
	if !strings.Contains(result[0], "src/") {
		t.Error("expected directory path preserved")
	}
}

func TestHighlightFindOutput_TruncationNote(t *testing.T) {
	lines := []string{
		"file1.go",
		"... (500 total files, truncated)",
	}
	result := highlightFindOutput(lines)

	if len(result) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(result))
	}
	if !strings.Contains(result[1], "truncated") {
		t.Error("expected truncation note preserved")
	}
}

func TestHighlightFindOutput_EmptyLines(t *testing.T) {
	result := highlightFindOutput([]string{})
	if len(result) != 0 {
		t.Errorf("expected 0 lines, got %d", len(result))
	}
}
