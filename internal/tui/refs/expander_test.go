package refs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpander_Expand(t *testing.T) {
	// Create a temp directory for testing.
	tempDir, err := os.MkdirTemp("", "refs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test files.
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("line 1\nline 2\nline 3\nline 4\nline 5"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create a subdirectory with files.
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	subFile := filepath.Join(subDir, "nested.txt")
	if err := os.WriteFile(subFile, []byte("nested content"), 0644); err != nil {
		t.Fatalf("failed to create nested file: %v", err)
	}

	e := NewExpander(tempDir)

	tests := []struct {
		name          string
		input         string
		wantErr       bool
		wantWarnings  int
		wantExpanded  bool
		checkContains string
	}{
		{
			name:         "no refs",
			input:        "just a message",
			wantErr:      false,
			wantWarnings: 0,
			wantExpanded: false,
		},
		{
			name:          "file ref",
			input:         "@file:test.txt",
			wantErr:       false,
			wantWarnings:  0,
			wantExpanded:  true,
			checkContains: "line 1",
		},
		{
			name:          "file ref with line range",
			input:         "@file:test.txt:2-3",
			wantErr:       false,
			wantWarnings:  0,
			wantExpanded:  true,
			checkContains: "line 2",
		},
		{
			name:         "file not found",
			input:        "@file:nonexistent.txt",
			wantErr:      false,
			wantWarnings: 1,
			wantExpanded: false,
		},
		{
			name:          "folder ref",
			input:         "@folder:subdir",
			wantErr:       false,
			wantWarnings:  0,
			wantExpanded:  true,
			checkContains: "nested.txt",
		},
		{
			name:         "diff stub",
			input:        "@diff",
			wantErr:      false,
			wantWarnings: 1, // not implemented yet
			wantExpanded: false,
		},
		{
			name:         "staged stub",
			input:        "@staged",
			wantErr:      false,
			wantWarnings: 1, // not implemented yet
			wantExpanded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := e.Expand(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Expand() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantWarnings > 0 && len(result.Warnings) != tt.wantWarnings {
				t.Errorf("Expand() warnings = %d, want %d, warnings: %v", len(result.Warnings), tt.wantWarnings, result.Warnings)
			}

			if tt.wantExpanded {
				if tt.checkContains != "" {
					if !strings.Contains(result.Expanded, tt.checkContains) {
						t.Errorf("Expand() expanded = %q, want to contain %q", result.Expanded, tt.checkContains)
					}
				}
			}

			// Check that warnings are present for unexpanded refs.
			if tt.wantWarnings > 0 && len(result.Warnings) == 0 {
				t.Errorf("Expand() expected warnings but got none")
			}
		})
	}
}

func TestExpander_ExpandFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "refs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file with 10 lines.
	testFile := filepath.Join(tempDir, "test.txt")
	lines := make([]string, 10)
	for i := 0; i < 10; i++ {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	e := NewExpander(tempDir)

	tests := []struct {
		name         string
		path         string
		lineRange    *LineRange
		wantContains string
		wantWarning  bool
	}{
		{
			name:         "full file",
			path:         "test.txt",
			lineRange:    nil,
			wantContains: "line 1",
		},
		{
			name:         "first 3 lines",
			path:         "test.txt",
			lineRange:    &LineRange{Start: 1, End: 3},
			wantContains: "line 1",
		},
		{
			name:         "single line",
			path:         "test.txt",
			lineRange:    &LineRange{Start: 5, End: 5},
			wantContains: "line 5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := ParsedRef{
				Type:      RefFile,
				RawValue:  tt.path,
				Value:     tt.path,
				LineRange: tt.lineRange,
			}
			content, warning := e.expandFile(ref)

			if tt.wantWarning {
				if warning == "" {
					t.Errorf("expandFile() expected warning, got none")
				}
			} else {
				if warning != "" {
					t.Errorf("expandFile() unexpected warning: %s", warning)
				}
				if !strings.Contains(content, tt.wantContains) {
					t.Errorf("expandFile() = %q, want to contain %q", content, tt.wantContains)
				}
			}
		})
	}
}

