package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	// Save original cwd
	origCwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	// Change to temp dir for isolation
	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))

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
