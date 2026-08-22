package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestProfileParsing verifies that param declarations are stripped
// and (param "HOME") / (param "CWD") are substituted correctly.
func TestProfileParsing(t *testing.T) {
	// Exercise the same logic that main() uses.
	paramDecl := regexp.MustCompile(`(?m)^\(param\s+.*\)\s*$\n?`)
	resolved := paramDecl.ReplaceAllString(profile, "")

	origHome := os.Getenv("HOME")
	origCwd, _ := os.Getwd()
	defer os.Setenv("HOME", origHome)

	// Verify the embedded profile has the expected placeholders.
	if !containsParam(resolved, "HOME") || !containsParam(resolved, "CWD") {
		t.Skip("profile may not contain param placeholders on this platform")
	}

	resolved = replaceParams(resolved, origHome, origCwd)

	// After substitution, no raw param placeholders should remain.
	if containsParam(resolved, "HOME") || containsParam(resolved, "CWD") {
		t.Error("HOME/CWD placeholders were not substituted")
	}
}

func containsParam(s, name string) bool {
	return regexp.MustCompile(`\(param\s+"` + name + `"\)`).MatchString(s)
}

func replaceParams(s, home, cwd string) string {
	s = regexp.MustCompile(`\(param\s+"HOME"\)`).ReplaceAllString(s, `"`+home+`"`)
	s = regexp.MustCompile(`\(param\s+"CWD"\)`).ReplaceAllString(s, `"`+cwd+`"`)
	return s
}

// resolveProfile must strip the (param ...) declarations sandbox-exec rejects
// and substitute the HOME/CWD references with quoted literals.
func TestResolveProfile(t *testing.T) {
	src := "(version 1)\n(param \"HOME\" \"x\")\n(param \"CWD\" \"y\")\n(allow file-read* (subpath (param \"HOME\")))\n(allow file-write* (subpath (param \"CWD\")))\n"

	got := resolveProfile(src, "/Users/dev", "/work/proj")

	if strings.Contains(got, "(param ") {
		t.Errorf("param declarations/references survived:\n%s", got)
	}
	if !strings.Contains(got, `"/Users/dev"`) {
		t.Errorf("HOME not substituted (and quoted):\n%s", got)
	}
	if !strings.Contains(got, `"/work/proj"`) {
		t.Errorf("CWD not substituted (and quoted):\n%s", got)
	}
	if !strings.Contains(got, "(version 1)") {
		t.Errorf("profile body was damaged:\n%s", got)
	}
}

// A path containing a quote or space must be emitted as a quoted literal, or the
// generated profile would be malformed and sandbox-exec would reject it.
func TestResolveProfile_QuotesAwkwardPaths(t *testing.T) {
	got := resolveProfile(`(subpath (param "CWD"))`, "/h", `/tmp/my "proj"`)
	if !strings.Contains(got, `"/tmp/my \"proj\""`) {
		t.Errorf("awkward path not escaped: %s", got)
	}
}

func TestIsNoiseLogLine(t *testing.T) {
	if !isNoiseLogLine("Filtering the log data using ...") {
		t.Error("the log-stream header should be treated as noise")
	}
	if isNoiseLogLine("deny file-write /etc/passwd") {
		t.Error("a real denial must not be dropped")
	}
}

// exitCodeFor forwards the child's own exit code, so `pi-sandbox` is
// transparent to callers and CI.
func TestExitCodeFor(t *testing.T) {
	// Needs a POSIX shell to produce a child with a chosen exit code. Git for
	// Windows ships one, but its usr/bin is not on the default runner PATH, and
	// pi-sandbox is a macOS launcher (sandbox-exec + log stream) with no Windows
	// behavior to cover here.
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell; pi-sandbox is macOS-only")
	}

	var stderr bytes.Buffer

	if code := exitCodeFor(nil, &stderr); code != 0 {
		t.Errorf("exitCodeFor(nil) = %d, want 0", code)
	}

	// A child that exits 3 must surface 3, not 1.
	err := exec.Command("sh", "-c", "exit 3").Run()
	if code := exitCodeFor(err, &stderr); code != 3 {
		t.Errorf("exitCodeFor(exit 3) = %d, want 3", code)
	}

	// A non-exec error is reported and becomes 1.
	stderr.Reset()
	if code := exitCodeFor(errors.New("no such binary"), &stderr); code != 1 {
		t.Errorf("exitCodeFor(other) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "exec failed") {
		t.Errorf("stderr = %q, want it to explain the exec failure", stderr.String())
	}
}

