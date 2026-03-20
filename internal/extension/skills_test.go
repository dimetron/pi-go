package extension

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkillFile(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "code-review")
	os.MkdirAll(skillDir, 0o755)
	path := filepath.Join(skillDir, "SKILL.md")
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

func TestParseSkillFileNameFromDirectory(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "my-skill")
	os.MkdirAll(skillDir, 0o755)
	// Skill without explicit name in frontmatter — should derive from directory.
	path := filepath.Join(skillDir, "SKILL.md")
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

	// Global skill: globalDir/lint/SKILL.md
	lintGlobal := filepath.Join(globalDir, "lint")
	os.MkdirAll(lintGlobal, 0o755)
	if err := os.WriteFile(filepath.Join(lintGlobal, "SKILL.md"), []byte(`---
name: lint
description: Run linter
---
Run the linter.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project skill overrides global with same name.
	lintProject := filepath.Join(projectDir, "lint")
	os.MkdirAll(lintProject, 0o755)
	if err := os.WriteFile(filepath.Join(lintProject, "SKILL.md"), []byte(`---
name: lint
description: Project linter
---
Run the project linter.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Project-only skill.
	deployDir := filepath.Join(projectDir, "deploy")
	os.MkdirAll(deployDir, 0o755)
	if err := os.WriteFile(filepath.Join(deployDir, "SKILL.md"), []byte(`---
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

// --- Audit mode integration tests ---

func setupSkillWithContent(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	os.MkdirAll(skillDir, 0o755)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSkillsWithOptionsBlockCritical(t *testing.T) {
	dir := t.TempDir()
	// Clean skill.
	setupSkillWithContent(t, dir, "clean", "---\nname: clean\ndescription: clean skill\n---\nClean body.")
	// Skill with BiDi override (U+202E) — critical.
	setupSkillWithContent(t, dir, "dirty", "---\nname: dirty\ndescription: dirty skill\n---\nHidden \u202E text.")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditBlock}, dir)
	if err != nil {
		t.Fatal(err)
	}

	// dirty should be blocked.
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill (dirty blocked), got %d", len(skills))
	}
	if skills[0].Name != "clean" {
		t.Errorf("expected clean skill, got %q", skills[0].Name)
	}
}

func TestLoadSkillsWithOptionsWarnCritical(t *testing.T) {
	dir := t.TempDir()
	// Skill with tag char (U+E0001) — critical.
	setupSkillWithContent(t, dir, "tagged", "---\nname: tagged\ndescription: tagged skill\n---\nTag \U000E0001 text.")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditWarn}, dir)
	if err != nil {
		t.Fatal(err)
	}

	// In warn mode, skill should still load.
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill in warn mode, got %d", len(skills))
	}
	if skills[0].Name != "tagged" {
		t.Errorf("expected tagged skill, got %q", skills[0].Name)
	}
}

func TestLoadSkillsWithOptionsSkipMode(t *testing.T) {
	dir := t.TempDir()
	// Skill with critical char — should load because scanning is skipped.
	setupSkillWithContent(t, dir, "critical", "---\nname: critical\ndescription: should load\n---\nBad \u202E char.")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected 1 skill in skip mode, got %d", len(skills))
	}
}

func TestLoadSkillsWithOptionsWarningOnlyLoads(t *testing.T) {
	dir := t.TempDir()
	// Skill with ZWSP (U+200B) — warning only, should always load.
	setupSkillWithContent(t, dir, "warn-only", "---\nname: warn-only\ndescription: warning skill\n---\nZero\u200Bwidth.")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditBlock}, dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected 1 skill (warning-only should load), got %d", len(skills))
	}
}

func TestLoadSkillsDefaultUsesBlock(t *testing.T) {
	dir := t.TempDir()
	// Skill with critical char — should be blocked by default LoadSkills().
	setupSkillWithContent(t, dir, "blocked", "---\nname: blocked\ndescription: should be blocked\n---\nTag \U000E0001 char.")
	setupSkillWithContent(t, dir, "ok", "---\nname: ok\ndescription: clean\n---\nClean.")

	skills, err := LoadSkills(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(skills) != 1 {
		t.Fatalf("expected 1 skill (blocked by default), got %d", len(skills))
	}
	if skills[0].Name != "ok" {
		t.Errorf("expected ok skill, got %q", skills[0].Name)
	}
}

func TestLoadSkillsNonExistentDir(t *testing.T) {
	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditBlock}, "/nonexistent/dir")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for nonexistent dir, got %d", len(skills))
	}
}
