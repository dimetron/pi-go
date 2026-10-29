package extension

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/plugin"
)

func TestDefaultSkillDirs(t *testing.T) {
	// Test DefaultSkillDirs (uses os.Getwd internally)
	dirs := DefaultSkillDirs()
	if len(dirs) == 0 {
		t.Error("DefaultSkillDirs returned empty")
	}
	t.Logf("DefaultSkillDirs: %v", dirs)
}

func TestDefaultSkillDirsIn(t *testing.T) {
	root := t.TempDir()
	piGoSkills := filepath.Join(root, ".pi-go", "skills")
	claudeSkills := filepath.Join(root, ".claude", "skills")
	agentsSkills := filepath.Join(root, ".agents", "skills")
	for _, dir := range []string{piGoSkills, claudeSkills, agentsSkills} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}

	dirs := DefaultSkillDirsIn(root)
	if len(dirs) == 0 {
		t.Error("DefaultSkillDirsIn returned empty")
	}
	t.Logf("DefaultSkillDirsIn: %v", dirs)

	// Verify it finds .claude/skills, .pi-go/skills and .agents/skills.
	foundClaude := false
	foundPigo := false
	foundAgents := false
	for _, d := range dirs {
		if d == claudeSkills {
			foundClaude = true
		}
		if d == piGoSkills {
			foundPigo = true
		}
		if d == agentsSkills {
			foundAgents = true
		}
	}
	if !foundClaude {
		t.Error("did not find .claude/skills")
	}
	if !foundPigo {
		t.Error("did not find .pi-go/skills")
	}
	if !foundAgents {
		t.Error("did not find .agents/skills")
	}
}

func TestDefaultSkillDirsInUserHome(t *testing.T) {
	// Test with a path that has no project skills.
	dirs := DefaultSkillDirsIn("/tmp")
	t.Logf("DefaultSkillDirsIn(/tmp): %v", dirs)

	// Should still include user-level directory.
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("cannot get user home dir")
	}

	userDir := filepath.Join(userHome, ".pi-go", "skills")
	agentsDir := filepath.Join(userHome, ".agents", "skills")
	found := false
	foundAgents := false
	for _, d := range dirs {
		if d == userDir {
			found = true
		}
		if d == agentsDir {
			foundAgents = true
		}
	}
	if !found {
		t.Errorf("user-level skill dir %q not included", userDir)
	}
	if !foundAgents {
		t.Errorf("~/.agents/skills dir %q not included", agentsDir)
	}
}

// ~/.agents/skills is the cross-agent layout. It must be searched even when
// PI_GO_HOME relocates pi-go's own home, and it must rank below ~/.pi-go/skills
// so a skill the user installed with `pi skills add` still wins a name clash.
func TestDefaultSkillDirsIncludesAgentsSkills(t *testing.T) {
	home := setTestHome(t)
	t.Setenv("PI_GO_HOME", filepath.Join(home, "relocated"))

	dirs := DefaultSkillDirsIn(t.TempDir())
	agentsDir := filepath.Join(home, ".agents", "skills")

	agentsIdx, pigoIdx := -1, -1
	for i, d := range dirs {
		switch d {
		case agentsDir:
			agentsIdx = i
		case filepath.Join(home, "relocated", "skills"):
			pigoIdx = i
		}
	}
	if agentsIdx < 0 {
		t.Fatalf("~/.agents/skills not included in %v", dirs)
	}
	if pigoIdx < 0 {
		t.Fatalf("PI_GO_HOME skills dir not included in %v", dirs)
	}
	if agentsIdx > pigoIdx {
		t.Errorf("~/.agents/skills (idx %d) must precede PI_GO_HOME/skills (idx %d): later dirs win", agentsIdx, pigoIdx)
	}
}

func TestDefaultSkillDirsNoDuplicates(t *testing.T) {
	// Ensure no duplicate directories in the result.
	dirs := DefaultSkillDirs()
	seen := make(map[string]bool)
	for _, d := range dirs {
		if seen[d] {
			t.Errorf("duplicate directory: %s", d)
		}
		seen[d] = true
	}
}

