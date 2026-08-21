package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/otel"
)

const (
	// defaultBashTimeout caps how long a command holds the foreground before it
	// is handed to the supervisor. It is a budget for the *turn*, not for the
	// command: nothing is killed at the threshold, the command keeps running,
	// and the caller gets a handle to watch it with.
	//
	// One minute is chosen so the common case stays whole — the vast majority
	// of commands in this repo (git, go vet, a package test run, a warm build)
	// finish well inside it — while anything genuinely long says so a minute in
	// rather than running the turn out. It is also minBashTimeout: a caller
	// that passes nothing and one that passes the smallest accepted value get
	// the same budget, which is the only defensible relationship between a
	// default and a floor. A caller that knows its command is long should raise
	// `timeout` rather than discover this limit; the idle check then does the
	// real work, firing well before the raised hard limit.
	defaultBashTimeout = time.Minute

	// minBashTimeout is the floor under both caller-supplied limits.
	//
	// It exists because `timeout` and `idle_timeout` are milliseconds, which is
	// invisible at the call site: a caller writing `timeout: 300` means five
	// minutes and gets 300ms. Nothing that small can succeed — `make` has not
	// printed its first line by then — so the command is handed off before it
	// has done anything, and the caller is left polling a handle to recover
	// work that would have finished in the foreground.
	//
	// Flooring at a minute costs a caller that genuinely wanted a sub-second
	// budget nothing it can use, since a handoff is not a kill: the command
	// keeps running either way. What it buys is that every command shorter than
	// a minute now completes in the foreground, whatever units the caller
	// thought it was passing.
	//
	// Subagent frontmatter had the same bug and took the same view — see
	// minAgentTimeoutMs in internal/subagent/agents.go, where `timeout: 30`
	// meant thirty seconds and SIGKILLed the agent 30ms in. That one ignores
	// the value and falls back to the default; this one floors, because a
	// handoff is recoverable where a kill is not, and the floor and the default
	// are the same minute anyway.
	minBashTimeout = time.Minute
	maxBashTimeout = 10 * time.Minute
)

// BashInput defines the parameters for the bash tool.
type BashInput struct {
	// The shell command to execute.
	Command string `json:"command"`
	// Optional timeout in MILLISECONDS. Default: 60000 (1 minute).
	// Min: 60000 (1 minute). Max: 600000 (10 minutes).
	// On expiry the command is moved to the background, not killed. Raise it for
	// work you already know is long (a full test suite, an image build) so it
	// finishes in the foreground instead of costing a round trip. Values below
	// the minute floor are raised to it, because they are nearly always seconds
	// written where milliseconds were expected.
	Timeout int `json:"timeout,omitempty"`
	// Optional idle timeout in MILLISECONDS. A command that produces no output
	// at all for this long is moved to the background. Default: 90000.
	// Min: 60000 (1 minute). Set it higher for commands that are legitimately
	// quiet for a long time.
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
	// Handle identifies a still-running command for bash_wait and bash_kill.
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

// BashWaitInput asks for output accumulated by a backgrounded command.
type BashWaitInput struct {
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

const maxBashWait = 60 * time.Second

const bashDescription = `Execute a shell command and return its output. Commands run in a bash shell. Use for system operations, running tests, building code, git operations, etc.

A command that runs past its timeout (1m by default), or that produces no output at all for 90s, is not killed: it keeps running in the background and the result carries running=true and a handle. Pass a larger timeout for work you already know is long — a full test suite, an image build — rather than letting the default hand it off. Use bash_wait to collect more of its output and bash_kill to stop it. A handle with no output at all usually means the command is far too broad — narrow it or kill it rather than waiting on it.

timeout and idle_timeout are in MILLISECONDS and are floored at 60000 (1 minute): a command that takes less than a minute always finishes in the foreground. Write 300000 for five minutes, not 300.`

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
		timeout:     clampDuration(input.Timeout, defaultBashTimeout, minBashTimeout, maxBashTimeout),
		idleTimeout: clampDuration(input.IdleTimeout, 0, minBashTimeout, maxBashTimeout),
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

	if n := flooredNote(input); n != "" {
		out.Note = strings.TrimSpace(out.Note + " " + n)
	}
	return out, nil
}

// flooredNote reports a limit that was raised to minBashTimeout.
//
// Silently correcting the value would fix this call and leave the caller
// repeating the mistake on the next one, so the note names the unit and gives
// the number that would have meant what the caller wrote. It is deliberately
// emitted whether or not the command was backgrounded: a caller that wrote
// `timeout: 300` and got a clean result still asked for something it did not
// get, and that is the cheapest moment to say so.
func flooredNote(input BashInput) string {
	var raised []string
	var suggested bool
	for _, f := range []struct {
		name string
		ms   int
	}{{"timeout", input.Timeout}, {"idle_timeout", input.IdleTimeout}} {
		if f.ms <= 0 || time.Duration(f.ms)*time.Millisecond >= minBashTimeout {
			continue
		}
		desc, ok := describeFloored(f.name, f.ms)
		raised = append(raised, desc)
		suggested = suggested || ok
	}
	if len(raised) == 0 {
		return ""
	}
	note := fmt.Sprintf("Raised to the %s floor: %s. These limits are in"+
		" milliseconds.", minBashTimeout, strings.Join(raised, ", "))
	if suggested {
		note += " A value this far under the floor is nearly always seconds" +
			" written where milliseconds were expected."
	}
	return note
}

// describeFloored renders one raised limit, and — where the number reads as a
// plausible seconds value — what to write instead.
//
// The suggestion is withheld once the seconds reading exceeds maxBashTimeout,
// because past that point it stops being a diagnosis and starts being noise:
// `idle_timeout: 5000` is a deliberate five seconds, and answering it with
// "write 5000000 for 1h23m20s" is worse than saying nothing.
func describeFloored(field string, ms int) (desc string, suggested bool) {
	asked := time.Duration(ms) * time.Millisecond
	if meant := time.Duration(ms) * time.Second; meant <= maxBashTimeout {
		return fmt.Sprintf("%s=%s (write %d for %s)", field, asked, ms*1000, meant), true
	}
	return fmt.Sprintf("%s=%s", field, asked), false
}

// clampDuration converts a millisecond input to a duration, substituting
// fallback when unset and holding the result within [minDur, maxDur].
//
// The floor applies only to a value the caller actually supplied. An unset
// input takes fallback unchanged, so a caller that passes nothing gets the
// default that was chosen for it rather than one bent by a floor meant to
// catch a unit mistake. Pass minDur = 0 where no floor applies.
func clampDuration(ms int, fallback, minDur, maxDur time.Duration) time.Duration {
	if ms <= 0 {
		return fallback
	}
	d := time.Duration(ms) * time.Millisecond
	if d < minDur {
		return minDur
	}
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
	waitTool, err := newTool("bash_wait",
		"Wait on a backgrounded shell command and return whatever it produced since the last wait. Blocks up to 60 seconds for new output or for the command to exit; use wait_ms for a shorter wait. Returns running=false and the exit code once it finishes, after which the handle is spent. Wait once with a generous wait_ms rather than calling this in a loop — each call is a round trip, and an empty result means nothing new since the last one, not that the command is stuck.",
		func(_ agent.Context, input BashWaitInput) (BashStatus, error) {
			if input.Handle == "" {
				return BashStatus{}, fmt.Errorf("handle is required (running: %v)", sup.Handles())
			}
			return sup.readOutput(input.Handle, clampDuration(input.WaitMs, maxBashWait, 0, maxBashWait))
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

	return []tool.Tool{waitTool, killTool}, nil
}
