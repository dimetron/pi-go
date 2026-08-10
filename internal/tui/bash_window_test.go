package tui

import (
	"strings"
	"testing"
)

// A backgrounded command has no exit status, and the -1 the bash tool reports
// in its place is a placeholder, not a failure. Rendering it through the
// exit-code path printed "exit -1: golangci-lint run ./..." for a lint run that
// was alive and would go on to pass — the card accused the command of crashing
// and named make's echoed recipe line as the evidence.
func TestFormatToolResult_BackgroundedCommandIsNotAFailure(t *testing.T) {
	data := map[string]any{
		"exit_code":    float64(-1),
		"running":      true,
		"handle":       "bg_16",
		"stdout":       "golangci-lint run ./...\n",
		"elapsed":      "5s",
		"idle":         "5s",
		"timeout":      "2m0s",
		"idle_timeout": "1s",
		"note":         "Command no output for 5s and was moved to the background",
	}

	got := formatToolResult(data)

	if strings.Contains(got, "exit -1") {
		t.Errorf("a running command must not be reported as an exit status, got %q", got)
	}
	for _, want := range []string{"⏳", "5s elapsed", "golangci-lint run ./..."} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

// The limits belong on the card because they are the actionable half of the
// story: "backgrounded" says the command went quiet, "idle_timeout 1s" says the
// threshold it crossed was one the caller chose.
func TestFormatToolResult_RunningCommandShowsItsLimits(t *testing.T) {
	data := map[string]any{
		"running":      true,
		"handle":       "bg_16",
		"stdout":       "",
		"timeout":      "2m0s",
		"idle_timeout": "1s",
	}

	got := formatToolResult(data)

	if !strings.Contains(got, "idle_timeout 1s") {
		t.Errorf("expected the idle limit in %q", got)
	}
	if !strings.Contains(got, "timeout 2m0s") {
		t.Errorf("expected the hard limit in %q", got)
	}
}

// A poll of a live command carries no exit_code at all — BashStatus omits it
// while running — so it used to miss the bash branch entirely and land in the
// raw-JSON fallback, which printed &-escaped argument soup and truncated it
// mid-token. Fifty-five of those in a row said nothing about the command.
func TestFormatToolResult_PollOfRunningCommandIsReadable(t *testing.T) {
	data := map[string]any{
		"handle":       "bg_16",
		"command":      "make lint && make vet",
		"running":      true,
		"stdout":       "",
		"stderr":       "",
		"elapsed":      "1m30s",
		"idle":         "1m25s",
		"timeout":      "2m0s",
		"idle_timeout": "1s",
	}

	got := formatToolResult(data)

	if strings.Contains(got, `&`) || strings.HasPrefix(got, "{") {
		t.Errorf("poll rendered as raw JSON: %q", got)
	}
	if !strings.Contains(got, "no new output") {
		t.Errorf("a silent poll must say so, got %q", got)
	}
	if !strings.Contains(got, "1m30s elapsed") {
		t.Errorf("expected elapsed time in %q", got)
	}
}

func TestFormatToolResult_FinishedBackgroundCommand(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want string
	}{
		{
			name: "clean exit",
			data: map[string]any{
				"handle": "bg_3", "running": false,
				"stdout": "0 issues.", "elapsed": "12s",
			},
			want: "exit 0 (bg_3), 12s elapsed",
		},
		{
			name: "failure",
			data: map[string]any{
				"handle": "bg_3", "running": false,
				"exit_code": float64(2), "stdout": "boom",
			},
			want: "exit 2 (bg_3)",
		},
		{
			// -1 on a stopped command means killed, or killed and not reaped
			// before the wait delay expired. There is no status to report, and
			// "exit -1" invites a hunt for a code that never existed.
			name: "killed without a status",
			data: map[string]any{
				"handle": "bg_3", "running": false,
				"exit_code": float64(-1), "stdout": "",
			},
			want: "killed, no exit status (bg_3)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolResult(tt.data)
			if !strings.HasPrefix(got, tt.want) {
				t.Errorf("formatToolResult() = %q, want prefix %q", got, tt.want)
			}
		})
	}
}

// A finished command reports its status, not the limits it never hit.
func TestFormatToolResult_FinishedCommandOmitsLimits(t *testing.T) {
	data := map[string]any{
		"handle": "bg_3", "running": false,
		"stdout": "done", "timeout": "2m0s", "idle_timeout": "90s",
	}

	if got := formatToolResult(data); strings.Contains(got, "limits:") {
		t.Errorf("finished command should not carry a limits hint, got %q", got)
	}
}

// The foreground path is untouched: no handle, no window.
func TestFormatToolResult_ForegroundBashUnchanged(t *testing.T) {
	data := map[string]any{"exit_code": float64(1), "stdout": "build failed"}

	got := formatToolResult(data)

	if got != "exit 1: build failed" {
		t.Errorf("formatToolResult() = %q, want %q", got, "exit 1: build failed")
	}
}

func TestBashOutputPreview(t *testing.T) {
	tests := []struct {
		name           string
		stdout, stderr string
		want           string
	}{
		{name: "empty", want: ""},
		{name: "falls back to stderr", stderr: "boom", want: "boom"},
		{name: "prefers stdout", stdout: "out", stderr: "err", want: "out"},
		{name: "short output is whole", stdout: "a\nb\nc\n", want: "a\nb\nc"},
		{name: "long output is head and tail", stdout: "1\n2\n3\n4\n5\n6", want: "1\n2\n5\n6"},
		{
			name:   "long lines are clipped",
			stdout: strings.Repeat("x", 100),
			want:   strings.Repeat("x", 77) + "...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bashOutputPreview(tt.stdout, tt.stderr); got != tt.want {
				t.Errorf("bashOutputPreview() = %q, want %q", got, tt.want)
			}
		})
	}
}
