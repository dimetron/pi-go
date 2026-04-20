package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// --- NewSandbox ---

func TestNewSandbox_ValidDir(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox(%s): %v", dir, err)
	}
	defer sb.Close()

	if sb.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", sb.Dir(), dir)
	}
}

func TestNewSandbox_NonexistentDir(t *testing.T) {
	_, err := NewSandbox("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestNewSandbox_RelativeDir(t *testing.T) {
	// A relative path like "." should resolve successfully
	sb, err := NewSandbox(".")
	if err != nil {
		t.Fatalf("NewSandbox('.'): %v", err)
	}
	defer sb.Close()

	if !filepath.IsAbs(sb.Dir()) {
		t.Errorf("Dir() should be absolute, got %q", sb.Dir())
	}
}

// --- Close ---

func TestSandbox_Close(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	if err := sb.Close(); err != nil {
		t.Errorf("Close() returned unexpected error: %v", err)
	}
}

// --- Dir and FS ---

func TestSandbox_DirAndFS(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	if sb.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", sb.Dir(), dir)
	}
	if sb.FS() == nil {
		t.Error("FS() returned nil")
	}
}

// --- Resolve ---

func TestSandbox_Resolve_RelativePath(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	rel, err := sb.Resolve("subdir/file.txt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if rel != "subdir/file.txt" {
		t.Errorf("Resolve(relative) = %q, want %q", rel, "subdir/file.txt")
	}
}

func TestSandbox_Resolve_AbsolutePathInside(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	absPath := filepath.Join(dir, "subdir", "file.txt")
	rel, err := sb.Resolve(absPath)
	if err != nil {
		t.Fatalf("Resolve(abs inside): %v", err)
	}
	if rel != filepath.Join("subdir", "file.txt") {
		t.Errorf("Resolve(abs) = %q, want %q", rel, filepath.Join("subdir", "file.txt"))
	}
}

func TestSandbox_Resolve_AbsolutePathOutsideRootFS(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Resolve returns a relative path (potentially with ..) for absolute paths outside.
	// The actual security enforcement is done by os.Root which blocks ".." traversal.
	// ReadFile with an absolute outside path should fail at os.Root level.
	_, err := sb.ReadFile("/etc/passwd")
	if err == nil {
		t.Error("expected error reading /etc/passwd via sandbox (os.Root blocks .. traversal)")
	}
}

func TestSandbox_Resolve_DotPath(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	rel, err := sb.Resolve(".")
	if err != nil {
		t.Fatalf("Resolve('.'): %v", err)
	}
	if rel != "." {
		t.Errorf("Resolve('.') = %q, want '.'", rel)
	}
}

// --- ReadFile ---

func TestSandbox_ReadFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	content := "hello sandbox"
	path := filepath.Join(dir, "hello.txt")
	os.WriteFile(path, []byte(content), 0o644)

	data, err := sb.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != content {
		t.Errorf("ReadFile = %q, want %q", string(data), content)
	}
}

func TestSandbox_ReadFile_Relative(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "rel.txt"), []byte("relative read"), 0o644)

	data, err := sb.ReadFile("rel.txt")
	if err != nil {
		t.Fatalf("ReadFile(relative): %v", err)
	}
	if string(data) != "relative read" {
		t.Errorf("ReadFile = %q, want %q", string(data), "relative read")
	}
}

func TestSandbox_ReadFile_NotExist(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := sb.ReadFile("nonexistent.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestSandbox_ReadFile_OutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := sb.ReadFile("/etc/passwd")
	if err == nil {
		t.Error("expected error for path outside sandbox")
	}
}

// --- WriteFile ---

func TestSandbox_WriteFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	err := sb.WriteFile("newfile.txt", []byte("written"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "newfile.txt"))
	if string(data) != "written" {
		t.Errorf("file content = %q, want %q", string(data), "written")
	}
}

func TestSandbox_WriteFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	err := sb.WriteFile("a/b/c/deep.txt", []byte("deep"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(deep): %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "a", "b", "c", "deep.txt"))
	if string(data) != "deep" {
		t.Errorf("deep file content = %q, want %q", string(data), "deep")
	}
}

