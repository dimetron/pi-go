package extension

import (
	"os"
	"path/filepath"
	"testing"
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
	if err := os.MkdirAll(piGoSkills, 0o755); err != nil {
		t.Fatalf("create .pi-go skills dir: %v", err)
	}
	if err := os.MkdirAll(claudeSkills, 0o755); err != nil {
		t.Fatalf("create .claude skills dir: %v", err)
	}

	dirs := DefaultSkillDirsIn(root)
	if len(dirs) == 0 {
		t.Error("DefaultSkillDirsIn returned empty")
	}
	t.Logf("DefaultSkillDirsIn: %v", dirs)

	// Verify it finds .claude/skills and .pi-go/skills.
	foundClaude := false
	foundPigo := false
	for _, d := range dirs {
		if d == claudeSkills {
			foundClaude = true
		}
		if d == piGoSkills {
			foundPigo = true
		}
	}
	if !foundClaude {
		t.Error("did not find .claude/skills")
	}
	if !foundPigo {
		t.Error("did not find .pi-go/skills")
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
	found := false
	for _, d := range dirs {
		if d == userDir {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("user-level skill dir %q not included", userDir)
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
