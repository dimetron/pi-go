//go:build unix

package provider

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// launchFoundationModelsServer starts `fm serve` detached from this process and
// returns a channel that yields its exit error, if any.
//
// Setsid rather than a mere process group: the server has to OUTLIVE pi, so it
// leaves both this terminal's session and pi's process group. That is the
// deliberate opposite of internal/procs, which isolates children precisely so
// cancellation reaches them — here cancellation must not.
//
// Output goes to a file, never to this process's streams. The TUI draws on the
// alternate screen, so a child inheriting its stdout would paint over the UI;
// the rule that keeps diagnostics off stdout in TUI code applies to anything the
// TUI starts.
//
// The returned channel is closed without a value when the exit status is not
// observable, which is the normal case for a detached child: Release hands the
// process to the OS and its exit code goes with it. EnsureFoundationModelsServer
// therefore polls /health for readiness and treats the channel only as an early
// failure signal.
func launchFoundationModelsServer() (<-chan error, error) {
	path, err := exec.LookPath("fm")
	if err != nil {
		return nil, errors.New("fm not found in PATH (install Apple's Foundation Models CLI)")
	}

	logPath := fmServerLogPath()
	if mkErr := os.MkdirAll(filepath.Dir(logPath), 0o700); mkErr != nil {
		return nil, fmt.Errorf("creating log directory: %w", mkErr)
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", logPath, err)
	}
	defer func() { _ = logFile.Close() }()

	// No context: a context would tie the server's lifetime to pi's, which is
	// exactly what detaching avoids.
	cmd := exec.Command(path, "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("running fm serve: %w", err)
	}
	// Release rather than Wait: pi is not this server's parent for life, and
	// reaping it here would either block a goroutine forever or race the caller's
	// own readiness polling for a status it does not need.
	_ = cmd.Process.Release()

	done := make(chan error)
	close(done)
	return done, nil
}
