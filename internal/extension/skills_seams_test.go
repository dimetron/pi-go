package extension

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestBundledSkillMain(t *testing.T) {
	t.Parallel()

	main := BundledSkillFile{RelPath: "bundled_skills/x/SKILL.md", Content: []byte("main")}
	other := BundledSkillFile{RelPath: "bundled_skills/x/ref.md", Content: []byte("ref")}
	emptyMain := BundledSkillFile{RelPath: "bundled_skills/x/SKILL.md"}

	tests := []struct {
		name  string
		files []BundledSkillFile
		want  string
	}{
		{"no files", nil, ""},
		{"exact match wins", []BundledSkillFile{other, main}, "main"},
		{"missing SKILL.md falls back to the first file", []BundledSkillFile{other}, "ref"},
		{"empty SKILL.md falls back to the first file", []BundledSkillFile{other, emptyMain}, "ref"},
		{"empty SKILL.md alone stays empty", []BundledSkillFile{emptyMain}, ""},
		{"other skill's SKILL.md is not a match", []BundledSkillFile{{RelPath: "bundled_skills/y/SKILL.md", Content: []byte("y")}}, "y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, string(bundledSkillMain(tt.files, "x"))); diff != "" {
				t.Errorf("bundledSkillMain mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSkillFileRefs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// A skill in a subdirectory, a subdirectory without one, and a stray file.
	setupSkillWithContent(t, dir, "alpha", "---\nname: alpha\n---\nbody")
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "loose.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	got := skillFileRefs(dir, entries)
	want := []skillFileRef{
		// The directory's own SKILL.md is listed first even when absent; the
		// caller stats it before use.
		{path: filepath.Join(dir, "SKILL.md"), defaultName: filepath.Base(dir)},
		{path: filepath.Join(dir, "alpha", "SKILL.md"), defaultName: "alpha"},
	}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(skillFileRef{})); diff != "" {
		t.Errorf("skillFileRefs mismatch (-want +got):\n%s", diff)
	}
}

func TestAuditBlocksSkill(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	setupSkillWithContent(t, dir, "clean", "---\nname: clean\n---\nClean body.")
	// U+202E (BiDi override) is a critical finding.
	setupSkillWithContent(t, dir, "dirty", "---\nname: dirty\n---\nHidden \u202E text.")

	clean := skillFileRef{path: filepath.Join(dir, "clean", "SKILL.md"), defaultName: "clean"}
	dirty := skillFileRef{path: filepath.Join(dir, "dirty", "SKILL.md"), defaultName: "dirty"}
	missing := skillFileRef{path: filepath.Join(dir, "gone", "SKILL.md"), defaultName: "gone"}

	tests := []struct {
		name string
		ref  skillFileRef
		mode AuditMode
		want bool
	}{
		{"skip mode never blocks", dirty, AuditSkip, false},
		{"clean file in block mode", clean, AuditBlock, false},
		{"critical file in block mode", dirty, AuditBlock, true},
		{"critical file in warn mode", dirty, AuditWarn, false},
		{"unreadable file only warns", missing, AuditBlock, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := auditBlocksSkill(tt.ref, tt.mode); got != tt.want {
				t.Errorf("auditBlocksSkill(%s, %s) = %v, want %v", tt.ref.defaultName, tt.mode, got, tt.want)
			}
		})
	}
}

func TestLoadDirSkills(t *testing.T) {
	t.Parallel()

	t.Run("missing directory is not an error", func(t *testing.T) {
		t.Parallel()
		skills, blocked, err := loadDirSkills(filepath.Join(t.TempDir(), "nope"), LoadOptions{AuditMode: AuditSkip})
		if err != nil {
			t.Fatalf("loadDirSkills: %v", err)
		}
		if len(skills) != 0 || len(blocked) != 0 {
			t.Errorf("got %d skills and %d blocked, want none", len(skills), len(blocked))
		}
	})

	t.Run("a file where a directory is expected is an error", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "skills")
		if err := os.WriteFile(path, []byte("not a dir"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := loadDirSkills(path, LoadOptions{AuditMode: AuditSkip})
		if err == nil {
			t.Fatal("loadDirSkills() = nil error, want a read failure")
		}
		if !strings.Contains(err.Error(), "reading skills dir") {
			t.Errorf("error = %q, want it to mention reading skills dir", err)
		}
	})

	t.Run("name falls back to the directory name", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		setupSkillWithContent(t, dir, "unnamed", "---\ndescription: no name key\n---\nbody")

		skills, _, err := loadDirSkills(dir, LoadOptions{AuditMode: AuditSkip})
		if err != nil {
			t.Fatalf("loadDirSkills: %v", err)
		}
		if len(skills) != 1 {
			t.Fatalf("got %d skills, want 1", len(skills))
		}
		if skills[0].Name != "unnamed" {
			t.Errorf("Name = %q, want %q", skills[0].Name, "unnamed")
		}
	})

	t.Run("critical skill is reported as blocked, not returned", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		setupSkillWithContent(t, dir, "clean", "---\nname: clean\n---\nClean body.")
		setupSkillWithContent(t, dir, "dirty", "---\nname: dirty\n---\nHidden \u202E text.")

		skills, blocked, err := loadDirSkills(dir, LoadOptions{AuditMode: AuditBlock})
		if err != nil {
			t.Fatalf("loadDirSkills: %v", err)
		}
		if len(skills) != 1 || skills[0].Name != "clean" {
			t.Fatalf("got %+v, want only the clean skill", skills)
		}
		want := []string{filepath.Join(dir, "dirty", "SKILL.md")}
		if diff := cmp.Diff(want, blocked); diff != "" {
			t.Errorf("blocked mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("the directory's own SKILL.md loads too", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "self-named")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\ndescription: d\n---\nbody"), 0o644); err != nil {
			t.Fatal(err)
		}

		skills, _, err := loadDirSkills(dir, LoadOptions{AuditMode: AuditSkip})
		if err != nil {
			t.Fatalf("loadDirSkills: %v", err)
		}
		if len(skills) != 1 || skills[0].Name != "self-named" {
			t.Fatalf("got %+v, want one skill named self-named", skills)
		}
	})
}

