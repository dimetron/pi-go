package subagent

import (
	"context"
	"fmt"
	"os"
	"strings"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
	"github.com/dimetron/pi-go/internal/acp/client/claudecode"
	"github.com/dimetron/pi-go/internal/acp/client/cursor"
	"github.com/dimetron/pi-go/internal/acp/client/gemini"
)

// acpSession is the shared interface implemented by every per-runner
// RunningSession (claudecode, gemini, cursor). It lets dispatchACP treat
// them uniformly when pumping events back to the orchestrator.
type acpSession interface {
	Events() <-chan sharedacp.Event
	Done() <-chan struct{}
	Cancel() error
	Wait() sharedacp.RunResult
}

// startACPSessionFn is the constructor used by dispatchACP to start a
// runner. It is overridable in tests so the dispatcher can be exercised
// without invoking real claude/gemini/cursor binaries.
var startACPSessionFn = startACPSession

// acpCompletionSentinel is the exact string the preamble asks ACP subagents
// to emit when their task is done. It is what pumpACPSession strips from the
// final result text on a graceful completion.
const acpCompletionSentinel = "<Task Completed>!"

// acpCompletionMatcher is the loose form used to detect completion in
// streamed text. Models routinely drop the trailing "!" or elide the space
// between their response and the tag (observed with Gemini emitting
// "Gemini CLI<Task Completed>" and Claude emitting "claude <Task Completed>"
// without the "!"), so match on the bracketed core and accept either form.
const acpCompletionMatcher = "<Task Completed>"

// acpPromptPreamble wraps an ACP subagent's prompt with a role/termination
// header. ACP agents (claude/gemini/cursor) run untethered from pi's tool
// loop, so they need an explicit "<Task Completed>" sentinel the caller can
// look for to know the turn is finished — otherwise agents may keep talking.
func acpPromptPreamble(agentName, task string) string {
	return fmt.Sprintf("You are subagent[%s], %s when done reply %s", agentName, task, acpCompletionSentinel)
}

// dispatchACP starts an ACP-based subagent (claude, gemini, cursor) and
// returns a *Process whose event channel is fed by the runner's session,
// translated into the orchestrator's Event format.
//
// pi is not invoked for ACP agents — the matching CLI binary is launched
// directly via its ACP adapter.
func dispatchACP(ctx context.Context, opts SpawnOpts, agentName string) (*Process, error) {
	if opts.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	prompt := acpPromptPreamble(agentName, opts.Prompt)
	if strings.TrimSpace(opts.Instruction) != "" {
		prompt = opts.Instruction + "\n\n" + prompt
	}

	timeoutCfg := ResolveTimeout(opts.Timeout)
	procCtx, cancel := context.WithTimeout(ctx, timeoutCfg.Absolute)

	sess, err := startACPSessionFn(procCtx, agentName, prompt, opts)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("starting %s ACP session: %w", agentName, err)
	}

	proc := &Process{
		events: make(chan Event, 256), // Increased from 64 to handle high-throughput scenarios
		done:   make(chan struct{}),
		cancel: func() {
			_ = sess.Cancel()
			cancel()
		},
	}

	go pumpACPSession(sess, proc, agentName)
	return proc, nil
}

// startACPSession picks the runner for agentName and starts a session.
//
// Note: opts.Model is intentionally NOT forwarded to ACP agents. The
// orchestrator resolves Model from pi's role config (e.g. "claude-haiku-4-5"),
// which is meaningful only to the pi binary. Forwarding it to gemini --model
// produces a 500 "Requested entity was not found" because Gemini's model
// namespace is different ("gemini-2.5-flash", etc.); cursor treats --model as
// informational and routes per-task on its side. Each ACP agent's own default
// model is the safe choice — users who need a specific Gemini model should set
// it via the gemini CLI's own config rather than through pi roles.
func startACPSession(ctx context.Context, agentName, prompt string, opts SpawnOpts) (acpSession, error) {
	switch agentName {
	case "claude":
		r := claudecode.Runner{ExtraEnv: opts.Env}
		return r.Start(ctx, claudecode.RunRequest{
			Prompt: prompt,
			CWD:    opts.WorkDir,
			Env:    opts.Env,
		})
	case "gemini":
		r := gemini.Runner{ExtraEnv: opts.Env}
		return r.Start(ctx, gemini.RunRequest{
			Prompt: prompt,
			CWD:    opts.WorkDir,
			Env:    opts.Env,
		})
	case "cursor":
		r := cursor.Runner{ExtraEnv: opts.Env}
		return r.Start(ctx, cursor.RunRequest{
			Prompt: prompt,
			CWD:    opts.WorkDir,
			Env:    opts.Env,
		})
	default:
		return nil, fmt.Errorf("unknown ACP agent %q", agentName)
	}
}