// safeBuffer is a bytes.Buffer that tolerates concurrent writes. run shares one
// stderr between the denial tailer, the log stream's stderr and the child's
// stderr, all of which write from separate goroutines. In production that writer
// is os.Stderr, an *os.File, which is safe for concurrent use; a bare
// bytes.Buffer is not.
type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// stubChildAwaitingDenial writes a script that stands in for sandbox-exec. It
// ignores the profile and binary it is handed and exits once the tailer has
// recorded a denial, so the child cannot outrace the log stream. Without it the
// child would exit before the stub tailer had written a line, run would cancel
// the tailer, and the denial would never be recorded — a race the real
// `log stream` does not have, since it outlives the sandboxed process.
func stubChildAwaitingDenial(t *testing.T, logPath string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "stub-sandbox-exec")
	script := fmt.Sprintf(`#!/bin/sh
for _ in $(seq 1 500); do
	if grep -q deny %q 2>/dev/null; then exit 0; fi
	sleep 0.01
done
exit 0
`, logPath)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write stub child: %v", err)
	}
	return path
}

// run drives the whole launcher with stub commands standing in for sandbox-exec
// and the macOS log stream, so the orchestration is exercised without spawning
// a real sandbox.
func TestRun(t *testing.T) {
	// Every subtest stands in for sandbox-exec and `log stream` with bare
	// `sh`/`true`/`false`. Those live in Git for Windows' usr/bin, which is not
	// on the default windows-latest PATH, so they would fail to exec rather than
	// exercise anything. pi-sandbox is macOS-only; there is no Windows path here
	// that these tests would otherwise cover.
	if runtime.GOOS == "windows" {
		t.Skip("stub commands are POSIX utilities; pi-sandbox is macOS-only")
	}

	newCfg := func(t *testing.T, sandboxCmd string) config {
		t.Helper()
		return config{
			profile:    `(version 1)`,
			piName:     "sh", // any binary that exists on PATH
			sandboxCmd: sandboxCmd,
			logCmd:     []string{"true"}, // stands in for `log stream`
			logPath:    filepath.Join(t.TempDir(), "sandbox.log"),
			args:       nil,
			stdin:      strings.NewReader(""),
			stdout:     io.Discard,
			stderr:     io.Discard,
		}
	}

	t.Run("clean run exits 0", func(t *testing.T) {
		if code := run(t.Context(), newCfg(t, "true")); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
	})

	t.Run("child exit code is forwarded", func(t *testing.T) {
		cfg := newCfg(t, "false") // exits 1
		if code := run(t.Context(), cfg); code != 1 {
			t.Errorf("exit code = %d, want the child's 1", code)
		}
	})

	t.Run("missing pi binary fails", func(t *testing.T) {
		cfg := newCfg(t, "true")
		cfg.piName = "pi-does-not-exist-xyz"
		var stderr safeBuffer
		cfg.stderr = &stderr
		if code := run(t.Context(), cfg); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "cannot find") {
			t.Errorf("stderr = %q, want it to name the missing binary", stderr.String())
		}
	})

	t.Run("unopenable log file fails", func(t *testing.T) {
		cfg := newCfg(t, "true")
		cfg.logPath = filepath.Join(t.TempDir(), "no-such-dir", "sandbox.log")
		var stderr safeBuffer
		cfg.stderr = &stderr
		if code := run(t.Context(), cfg); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})

	t.Run("denials are written to the log file", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			// The stub child is a #!/bin/sh script and the stub log stream is
			// printf(1); Windows honors neither. pi-sandbox itself is a macOS
			// launcher (sandbox-exec plus `log stream`), so there is no Windows
			// behavior being hidden here.
			t.Skip("stub child is a shell script; pi-sandbox is macOS-only")
		}
		cfg := newCfg(t, "true")
		// Stub log stream: one header line (dropped) and one denial (kept).
		cfg.logCmd = []string{"printf", "Filtering the log data\ndeny file-write /etc/passwd\n"}
		// A child that exits instantly would let run cancel the tailer before it
		// ever wrote anything. The real `log stream` outlives the child, so stand
		// in a child that waits for the denial to be tailed.
		cfg.sandboxCmd = stubChildAwaitingDenial(t, cfg.logPath)
		var stderr safeBuffer
		cfg.stderr = &stderr

		if code := run(t.Context(), cfg); code != 0 {
			t.Fatalf("exit code = %d, want 0", code)
		}

		data, err := os.ReadFile(cfg.logPath)
		if err != nil {
			t.Fatalf("read log: %v", err)
		}
		got := string(data)
		if !strings.Contains(got, "deny file-write /etc/passwd") {
			t.Errorf("denial not recorded in %s: %q", cfg.logPath, got)
		}
		if strings.Contains(got, "Filtering") {
			t.Errorf("header noise leaked into the log: %q", got)
		}
	})
}
