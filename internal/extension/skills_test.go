package extension

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkillFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code-review.SKILL.md")
	content := `---
name: code-review
description: Review code for quality and security issues
tools: read, grep, bash
---
You are a code reviewer. Analyze the code for:
- Security vulnerabilities
- Performance issues
- Code style
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	skill, err := parseSkillFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if skill.Name != "code-review" {
		t.Errorf("name = %q, want %q", skill.Name, "code-review")
	}
	if skill.Description != "Review code for quality and security issues" {
		t.Errorf("description = %q", skill.Description)
	}
	if len(skill.Tools) != 3 {
		t.Fatalf("tools = %v, want 3 tools", skill.Tools)
	}
	if skill.Tools[0] != "read" || skill.Tools[1] != "grep" || skill.Tools[2] != "bash" {
		t.Errorf("tools = %v", skill.Tools)
	}
	if skill.Instruction == "" {
		t.Error("instruction should not be empty")
	}
}

func TestParseSkillFileNameFromFilename(t *testing.T) {
	dir := t.TempDir()
	// Skill without explicit name in frontmatter — should derive from filename.
	path := filepath.Join(dir, "my-skill.SKILL.md")
	content := `---
description: A test skill
---
Do something.
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	skill, err := parseSkillFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if skill.Name != "my-skill" {
		t.Errorf("name = %q, want %q", skill.Name, "my-skill")
	}
}

func TestLoadSkills(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	// Global skill.
	if err := os.WriteFile(filepath.Join(globalDir, "lint.SKILL.md"), []byte(`---
name: lint
description: Run linter
---
Run the linter.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project skill overrides global with same name.
	if err := os.WriteFile(filepath.Join(projectDir, "lint.SKILL.md"), []byte(`---
name: lint
description: Project linter
---
Run the project linter.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project-only skill.
	if err := os.WriteFile(filepath.Join(projectDir, "deploy.SKILL.md"), []byte(`---
name: deploy
description: Deploy the app
---
Deploy steps.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadSkills(globalDir, projectDir)
	if err != nil {
		t.Fatal(err)
	}

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	// lint should be overridden by project version.
	lint, ok := FindSkill(skills, "lint")
	if !ok {
		t.Fatal("lint skill not found")
	}
	if lint.Description != "Project linter" {
		t.Errorf("lint description = %q, want project override", lint.Description)
	}

	_, ok = FindSkill(skills, "deploy")
	if !ok {
		t.Fatal("deploy skill not found")
	}
}

func TestLoadSkillsEmptyDir(t *testing.T) {
	skills, err := LoadSkills(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestFindSkillNotFound(t *testing.T) {
	_, ok := FindSkill(nil, "nonexistent")
	if ok {
		t.Error("expected not found")
	}
}