// pumpACPSession translates events from an ACP session into orchestrator
// Events, captures the final result on proc, then closes proc.events and
// proc.done. The defer order is important: events must close before done
// so consumers ranging over Events() exit before Wait() returns.
func pumpACPSession(sess acpSession, proc *Process, agentName string) {
	defer close(proc.done)
	defer close(proc.events)

	sentStart := false
	var textAcc strings.Builder
	// gracefulCompletion is set when the agent emits the <Task Completed>!
	// sentinel. Once flipped, sess.Cancel() is invoked to tear down the
	// subprocess, and the resulting session error is coerced back to
	// StatusSuccess since the agent itself signaled it was done.
	gracefulCompletion := false
	for ev := range sess.Events() {
		if !sentStart && ev.SessionID != "" {
			sendProcEvent(proc, Event{Type: "message_start", SessionID: ev.SessionID})
			sentStart = true
		}
		switch ev.Type {
		case sharedacp.EventTypeMessage:
			sendProcEvent(proc, Event{Type: "text_delta", Content: ev.Content, SessionID: ev.SessionID})
			if !gracefulCompletion {
				textAcc.WriteString(ev.Content)
				if strings.Contains(textAcc.String(), acpCompletionMatcher) {
					gracefulCompletion = true
					_ = sess.Cancel()
				}
			}
		case sharedacp.EventTypeProgress, sharedacp.EventTypeTool:
			sendProcEvent(proc, Event{Type: "tool_call", Content: ev.Content, SessionID: ev.SessionID})
		case sharedacp.EventTypeStderr:
			sendProcEvent(proc, Event{Type: "stderr", Content: ev.Content, SessionID: ev.SessionID})
		case sharedacp.EventTypeError:
			sendProcEvent(proc, Event{Type: "error", Error: ev.Error, SessionID: ev.SessionID})
		default:
			sendProcEvent(proc, Event{Type: ev.Type, Content: ev.Content, SessionID: ev.SessionID})
		}
	}

	result := sess.Wait()

	if !sentStart && result.SessionID != "" {
		sendProcEvent(proc, Event{Type: "message_start", SessionID: result.SessionID})
	}

	if gracefulCompletion {
		// The Cancel() above will typically make the runner surface a kill
		// error; override so the caller sees a clean completion and strip
		// both the strict and the loose sentinel form from the final text.
		result.Status = sharedacp.StatusSuccess
		result.Error = ""
		r := result.Result
		r = strings.ReplaceAll(r, acpCompletionSentinel, "")
		r = strings.ReplaceAll(r, acpCompletionMatcher, "")
		result.Result = strings.TrimSpace(r)
	}

	proc.mu.Lock()
	proc.result = result.Result
	if result.Status == sharedacp.StatusError {
		errMsg := strings.TrimSpace(result.Error)
		// Always append stderr for diagnostics, especially on timeout/kill.
		// The RunResult may contain stderr content from the subprocess.
		if result.Stderr != "" {
			stderrMsg := strings.TrimSpace(result.Stderr)
			if stderrMsg != "" {
				if errMsg != "" {
					errMsg = fmt.Sprintf("%s\nstderr: %s", errMsg, stderrMsg)
				} else {
					errMsg = fmt.Sprintf("stderr: %s", stderrMsg)
				}
			}
		}
		if errMsg == "" {
			errMsg = "subprocess failed"
		}
		proc.err = fmt.Errorf("%s ACP %s", agentName, errMsg)
		proc.mu.Unlock()
		sendProcEvent(proc, Event{Type: "error", Error: proc.err.Error(), SessionID: result.SessionID})
	} else {
		proc.mu.Unlock()
	}

	sendProcEvent(proc, Event{Type: "message_end", SessionID: result.SessionID, StopReason: result.StopReason})
}

// sendProcEvent enqueues an event without blocking; events are dropped if
// the consumer has fallen behind. Mirrors Process.sendEvent semantics.
func sendProcEvent(p *Process, ev Event) {
	select {
	case p.events <- ev:
	default:
		// Log dropped events to stderr for debugging. This helps identify
		// when the event consumer (TUI/orchestrator) falls behind the producer.
		fmt.Fprintf(os.Stderr, "pi-go: dropped event type=%s session=%s\n", ev.Type, ev.SessionID)
	}
}
