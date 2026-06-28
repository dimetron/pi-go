package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionString(t *testing.T) {
	orig := BuildTag
	defer func() { BuildTag = orig }()

	BuildTag = ""
	if got := versionString(); got != Version {
		t.Errorf("versionString() with empty BuildTag = %q, want %q", got, Version)
	}

	BuildTag = "abc123"
	if got := versionString(); got != Version+"+abc123" {
		t.Errorf("versionString() = %q, want %q", got, Version+"+abc123")
	}
}

func TestFormatPrintSkillLoad(t *testing.T) {
	t.Parallel()
	if got := formatPrintSkillLoad(3, nil); !strings.Contains(got, "loaded 3") {
		t.Errorf("formatPrintSkillLoad success = %q, want it to mention loaded 3", got)
	}
	if got := formatPrintSkillLoad(0, errors.New("boom")); !strings.Contains(got, "failed") {
		t.Errorf("formatPrintSkillLoad error = %q, want it to mention failed", got)
	}
}

func TestScanFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Supported project file.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Unsupported extension (skipped in project mode).
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Empty supported file (skipped because blank).
	if err := os.WriteFile(filepath.Join(dir, "empty.go"), []byte("   \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Conversation file (included in convos mode).
	if err := os.WriteFile(filepath.Join(dir, "chat.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Skipped directory.
	if err := os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "skip.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	proj, err := scanFiles(dir, false)
	if err != nil {
		t.Fatalf("scanFiles(project): %v", err)
	}
	if !sliceContains(proj, "main.go") {
		t.Errorf("project scan = %v, want main.go", proj)
	}
	if sliceContains(proj, filepath.Join("node_modules", "skip.go")) {
		t.Errorf("project scan should skip node_modules, got %v", proj)
	}
	if sliceContains(proj, "data.bin") {
		t.Errorf("project scan should skip unsupported extension, got %v", proj)
	}

	convos, err := scanFiles(dir, true)
	if err != nil {
		t.Fatalf("scanFiles(convos): %v", err)
	}
	if !sliceContains(convos, "chat.jsonl") {
		t.Errorf("convos scan = %v, want chat.jsonl", convos)
	}
}

func sliceContains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
