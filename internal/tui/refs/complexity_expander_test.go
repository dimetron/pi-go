package refs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// selectLines carries the clamping that expandFile used to do inline. The
// table pins every boundary the original branch chain encoded: an absent
// range, a range that runs off either end, an inverted range, and the line cap
// applied with and without a range.
func TestSelectLines(t *testing.T) {
	tenLines := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10"}

	tests := []struct {
		name          string
		lines         []string
		lineRange     *LineRange
		maxLines      int
		wantStart     int
		wantEnd       int
		wantTruncated bool
	}{
		{
			name:      "no range selects the whole file",
			lines:     tenLines,
			maxLines:  500,
			wantStart: 0,
			wantEnd:   9,
		},
		{
			name:      "single line file",
			lines:     []string{"only"},
			maxLines:  500,
			wantStart: 0,
			wantEnd:   0,
		},
		{
			name:      "range converts from 1-based to 0-based",
			lines:     tenLines,
			lineRange: &LineRange{Start: 2, End: 4},
			maxLines:  500,
			wantStart: 1,
			wantEnd:   3,
		},
		{
			name:      "single line range",
			lines:     tenLines,
			lineRange: &LineRange{Start: 5, End: 5},
			maxLines:  500,
			wantStart: 4,
			wantEnd:   4,
		},
		{
			name:      "start below one clamps to the first line",
			lines:     tenLines,
			lineRange: &LineRange{Start: 0, End: 3},
			maxLines:  500,
			wantStart: 0,
			wantEnd:   2,
		},
		{
			name:      "end past EOF clamps to the last line",
			lines:     tenLines,
			lineRange: &LineRange{Start: 8, End: 99},
			maxLines:  500,
			wantStart: 7,
			wantEnd:   9,
		},
		{
			name:      "start past EOF collapses onto the last line",
			lines:     tenLines,
			lineRange: &LineRange{Start: 50, End: 60},
			maxLines:  500,
			wantStart: 9,
			wantEnd:   9,
		},
		{
			name:      "inverted range collapses to the end line",
			lines:     tenLines,
			lineRange: &LineRange{Start: 7, End: 3},
			maxLines:  500,
			wantStart: 2,
			wantEnd:   2,
		},
		{
			name:          "cap truncates a whole file",
			lines:         tenLines,
			maxLines:      3,
			wantStart:     0,
			wantEnd:       2,
			wantTruncated: true,
		},
		{
			name:          "cap truncates inside a range",
			lines:         tenLines,
			lineRange:     &LineRange{Start: 4, End: 10},
			maxLines:      2,
			wantStart:     3,
			wantEnd:       4,
			wantTruncated: true,
		},
		{
			name:      "cap larger than the selection leaves it alone",
			lines:     tenLines,
			lineRange: &LineRange{Start: 3, End: 5},
			maxLines:  100,
			wantStart: 2,
			wantEnd:   4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, truncated := selectLines(tt.lines, tt.lineRange, tt.maxLines)
			if start != tt.wantStart || end != tt.wantEnd || truncated != tt.wantTruncated {
				t.Fatalf("selectLines() = (%d, %d, %v), want (%d, %d, %v)",
					start, end, truncated, tt.wantStart, tt.wantEnd, tt.wantTruncated)
			}
			// Whatever the inputs, the bounds must be a legal slice of lines.
			if start < 0 || end >= len(tt.lines) || start > end {
				t.Fatalf("bounds (%d, %d) are not a valid slice of %d lines", start, end, len(tt.lines))
			}
		})
	}
}