// TestLoadDirSkillsSource pins how a skill's Source is derived: an absolute
// directory is user-level, a relative one is project-level. It cannot be
// parallel — t.Chdir forbids it.
func TestLoadDirSkillsSource(t *testing.T) {
	dir := t.TempDir()
	setupSkillWithContent(t, dir, "alpha", "---\nname: alpha\ndescription: d\n---\nbody")

	skills, _, err := loadDirSkills(dir, LoadOptions{AuditMode: AuditSkip})
	if err != nil {
		t.Fatalf("loadDirSkills: %v", err)
	}
	if len(skills) != 1 || skills[0].Source != "user" {
		t.Fatalf("got %+v, want one skill with Source=user", skills)
	}

	// Re-read the same tree through a relative path.
	t.Chdir(dir)
	relSkills, _, err := loadDirSkills(".", LoadOptions{AuditMode: AuditSkip})
	if err != nil {
		t.Fatalf("loadDirSkills(.): %v", err)
	}
	if len(relSkills) != 1 || relSkills[0].Source != "project" {
		t.Fatalf("got %+v, want one skill with Source=project", relSkills)
	}
}

// TestLoadSkillsWithOptionsOverrideOrder pins the layering LoadSkillsWithOptions
// implements: later directories replace earlier ones in place, keeping the
// original slice position rather than appending a duplicate.
func TestLoadSkillsWithOptionsOverrideOrder(t *testing.T) {
	userDir := t.TempDir()
	projectDir := t.TempDir()
	setupSkillWithContent(t, userDir, "shared", "---\nname: shared\ndescription: from user\n---\nu")
	setupSkillWithContent(t, userDir, "user-only", "---\nname: user-only\ndescription: u\n---\nu")
	setupSkillWithContent(t, projectDir, "shared", "---\nname: shared\ndescription: from project\n---\np")

	skills, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, userDir, projectDir)
	if err != nil {
		t.Fatalf("LoadSkillsWithOptions: %v", err)
	}

	var got []Skill
	for _, s := range skills {
		if s.Name == "shared" || s.Name == "user-only" {
			got = append(got, s)
		}
	}
	if len(got) != 2 {
		t.Fatalf("got %d matching skills, want 2: %+v", len(got), got)
	}
	if got[0].Name != "shared" {
		t.Errorf("first skill = %q, want the overridden %q to keep its slot", got[0].Name, "shared")
	}
	if got[0].Description != "from project" {
		t.Errorf("Description = %q, want the project skill to win", got[0].Description)
	}
}

// TestLoadSkillsWithOptionsErrorFromDir checks that a directory-level failure
// aborts the whole load rather than being skipped.
func TestLoadSkillsWithOptionsErrorFromDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "skills")
	if err := os.WriteFile(path, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSkillsWithOptions(LoadOptions{AuditMode: AuditSkip}, path); err == nil {
		t.Fatal("LoadSkillsWithOptions() = nil error, want a read failure")
	}
}
