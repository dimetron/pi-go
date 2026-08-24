package tools

import (
	"context"
	"path/filepath"
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

// wantsBash reports whether exec resolved the command to a bash binary.
//
// It compares the base name with any executable suffix trimmed, because
// exec.Command stores the resolved path and Windows resolves "bash" to
// "C:\\Program Files\\Git\\bin\\bash.exe". A plain HasSuffix(path, "bash")
// passes on Unix and fails on exactly the platform this file exists to cover.
func wantsBash(t *testing.T, path string) {
	t.Helper()
	if got := strings.TrimSuffix(filepath.Base(path), ".exe"); got != "bash" {
		t.Errorf("Path = %q, want it to resolve to bash", path)
	}
}

// TestResolveShellKind_PowerShellWhenBashIsMissing covers the one line the
// whole branch exists for: the decision to fall back.
//
// Nothing else reaches it. shell_exit_test.go injects the shell kind rather
// than resolving it, precisely because Windows CI ships a git-bash on PATH --
// so on that runner LookPath succeeds and the PowerShell return is
// unreachable. Emptying PATH reproduces the actual defect condition (a Windows
// machine with no bash), and exec.LookPath re-reads PATH on every call, so no
// production seam is needed to get there.
func TestResolveShellKind_PowerShellWhenBashIsMissing(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the PowerShell fallback only exists on Windows")
	}
	t.Setenv("PATH", "")

	if got := resolveShellKind(); got != shellKindPowerShell {
		t.Errorf("resolveShellKind() with no bash on PATH = %q, want %q", got, shellKindPowerShell)
	}
}

func TestBuildShellCommand_Bash(t *testing.T) {
	cmd := buildShellCommand(context.Background(), shellKindBash, "echo hi")

	wantsBash(t, cmd.Path)
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
	if !strings.HasSuffix(script, psExitEpilogue) {
		t.Errorf("script = %q, want the exit-code epilogue appended", script)
	}
}

// epilogueStatements splits psExitEpilogue into its statements, in order.
//
// The epilogue is PowerShell, and PowerShell separates statements by newline
// or `;` — so a line-by-line reading would miss a second statement smuggled
// onto the capture line, which is exactly where an ordering mistake would go.
func epilogueStatements(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(psExitEpilogue, "\n") {
		// Only split the top level: `;` also appears inside the braces of the
		// failure branch, which is one statement as far as ordering goes.
		if strings.Contains(line, "{") {
			if s := strings.TrimSpace(line); s != "" {
				out = append(out, s)
			}
			continue
		}
		for _, stmt := range strings.Split(line, ";") {
			if s := strings.TrimSpace(stmt); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// The epilogue is appended to every PowerShell script the agent runs, and the
// Windows CI job is the only place it is ever executed — everywhere else this
// test is the only thing standing between a broken wrapper and a green build.
// So its shape is a contract, not formatting.
//
// What it has to get right: read BOTH failure signals, read `$?` before
// anything can clobber it, let a real exit code win where there is one, and
// still fail when `$?` is false with no exit code to report.
func TestPSExitEpilogue_ReadsBothFailureSignalsInOrder(t *testing.T) {
	// Appended verbatim, so without the leading newline a script ending in a
	// comment would swallow the epilogue whole and the exit code would go back
	// to being powershell's.
	if !strings.HasPrefix(psExitEpilogue, "\n") {
		t.Errorf("epilogue = %q, want it to start on its own line", psExitEpilogue)
	}

	stmts := epilogueStatements(t)
	if len(stmts) < 3 {
		t.Fatalf("epilogue = %q, parsed as %q; want a capture and both exit branches",
			psExitEpilogue, stmts)
	}

	// `$?` reflects only the immediately preceding statement, so the very
	// first statement of the epilogue has to read it. Anything inserted above
	// — even another assignment — makes the wrapper report the epilogue's own
	// success instead of the script's, and every failure comes back green.
	if !strings.Contains(stmts[0], "$?") {
		t.Errorf("first epilogue statement = %q, want it to capture $? before any"+
			" statement here can overwrite it", stmts[0])
	}
	for _, s := range stmts[1:] {
		if strings.Contains(s, "$?") {
			t.Errorf("epilogue reads $? again at %q; only the first statement still"+
				" sees the caller's script", s)
		}
	}

	// Both signals are captured before the first branch. Reading only
	// $LASTEXITCODE is the bug this exists to prevent: it is $null until a
	// native command runs, and a non-terminating cmdlet error leaves it
	// untouched, so a failing Get-ChildItem would report success.
	branchAt := len(stmts)
	for i, s := range stmts {
		if strings.HasPrefix(s, "if ") {
			branchAt = i
			break
		}
	}
	prologue := strings.Join(stmts[:branchAt], "\n")
	if !strings.Contains(prologue, "$LASTEXITCODE") {
		t.Errorf("epilogue prologue = %q, want both signals captured before the"+
			" first branch", prologue)
	}
	branches := stmts[branchAt:]
	if len(branches) == 0 {
		t.Fatalf("epilogue = %q, want at least one exit branch", psExitEpilogue)
	}
	joined := strings.Join(branches, "\n")
	if !strings.Contains(joined, "$__piOK") {
		t.Errorf("epilogue branches = %q, want the $? capture to decide the exit;"+
			" $LASTEXITCODE alone cannot see a cmdlet failure", joined)
	}

	// `$?` decides and `$LASTEXITCODE` only supplies the number, so the very
	// first branch has to be the success branch and it has to exit 0.
	//
	// Reversing these two is a real bug, not a style point: a cmdlet does not
	// reset $LASTEXITCODE, so after `cmd /c exit 3; Write-Output recovered` it
	// still reads 3 while $? is true. Consulting the code first reports 3 for
	// a script whose last statement succeeded, where bash reports 0.
	if !strings.Contains(branches[0], "$__piOK") {
		t.Errorf("first branch = %q, want $? to decide before the exit code is"+
			" consulted", branches[0])
	}
	if !strings.Contains(branches[0], "exit 0") {
		t.Errorf("first branch = %q, want a true $? to exit 0", branches[0])
	}
	// The code is only reachable once $? is false, and a failure with no code
	// of its own still has to be non-zero — the `Get-ChildItem C:\missing`
	// shape, where no native command ever ran to set one.
	rest := strings.Join(branches[1:], "\n")
	for _, want := range []string{"exit $__piCode", "exit 1"} {
		if !strings.Contains(rest, want) {
			t.Errorf("failure path = %q, want %q", rest, want)
		}
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
	if !strings.HasSuffix(strings.Join(cmd.Args, " "), psExitEpilogue) {
		t.Errorf("args = %v, want the exit-code epilogue appended", cmd.Args)
	}

	withShellKind(t, shellKindBash)
	cmd = shellCommand(context.Background(), "echo hi")
	wantsBash(t, cmd.Path)
}
