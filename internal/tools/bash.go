package tools

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/otel"
)

const (
	defaultBashTimeout = 120 * time.Second
	maxBashTimeout     = 10 * time.Minute
)

// BashInput defines the parameters for the bash tool.
type BashInput struct {
	// The shell command to execute.
	Command string `json:"command"`
	// Optional timeout in milliseconds. Default: 120000 (2 minutes). Max: 600000 (10 minutes).
	// On expiry the command is moved to the background, not killed.
	Timeout int `json:"timeout,omitempty"`
	// Optional idle timeout in milliseconds. A command that produces no output
	// at all for this long is moved to the background. Default: 90000.
	// Set it higher for commands that are legitimately quiet for a long time.
	IdleTimeout int `json:"idle_timeout,omitempty"`
}

// BashOutput contains the result of executing a shell command.
type BashOutput struct {
	// Standard output from the command.
	Stdout string `json:"stdout"`
	// Standard error from the command.
	Stderr string `json:"stderr"`
	// Exit code of the command. -1 when the command has not exited.
	ExitCode int `json:"exit_code"`
	// Running is true when the command outlived its timeout and is still
	// executing in the background.
	Running bool `json:"running,omitempty"`
	// Handle identifies a still-running command for bash_output and bash_kill.
	Handle string `json:"handle,omitempty"`
	// Elapsed is how long the command has been running in total.
	Elapsed string `json:"elapsed,omitempty"`
	// Idle is how long it has been since the command last produced output.
	Idle string `json:"idle,omitempty"`
	// Timeout and IdleTimeout are the limits this command actually ran under,
	// after defaults and clamping. They are reported rather than left implicit
	// because a handoff is otherwise unexplainable from the result alone: the
	// caller sees "moved to the background" with no way to tell whether the
	// limit it hit was the 90s default or a 1s value it passed itself.
	Timeout     string `json:"timeout,omitempty"`
	IdleTimeout string `json:"idle_timeout,omitempty"`
	// Note explains a non-obvious outcome in terms the model can act on.
	Note string `json:"note,omitempty"`
}

// BashStatus is the result of inspecting or stopping a backgrounded command.
type BashStatus struct {
	Handle  string `json:"handle"`
	Command string `json:"command"`
	// Running reports whether the command is still executing.
	Running bool `json:"running"`
	// ExitCode is meaningful only once Running is false.
	ExitCode int `json:"exit_code,omitempty"`
	// Stdout and Stderr carry only what was produced since the previous read.
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	// Elapsed is how long the command has been running in total.
	Elapsed string `json:"elapsed,omitempty"`
	// Idle is how long it has been since the command last produced output.
	Idle string `json:"idle,omitempty"`
	// Timeout and IdleTimeout are the limits the command was started under, so
	// a poll can say why it was handed off without the caller having to
	// remember what it asked for several turns ago.
	Timeout     string `json:"timeout,omitempty"`
	IdleTimeout string `json:"idle_timeout,omitempty"`
	Note        string `json:"note,omitempty"`
}

// BashOutputInput asks for output accumulated by a backgrounded command.
type BashOutputInput struct {
	// Handle returned by a previous bash call.
	Handle string `json:"handle"`
	// WaitMs blocks up to this long for new output or for the command to exit,
	// Defaults to 60000. Prefer waiting over polling
	// in a loop.
	WaitMs int `json:"wait_ms,omitempty"`
}

// BashKillInput identifies a backgrounded command to stop.
type BashKillInput struct {
	// Handle returned by a previous bash call.
	Handle string `json:"handle"`
}

const maxBashOutputWait = 60 * time.Second

const bashDescription = `Execute a shell command and return its output. Commands run in a bash shell. Use for system operations, running tests, building code, git operations, etc.

A command that runs past its timeout, or that produces no output at all for 90s, is not killed: it keeps running in the background and the result carries running=true and a handle. Use bash_output to collect more of its output and bash_kill to stop it. A handle with no output at all usually means the command is far too broad — narrow it or kill it rather than waiting on it.`

func newBashTool(sb *Sandbox, sup *BashSupervisor) (tool.Tool, error) {
	return newTool("bash", bashDescription, func(ctx agent.Context, input BashInput) (BashOutput, error) {
		return bashHandler(sb, sup, ctx, input)
	})
}

func bashHandler(sb *Sandbox, sup *BashSupervisor, ctx agent.Context, input BashInput) (BashOutput, error) {
	if input.Command == "" {
		return BashOutput{}, fmt.Errorf("command is required")
	}

	// Use background context if agent.Context is nil (e.g. in unit tests)
	var parentCtx context.Context
	if ctx != nil {
		parentCtx = ctx
	} else {
		parentCtx = context.Background()
	}

	parentCtx, span := otel.Tracer("pi-go").Start(parentCtx, "tool.bash")
	span.SetAttributes(
		attribute.String("bash.command", input.Command),
		attribute.String("bash.cwd", sb.Dir()),
	)
	defer span.End()

	out, err := sup.Run(parentCtx, runRequest{
		dir:         sb.Dir(),
		command:     input.Command,
		timeout:     clampDuration(input.Timeout, defaultBashTimeout, maxBashTimeout),
		idleTimeout: clampDuration(input.IdleTimeout, 0, maxBashTimeout),
	})
	if err != nil {
		span.RecordError(err)
		return BashOutput{}, fmt.Errorf("executing command: %w", err)
	}

	span.SetAttributes(
		attribute.Int("bash.exit_code", out.ExitCode),
		attribute.Int("bash.stdout_len", len(out.Stdout)),
		attribute.Int("bash.stderr_len", len(out.Stderr)),
		attribute.Bool("bash.backgrounded", out.Running),
	)
	return out, nil
}

// clampDuration converts a millisecond input to a duration, substituting
// fallback when unset and capping at maxDur.
func clampDuration(ms int, fallback, maxDur time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	d := time.Duration(ms) * time.Millisecond
	if d > maxDur {
		return maxDur
	}
	return d
}

// BashControlTools returns the tools for inspecting and stopping commands that
// the bash tool moved to the background.
//
// They are separate from CoreTools because they are only worth advertising to
// the model when something can actually background a command — a caller that
// builds its own supervisor and never streams need not carry the extra schema.
func BashControlTools(sup *BashSupervisor) ([]tool.Tool, error) {
	outputTool, err := newTool("bash_output",
		"Read output produced by a backgrounded shell command since the last read. Blocks up to 60 seconds for new output or command exit; use wait_ms to choose a shorter wait. Returns running=false and the exit code once it finishes, after which the handle is spent.",
		func(_ agent.Context, input BashOutputInput) (BashStatus, error) {
			if input.Handle == "" {
				return BashStatus{}, fmt.Errorf("handle is required (running: %v)", sup.Handles())
			}
			return sup.readOutput(input.Handle, clampDuration(input.WaitMs, maxBashOutputWait, maxBashOutputWait))
		})
	if err != nil {
		return nil, err
	}

	killTool, err := newTool("bash_kill",
		"Stop a backgrounded shell command and every process it spawned. Returns any output produced since the last read.",
		func(_ agent.Context, input BashKillInput) (BashStatus, error) {
			if input.Handle == "" {
				return BashStatus{}, fmt.Errorf("handle is required (running: %v)", sup.Handles())
			}
			return sup.killHandle(input.Handle)
		})
	if err != nil {
		return nil, err
	}

	return []tool.Tool{outputTool, killTool}, nil
}