func TestSandbox_WriteFile_Overwrite(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "overwrite.txt"), []byte("original"), 0o644)

	err := sb.WriteFile("overwrite.txt", []byte("replaced"), 0o644)
	if err != nil {
		t.Fatalf("WriteFile(overwrite): %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "overwrite.txt"))
	if string(data) != "replaced" {
		t.Errorf("overwritten content = %q, want %q", string(data), "replaced")
	}
}

func TestSandbox_WriteFile_OutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	err := sb.WriteFile("/etc/passwd", []byte("bad"), 0o644)
	if err == nil {
		t.Error("expected error writing outside sandbox")
	}
}

// --- Open ---

func TestSandbox_Open(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "open.txt"), []byte("open content"), 0o644)

	f, err := sb.Open("open.txt")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	buf := make([]byte, 12)
	n, _ := f.Read(buf)
	if string(buf[:n]) != "open content" {
		t.Errorf("Read = %q, want %q", string(buf[:n]), "open content")
	}
}

func TestSandbox_Open_NotExist(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := sb.Open("missing.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestSandbox_Open_OutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := sb.Open("/etc/passwd")
	if err == nil {
		t.Error("expected error for path outside sandbox")
	}
}

// --- Stat ---

func TestSandbox_Stat_File(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "stat.txt"), []byte("stat content"), 0o644)

	info, err := sb.Stat("stat.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.IsDir() {
		t.Error("expected file, got directory")
	}
	if info.Size() != 12 {
		t.Errorf("Size = %d, want 12", info.Size())
	}
}

func TestSandbox_Stat_Directory(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	info, err := sb.Stat("subdir")
	if err != nil {
		t.Fatalf("Stat(dir): %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

func TestSandbox_Stat_NotExist(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := sb.Stat("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}
}

func TestSandbox_Stat_OutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := sb.Stat("/etc")
	if err == nil {
		t.Error("expected error for path outside sandbox")
	}
}

// --- ReadDir ---

func TestSandbox_ReadDir(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	entries, err := sb.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("ReadDir = %d entries, want 3", len(entries))
	}
}

func TestSandbox_ReadDir_Empty(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	entries, err := sb.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(empty): %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("ReadDir(empty) = %d entries, want 0", len(entries))
	}
}

func TestSandbox_ReadDir_NotExist(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := sb.ReadDir("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestSandbox_ReadDir_AbsolutePath(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644)

	// Using absolute path inside sandbox
	entries, err := sb.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(abs): %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("ReadDir(abs) = %d entries, want 1", len(entries))
	}
}

// --- MkdirAll ---

func TestSandbox_MkdirAll(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	err := sb.MkdirAll("a/b/c", 0o755)
	if err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "a", "b", "c"))
	if err != nil {
		t.Fatalf("Stat after MkdirAll: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory after MkdirAll")
	}
}

func TestSandbox_MkdirAll_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create once
	if err := sb.MkdirAll("existing/dir", 0o755); err != nil {
		t.Fatalf("MkdirAll first call: %v", err)
	}
	// Create again — should be idempotent
	if err := sb.MkdirAll("existing/dir", 0o755); err != nil {
		t.Errorf("MkdirAll (already exists) returned error: %v", err)
	}
}

func TestSandbox_MkdirAll_OutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	err := sb.MkdirAll("/etc/baddir", 0o755)
	if err == nil {
		t.Error("expected error for MkdirAll outside sandbox")
	}
}

// --- isTransientReadError ---

func TestIsTransientReadError_Nil(t *testing.T) {
	if isTransientReadError(nil) {
		t.Error("nil error should not be transient")
	}
}

func TestIsTransientReadError_TextFileBusy(t *testing.T) {
	err := fmt.Errorf("open file: text file busy")
	if !isTransientReadError(err) {
		t.Error("'text file busy' should be transient")
	}
}

func TestIsTransientReadError_ResourceUnavailable(t *testing.T) {
	err := fmt.Errorf("read: resource temporarily unavailable")
	if !isTransientReadError(err) {
		t.Error("'resource temporarily unavailable' should be transient")
	}
}