// A skill exposed through a symlinked directory must load. Plugin installs and
// cross-agent layouts (e.g. ~/.gemini/config/skills/<name>) present skills this
// way, and a symlink's DirEntry reports IsDir() == false.
func TestLoadSkillsThroughSymlinkedDir(t *testing.T) {
	// Real skill lives outside the scanned dir.
	real := t.TempDir()
	linked := filepath.Join(real, "linked")
	if err := os.MkdirAll(linked, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: linked\ndescription: Reached through a symlink\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(linked, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	skillsDir := t.TempDir()
	if err := os.Symlink(linked, filepath.Join(skillsDir, "linked")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, skillsDir)
	if err != nil {
		t.Fatalf("LoadSkillsWithOptions: %v", err)
	}
	if _, ok := FindSkill(skills, "linked"); !ok {
		t.Fatalf("skill behind a symlinked dir was not loaded; got %d skills", len(skills))
	}
}

// A symlink pointing at a file (not a directory) must not be treated as a
// skill directory.
func TestLoadSkillsIgnoresFileSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "notaskill.md")
	if err := os.WriteFile(target, []byte("# nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := t.TempDir()
	if err := os.Symlink(target, filepath.Join(skillsDir, "dangling")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, skillsDir)
	if err != nil {
		t.Fatalf("LoadSkillsWithOptions: %v", err)
	}
	if _, ok := FindSkill(skills, "dangling"); ok {
		t.Fatal("file symlink was loaded as a skill")
	}
}

// A plugin skill must never override a same-named user or project skill.
// Precedence is directory order, so the assertion is on ordering plus the
// loader's override rule.
func TestPluginSkillDirsComeBeforeUserAndProject(t *testing.T) {
	root := t.TempDir()
	projectSkills := filepath.Join(root, ".pi-go", "skills")
	if err := os.MkdirAll(projectSkills, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkillFile(t, filepath.Join(projectSkills, "shared"), "shared", "project version")

	// A plugin directory supplied ahead of the user and project dirs.
	pluginDir := filepath.Join(t.TempDir(), "skills")
	writeSkillFile(t, filepath.Join(pluginDir, "shared"), "shared", "plugin version")
	writeSkillFile(t, filepath.Join(pluginDir, "plugin-only"), "plugin-only", "plugin only")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, pluginDir, projectSkills)
	if err != nil {
		t.Fatalf("LoadSkillsWithOptions: %v", err)
	}
	body, err := LoadSkillBody(skills, "shared")
	if err != nil {
		t.Fatalf("LoadSkillBody: %v", err)
	}
	if body != "project version" {
		t.Errorf("shared skill body = %q, want the project version to win", body)
	}
	if _, ok := FindSkill(skills, "plugin-only"); !ok {
		t.Error("plugin-only skill was not loaded")
	}
}

func writeSkillFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// PI_GO_HOME relocates the directory `pi skills add` installs into. Loading
// must read the same place, or an install would report success into a
// directory no session ever scans.
func TestDefaultSkillDirsHonorsPiGoHome(t *testing.T) {
	piHome := t.TempDir()
	t.Setenv("PI_GO_HOME", piHome)

	want := filepath.Join(piHome, "skills")
	found := false
	for _, d := range DefaultSkillDirsIn(t.TempDir()) {
		if d == want {
			found = true
		}
	}
	if !found {
		t.Errorf("PI_GO_HOME skills dir %q not included in %v", want, DefaultSkillDirsIn(t.TempDir()))
	}
}

// A plugin installed under a relocated PI_GO_HOME must be visible too. The
// registry lives under the same home as the skills directory, so resolving one
// from PI_GO_HOME and the other from ~/.pi-go would load user skills while
// silently skipping every plugin alongside them.
func TestDefaultSkillDirsHonorsPiGoHomeForPlugins(t *testing.T) {
	piHome := t.TempDir()
	t.Setenv("PI_GO_HOME", piHome)

	pluginSkills := filepath.Join(plugin.Root(piHome), "demo", "skills")
	writeSkillFile(t, filepath.Join(pluginSkills, "demo-skill"), "demo-skill", "from a plugin")

	reg := plugin.NewRegistry()
	reg.Plugins["demo"] = plugin.Installed{
		Name:        "demo",
		SkillsDir:   filepath.Join("skills"),
		InstalledAt: "2026-01-01T00:00:00Z",
	}
	if err := reg.Save(piHome); err != nil {
		t.Fatalf("saving registry: %v", err)
	}

	dirs := DefaultSkillDirsIn(t.TempDir())
	found := false
	for _, d := range dirs {
		if d == pluginSkills {
			found = true
		}
	}
	if !found {
		t.Errorf("plugin skills dir %q from PI_GO_HOME not included in %v", pluginSkills, dirs)
	}
}