// readRefFile is the validate-resolve-read step expandFile used to inline.
// Every rejection must come back as a warning with no content.
func TestReadRefFile(t *testing.T) {
	tempDir := t.TempDir()

	writeFile := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(tempDir, name), data, 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	writeFile("plain.txt", []byte("hello\nworld"))
	writeFile("blob.txt", append([]byte("text"), make([]byte, 512)...))

	e := NewExpander(tempDir)

	tests := []struct {
		name        string
		path        string
		wantContent string
		wantWarning string // substring; empty means no warning expected
	}{
		{
			name:        "reads a relative path against the work dir",
			path:        "plain.txt",
			wantContent: "hello\nworld",
		},
		{
			name:        "reads an absolute path unchanged",
			path:        filepath.Join(tempDir, "plain.txt"),
			wantContent: "hello\nworld",
		},
		{
			name:        "empty path",
			path:        "",
			wantWarning: "file path is empty",
		},
		{
			name:        "path rejected by the validator",
			path:        "../escape.txt",
			wantWarning: "file ../escape.txt:",
		},
		{
			name:        "binary extension rejected by the validator",
			path:        "image.png",
			wantWarning: "binary files are not supported",
		},
		{
			name:        "missing file",
			path:        "nope.txt",
			wantWarning: "file nope.txt:",
		},
		{
			name:        "binary content behind a text extension",
			path:        "blob.txt",
			wantWarning: "file blob.txt: binary files are not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, warning := e.readRefFile(tt.path)
			if tt.wantWarning != "" {
				if !strings.Contains(warning, tt.wantWarning) {
					t.Fatalf("warning = %q, want it to contain %q", warning, tt.wantWarning)
				}
				if data != nil {
					t.Fatalf("data = %q, want nil alongside a warning", data)
				}
				return
			}
			if warning != "" {
				t.Fatalf("unexpected warning: %s", warning)
			}
			if string(data) != tt.wantContent {
				t.Fatalf("data = %q, want %q", data, tt.wantContent)
			}
		})
	}
}

// An Expander with no work dir must not join relative paths onto "".
func TestReadRefFile_NoWorkDirUsesPathAsGiven(t *testing.T) {
	tempDir := t.TempDir()
	abs := filepath.Join(tempDir, "plain.txt")
	if err := os.WriteFile(abs, []byte("body"), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	e := NewExpander("")
	data, warning := e.readRefFile(abs)
	if warning != "" {
		t.Fatalf("unexpected warning: %s", warning)
	}
	if string(data) != "body" {
		t.Fatalf("data = %q, want %q", data, "body")
	}
}

// expandFile's own remaining branches: the header shape with and without a
// range, and the truncation footer.
func TestExpandFile_HeaderAndTruncationFooter(t *testing.T) {
	tempDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tempDir, "many.txt"), []byte("a\nb\nc\nd"), 0o600); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	e := NewExpander(tempDir)
	e.maxLines = 2

	whole, warning := e.expandFile(ParsedRef{Type: RefFile, RawValue: "many.txt", Value: "many.txt"})
	if warning != "" {
		t.Fatalf("unexpected warning: %s", warning)
	}
	if !strings.HasPrefix(whole, "[Referenced file: many.txt]") {
		t.Fatalf("content = %q, want the plain header", whole)
	}
	if !strings.Contains(whole, "[Truncated: file exceeds 2 line limit]") {
		t.Fatalf("content = %q, want the truncation footer", whole)
	}
	if !strings.Contains(whole, "```\na\nb\n```") {
		t.Fatalf("content = %q, want only the first 2 lines", whole)
	}

	ranged, warning := e.expandFile(ParsedRef{
		Type:      RefFile,
		RawValue:  "many.txt",
		Value:     "many.txt",
		LineRange: &LineRange{Start: 3, End: 4},
	})
	if warning != "" {
		t.Fatalf("unexpected warning: %s", warning)
	}
	if !strings.HasPrefix(ranged, "[Referenced file: many.txt:3-4]") {
		t.Fatalf("content = %q, want the ranged header", ranged)
	}
	if strings.Contains(ranged, "[Truncated") {
		t.Fatalf("content = %q, want no truncation footer for a 2-line range", ranged)
	}

	if _, warning := e.expandFile(ParsedRef{Type: RefFile}); warning != "file path is empty" {
		t.Fatalf("warning = %q, want the empty-path warning to reach the caller", warning)
	}
}
