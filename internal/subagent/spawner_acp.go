package subagent

import (
	"context"
	"fmt"
	"strings"

	sharedacp "github.com/dimetron/pi-go/internal/acp"
	"github.com/dimetron/pi-go/internal/acp/client/agy"
	"github.com/dimetron/pi-go/internal/acp/client/claudecode"
	"github.com/dimetron/pi-go/internal/acp/client/copilot"
	"github.com/dimetron/pi-go/internal/acp/client/cursor"
	"github.com/dimetron/pi-go/internal/acp/client/gemini"
	"github.com/dimetron/pi-go/internal/notice"
)

// acpSession is the shared interface implemented by every per-runner
// RunningSession (claudecode, gemini, cursor, agy). It lets dispatchACP treat
// them uniformly when pumping events back to the orchestrator.
type acpSession interface {
	Events() <-chan sharedacp.Event
	Done() <-chan struct{}
	Cancel() error
	Wait() sharedacp.RunResult
}

// startACPSessionFn is the constructor used by dispatchACP to start a
// runner. It is overridable in tests so the dispatcher can be exercised
// without invoking real claude/gemini/cursor/agy binaries.
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
//
// The preamble also enforces anti-hallucination rules: the agent must not
// claim completion without verifying that actual deliverables exist. This
// prevents false "completed" reports where the agent says it finished but
// no files were created or no build was run.
func acpPromptPreamble(agentName, task string) string {
	return fmt.Sprintf("You are subagent[%s], %s when done reply %s\n\n"+
		"ANTI-HALLUCINATION RULES (critical):\n"+
		"- Before claiming completion, run `git diff --name-only` and list the actual changed files.\n"+
		"- If the changed file list is empty, you have not delivered anything. Say so honestly.\n"+
		"- Never claim a build or test passes without running the actual command and pasting the output.\n"+
		"- Never claim a file exists that you did not create. Verify with `ls` or `git status`.\n"+
		"- Do not fabricate tool output. If a command failed, report the failure.\n"+
		"- Only reply %s after you have verified your deliverables exist.",
		agentName, task, acpCompletionSentinel, acpCompletionSentinel)
}

// dispatchACP starts an ACP-based subagent (claude, gemini, cursor, agy) and
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
	case "copilot":
		r := copilot.Runner{ExtraEnv: opts.Env}
		return r.Start(ctx, copilot.RunRequest{
			Prompt: prompt,
			CWD:    opts.WorkDir,
			Env:    opts.Env,
		})
	case "agy":
		r := agy.Runner{ExtraEnv: opts.Env}
		return r.Start(ctx, agy.RunRequest{
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

	sentStart, gracefulCompletion := pumpACPEvents(sess, proc)

	result := sess.Wait()

	if !sentStart && result.SessionID != "" {
		sendProcEvent(proc, Event{Type: "message_start", SessionID: result.SessionID})
	}

	if gracefulCompletion {
		result = completeGracefully(result)
	}

	proc.mu.Lock()
	proc.result = result.Result
	if result.Status == sharedacp.StatusError {
		proc.err = fmt.Errorf("%s ACP %s", agentName, acpErrorText(result))
		proc.mu.Unlock()
		sendProcEvent(proc, Event{Type: "error", Error: proc.err.Error(), SessionID: result.SessionID})
	} else {
		proc.mu.Unlock()
	}

	sendProcEvent(proc, Event{Type: "message_end", SessionID: result.SessionID, StopReason: result.StopReason})
}

// pumpACPEvents forwards the session's event stream to proc until it closes.
//
// sentStart reports whether a message_start was emitted, so the caller can
// still emit one from the final result. gracefulCompletion is set when the
// agent emits the <Task Completed>! sentinel: once flipped, sess.Cancel() is
// invoked to tear down the subprocess, and the resulting session error is
// coerced back to StatusSuccess since the agent itself signaled it was done.
func pumpACPEvents(sess acpSession, proc *Process) (sentStart, gracefulCompletion bool) {
	var textAcc strings.Builder
	for ev := range sess.Events() {
		if !sentStart && ev.SessionID != "" {
			sendProcEvent(proc, Event{Type: "message_start", SessionID: ev.SessionID})
			sentStart = true
		}
		sendProcEvent(proc, acpProcEvent(ev))

		if ev.Type != sharedacp.EventTypeMessage || gracefulCompletion {
			continue
		}
		textAcc.WriteString(ev.Content)
		if strings.Contains(textAcc.String(), acpCompletionMatcher) {
			gracefulCompletion = true
			_ = sess.Cancel()
		}
	}
	return sentStart, gracefulCompletion
}

// acpProcEvent maps one ACP session event onto the orchestrator's Event shape.
func acpProcEvent(ev sharedacp.Event) Event {
	switch ev.Type {
	case sharedacp.EventTypeMessage:
		return Event{Type: "text_delta", Content: ev.Content, SessionID: ev.SessionID}
	case sharedacp.EventTypeProgress, sharedacp.EventTypeTool:
		return Event{Type: "tool_call", Content: ev.Content, SessionID: ev.SessionID}
	case sharedacp.EventTypeStderr:
		return Event{Type: "stderr", Content: ev.Content, SessionID: ev.SessionID}
	case sharedacp.EventTypeError:
		return Event{Type: "error", Error: ev.Error, SessionID: ev.SessionID}
	default:
		return Event{Type: ev.Type, Content: ev.Content, SessionID: ev.SessionID}
	}
}

// completeGracefully rewrites a run that ended on the <Task Completed>!
// sentinel. The Cancel() that sentinel triggered will typically make the runner
// surface a kill error; override so the caller sees a clean completion and
// strip both the strict and the loose sentinel form from the final text.
func completeGracefully(result sharedacp.RunResult) sharedacp.RunResult {
	result.Status = sharedacp.StatusSuccess
	result.Error = ""
	r := result.Result
	r = strings.ReplaceAll(r, acpCompletionSentinel, "")
	r = strings.ReplaceAll(r, acpCompletionMatcher, "")
	result.Result = strings.TrimSpace(r)
	return result
}

// acpErrorText builds the message for a failed ACP run. Stderr is always
// appended for diagnostics, especially on timeout/kill, where the RunResult
// carries the subprocess's stderr and often nothing else.
func acpErrorText(result sharedacp.RunResult) string {
	errMsg := strings.TrimSpace(result.Error)
	if stderrMsg := strings.TrimSpace(result.Stderr); stderrMsg != "" {
		if errMsg != "" {
			errMsg = fmt.Sprintf("%s\nstderr: %s", errMsg, stderrMsg)
		} else {
			errMsg = fmt.Sprintf("stderr: %s", stderrMsg)
		}
	}
	if errMsg == "" {
		return "subprocess failed"
	}
	return errMsg
}

// sendProcEvent enqueues an event without blocking; events are dropped if
// the consumer has fallen behind. Mirrors Process.sendEvent semantics.
func sendProcEvent(p *Process, ev Event) {
	select {
	case p.events <- ev:
	default:
		// Report dropped events so a consumer (TUI/orchestrator) that has
		// fallen behind the producer is visible rather than silent. It goes
		// through the notice sink, not os.Stderr: this fires mid-turn, while
		// the TUI owns the terminal, and a direct write lands inside the
		// painted frame.
		notice.Notifyf("dropped event type=%s session=%s", ev.Type, ev.SessionID)
	}
}
