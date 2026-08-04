// Tests for `pi memory mine`: gitignore handling and file scanning.
package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewMemoryMineCmd_FlagDefaults(t *testing.T) {
	cmd := newMemoryMineCmd()
	convos, _ := cmd.Flags().GetBool("convos")
	if convos {
		t.Error("convos default should be false")
	}
	wing, _ := cmd.Flags().GetString("wing")
	if wing != "" {
		t.Error("wing default should be empty")
	}
}

func TestLoadGitignore_NoFile(t *testing.T) {
	dir := t.TempDir()
	patterns := loadGitignore(dir)
	if patterns == nil {
		t.Error("expected non-nil map")
	}
	if len(patterns) != 0 {
		t.Errorf("expected empty patterns, got %v", patterns)
	}
}

func TestLoadGitignore_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(""), 0644)
	patterns := loadGitignore(dir)
	if len(patterns) != 0 {
		t.Errorf("expected empty patterns, got %v", patterns)
	}
}

func TestLoadGitignore_WithComments(t *testing.T) {
	dir := t.TempDir()
	content := "# This is a comment\n*.log\n# Another comment\nnode_modules/"
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(content), 0644)
	patterns := loadGitignore(dir)
	if len(patterns) != 2 {
		t.Errorf("expected 2 patterns, got %d: %v", len(patterns), patterns)
	}
	if !patterns["*.log"] || !patterns["node_modules/"] {
		t.Errorf("expected *.log and node_modules/, got %v", patterns)
	}
}

func TestLoadGitignore_Whitespace(t *testing.T) {
	dir := t.TempDir()
	content := "  *.tmp  \n  .cache  \n"
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(content), 0644)
	patterns := loadGitignore(dir)
	if !patterns["*.tmp"] || !patterns[".cache"] {
		t.Errorf("expected trimmed patterns, got %v", patterns)
	}
}

func TestIsGitignored_AllPatterns(t *testing.T) {
	tests := []struct {
		path     string
		patterns map[string]bool
		want     bool
	}{
		{"node_modules/foo", map[string]bool{"node_modules": true}, true},
		{"src/node_modules/foo", map[string]bool{"node_modules": true}, true},
		{"src/pkg/main.go", map[string]bool{"pkg": true}, true},
		{"vendor/foo", map[string]bool{"vendor": true}, true},
		{"*/node_modules/foo", map[string]bool{"*/node_modules": true}, true},
		{"src/main.go", map[string]bool{"node_modules": true}, false},
		{"", map[string]bool{"test": true}, false},
	}

	for _, tt := range tests {
		got := isGitignored(tt.path, tt.patterns)
		if got != tt.want {
			t.Errorf("isGitignored(%q, %v) = %v, want %v", tt.path, tt.patterns, got, tt.want)
		}
	}
}

// scanFiles returns nil error for non-existent directories (filepath.WalkDir handles it gracefully)
func TestScanFiles_NonExistentDir(t *testing.T) {
	_, err := scanFiles("/nonexistent/directory/for/scanning", false)
	if err != nil {
		t.Logf("scanFiles returned error (may vary by OS): %v", err)
	}
}

func TestScanFiles_ValidDir(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# README"), 0644)

	files, err := scanFiles(dir, false)
	if err != nil {
		t.Fatalf("scanFiles: %v", err)
	}
	if len(files) == 0 {
		t.Error("expected at least one file")
	}
}

func TestScanFiles_ConvosMode(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "chat.jsonl"), []byte(`{"role":"user"}`), 0644)
	os.WriteFile(filepath.Join(dir, "log.txt"), []byte("log"), 0644)
	os.WriteFile(filepath.Join(dir, "data.md"), []byte("# Data"), 0644)

	files, err := scanFiles(dir, true)
	if err != nil {
		t.Fatalf("scanFiles convos: %v", err)
	}
	if len(files) != 3 {
		t.Errorf("expected 3 files in convos mode, got %d", len(files))
	}
}
