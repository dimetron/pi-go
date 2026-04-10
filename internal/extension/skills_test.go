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

	if len(skills) != 3 {
		t.Fatalf("expected 3 skills (including bundled), got %d", len(skills))
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
	if len(skills) != 1 {
		t.Errorf("expected 1 skill (bundled only), got %d", len(skills))
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
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills (clean + bundled; dirty blocked), got %d", len(skills))
	}
	if _, ok := FindSkill(skills, "clean"); !ok {
		t.Error("expected clean skill to be loaded")
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
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills in warn mode (tagged + bundled), got %d", len(skills))
	}
	if _, ok := FindSkill(skills, "tagged"); !ok {
		t.Error("expected tagged skill to be loaded")
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

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills in skip mode (critical + bundled), got %d", len(skills))
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

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills (warning-only + bundled), got %d", len(skills))
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

	if len(skills) != 2 {
		t.Fatalf("expected 2 skills (ok + bundled; blocked excluded), got %d", len(skills))
	}
	if _, ok := FindSkill(skills, "ok"); !ok {
		t.Error("expected ok skill to be loaded")
	}
}

func TestLoadSkillsNonExistentDir(t *testing.T) {
	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditBlock}, "/nonexistent/dir")
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Errorf("expected 1 skill for nonexistent dir (bundled only), got %d", len(skills))
	}
}

func TestLoadBundledSkills(t *testing.T) {
	bundled, err := LoadBundledSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(bundled) == 0 {
		t.Fatal("expected at least one bundled skill (agents-md)")
	}

	agentsMD, ok := bundled["agents-md"]
	if !ok {
		t.Fatal("expected agents-md bundled skill")
	}
	if len(agentsMD) == 0 {
		t.Fatal("expected at least one file in agents-md skill")
	}

	// Should have SKILL.md
	hasSKILL := false
	for _, f := range agentsMD {
		if f.RelPath == "bundled_skills/agents-md/SKILL.md" {
			hasSKILL = true
			break
		}
	}
	if !hasSKILL {
		t.Error("expected SKILL.md in agents-md skill files")
	}
}

func TestLoadBundledSkills_WithSource(t *testing.T) {
	// LoadSkillsWithOptions should include bundled skills.
	// When no filesystem dirs provided, should still get bundled.
	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip})
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) == 0 {
		t.Fatal("expected bundled skills even with no dirs")
	}
	var found bool
	for _, s := range skills {
		if s.Name == "agents-md" {
			found = true
			if s.Source != "bundled" {
				t.Errorf("expected source=bundled, got %q", s.Source)
			}
			if s.Description == "" {
				t.Error("expected non-empty description for agents-md")
			}
			if s.Instruction == "" {
				t.Error("expected non-empty instruction for agents-md")
			}
		}
	}
	if !found {
		t.Error("expected agents-md in loaded skills")
	}
}

func TestLoadBundledSkills_Override(t *testing.T) {
	// A filesystem skill should override the bundled one.
	dir := t.TempDir()
	setupSkillWithContent(t, dir, "agents-md", `---
name: agents-md
description: Custom agents-md override
---
Custom instruction.
`)
	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) == 0 {
		t.Fatal("expected at least one skill")
	}
	// Should use the filesystem version, not the bundled one.
	for _, s := range skills {
		if s.Name == "agents-md" {
			if s.Source != "user" {
				t.Errorf("expected source=user (filesystem override), got %q", s.Source)
			}
		}
	}
}
