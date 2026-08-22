package cli

import (
	"os"
	"path/filepath"
	"runtime"
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
	if runtime.GOOS == "windows" {
		// The shutdown path under test is driven by SIGINT, and a process
		// cannot deliver one to itself on Windows: os.Process.Signal supports
		// only Kill there, so proc.Signal(syscall.SIGINT) returns an error and
		// runServe would be left serving for the rest of the package run.
		t.Skip("cannot raise SIGINT against our own process on Windows")
	}

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
	// runServe opens ./serve.log before it does anything else, so a directory
	// sitting at that name makes the open fail on every OS. The previous
	// spelling -- chdir into a 0555 directory -- was a no-op on Windows, where
	// mode bits do not restrict a directory: the open would succeed and
	// runServe would go on to serve until it was signaled, hanging the test
	// until the package timeout. It was also a no-op for root on Unix.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "serve.log"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	origProject := flagServeProject
	flagServeProject = dir
	defer func() { flagServeProject = origProject }()

	err := runServe(&cobra.Command{}, nil)
	if err == nil {
		t.Fatal("runServe() = nil, want the serve.log open to fail")
	}
	if !strings.Contains(err.Error(), "serve.log") {
		t.Errorf("runServe() = %v, want an error naming serve.log", err)
	}
}
