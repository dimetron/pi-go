package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillDir writes a skill directory holding a SKILL.md with frontmatter.
func writeSkillDir(t *testing.T, dir, name, description string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	writeFile(t, filepath.Join(dir, "SKILL.md"),
		"---\nname: "+name+"\ndescription: "+description+"\n---\nBody.\n")
}

// newTestManager returns a Manager rooted at a fresh pi-go home.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	return &Manager{PiHome: t.TempDir()}
}

func TestDiscoverSkillsMarketplaceLayout(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, filepath.Join(root, "plugins", "show-me", "skills", "show-me"), "show-me", "show stuff")
	writeSkillDir(t, filepath.Join(root, "plugins", "rpi", "skills", "rpi"), "rpi", "research")
	// A nested reference directory is part of a skill, never a skill itself.
	writeFile(t, filepath.Join(root, "plugins", "rpi", "skills", "rpi", "references", "SKILL.md"), "not a skill")

	got, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d skills, want 2: %+v", len(got), got)
	}
	// Sorted by name: rpi before show-me.
	if got[0].Name != "rpi" || got[1].Name != "show-me" {
		t.Fatalf("names = %q, %q; want rpi, show-me", got[0].Name, got[1].Name)
	}
	if got[1].Description != "show stuff" {
		t.Errorf("description = %q, want %q", got[1].Description, "show stuff")
	}
}

func TestDiscoverSkillsPiGoLayout(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, filepath.Join(root, ".pi-go", "skills", "foo"), "foo", "d")

	got, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(got) != 1 || got[0].Name != "foo" {
		t.Fatalf("got %+v, want one skill named foo", got)
	}
}

func TestDiscoverSkillsRootSkill(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, filepath.Join(root, "solo"), "solo", "rooted")

	got, err := DiscoverSkills(filepath.Join(root, "solo"))
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(got) != 1 || got[0].Name != "solo" {
		t.Fatalf("got %+v, want the skill named solo", got)
	}
}

func TestDiscoverSkillsNone(t *testing.T) {
	got, err := DiscoverSkills(t.TempDir())
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d skills, want 0: %+v", len(got), got)
	}
}

func TestInstallSkillsFromLocalDirSelectsNamedSkill(t *testing.T) {
	src := t.TempDir()
	writeSkillDir(t, filepath.Join(src, "skills", "wanted"), "wanted", "the one")
	writeSkillDir(t, filepath.Join(src, "skills", "other"), "other", "not this one")

	m := newTestManager(t)
	got, err := m.InstallSkills(context.Background(), src, []string{"wanted"}, false)
	if err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	if len(got) != 1 || got[0].Name != "wanted" {
		t.Fatalf("installed %+v, want only wanted", got)
	}
	if _, err := os.Stat(filepath.Join(SkillsDir(m.PiHome), "wanted", "SKILL.md")); err != nil {
		t.Errorf("SKILL.md not installed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(SkillsDir(m.PiHome), "other")); !os.IsNotExist(err) {
		t.Errorf("unselected skill was installed: %v", err)
	}
}

func TestInstallSkillsCopiesBundleFiles(t *testing.T) {
	src := t.TempDir()
	skillDir := filepath.Join(src, "skills", "withrefs")
	writeSkillDir(t, skillDir, "withrefs", "d")
	writeFile(t, filepath.Join(skillDir, "references", "notes.md"), "keep me")

	m := newTestManager(t)
	if _, err := m.InstallSkills(context.Background(), src, nil, false); err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(SkillsDir(m.PiHome), "withrefs", "references", "notes.md"))
	if err != nil {
		t.Fatalf("bundled file missing: %v", err)
	}
	if string(raw) != "keep me" {
		t.Errorf("bundled file = %q, want %q", raw, "keep me")
	}
}

func TestInstallSkillsInstallsEverythingWithoutFilter(t *testing.T) {
	src := t.TempDir()
	writeSkillDir(t, filepath.Join(src, "skills", "a"), "a", "d")
	writeSkillDir(t, filepath.Join(src, "skills", "b"), "b", "d")

	m := newTestManager(t)
	got, err := m.InstallSkills(context.Background(), src, nil, false)
	if err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("installed %d skills, want 2", len(got))
	}
}

func TestInstallSkillsUnknownNameListsAvailable(t *testing.T) {
	src := t.TempDir()
	writeSkillDir(t, filepath.Join(src, "skills", "present"), "present", "d")

	m := newTestManager(t)
	_, err := m.InstallSkills(context.Background(), src, []string{"absent"}, false)
	if err == nil {
		t.Fatal("expected an error for an unknown skill name")
	}
	if want := "available: present"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to mention %q", err, want)
	}
}

