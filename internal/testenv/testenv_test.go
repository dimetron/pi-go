package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSetHomeIsScopedToTheTest(t *testing.T) {
	before, beforeErr := os.UserHomeDir()

	dir := t.TempDir()
	t.Run("inside", func(t *testing.T) {
		SetHome(t, dir)
		got, err := os.UserHomeDir()
		if err != nil {
			t.Fatalf("UserHomeDir: %v", err)
		}
		if got != dir {
			t.Errorf("UserHomeDir = %q, want %q", got, dir)
		}
	})

	after, afterErr := os.UserHomeDir()
	if after != before || (afterErr == nil) != (beforeErr == nil) {
		t.Errorf("home after the subtest = %q (%v), want it restored to %q (%v)", after, afterErr, before, beforeErr)
	}
}

func TestSetUnwritableHomeBlocksCreationBelowIt(t *testing.T) {
	home := SetUnwritableHome(t)

	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("stat %s: %v", home, err)
	}
	if info.IsDir() {
		t.Fatalf("%s is a directory, want a regular file standing in for HOME", home)
	}
	if got, _ := os.UserHomeDir(); got != home {
		t.Errorf("UserHomeDir = %q, want %q", got, home)
	}
	if err := os.MkdirAll(filepath.Join(home, ".pi-go"), 0o755); err == nil {
		t.Error("MkdirAll below the file succeeded, want an error")
	}
}

func TestRequireShellReturnsARunnableShell(t *testing.T) {
	sh := RequireShell(t)
	if out, err := exec.Command(sh, "-c", "echo ok").Output(); err != nil || string(out) != "ok\n" {
		t.Errorf("%s -c 'echo ok' = %q, %v", sh, out, err)
	}
}

func TestRequireShellSkipsWithoutOne(t *testing.T) {
	// An empty PATH makes LookPath fail for bash and sh alike, so the helper
	// must skip rather than return a path that cannot run.
	t.Setenv("PATH", "")
	sh := RequireShell(t)
	t.Errorf("RequireShell returned %q with an empty PATH, want the test skipped", sh)
}

func TestUnsetHomeLeavesNothingToResolve(t *testing.T) {
	UnsetHome(t)
	if home, err := os.UserHomeDir(); err == nil {
		t.Errorf("UserHomeDir = %q, want an error with HOME unset", home)
	}
}

func TestFakeBinaryRuns(t *testing.T) {
	dir := t.TempDir()
	bin := FakeBinary(t, dir, "tool")
	if filepath.Dir(bin) != dir {
		t.Errorf("binary written to %q, want it under %q", bin, dir)
	}
	run := exec.Command(bin)
	if runtime.GOOS == "windows" {
		// A .bat is not a PE image; it runs through the command interpreter.
		run = exec.Command("cmd", "/c", bin)
	}
	if err := run.Run(); err != nil {
		t.Errorf("running %s: %v", bin, err)
	}
	// It must also be found by name through PATH, which is what callers that
	// put dir on PATH rely on.
	t.Setenv("PATH", dir)
	if _, err := exec.LookPath("tool"); err != nil {
		t.Errorf("LookPath(tool) with PATH=%s: %v", dir, err)
	}
}
