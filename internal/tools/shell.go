package tools

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
)

// The shell the bash tool drives. Bash everywhere, except a Windows machine
// with no bash on PATH, where Windows PowerShell is the only thing left to run
// commands with (see specs/defect/windows-tools.md).
const (
	shellKindBash       = "bash"
	shellKindPowerShell = "powershell"
)

// resolveShellKind inspects the machine. It is separate from the cached
// accessor so a test can call it without deciding the process-wide answer.
func resolveShellKind() string {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("bash"); err != nil {
			return shellKindPowerShell
		}
	}
	return shellKindBash
}

var currentShellKind = sync.OnceValue(resolveShellKind)

// CurrentShellKind reports the resolved shell kind, computing it once.
// shellKindPowerShell means the bash tool executes through Windows PowerShell
// and bash-specific syntax is unavailable; callers can use it to skip
// registering bash-only tools or to adjust tool descriptions for the model.
func CurrentShellKind() string { return currentShellKind() }

// shellCommand builds the exec command that runs script in this machine's shell.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	return buildShellCommand(ctx, CurrentShellKind(), script)
}

// psExitEpilogue makes powershell.exe report failure the way bash does.
//
// Two separate things can fail and only one of them sets an exit code.
// `$LASTEXITCODE` is written by native executables and is $null until one
// runs, so a script of pure cmdlets leaves it unset. `$?` covers both, but a
// non-terminating cmdlet error — the ordinary kind, `Get-ChildItem` on a
// missing path — sets `$?` false while leaving `$LASTEXITCODE` untouched.
// Reading either one alone reports a failed command as a success: without this
// a failing `go test` (native, exit 1) or a failing `Test-Path` chain (cmdlet,
// no exit code) both come back green and the agent believes them.
//
// Both are captured on the first line, before any statement here can overwrite
// them — `$?` in particular reflects only the immediately preceding statement.
//
// `$?` decides, and `$LASTEXITCODE` only supplies the number. That order
// matters: a cmdlet does not reset `$LASTEXITCODE`, so after
// `cmd /c exit 3; Write-Output recovered` it still reads 3 while `$?` is true.
// Consulting the code first would report 3 for a script whose last statement
// succeeded, where bash reports 0 — `(exit 3); echo recovered` is a success.
const psExitEpilogue = `
$__piOK = $?; $__piCode = $LASTEXITCODE
if ($__piOK) { exit 0 }
if ($__piCode) { exit $__piCode }
exit 1`

// buildShellCommand is shellCommand with the shell named explicitly, so the
// PowerShell wrapper can be exercised from any platform.
func buildShellCommand(ctx context.Context, kind, script string) *exec.Cmd {
	if kind == shellKindPowerShell {
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script+psExitEpilogue)
	}
	return exec.CommandContext(ctx, "bash", "-c", script)
}
