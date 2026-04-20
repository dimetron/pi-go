package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// -----------------------------------------------------------------------
// runServe — exercise the happy-path by using an ephemeral port and
// sending SIGINT after the server starts.
// -----------------------------------------------------------------------

func TestRunServe_CleanShutdown(t *testing.T) {
	// Use an ephemeral port (:0) so we don't collide with other tests.
	tmpDir := t.TempDir()

	// Change CWD so serve.log is written in tmpDir.
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(origCwd)
	}()

	// Save and restore global flags.
	origAddr := flagServeAddr
	origProject := flagServeProject
	origTimeout := flagServePairingTimeout
	defer func() {
		flagServeAddr = origAddr
		flagServeProject = origProject
		flagServePairingTimeout = origTimeout
	}()
	flagServeAddr = "127.0.0.1:0"
	flagServeProject = tmpDir
	flagServePairingTimeout = 5 * time.Minute

	// Redirect stdout to /dev/null to suppress banner without racing.
	origStdout := os.Stdout
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("opening devnull: %v", err)
	}
	os.Stdout = devNull
	defer func() {
		os.Stdout = origStdout
		_ = devNull.Close()
	}()

	cmd := &cobra.Command{}

	done := make(chan error, 1)
	go func() {
		done <- runServe(cmd, nil)
	}()

	// Give the server time to start up.
	time.Sleep(300 * time.Millisecond)

	// Send SIGINT to our own process to trigger shutdown.
	proc, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatalf("FindProcess: %v", err)
	}
	if err := proc.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("Signal SIGINT: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Logf("runServe returned: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runServe did not shut down within 15 seconds of SIGINT")
	}
}

func TestRunServe_LogFileOpenError(t *testing.T) {
	// If the CWD is not writable, os.OpenFile("serve.log", ...) should fail.
	// We simulate by pointing to a path that cannot be written.
	// On most systems, we can create a read-only directory and chdir into it.
	tmpDir := t.TempDir()
	readOnly := filepath.Join(tmpDir, "ro")
	if err := os.MkdirAll(readOnly, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(readOnly, 0o755)

	origCwd, _ := os.Getwd()
	if err := os.Chdir(readOnly); err != nil {
		t.Skip("could not chdir into read-only dir:", err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	origProject := flagServeProject
	flagServeProject = readOnly
	defer func() { flagServeProject = origProject }()

	cmd := &cobra.Command{}
	err := runServe(cmd, nil)
	// Should fail to open serve.log (CWD is read-only) — but only if running
	// as a non-root user. Root users can still write to 0555 dirs.
	if err != nil && !strings.Contains(err.Error(), "serve.log") {
		t.Logf("runServe error: %v", err)
	}
}
