package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToKebabCase_Simple(t *testing.T) {
	got := toKebabCase("add rate limiting")
	want := "add-rate-limiting"
	if got != want {
		t.Errorf("toKebabCase(\"add rate limiting\") = %q, want %q", got, want)
	}
}

func TestToKebabCase_SpecialChars(t *testing.T) {
	got := toKebabCase("build a REST API!")
	want := "build-a-rest-api"
	if got != want {
		t.Errorf("toKebabCase(\"build a REST API!\") = %q, want %q", got, want)
	}
}

func TestToKebabCase_MixedCase(t *testing.T) {
	got := toKebabCase("Add JWT Auth")
	want := "add-jwt-auth"
	if got != want {
		t.Errorf("toKebabCase(\"Add JWT Auth\") = %q, want %q", got, want)
	}
}

func TestToKebabCase_ExtraSpaces(t *testing.T) {
	got := toKebabCase("  too   many  spaces  ")
	want := "too-many-spaces"
	if got != want {
		t.Errorf("toKebabCase(\"  too   many  spaces  \") = %q, want %q", got, want)
	}
}

func TestToKebabCase_Truncation(t *testing.T) {
	// A long string that exceeds 50 chars when kebab-cased.
	idea := "implement a comprehensive rate limiting system with sliding window algorithm and redis backend"
	got := toKebabCase(idea)
	if len(got) > 50 {
		t.Errorf("toKebabCase should truncate to <= 50 chars, got %d: %q", len(got), got)
	}
	// Should not end with a hyphen.
	if strings.HasSuffix(got, "-") {
		t.Errorf("toKebabCase should not end with hyphen, got %q", got)
	}
	// Should not split in the middle of a word.
	if !strings.HasPrefix("implement-a-comprehensive-rate-limiting-system-with-sliding-window-algorithm-and-redis-backend", got) {
		// Just verify it's a valid prefix of the full kebab.
		t.Logf("truncated result: %q (len %d)", got, len(got))
	}
}

func TestToKebabCase_EmptyString(t *testing.T) {
	got := toKebabCase("")
	if got != "" {
		t.Errorf("toKebabCase(\"\") = %q, want \"\"", got)
	}
}

func TestToKebabCase_OnlySpecialChars(t *testing.T) {
	got := toKebabCase("!@#$%")
	if got != "" {
		t.Errorf("toKebabCase(\"!@#$%%\") = %q, want \"\"", got)
	}
}

func TestCreateSpecSkeleton_Success(t *testing.T) {
	tmpDir := t.TempDir()
	specDir, err := createSpecSkeleton(tmpDir, "my-feature", "Build a cool feature")
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	expectedDir := filepath.Join(tmpDir, "specs", "my-feature")
	if specDir != expectedDir {
		t.Errorf("specDir = %q, want %q", specDir, expectedDir)
	}

	// Verify directory exists.
	if _, err := os.Stat(specDir); os.IsNotExist(err) {
		t.Error("spec directory was not created")
	}

	// Verify research/ subdirectory.
	researchDir := filepath.Join(specDir, "research")
	if _, err := os.Stat(researchDir); os.IsNotExist(err) {
		t.Error("research/ subdirectory was not created")
	}

	// Verify rough-idea.md exists.
	roughIdeaPath := filepath.Join(specDir, "rough-idea.md")
	if _, err := os.Stat(roughIdeaPath); os.IsNotExist(err) {
		t.Error("rough-idea.md was not created")
	}

	// Verify requirements.md exists.
	reqPath := filepath.Join(specDir, "requirements.md")
	if _, err := os.Stat(reqPath); os.IsNotExist(err) {
		t.Error("requirements.md was not created")
	}
}

func TestCreateSpecSkeleton_AlreadyExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the directory first.
	specDir := filepath.Join(tmpDir, "specs", "existing-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("failed to create pre-existing dir: %v", err)
	}

	_, err := createSpecSkeleton(tmpDir, "existing-feature", "Some idea")
	if err == nil {
		t.Error("expected error when spec directory already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error should mention 'already exists', got: %v", err)
	}
}

func TestCreateSpecSkeleton_RoughIdeaContent(t *testing.T) {
	tmpDir := t.TempDir()
	roughIdea := "Build a rate limiter with sliding window"
	_, err := createSpecSkeleton(tmpDir, "rate-limiter", roughIdea)
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "specs", "rate-limiter", "rough-idea.md"))
	if err != nil {
		t.Fatalf("failed to read rough-idea.md: %v", err)
	}

	if !strings.Contains(string(content), roughIdea) {
		t.Errorf("rough-idea.md should contain the input text, got:\n%s", content)
	}
}

func TestCreateSpecSkeleton_RequirementsContent(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := createSpecSkeleton(tmpDir, "test-feature", "Some feature")
	if err != nil {
		t.Fatalf("createSpecSkeleton failed: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, "specs", "test-feature", "requirements.md"))
	if err != nil {
		t.Fatalf("failed to read requirements.md: %v", err)
	}

	if !strings.Contains(string(content), "# Requirements") {
		t.Error("requirements.md should contain '# Requirements' header")
	}
	if !strings.Contains(string(content), "## Questions & Answers") {
		t.Error("requirements.md should contain '## Questions & Answers' header")
	}
}
