package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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

// run drives the whole launcher with stub commands standing in for sandbox-exec
// and the macOS log stream, so the orchestration is exercised without spawning
// a real sandbox.
func TestRun(t *testing.T) {
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
		var stderr bytes.Buffer
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
		var stderr bytes.Buffer
		cfg.stderr = &stderr
		if code := run(t.Context(), cfg); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	})

	t.Run("denials are written to the log file", func(t *testing.T) {
		cfg := newCfg(t, "true")
		// Stub log stream: one header line (dropped) and one denial (kept).
		cfg.logCmd = []string{"printf", "Filtering the log data\ndeny file-write /etc/passwd\n"}
		var stderr bytes.Buffer
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
