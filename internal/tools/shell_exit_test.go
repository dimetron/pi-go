package tools

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

// anyNonZero asks for a failure without pinning its number. `ls` on a missing
// path is 1 on GNU and 2 on some others, and the PowerShell side has no
// convention at all — what matters is that a failure is not reported as
// success.
const anyNonZero = -1

// shellExitCase pairs equivalent bash and PowerShell scripts with the exit code
// both shells are expected to produce.
//
// Writing it as one table is the point. The PowerShell epilogue exists to make
// powershell.exe report failure the way bash does, and a table that runs the
// bash script on Unix and the PowerShell script on Windows states that as a
// single expectation rather than two lists of numbers that can drift apart.
type shellExitCase struct {
	name string
	bash string
	ps   string
	want int
}

var shellExitCases = []shellExitCase{
	{
		name: "success with output",
		bash: `echo hello`,
		ps:   `Write-Output hello`,
		want: 0,
	},
	{
		// The original defect: powershell.exe exits 0 for a pipeline whose
		// native commands failed, so a failing `go test` looked green.
		name: "native command exit code propagates",
		bash: `exit 3`,
		ps:   `cmd /c exit 3`,
		want: 3,
	},
	{
		name: "native command success",
		bash: `true`,
		ps:   `cmd /c exit 0`,
		want: 0,
	},
	{
		// A non-terminating cmdlet error sets $? but leaves $LASTEXITCODE
		// untouched, so an epilogue reading only $LASTEXITCODE reports this
		// as a success.
		name: "builtin failure with no exit code",
		bash: `ls /definitely-does-not-exist-pi-go`,
		ps:   `Get-ChildItem C:\definitely-does-not-exist-pi-go`,
		want: anyNonZero,
	},
	{
		// The nastier shape of the same bug: a native command succeeds first,
		// leaving $LASTEXITCODE at 0, and the cmdlet that fails after it
		// cannot overwrite that 0.
		name: "builtin failure after native success",
		bash: `true; ls /definitely-does-not-exist-pi-go`,
		ps:   `cmd /c exit 0; Get-ChildItem C:\definitely-does-not-exist-pi-go`,
		want: anyNonZero,
	},
	{
		// The regression the epilogue could plausibly introduce, and the
		// reason it cannot lean on $? alone. Windows PowerShell has a
		// reputation for treating a native command's stderr output as
		// failure; if that happened here, every successful `git clone` --
		// which reports progress on stderr -- would come back as an error.
		// That would be worse than the bug being fixed, so it is pinned.
		name: "stderr output does not imply failure",
		bash: `echo oops 1>&2; true`,
		ps:   `cmd /c "echo oops 1>&2 & exit 0"`,
		want: 0,
	},
	{
		// A command the machine does not have -- a missing `git` or `go`, or a
		// typo -- is the shape a user actually hits, and the old wrapper
		// reported it as success with the error text only on stderr. bash
		// answers 127; the number is not the point, being non-zero is.
		name: "command not found",
		bash: `this-command-does-not-exist-pi-go`,
		ps:   `This-Command-Does-Not-Exist-PiGo`,
		want: anyNonZero,
	},
	{
		// Bash reports the LAST command, so a failure followed by a success is
		// a success. Worth stating outright: it looks like a missed failure,
		// but matching bash is the whole contract, and a wrapper that reported
		// non-zero here would be the one that is wrong.
		name: "failure followed by success is success",
		bash: `ls /definitely-does-not-exist-pi-go 2>/dev/null; true`,
		ps:   `Get-ChildItem C:\definitely-does-not-exist-pi-go 2>$null; cmd /c exit 0`,
		want: 0,
	},
	{
		// A cmdlet does not reset $LASTEXITCODE, so a failing native command
		// leaves its code sitting there while a later statement succeeds.
		// bash reports the last command, so this is a success -- reading the
		// stale code instead of $? would report 3 for a script that worked.
		name: "native failure recovered by a later success",
		bash: `(exit 3); echo recovered`,
		ps:   `cmd /c exit 3; Write-Output recovered`,
		want: 0,
	},
	{
		// A script that exits on its own must win: the epilogue is appended
		// after it and must never run.
		name: "explicit exit wins",
		bash: `exit 7`,
		ps:   `exit 7`,
		want: 7,
	},
	{
		// The epilogue is appended verbatim, so a script ending in a comment
		// would swallow it if it were not separated by a newline.
		name: "trailing comment does not swallow the epilogue",
		bash: "echo hi\n# trailing comment",
		ps:   "Write-Output hi\n# trailing comment",
		want: 0,
	},
}

// TestShellCommand_ExitCodes runs each case through the real shell.
//
// On Windows this is the only place the PowerShell epilogue is executed rather
// than string-matched, and it is why buildShellCommand takes the shell kind as
// an argument: Windows CI has a git-bash on PATH, so CurrentShellKind() there
// reports bash and the PowerShell path would otherwise never be exercised.
func TestShellCommand_ExitCodes(t *testing.T) {
	kind := shellKindBash
	if runtime.GOOS == "windows" {
		kind = shellKindPowerShell
	}

	for _, tc := range shellExitCases {
		t.Run(tc.name, func(t *testing.T) {
			script := tc.bash
			if kind == shellKindPowerShell {
				script = tc.ps
			}

			cmd := buildShellCommand(context.Background(), kind, script)
			out, err := cmd.CombinedOutput()

			var exitErr *exec.ExitError
			if err != nil && !errors.As(err, &exitErr) {
				t.Fatalf("running %q in %s: %v", script, kind, err)
			}
			got := cmd.ProcessState.ExitCode()

			switch {
			case tc.want == anyNonZero && got == 0:
				t.Errorf("%s: %q exited 0, want a failure\noutput: %s", kind, script, out)
			case tc.want != anyNonZero && got != tc.want:
				t.Errorf("%s: %q exited %d, want %d\noutput: %s", kind, script, got, tc.want, out)
			}
		})
	}
}
