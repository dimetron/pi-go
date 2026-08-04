package extension

import (
	"os"
	"path/filepath"
	"strings"
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
	if skill.Instruction != "" {
		t.Error("parseSkillFile should not populate Instruction (lazy load); got non-empty")
	}
	if skill.BodyPath != path {
		t.Errorf("BodyPath = %q, want %q", skill.BodyPath, path)
	}
	// Body is loaded on demand.
	body, err := LoadSkillBody([]Skill{skill}, skill.Name)
	if err != nil {
		t.Fatalf("LoadSkillBody: %v", err)
	}
	if !strings.Contains(body, "You are a code reviewer") {
		t.Errorf("body missing expected content, got %q", body)
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

	if want := 2 + bundledSkillCount(t); len(skills) != want {
		t.Fatalf("expected %d skills (lint + deploy + bundled), got %d", want, len(skills))
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
	if want := bundledSkillCount(t); len(skills) != want {
		t.Errorf("expected %d skills (bundled only), got %d", want, len(skills))
	}
}

func TestFindSkillNotFound(t *testing.T) {
	_, ok := FindSkill(nil, "nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

// --- Audit mode integration tests ---

// bundledSkillCount returns the number of embedded bundled skills so count
// assertions stay correct as bundled skills are added or removed.
func bundledSkillCount(t *testing.T) int {
	t.Helper()
	b, err := LoadBundledSkills()
	if err != nil {
		t.Fatalf("LoadBundledSkills: %v", err)
	}
	return len(b)
}

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
	if want := 1 + bundledSkillCount(t); len(skills) != want {
		t.Fatalf("expected %d skills (clean + bundled; dirty blocked), got %d", want, len(skills))
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
	if want := 1 + bundledSkillCount(t); len(skills) != want {
		t.Fatalf("expected %d skills in warn mode (tagged + bundled), got %d", want, len(skills))
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

	if want := 1 + bundledSkillCount(t); len(skills) != want {
		t.Fatalf("expected %d skills in skip mode (critical + bundled), got %d", want, len(skills))
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

	if want := 1 + bundledSkillCount(t); len(skills) != want {
		t.Fatalf("expected %d skills (warning-only + bundled), got %d", want, len(skills))
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

	if want := 1 + bundledSkillCount(t); len(skills) != want {
		t.Fatalf("expected %d skills (ok + bundled; blocked excluded), got %d", want, len(skills))
	}
	if _, ok := FindSkill(skills, "ok"); !ok {
		t.Error("expected ok skill to be loaded")
	}
}

func TestLoadSkillsDirectSkillFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(`---
name: direct-skill
description: Direct skill file.
---
# Direct Skill
`), 0o644); err != nil {
		t.Fatalf("WriteFile(SKILL.md) error: %v", err)
	}

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, dir)
	if err != nil {
		t.Fatal(err)
	}
	skill, ok := FindSkill(skills, "direct-skill")
	if !ok {
		t.Fatalf("expected direct-skill to be loaded from %s/SKILL.md", dir)
	}
	if skill.Description != "Direct skill file." {
		t.Errorf("description = %q", skill.Description)
	}
}

func TestLoadSkillsNonExistentDir(t *testing.T) {
	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditBlock}, "/nonexistent/dir")
	if err != nil {
		t.Fatal(err)
	}
	if want := bundledSkillCount(t); len(skills) != want {
		t.Errorf("expected %d skills for nonexistent dir (bundled only), got %d", want, len(skills))
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
			// Body is lazy-loaded for bundled skills; LoadSkillBody should
			// still return the full markdown body.
			body, err := LoadSkillBody(skills, "agents-md")
			if err != nil {
				t.Errorf("LoadSkillBody(agents-md): %v", err)
			}
			if body == "" {
				t.Error("expected non-empty body for agents-md after LoadSkillBody")
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

func TestSkillBodySize(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "sized-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
description: For size testing
---
This is a body of known length 0123456789.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, dir)
	if err != nil {
		t.Fatal(err)
	}
	var found *Skill
	for i := range skills {
		if skills[i].Name == "sized-skill" {
			found = &skills[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("expected sized-skill in loaded skills, got %d skills", len(skills))
	}
	name := found.Name
	skills = []Skill{*found}

	// Before any load, size is unknown.
	if size, ok := SkillBodySize(skills, name); ok {
		t.Errorf("size should be unknown before load, got size=%d ok=true", size)
	}
	// Load the body.
	body, err := LoadSkillBody(skills, name)
	if err != nil {
		t.Fatal(err)
	}
	if size, ok := SkillBodySize(skills, name); !ok || size != len(body) {
		t.Errorf("after load: got (%d, %v), want (%d, true)", size, ok, len(body))
	}
	// Unknown skill returns (0, false).
	if size, ok := SkillBodySize(skills, "no-such-skill"); ok || size != 0 {
		t.Errorf("unknown skill: got (%d, %v), want (0, false)", size, ok)
	}
}