func TestIsTransientReadError_IOError(t *testing.T) {
	err := fmt.Errorf("read: input/output error")
	if !isTransientReadError(err) {
		t.Error("'input/output error' should be transient")
	}
}

func TestIsTransientReadError_ETIMEDOUT(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", syscall.ETIMEDOUT)
	if !isTransientReadError(err) {
		t.Error("ETIMEDOUT should be transient")
	}
}

func TestIsTransientReadError_ETIMEDOUT_Direct(t *testing.T) {
	if !isTransientReadError(syscall.ETIMEDOUT) {
		t.Error("direct ETIMEDOUT should be transient")
	}
}

func TestIsTransientReadError_OtherError(t *testing.T) {
	err := errors.New("permission denied")
	if isTransientReadError(err) {
		t.Error("'permission denied' should not be transient")
	}
}

func TestIsTransientReadError_NotFound(t *testing.T) {
	err := os.ErrNotExist
	if isTransientReadError(err) {
		t.Error("ErrNotExist should not be transient")
	}
}

// --- Worktree path normalization ---

func TestSandbox_Resolve_WorktreeRelativePath(t *testing.T) {
	// Simulate subagent worktree scenario:
	// - Sandbox root is the repo root
	// - Worktree is a subdirectory
	// - Agent tries to access files using ../.. patterns relative to worktree

	repoRoot := t.TempDir()
	worktreeDir := filepath.Join(repoRoot, "worktrees", "agent-123")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("creating worktree dir: %v", err)
	}

	// Create a file in the repo root
	os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module example"), 0o644)

	// Create sandbox with worktree context
	sb, err := NewSandbox(repoRoot, worktreeDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	// Test resolving ../../go.mod from worktree perspective
	rel, err := sb.Resolve("../../go.mod")
	if err != nil {
		t.Fatalf("Resolve(../../go.mod): %v", err)
	}
	if rel != "go.mod" {
		t.Errorf("Resolve(../../go.mod) = %q, want %q", rel, "go.mod")
	}
}

func TestSandbox_Resolve_WorktreeRelativePathDeep(t *testing.T) {
	repoRoot := t.TempDir()
	worktreeDir := filepath.Join(repoRoot, "worktrees", "agent-456", "subdir")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("creating worktree dir: %v", err)
	}

	// Create files at various levels
	os.WriteFile(filepath.Join(repoRoot, "go.mod"), []byte("module root"), 0o644)
	os.WriteFile(filepath.Join(repoRoot, "worktrees", "go.sum"), []byte("checksums"), 0o644)

	sb, err := NewSandbox(repoRoot, worktreeDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	tests := []struct {
		path string
		want string
		desc string
	}{
		// ../../ from subdir = worktrees/agent-456
		{"../../go.mod", "worktrees/go.mod", "file in worktrees dir"},
		{"../go.sum", filepath.Join("worktrees", "agent-456", "go.sum"), "file in agent dir"},
		{"../..", "worktrees", "parent of agent dir"},
	}

	for _, tt := range tests {
		rel, err := sb.Resolve(tt.path)
		if err != nil {
			t.Errorf("%s: Resolve(%q): %v", tt.desc, tt.path, err)
			continue
		}
		if rel != tt.want {
			t.Errorf("%s: Resolve(%q) = %q, want %q", tt.desc, tt.path, rel, tt.want)
		}
	}
}

func TestSandbox_Resolve_WorktreePathEscapes(t *testing.T) {
	// Test that worktree-relative paths that would escape the sandbox are rejected
	repoRoot := t.TempDir()
	worktreeDir := filepath.Join(repoRoot, "worktrees", "agent-789")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("creating worktree dir: %v", err)
	}

	sb, err := NewSandbox(repoRoot, worktreeDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	// This should fail: ../../../../../etc/passwd would escape the sandbox
	_, err = sb.Resolve("../../../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path that escapes sandbox")
	}
}

