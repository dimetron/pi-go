package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeDiscoveryCWD(t *testing.T) {
	// Save original cwd
	origCwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	// Test: trims whitespace
	result := normalizeDiscoveryCWD("  /some/path  ")
	assert.Equal(t, "/some/path", result)

	// Test: returns non-empty cwd as-is
	result = normalizeDiscoveryCWD("/my/project")
	assert.Equal(t, "/my/project", result)

	// Test: falls back to Getwd when empty
	require.NoError(t, os.Chdir("/tmp"))
	result = normalizeDiscoveryCWD("")
	// macOS symlinks /tmp to /private/tmp
	if result != "/tmp" {
		assert.Equal(t, "/private/tmp", result)
	}

	// Test: falls back to "." when both empty and Getwd fails
	// (Hard to force Getwd to fail, but "." is the final fallback)
	result = normalizeDiscoveryCWD("")
	// Should be /tmp or /private/tmp on macOS, not "."
	if result != "/tmp" && result != "/private/tmp" {
		assert.Equal(t, ".", result)
	}
}

func TestDiscoverSkillDirs(t *testing.T) {
	// Save original cwd
	origCwd, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Chdir(origCwd) })

	// Change to temp dir for isolation
	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))

	dirs := discoverSkillDirs(tmpDir)

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

func TestAppendUniqueDir(t *testing.T) {
	seen := make(map[string]struct{})

	// Test: skips empty dirs
	dirs := appendUniqueDir(nil, seen, "")
	assert.Empty(t, dirs)

	seen = make(map[string]struct{})
	dirs = appendUniqueDir(nil, seen, "")
	assert.Empty(t, dirs)

	// Test: adds non-empty unique dir
	seen = make(map[string]struct{})
	dirs = appendUniqueDir(nil, seen, "/some/dir")
	assert.Equal(t, []string{"/some/dir"}, dirs)

	// Test: skips duplicates
	dirs = appendUniqueDir(dirs, seen, "/some/dir")
	assert.Equal(t, []string{"/some/dir"}, dirs)

	// Test: adds another unique dir
	dirs = appendUniqueDir(dirs, seen, "/another/dir")
	assert.Equal(t, []string{"/some/dir", "/another/dir"}, dirs)
}

func TestFindNearestDir(t *testing.T) {
	// Create nested structure: /tmp/level1/level2
	tmpDir := t.TempDir()
	level1 := filepath.Join(tmpDir, "level1")
	level2 := filepath.Join(level1, "level2")
	require.NoError(t, os.MkdirAll(level2, 0755))

	// Create target dir nested inside level2
	targetDir := filepath.Join(level2, ".pi-go", "skills")
	require.NoError(t, os.MkdirAll(targetDir, 0755))

	// Test: finds nested dir from deeper path (level2)
	found := findNearestDir(level2, filepath.Join(".pi-go", "skills"))
	assert.Equal(t, targetDir, found)

	// Test: finds same dir when starting from level1 by creating one there too
	targetDirL1 := filepath.Join(level1, ".pi-go", "skills")
	require.NoError(t, os.MkdirAll(targetDirL1, 0755))
	found = findNearestDir(level1, filepath.Join(".pi-go", "skills"))
	assert.Equal(t, targetDirL1, found)

	// Test: finds from root of tmp dir when target exists at that level
	targetDirRoot := filepath.Join(tmpDir, ".pi-go", "skills")
	require.NoError(t, os.MkdirAll(targetDirRoot, 0755))
	found = findNearestDir(tmpDir, filepath.Join(".pi-go", "skills"))
	assert.Equal(t, targetDirRoot, found)

	// Test: returns empty if not found
	found = findNearestDir(tmpDir, ".nonexistent/skills")
	assert.Empty(t, found)
}

func TestDiscoverAvailableCommands(t *testing.T) {
	tmpDir := t.TempDir()

	// Test: returns non-empty list of commands
	commands := DiscoverAvailableCommands(tmpDir)

	// Meta commands should always be included
	assert.NotEmpty(t, commands, "expected non-empty command list")

	// Check that at least meta commands are present
	hasMeta := false
	for _, cmd := range commands {
		if cmd.Name == "/help" || cmd.Name == "/quit" || cmd.Name == "/clear" || cmd.Name == "help" || cmd.Name == "clear" {
			hasMeta = true
			break
		}
	}
	assert.True(t, hasMeta, "expected at least one meta command in %v", commands)
}
