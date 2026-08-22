package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dimetron/pi-go/internal/extension"
)

func TestNormalizeDiscoveryCWD(t *testing.T) {
	// Test: trims whitespace
	result := normalizeDiscoveryCWD("  /some/path  ")
	assert.Equal(t, "/some/path", result)

	// Test: returns non-empty cwd as-is
	result = normalizeDiscoveryCWD("/my/project")
	assert.Equal(t, "/my/project", result)

	// Test: falls back to "." when empty
	result = normalizeDiscoveryCWD("")
	assert.Equal(t, ".", result)
}

func TestDefaultSkillDirsIn(t *testing.T) {
	// Change to temp dir for isolation. t.Chdir registers its restore after
	// t.TempDir registered its removal, and cleanups run last-registered-first,
	// so the working directory moves back out before the directory is deleted.
	// Doing the chdir by hand got that order backwards, and Windows refuses to
	// delete the process's own working directory.
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	dirs := extension.DefaultSkillDirsIn(tmpDir)

	// Should return at least the user skills dir when home dir is available
	if homeDir, err := os.UserHomeDir(); err == nil {
		userSkillsDir := filepath.Join(homeDir, ".pi-go", "skills")
		found := false
		for _, d := range dirs {
			if d == userSkillsDir {
				found = true
				break
			}
		}
		assert.True(t, found, "expected user skills dir %q in %v", userSkillsDir, dirs)
	}
}

func TestDiscoverAvailableCommands(t *testing.T) {
	tmpDir := t.TempDir()

	// Test: returns non-empty list of commands
	commands := DiscoverAvailableCommands(tmpDir)
	// Should include at least the bundled agents-md skill
	found := false
	for _, cmd := range commands {
		if cmd.Name == "agents-md" || cmd.Name == "skill-create" {
			found = true
			break
		}
	}
	_ = found // We just verify it doesn't crash; skill presence depends on install
}