func TestExpander_ExpandFolder(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "refs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test structure.
	subDir := filepath.Join(tempDir, "folder")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	files := []string{"a.txt", "b.txt", "c.txt"}
	for _, name := range files {
		if err := os.WriteFile(filepath.Join(subDir, name), []byte("content"), 0644); err != nil {
			t.Fatalf("failed to create file %s: %v", name, err)
		}
	}

	e := NewExpander(tempDir)

	content, warning := e.expandFolder(ParsedRef{
		Type:  RefFolder,
		Value: "folder",
	})

	if warning != "" {
		t.Errorf("expandFolder() unexpected warning: %s", warning)
	}

	// Check all files are present.
	for _, name := range files {
		if !strings.Contains(content, name) {
			t.Errorf("expandFolder() = %q, want to contain %s", content, name)
		}
	}
}

func TestExpandStaged(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "refs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	exp := NewExpander(tempDir)
	content, hint := exp.expandStaged(ParsedRef{Type: RefStaged})
	if content != "" {
		t.Errorf("expandStaged expected empty content, got %q", content)
	}
	if !strings.Contains(hint, "not yet implemented") {
		t.Errorf("expandStaged expected 'not yet implemented' hint, got %q", hint)
	}
}

func TestExpandGitLog(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "refs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	exp := NewExpander(tempDir)
	content, hint := exp.expandGitLog(ParsedRef{Type: RefGit})
	if content != "" {
		t.Errorf("expandGitLog expected empty content, got %q", content)
	}
	if !strings.Contains(hint, "not yet implemented") {
		t.Errorf("expandGitLog expected 'not yet implemented' hint, got %q", hint)
	}
}

func TestExpandURL(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "refs-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	exp := NewExpander(tempDir)
	content, hint := exp.expandURL(ParsedRef{Type: RefURL})
	if content != "" {
		t.Errorf("expandURL expected empty content, got %q", content)
	}
	if !strings.Contains(hint, "not yet implemented") {
		t.Errorf("expandURL expected 'not yet implemented' hint, got %q", hint)
	}
}

func TestFormatSize(t *testing.T) {
	tests := []struct {
		input    int64
		expected string
	}{
		{0, "0B"},
		{512, "512B"},
		{1023, "1023B"},
		{1024, "1.0KB"},
		{1536, "1.5KB"},
		{1024 * 1024, "1.0MB"},
		{1024*1024*100 + 512*1024, "100.5MB"},
		{1024 * 1024 * 1024, "1.0GB"},
	}

	for _, tt := range tests {
		got := formatSize(tt.input)
		if got != tt.expected {
			t.Errorf("formatSize(%d) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name      string
		content   string
		maxLines  int
		wantTrunc bool
		wantLines int
	}{
		{
			name:      "no truncation needed",
			content:   "line1\nline2\nline3",
			maxLines:  5,
			wantTrunc: false,
			wantLines: 3,
		},
		{
			name:      "truncation needed",
			content:   "line1\nline2\nline3\nline4\nline5",
			maxLines:  3,
			wantTrunc: true,
			wantLines: 3,
		},
		{
			name:      "exact limit",
			content:   "line1\nline2\nline3",
			maxLines:  3,
			wantTrunc: false,
			wantLines: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncated, wasTrunc := Truncate(tt.content, tt.maxLines)
			if wasTrunc != tt.wantTrunc {
				t.Errorf("Truncate() wasTrunc = %v, want %v", wasTrunc, tt.wantTrunc)
			}
			gotLines := len(strings.Split(truncated, "\n"))
			if gotLines != tt.wantLines {
				t.Errorf("Truncate() lines = %d, want %d", gotLines, tt.wantLines)
			}
		})
	}
}
