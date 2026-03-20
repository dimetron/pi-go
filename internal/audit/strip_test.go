package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripDangerousCritical(t *testing.T) {
	// Tag character (critical) should be removed.
	input := "Hello \U000E0001 World"
	got := StripDangerous(input)
	if strings.ContainsRune(got, 0xE0001) {
		t.Error("critical tag char should be stripped")
	}
	if got != "Hello  World" {
		t.Errorf("got %q, want %q", got, "Hello  World")
	}
}

func TestStripDangerousWarning(t *testing.T) {
	// ZWSP (warning) should be removed.
	input := "word\u200Bword"
	got := StripDangerous(input)
	if got != "wordword" {
		t.Errorf("got %q, want %q", got, "wordword")
	}
}

func TestStripDangerousInfoPreserved(t *testing.T) {
	// NBSP (info) should be preserved.
	input := "hello\u00A0world"
	got := StripDangerous(input)
	if got != input {
		t.Errorf("info chars should be preserved: got %q", got)
	}
}

func TestStripDangerousEmojiZWJ(t *testing.T) {
	// ZWJ between emoji should be preserved.
	input := "\U0001F468\u200D\U0001F469"
	got := StripDangerous(input)
	if got != input {
		t.Errorf("emoji ZWJ should be preserved: got %q", got)
	}
}

func TestStripDangerousBOMAtStart(t *testing.T) {
	// BOM at file start should be preserved.
	input := "\uFEFFHello"
	got := StripDangerous(input)
	if got != input {
		t.Errorf("BOM at start should be preserved: got %q", got)
	}
}

func TestStripDangerousBOMMidFile(t *testing.T) {
	// BOM mid-file should be removed.
	input := "Hello\uFEFFWorld"
	got := StripDangerous(input)
	if got != "HelloWorld" {
		t.Errorf("mid-file BOM should be stripped: got %q", got)
	}
}

func TestStripDangerousIdempotent(t *testing.T) {
	clean := "This is clean ASCII content."
	got := StripDangerous(clean)
	if got != clean {
		t.Errorf("clean content should be unchanged: got %q", got)
	}
}

func TestStripFileBak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.md")
	original := "Hello \u202E World"
	_ = os.WriteFile(path, []byte(original), 0o644)

	if err := StripFile(path); err != nil {
		t.Fatal(err)
	}

	// Check backup.
	bakData, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatal("backup not created")
	}
	if string(bakData) != original {
		t.Error("backup content mismatch")
	}

	// Check stripped.
	stripped, _ := os.ReadFile(path)
	if strings.ContainsRune(string(stripped), 0x202E) {
		t.Error("stripped file still contains dangerous char")
	}
}

func TestStripFileCleanNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.md")
	os.WriteFile(path, []byte("clean content"), 0o644)

	if err := StripFile(path); err != nil {
		t.Fatal(err)
	}

	// No backup should be created for clean files.
	if _, err := os.Stat(path + ".bak"); err == nil {
		t.Error("backup should not be created for clean files")
	}
}

func TestStripFileNotExist(t *testing.T) {
	err := StripFile("/nonexistent/path/file.md")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestStripDangerousBiDiOverrides(t *testing.T) {
	// All BiDi overrides should be removed.
	input := "a\u202Ab\u202Bc\u202Cd\u202De\u202Ef"
	got := StripDangerous(input)
	if got != "abcdef" {
		t.Errorf("BiDi overrides should be stripped: got %q", got)
	}
}

func TestStripDangerousVariationSelectors(t *testing.T) {
	input := "text\U000E0100more"
	got := StripDangerous(input)
	if got != "textmore" {
		t.Errorf("variation selectors should be stripped: got %q", got)
	}
}
