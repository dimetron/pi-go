//go:build windows

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Regression test for Windows: task names built with filepath.Join contain
// backslashes, which leak into git refs (backup branch "specs/<taskName>")
// and make git fail with exit status 128. Task names must always use
// forward slashes.
func TestNewTaskName_NoBackslashes(t *testing.T) {
	got := newTaskName(`features\TOO`, "001", "my-feature")
	if strings.ContainsRune(got, '\\') {
		t.Errorf("task name contains backslash: %q", got)
	}
	if want := "features/TOO/001-my-feature"; got != want {
		t.Errorf("newTaskName() = %q, want %q", got, want)
	}
}

func TestFindExistingSpec_NoBackslashes(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, "specs", "features", "TOO", "001-existing-spec")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got := findExistingSpec(dir, `features\TOO`, "existing-spec")
	if got == "" {
		t.Fatal("findExistingSpec() returned empty")
	}
	if strings.ContainsRune(got, '\\') {
		t.Errorf("existing spec path contains backslash: %q", got)
	}
}