func TestSandbox_Resolve_WorktreeNoEscapeWithoutWorktree(t *testing.T) {
	// Without worktree context, ../ paths should still be blocked
	dir := t.TempDir()
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	// Without worktree, ../ paths should be rejected
	_, err = sb.Resolve("../go.mod")
	if err == nil {
		t.Error("expected error for ../ path without worktree context")
	}
}

func TestSandbox_SetWorktreeDir(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	// Without worktree, ../ paths are blocked
	_, err = sb.Resolve("../go.mod")
	if err == nil {
		t.Error("expected error without worktree set")
	}

	// Set worktree: dir/worktrees/agent
	worktreeDir := filepath.Join(dir, "worktrees", "agent")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("creating worktree: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module"), 0o644)

	if err := sb.SetWorktreeDir(worktreeDir); err != nil {
		t.Fatalf("SetWorktreeDir: %v", err)
	}

	// Now ../ should work (but we need ../../ from worktrees/agent)
	rel, err := sb.Resolve("../../go.mod")
	if err != nil {
		t.Fatalf("Resolve with worktree: %v", err)
	}
	if rel != "go.mod" {
		t.Errorf("Resolve = %q, want %q", rel, "go.mod")
	}

	// Clear worktree
	if err := sb.SetWorktreeDir(""); err != nil {
		t.Fatalf("SetWorktreeDir: %v", err)
	}

	// ../ should fail again
	_, err = sb.Resolve("../go.mod")
	if err == nil {
		t.Error("expected error after clearing worktree")
	}
}

// --- Extra roots ---

func TestSandbox_ExtraDir_ReadWrite(t *testing.T) {
	// Primary sandbox root.
	projectDir := t.TempDir()
	// Extra directory (simulates ~/.pi-go/).
	extraDir := t.TempDir()

	sb, err := NewSandbox(projectDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	// Before adding extra dir, absolute paths under it should fail.
	testFile := filepath.Join(extraDir, "log", "test.log")
	_, err = sb.ReadFile(testFile)
	if err == nil {
		t.Error("expected error reading from non-allowed extra dir")
	}

	// Add extra dir.
	if err := sb.AddExtraDir(extraDir); err != nil {
		t.Fatalf("AddExtraDir: %v", err)
	}

	// Write a file via the extra root.
	if err := sb.WriteFile(testFile, []byte("log entry"), 0o644); err != nil {
		t.Fatalf("WriteFile to extra dir: %v", err)
	}

	// Read it back.
	data, err := sb.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile from extra dir: %v", err)
	}
	if string(data) != "log entry" {
		t.Errorf("ReadFile = %q, want %q", data, "log entry")
	}

	// Stat should work.
	info, err := sb.Stat(testFile)
	if err != nil {
		t.Fatalf("Stat extra dir file: %v", err)
	}
	if info.Name() != "test.log" {
		t.Errorf("Stat name = %q, want %q", info.Name(), "test.log")
	}

	// ReadDir should work.
	entries, err := sb.ReadDir(filepath.Join(extraDir, "log"))
	if err != nil {
		t.Fatalf("ReadDir extra dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "test.log" {
		t.Errorf("ReadDir = %v, want [test.log]", entries)
	}

	// Primary sandbox still works.
	if err := sb.WriteFile("project.txt", []byte("project"), 0o644); err != nil {
		t.Fatalf("WriteFile to primary: %v", err)
	}
	pData, err := sb.ReadFile("project.txt")
	if err != nil {
		t.Fatalf("ReadFile from primary: %v", err)
	}
	if string(pData) != "project" {
		t.Errorf("primary ReadFile = %q, want %q", pData, "project")
	}
}

func TestSandbox_ExtraDir_StillBlocksOtherPaths(t *testing.T) {
	projectDir := t.TempDir()
	extraDir := t.TempDir()
	otherDir := t.TempDir()

	sb, err := NewSandbox(projectDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	if err := sb.AddExtraDir(extraDir); err != nil {
		t.Fatalf("AddExtraDir: %v", err)
	}

	// Paths under a different directory should still be blocked.
	_, err = sb.ReadFile(filepath.Join(otherDir, "secret.txt"))
	if err == nil {
		t.Error("expected error reading from non-allowed directory")
	}
}

// TestSandbox_AddExtraDir_CreatesMissingDir verifies AddExtraDir creates the
// directory if it does not already exist.
func TestSandbox_AddExtraDir_CreatesMissingDir(t *testing.T) {
	projectDir := t.TempDir()
	sb, err := NewSandbox(projectDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	extraDir := filepath.Join(t.TempDir(), "new", "nested")
	if err := sb.AddExtraDir(extraDir); err != nil {
		t.Fatalf("AddExtraDir: %v", err)
	}
	info, err := os.Stat(extraDir)
	if err != nil {
		t.Fatalf("extra dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory, got file")
	}
}

// TestSandbox_AddExtraDir_MkdirAllError verifies AddExtraDir returns an error
// when the parent path is a file (mkdir would fail).
func TestSandbox_AddExtraDir_MkdirAllError(t *testing.T) {
	projectDir := t.TempDir()
	sb, err := NewSandbox(projectDir)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()

	// Make a file, then try to use a path beneath it as an extra dir.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o644); err != nil {
		t.Fatalf("writing blocker: %v", err)
	}
	badDir := filepath.Join(blocker, "child")
	err = sb.AddExtraDir(badDir)
	if err == nil {
		t.Error("expected AddExtraDir error when path parent is a file")
	}
}

// TestSandbox_WriteFile_MkdirAllError covers the branch where creating
// intermediate directories fails because the target path includes a file
// component that is not a directory.
func TestSandbox_WriteFile_BlockedByFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Make a plain file, then try to write under it as if it were a dir.
	if err := sb.WriteFile("notadir", []byte("x"), 0o644); err != nil {
		t.Fatalf("seed write: %v", err)
	}
	err := sb.WriteFile("notadir/child.txt", []byte("x"), 0o644)
	if err == nil {
		t.Error("expected error when writing under a regular file")
	}
}

// TestSandbox_Open_OnDirectoryUnreadableFile covers the Open path when the
// target is a directory (which Open succeeds on) and verifies content.
func TestSandbox_Open_OnDirectory(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := sb.Open("sub")
	if err != nil {
		t.Fatalf("Open on directory: %v", err)
	}
	defer f.Close()

	// reading a directory as a file should error or return early.
	// Just verify Open succeeded (covers the happy path).
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected IsDir=true")
	}
}

