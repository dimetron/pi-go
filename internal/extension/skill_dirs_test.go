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
	// Test with a known project path.
	dirs := DefaultSkillDirsIn("/Users/dimetron/p6s/pi-dev/pi-go")
	if len(dirs) == 0 {
		t.Error("DefaultSkillDirsIn returned empty")
	}
	t.Logf("DefaultSkillDirsIn: %v", dirs)

	// Verify it finds .claude/skills and .pi-go/skills.
	foundClaraude := false
	foundPigo := false
	for _, d := range dirs {
		if d == "/Users/dimetron/p6s/pi-dev/pi-go/.claude/skills" {
			foundClaraude = true
		}
		if d == "/Users/dimetron/p6s/pi-dev/pi-go/.pi-go/skills" {
			foundPigo = true
		}
	}
	if !foundClaraude {
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
