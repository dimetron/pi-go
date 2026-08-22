// Package testenv holds small helpers that keep tests portable across
// operating systems. It is only imported from _test.go files.
package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// SetHome points the user's home directory at dir for the duration of the test.
//
// os.UserHomeDir reads $HOME on Unix but %USERPROFILE% on Windows, so a test
// that sets only HOME still resolves to the real profile directory there --
// which makes the test read and write the developer's (or CI runner's) actual
// ~/.pi-go instead of its sandbox.
func SetHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

// SetUnwritableHome points the home directory at a regular file for the
// duration of the test, so anything the code under test tries to create below
// ~ fails. It returns that path.
//
// Tests used to spell this as HOME=/nonexistent/..., which does not travel:
// on Windows a leading slash is drive-relative, so os.MkdirAll cheerfully
// creates the directory and the expected failure never happens.
func SetUnwritableHome(t *testing.T) string {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home-is-a-file")
	if err := os.WriteFile(home, nil, 0o600); err != nil {
		t.Fatalf("creating the file that stands in for HOME: %v", err)
	}
	SetHome(t, home)
	return home
}

// RequireShell skips the test unless a POSIX shell is on PATH.
//
// The GitHub Windows runners ship Git for Windows, so "bash" and "sh" resolve
// there; a bare Windows box has neither. Tests that drive shell commands call
// this instead of hardcoding /bin/sh, which never exists on Windows.
func RequireShell(t *testing.T) string {
	t.Helper()
	sh, err := exec.LookPath("bash")
	if err != nil {
		sh, err = exec.LookPath("sh")
	}
	if err != nil {
		t.Skipf("no POSIX shell on PATH: %v", err)
	}
	return sh
}

// UnsetHome removes the home-directory environment variables for the duration
// of the test, so os.UserHomeDir has nothing to resolve. Unsetting HOME alone
// leaves %USERPROFILE% in place, which is the only variable Windows consults.
func UnsetHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", "")
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", "")
	}
	os.Unsetenv("HOME")
	if runtime.GOOS == "windows" {
		os.Unsetenv("USERPROFILE")
	}
}

// FakeBinary writes a do-nothing executable named name into dir and returns
// its path.
//
// On Windows the file gets a .bat suffix, because exec.LookPath there resolves
// only the suffixes listed in %PATHEXT% -- an extensionless file is invisible
// to it, however executable its mode bits claim to be.
func FakeBinary(t *testing.T, dir, name string) string {
	t.Helper()
	body := "#!/bin/sh\nexit 0\n"
	if runtime.GOOS == "windows" {
		name += ".bat"
		body = "@exit /b 0\r\n"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake binary %s: %v", path, err)
	}
	return path
}
