package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGates_Standard(t *testing.T) {
	content := `# My Spec

## Gates

- **build**: ` + "`go build ./...`" + `

## Reference

Some reference.
`
	gates := parseGates(content)
	if len(gates) != 1 {
		t.Fatalf("expected 1 gate, got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate name = %q, want %q", gates[0].Name, "build")
	}
	if gates[0].Command != "go build ./..." {
		t.Errorf("gate command = %q, want %q", gates[0].Command, "go build ./...")
	}
}

func TestParseGates_Multiple(t *testing.T) {
	content := `## Gates

- **build**: ` + "`go build ./...`" + `
- **test**: ` + "`go test ./...`" + `
- **vet**: ` + "`go vet ./...`" + `
`
	gates := parseGates(content)
	if len(gates) != 3 {
		t.Fatalf("expected 3 gates, got %d", len(gates))
	}
	expected := []struct{ name, cmd string }{
		{"build", "go build ./..."},
		{"test", "go test ./..."},
		{"vet", "go vet ./..."},
	}
	for i, e := range expected {
		if gates[i].Name != e.name {
			t.Errorf("gate[%d].Name = %q, want %q", i, gates[i].Name, e.name)
		}
		if gates[i].Command != e.cmd {
			t.Errorf("gate[%d].Command = %q, want %q", i, gates[i].Command, e.cmd)
		}
	}
}

func TestParseGates_NoSection(t *testing.T) {
	content := `# My Spec

## Objective

Do something.

## Reference

Some reference.
`
	gates := parseGates(content)
	if len(gates) != 0 {
		t.Errorf("expected 0 gates, got %d", len(gates))
	}
}

func TestParseGates_Malformed(t *testing.T) {
	content := `## Gates

- **build**: ` + "`go build ./...`" + `
- this line has no backtick command
- not a gate at all
- **test**: ` + "`go test ./...`" + `
`
	gates := parseGates(content)
	if len(gates) != 2 {
		t.Fatalf("expected 2 gates (skipping malformed), got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate[0].Name = %q, want %q", gates[0].Name, "build")
	}
	if gates[1].Name != "test" {
		t.Errorf("gate[1].Name = %q, want %q", gates[1].Name, "test")
	}
}

func TestParseGates_StopsAtNextHeading(t *testing.T) {
	content := `## Gates

- **build**: ` + "`go build ./...`" + `

## Constraints

- **lint**: ` + "`golangci-lint run`" + `
`
	gates := parseGates(content)
	if len(gates) != 1 {
		t.Fatalf("expected 1 gate (stops at next heading), got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate name = %q, want %q", gates[0].Name, "build")
	}
}

func TestParseGates_PlainFormat(t *testing.T) {
	content := `## Gates

- build: ` + "`go build ./...`" + `
- test: ` + "`go test ./...`" + `
`
	gates := parseGates(content)
	if len(gates) != 2 {
		t.Fatalf("expected 2 gates (plain format), got %d", len(gates))
	}
	if gates[0].Name != "build" {
		t.Errorf("gate[0].Name = %q, want %q", gates[0].Name, "build")
	}
	if gates[1].Name != "test" {
		t.Errorf("gate[1].Name = %q, want %q", gates[1].Name, "test")
	}
}

func TestReadPromptMD_Success(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, "specs", "my-feature")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}

	expected := "# My Feature\n\n## Objective\n\nBuild something.\n"
	if err := os.WriteFile(filepath.Join(specDir, "PROMPT.md"), []byte(expected), 0o644); err != nil {
		t.Fatal(err)
	}

	content, err := readPromptMD(tmpDir, "my-feature")
	if err != nil {
		t.Fatalf("readPromptMD failed: %v", err)
	}
	if content != expected {
		t.Errorf("content = %q, want %q", content, expected)
	}
}

func TestReadPromptMD_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	_, err := readPromptMD(tmpDir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing PROMPT.md")
	}
	if !strings.Contains(err.Error(), "PROMPT.md not found") {
		t.Errorf("error should mention 'PROMPT.md not found', got: %v", err)
	}
}

func TestListAvailableSpecs(t *testing.T) {
	tmpDir := t.TempDir()
	specsDir := filepath.Join(tmpDir, "specs")

	// Create spec with PROMPT.md.
	spec1 := filepath.Join(specsDir, "alpha-feature")
	if err := os.MkdirAll(spec1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec1, "PROMPT.md"), []byte("# Alpha"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create spec with PROMPT.md.
	spec2 := filepath.Join(specsDir, "beta-feature")
	if err := os.MkdirAll(spec2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spec2, "PROMPT.md"), []byte("# Beta"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create spec WITHOUT PROMPT.md (should be excluded).
	spec3 := filepath.Join(specsDir, "gamma-incomplete")
	if err := os.MkdirAll(spec3, 0o755); err != nil {
		t.Fatal(err)
	}

	specs, err := listAvailableSpecs(tmpDir)
	if err != nil {
		t.Fatalf("listAvailableSpecs failed: %v", err)
	}

	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d: %v", len(specs), specs)
	}
	if specs[0] != "alpha-feature" {
		t.Errorf("specs[0] = %q, want %q", specs[0], "alpha-feature")
	}
	if specs[1] != "beta-feature" {
		t.Errorf("specs[1] = %q, want %q", specs[1], "beta-feature")
	}
}

func TestListAvailableSpecs_NoSpecsDir(t *testing.T) {
	tmpDir := t.TempDir()
	specs, err := listAvailableSpecs(tmpDir)
	if err != nil {
		t.Fatalf("listAvailableSpecs failed: %v", err)
	}
	if len(specs) != 0 {
		t.Errorf("expected 0 specs, got %d", len(specs))
	}
}