// TestSandbox_ReadDir_OnFile covers ReadDir's error path when the name is a
// regular file rather than a directory.
func TestSandbox_ReadDir_OnFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	if err := sb.WriteFile("file.txt", []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := sb.ReadDir("file.txt")
	if err == nil {
		t.Error("expected ReadDir on regular file to fail")
	}
}

// TestSandbox_SetWorktreeDir_Empty verifies clearing via empty string.
func TestSandbox_SetWorktreeDir_Empty(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, filepath.Join(dir, "wt"))
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	defer sb.Close()
	if err := sb.SetWorktreeDir(""); err != nil {
		t.Fatalf("SetWorktreeDir(''): %v", err)
	}
}

// --- parseGitignoreLine ---

func TestParseGitignoreLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		dir     string
		wantOK  bool
		wantNeg bool
		wantDir bool
		wantPat string
	}{
		{"empty line", "", "", false, false, false, ""},
		{"whitespace-only not skipped", "   ", "", true, false, false, "   "},
		{"comment", "# this is a comment", "", false, false, false, ""},
		{"simple pattern", "*.log", "", true, false, false, "*.log"},
		{"negation", "!important.log", "", true, true, false, "important.log"},
		{"directory pattern", "build/", "", true, false, true, "build"},
		{"negated directory", "!keep/", "", true, true, true, "keep"},
		{"double-star collapse", "**", "", true, false, false, "*"},
		{"recursive glob", "**/foo", "", true, false, false, "*/foo"},
		{"trailing CR stripped", "*.tmp\r", "", true, false, false, "*.tmp"},
		{"with dir context", "out", "src", true, false, false, "out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGitignoreLine(tt.line, tt.dir)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.isNeg != tt.wantNeg {
				t.Errorf("isNeg = %v, want %v", got.isNeg, tt.wantNeg)
			}
			if got.isDir != tt.wantDir {
				t.Errorf("isDir = %v, want %v", got.isDir, tt.wantDir)
			}
			if got.pattern != tt.wantPat {
				t.Errorf("pattern = %q, want %q", got.pattern, tt.wantPat)
			}
			if got.dir != tt.dir {
				t.Errorf("dir = %q, want %q", got.dir, tt.dir)
			}
		})
	}
}

