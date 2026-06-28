package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteHandlerPaths(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Missing file_path is rejected.
	if _, err := writeHandler(sb, WriteInput{Content: "x"}); err == nil {
		t.Error("expected error for empty file_path")
	}

	// Successful write into a nested (auto-created) directory.
	target := filepath.Join(dir, "nested", "out.txt")
	out, err := writeHandler(sb, WriteInput{FilePath: target, Content: "hello"})
	if err != nil {
		t.Fatalf("writeHandler success: %v", err)
	}
	if out.BytesWritten != len("hello") {
		t.Errorf("BytesWritten = %d, want %d", out.BytesWritten, len("hello"))
	}
	data, _ := os.ReadFile(target)
	if string(data) != "hello" {
		t.Errorf("file content = %q, want hello", data)
	}

	// Writing outside the sandbox root is rejected.
	if _, err := writeHandler(sb, WriteInput{FilePath: "/etc/should-not-write", Content: "x"}); err == nil {
		t.Error("expected error writing outside sandbox")
	}
}

func TestSandboxReadFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	path := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := sb.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("ReadFile = %q, want data", got)
	}

	// Missing file returns a non-transient error immediately.
	if _, err := sb.ReadFile(filepath.Join(dir, "missing.txt")); err == nil {
		t.Error("expected error reading missing file")
	}

	// Outside the sandbox is rejected.
	if _, err := sb.ReadFile("/etc/hosts"); err == nil {
		t.Error("expected error reading outside sandbox")
	}
}

func TestSandboxAddExtraDir(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	sb := testSandbox(t, root)

	if err := sb.AddExtraDir(extra); err != nil {
		t.Fatalf("AddExtraDir: %v", err)
	}

	// A file under the extra dir is now readable via its absolute path.
	ef := filepath.Join(extra, "extra.txt")
	if err := os.WriteFile(ef, []byte("extra-data"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := sb.ReadFile(ef)
	if err != nil {
		t.Fatalf("ReadFile extra: %v", err)
	}
	if string(got) != "extra-data" {
		t.Errorf("extra ReadFile = %q, want extra-data", got)
	}
}

func TestSandboxResolveWorktreePath(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "sub")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}

	sb, err := NewSandbox(root, worktree)
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { sb.Close() })

	// A file in the sandbox root, referenced from the worktree via "../".
	if err := os.WriteFile(filepath.Join(root, "top.txt"), []byte("top"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := sb.ReadFile("../top.txt")
	if err != nil {
		t.Fatalf("ReadFile worktree-relative: %v", err)
	}
	if string(got) != "top" {
		t.Errorf("worktree ReadFile = %q, want top", got)
	}

	// A path that escapes the sandbox root must be rejected.
	if _, err := sb.ReadFile("../../../../etc/passwd"); err == nil {
		t.Error("expected error for escaping worktree-relative path")
	}
}

func TestShouldSkipDir(t *testing.T) {
	t.Parallel()
	skip := []string{".git", ".hidden", "node_modules", "vendor", "__pycache__"}
	for _, d := range skip {
		if !shouldSkipDir(d) {
			t.Errorf("shouldSkipDir(%q) = false, want true", d)
		}
	}
	keep := []string{".", ".pi-go", ".cursor", ".claude", "src", "internal"}
	for _, d := range keep {
		if shouldSkipDir(d) {
			t.Errorf("shouldSkipDir(%q) = true, want false", d)
		}
	}
}
