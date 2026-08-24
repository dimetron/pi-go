package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// A machine that has bash gets bash. The Windows-without-bash branch is the
// one that cannot be reached from here, which is why buildShellCommand takes
// the kind as an argument rather than reading it back from the machine.
func TestResolveShellKind_UsesBashWhereItExists(t *testing.T) {
	got := resolveShellKind()
	if runtime.GOOS != "windows" && got != shellKindBash {
		t.Errorf("resolveShellKind() = %q on %s, want %q", got, runtime.GOOS, shellKindBash)
	}
	if got != shellKindBash && got != shellKindPowerShell {
		t.Errorf("resolveShellKind() = %q, want one of the two known kinds", got)
	}
	// The cached accessor must agree with a fresh resolution.
	if CurrentShellKind() != got {
		t.Errorf("CurrentShellKind() = %q, resolveShellKind() = %q", CurrentShellKind(), got)
	}
}

func TestBuildShellCommand_Bash(t *testing.T) {
	cmd := buildShellCommand(context.Background(), shellKindBash, "echo hi")

	if !strings.HasSuffix(cmd.Path, "bash") {
		t.Errorf("Path = %q, want it to end in bash", cmd.Path)
	}
	want := []string{"-c", "echo hi"}
	if got := cmd.Args[1:]; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("args = %v, want %v", got, want)
	}
}

// The exit-code suffix is the whole reason the PowerShell path is not just a
// different argv: powershell.exe exits 0 for a pipeline whose native commands
// failed, so without it a failing `go test` reports success to the agent.
func TestBuildShellCommand_PowerShellPropagatesExitCode(t *testing.T) {
	cmd := buildShellCommand(context.Background(), shellKindPowerShell, "go test ./...")

	if !strings.Contains(cmd.Path, "powershell") {
		t.Errorf("Path = %q, want powershell.exe", cmd.Path)
	}
	args := cmd.Args[1:]
	if len(args) != 3 || args[0] != "-NoProfile" || args[1] != "-Command" {
		t.Fatalf("args = %v, want -NoProfile -Command <script>", args)
	}
	script := args[2]
	if !strings.HasPrefix(script, "go test ./...") {
		t.Errorf("script = %q, want the caller's command first", script)
	}
	if !strings.Contains(script, "exit $LASTEXITCODE") {
		t.Errorf("script = %q, want the exit-code propagation suffix", script)
	}
}

// withShellKind pins the process-wide shell kind for one test. It is not safe
// in a parallel test: the kind is a package-level cache that every command the
// package builds reads, so a test that swaps it must be the only one running.
func withShellKind(t *testing.T, kind string) {
	t.Helper()
	prev := currentShellKind
	currentShellKind = func() string { return kind }
	t.Cleanup(func() { currentShellKind = prev })
}

// shellCommand is the one place the supervisor turns a script into a process,
// so it is where the Windows fallback either takes effect or quietly does not:
// a hardcoded "bash" here would leave resolveShellKind reporting the right
// answer while every command on the machine still died with "bash not found".
func TestShellCommand_FollowsTheResolvedShell(t *testing.T) {
	withShellKind(t, shellKindPowerShell)
	cmd := shellCommand(context.Background(), "echo hi")
	if !strings.Contains(cmd.Path, "powershell") {
		t.Errorf("Path = %q on a PowerShell machine, want powershell.exe", cmd.Path)
	}
	if !strings.Contains(strings.Join(cmd.Args, " "), "exit $LASTEXITCODE") {
		t.Errorf("args = %v, want the exit-code propagation suffix", cmd.Args)
	}

	withShellKind(t, shellKindBash)
	cmd = shellCommand(context.Background(), "echo hi")
	if !strings.HasSuffix(cmd.Path, "bash") {
		t.Errorf("Path = %q on a bash machine, want bash", cmd.Path)
	}
}