func TestInstallSkillsNoSkillsFound(t *testing.T) {
	m := newTestManager(t)
	_, err := m.InstallSkills(context.Background(), t.TempDir(), nil, false)
	if err == nil {
		t.Fatal("expected an error when the source contains no skills")
	}
}

func TestInstallSkillsRefusesExistingWithoutForce(t *testing.T) {
	src := t.TempDir()
	writeSkillDir(t, filepath.Join(src, "skills", "dup"), "dup", "new body")

	m := newTestManager(t)
	if _, err := m.InstallSkills(context.Background(), src, nil, false); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// A second install without --force must fail and leave the first copy be.
	if _, err := m.InstallSkills(context.Background(), src, nil, false); err == nil {
		t.Fatal("expected an error when the skill is already installed")
	}
	raw, err := os.ReadFile(filepath.Join(SkillsDir(m.PiHome), "dup", "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed SKILL.md: %v", err)
	}
	if !strings.Contains(string(raw), "new body") {
		t.Errorf("installed copy changed unexpectedly:\n%s", raw)
	}

	// With force the install replaces it.
	if _, err := m.InstallSkills(context.Background(), src, nil, true); err != nil {
		t.Fatalf("forced install: %v", err)
	}
}

func TestInstallSkillsRefusesSourceInsideDestination(t *testing.T) {
	m := newTestManager(t)
	// The source is the destination's own skill directory.
	skillDir := filepath.Join(SkillsDir(m.PiHome), "self")
	writeSkillDir(t, skillDir, "self", "d")

	_, err := m.InstallSkills(context.Background(), skillDir, nil, true)
	if err == nil {
		t.Fatal("expected an error installing a skill into itself")
	}
	// The refusal must not have deleted the source.
	if _, statErr := os.Stat(filepath.Join(skillDir, "SKILL.md")); statErr != nil {
		t.Fatalf("source was removed before the refusal: %v", statErr)
	}
}

func TestInstallSkillsFromGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	writeSkillDir(t, filepath.Join(repo, "plugins", "show-me", "skills", "show-me"), "show-me", "show stuff")
	gitInit(t, repo)

	m := newTestManager(t)
	got, err := m.InstallSkills(context.Background(), repo, []string{"show-me"}, false)
	if err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	if len(got) != 1 || got[0].Name != "show-me" {
		t.Fatalf("installed %+v, want show-me", got)
	}
}

func TestListInstalledSkills(t *testing.T) {
	m := newTestManager(t)
	writeSkillDir(t, filepath.Join(SkillsDir(m.PiHome), "beta"), "beta", "b")
	writeSkillDir(t, filepath.Join(SkillsDir(m.PiHome), "alpha"), "alpha", "a")
	// A directory with no SKILL.md is not a skill.
	if err := os.MkdirAll(filepath.Join(SkillsDir(m.PiHome), "notaskill"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ListInstalledSkills(m.PiHome)
	if err != nil {
		t.Fatalf("ListInstalledSkills: %v", err)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "beta" {
		t.Fatalf("got %+v, want alpha then beta", got)
	}
	if got[0].Description != "a" {
		t.Errorf("description = %q, want %q", got[0].Description, "a")
	}
}

func TestListInstalledSkillsMissingDir(t *testing.T) {
	got, err := ListInstalledSkills(t.TempDir())
	if err != nil {
		t.Fatalf("ListInstalledSkills: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d skills, want 0", len(got))
	}
}

func TestRemoveSkills(t *testing.T) {
	m := newTestManager(t)
	writeSkillDir(t, filepath.Join(SkillsDir(m.PiHome), "gone"), "gone", "d")

	removed, missing := RemoveSkills(m.PiHome, []string{"gone", "absent"})
	if len(removed) != 1 || removed[0] != "gone" {
		t.Errorf("removed = %v, want [gone]", removed)
	}
	if len(missing) != 1 || missing[0] != "absent" {
		t.Errorf("missing = %v, want [absent]", missing)
	}
	if _, err := os.Stat(filepath.Join(SkillsDir(m.PiHome), "gone")); !os.IsNotExist(err) {
		t.Errorf("skill directory still present: %v", err)
	}
}

func TestSkillDescriptionHandlesCRLFAndBOM(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"),
		"\ufeff---\r\nname: crlf\r\ndescription: windows-edited\r\n---\r\nBody.\r\n")

	if got := skillDescription(dir); got != "windows-edited" {
		t.Errorf("description = %q, want %q", got, "windows-edited")
	}
}

func TestSkillDescriptionNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "SKILL.md"), "Just a body.\n")

	if got := skillDescription(dir); got != "" {
		t.Errorf("description = %q, want empty", got)
	}
}