// --- GitignorePattern.matches ---

func TestGitignorePattern_Matches(t *testing.T) {
	tests := []struct {
		name string
		p    GitignorePattern
		path string
		want bool
	}{
		{
			name: "simple match",
			p:    GitignorePattern{pattern: "*.log"},
			path: "app.log",
			want: true,
		},
		{
			name: "simple no match",
			p:    GitignorePattern{pattern: "*.log"},
			path: "app.go",
			want: false,
		},
		{
			name: "dir-scoped match",
			p:    GitignorePattern{dir: "src", pattern: "*.go"},
			path: "src/main.go",
			want: true,
		},
		{
			name: "dir-scoped outside dir",
			p:    GitignorePattern{dir: "src", pattern: "*.go"},
			path: "lib/main.go",
			want: false,
		},
		{
			name: "dir-scoped subdir match",
			p:    GitignorePattern{dir: "src", pattern: "*.go"},
			path: "src/sub/main.go",
			want: true,
		},
		{
			name: "isDir flag forwarded",
			p:    GitignorePattern{pattern: "build", isDir: true},
			path: "build",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.matches(tt.path); got != tt.want {
				t.Errorf("matches(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// --- loadGitignore / LoadGitignorePatterns ---

func TestSandbox_LoadGitignorePatterns_RootOnly(t *testing.T) {
	dir := t.TempDir()
	// Write a root .gitignore and a .git dir that must be skipped.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"),
		[]byte("# comment\n\n*.log\n!keep.log\nbuild/\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	// .git directory with a nested .gitignore that must NOT be read.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", ".gitignore"),
		[]byte("SHOULD_NOT_LOAD\n"), 0o644); err != nil {
		t.Fatalf("write .git gitignore: %v", err)
	}

	sb := testSandbox(t, dir)
	patterns, err := sb.LoadGitignorePatterns()
	if err != nil {
		t.Fatalf("LoadGitignorePatterns: %v", err)
	}
	if len(patterns) == 0 {
		t.Fatal("expected at least one pattern")
	}
	for _, p := range patterns {
		if p.pattern == "SHOULD_NOT_LOAD" {
			t.Error(".git/.gitignore should be skipped")
		}
	}
}

func TestSandbox_LoadGitignorePatterns_Nested(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"),
		[]byte("*.log\n"), 0o644); err != nil {
		t.Fatalf("write gitignore: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", ".gitignore"),
		[]byte("*.tmp\n"), 0o644); err != nil {
		t.Fatalf("write nested gitignore: %v", err)
	}

	sb := testSandbox(t, dir)
	patterns, err := sb.LoadGitignorePatterns()
	if err != nil {
		t.Fatalf("LoadGitignorePatterns: %v", err)
	}

	hasLog, hasTmp := false, false
	for _, p := range patterns {
		if p.pattern == "*.log" && p.dir == "" {
			hasLog = true
		}
		if p.pattern == "*.tmp" && p.dir == "sub" {
			hasTmp = true
		}
	}
	if !hasLog {
		t.Error("missing *.log root pattern")
	}
	if !hasTmp {
		t.Error("missing *.tmp sub pattern")
	}
}

func TestSandbox_LoadGitignorePatterns_NoFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	patterns, err := sb.LoadGitignorePatterns()
	if err != nil {
		t.Fatalf("LoadGitignorePatterns: %v", err)
	}
	if patterns != nil {
		t.Errorf("expected nil patterns when no .gitignore present, got %v", patterns)
	}
}

// --- shouldSkipPath ---

// fakeDirEntry is a minimal fs.DirEntry for unit tests.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return f.isDir }
func (f fakeDirEntry) Type() os.FileMode          { return 0 }
func (f fakeDirEntry) Info() (os.FileInfo, error) { return nil, nil }

func TestShouldSkipPath(t *testing.T) {
	patterns := []GitignorePattern{
		{pattern: "*.log"},              // file matches
		{pattern: "build", isDir: true}, // dir-only
	}

	tests := []struct {
		name    string
		relPath string
		entry   fakeDirEntry
		want    bool
	}{
		{"node_modules skipped", "node_modules", fakeDirEntry{name: "node_modules", isDir: true}, true},
		{"vendor skipped", "vendor", fakeDirEntry{name: "vendor", isDir: true}, true},
		{"__pycache__ skipped", "__pycache__", fakeDirEntry{name: "__pycache__", isDir: true}, true},
		{"hidden dir skipped", ".secret", fakeDirEntry{name: ".secret", isDir: true}, true},
		{"agent .pi-go NOT skipped", ".pi-go", fakeDirEntry{name: ".pi-go", isDir: true}, false},
		{"agent .cursor NOT skipped", ".cursor", fakeDirEntry{name: ".cursor", isDir: true}, false},
		{"agent .claude NOT skipped", ".claude", fakeDirEntry{name: ".claude", isDir: true}, false},
		{"target dir skipped", "target", fakeDirEntry{name: "target", isDir: true}, true},
		{"plain file not skipped", "main.go", fakeDirEntry{name: "main.go", isDir: false}, false},
		{"dot path allowed", ".", fakeDirEntry{name: ".", isDir: true}, false},
		{"gitignore *.log matches", "app.log", fakeDirEntry{name: "app.log", isDir: false}, true},
		{"gitignore isDir pattern skips dir", "build", fakeDirEntry{name: "build", isDir: true}, true},
		{"gitignore isDir pattern NOT skip file", "build", fakeDirEntry{name: "build", isDir: false}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldSkipPath(tt.relPath, tt.entry, patterns)
			if got != tt.want {
				t.Errorf("shouldSkipPath(%q) = %v, want %v", tt.relPath, got, tt.want)
			}
		})
	}
}

// TestShouldSkipPath_Negation covers the negation short-circuit.
func TestShouldSkipPath_Negation(t *testing.T) {
	// Negation pattern alone — when it matches, returns false immediately.
	patterns := []GitignorePattern{
		{pattern: "keep.log", isNeg: true},
	}
	entry := fakeDirEntry{name: "keep.log", isDir: false}
	if shouldSkipPath("keep.log", entry, patterns) {
		t.Error("expected negation to prevent skip")
	}
}

// --- readFileFromFS ---

func TestReadFileFromFS(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	sb := testSandbox(t, dir)

	data, err := readFileFromFS(sb.FS(), "hello.txt")
	if err != nil {
		t.Fatalf("readFileFromFS: %v", err)
	}
	if string(data) != "hi" {
		t.Errorf("got %q, want %q", data, "hi")
	}
}

func TestReadFileFromFS_Missing(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := readFileFromFS(sb.FS(), "missing.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// --- NewSandbox worktreeDir validation ---

func TestNewSandbox_WithEmptyWorktree(t *testing.T) {
	dir := t.TempDir()
	sb, err := NewSandbox(dir, "")
	if err != nil {
		t.Fatalf("NewSandbox(empty worktree): %v", err)
	}
	defer sb.Close()

	// Without a worktree, ../ paths should be rejected.
	_, err = sb.Resolve("../escape")
	if err == nil {
		t.Error("expected error with empty worktree")
	}
}

// --- Resolve edge cases ---

func TestSandbox_Resolve_AbsoluteOutsideReturnsError(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// An absolute path unrelated to sandbox should yield a relative path
	// containing "..", which Resolve detects and rejects.
	_, err := sb.Resolve("/completely/unrelated/path")
	if err == nil {
		t.Error("expected error for absolute path outside sandbox")
	}
}
