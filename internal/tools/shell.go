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

// buildShellCommand is shellCommand with the shell named explicitly, so the
// PowerShell wrapper can be exercised from any platform.
//
// The PowerShell wrapper propagates the last native command's exit code:
// powershell.exe otherwise exits 0 for a pipeline whose native commands
// failed, so a failing `go test` would look successful to the agent.
func buildShellCommand(ctx context.Context, kind, script string) *exec.Cmd {
	if kind == shellKindPowerShell {
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", script+"\n; exit $LASTEXITCODE")
	}
	return exec.CommandContext(ctx, "bash", "-c", script)
}
