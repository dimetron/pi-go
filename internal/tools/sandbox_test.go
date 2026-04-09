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
