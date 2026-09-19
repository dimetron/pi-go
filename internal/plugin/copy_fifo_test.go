//go:build unix

package plugin

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// copyTree must skip special files rather than copying them. A named pipe
// stands in for the sockets and devices it is there to avoid.
//
// The test is behind the unix build tag because syscall.Mkfifo does not exist
// on Windows, and a runtime GOOS check would not stop the file from failing to
// compile there.
func TestCopyTree_SkipsSpecialFiles(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "kept.md"), "kept\n")
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Skipf("FIFOs unavailable: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "out")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "kept.md")); err != nil {
		t.Errorf("a regular file beside a FIFO was not copied: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "pipe")); !os.IsNotExist(err) {
		t.Error("a special file was copied; it should be skipped")
	}
}
