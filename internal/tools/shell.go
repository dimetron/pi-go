package tools

import (
	"context"
	"os/exec"
	"runtime"
	"sync"
)

// shellKind names the shell the execute tool drives: "bash" everywhere,
// except Windows without a bash on PATH, where Windows PowerShell is used.
// It is a var so tests can stub the lookup.
var shellKind = func() string {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("bash"); err != nil {
			return "powershell"
		}
	}
	return "bash"
}

var (
	shellOnce   sync.Once
	shellCached string
)

// CurrentShellKind reports the resolved shell kind, computing it once.
// "powershell" means the bash tool executes through Windows PowerShell and
// bash-specific syntax is unavailable; callers can use it to skip registering
// bash-only tools or to adjust tool descriptions for the model.
func CurrentShellKind() string {
	shellOnce.Do(func() { shellCached = shellKind() })
	return shellCached
}

// shellCommand builds the exec command that runs script in the platform shell:
// `bash -c` normally, or `powershell -NoProfile -Command` on Windows when no
// bash is available (see specs/defect/windows-tools.md).
//
// The PowerShell wrapper propagates the last native command's exit code:
// powershell.exe otherwise exits 0 for a pipeline whose native commands
// failed, so a failing `go test` would look successful to the agent.
func shellCommand(ctx context.Context, script string) *exec.Cmd {
	if CurrentShellKind() == "powershell" {
		wrapped := script + "\n; exit $LASTEXITCODE"
		return exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-Command", wrapped)
	}
	return exec.CommandContext(ctx, "bash", "-c", script)
}
