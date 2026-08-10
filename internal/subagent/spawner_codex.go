package subagent

import (
	"context"
	"fmt"
	"strings"

	"github.com/dimetron/pi-go/internal/codex"
)

// codexAgentNames is the set of bundled agent names backed by the Codex
// app-server's JSON-RPC protocol (direct mode: pi-go spawns `codex app-server`
// itself, no broker and no ACP shim).
//
// These names must never be added to acpAgentNames — that would route them
// through dispatchACP and the ACP SDK, which cannot speak this protocol.
var codexAgentNames = map[string]struct{}{
	"codex":        {},
	"codex-review": {},
}

// isCodexAgent reports whether the named agent is a Codex app-server adapter.
func isCodexAgent(name string) bool {
	_, ok := codexAgentNames[name]
	return ok
}

// codexSession is the shared interface implemented by codex.Session. It lets
// dispatchCodex be exercised without a real codex binary.
type codexSession interface {
	Events() <-chan codex.Event
	Done() <-chan struct{}
	Cancel() error
	Wait() codex.RunResult
}

// startCodexSessionFn is the constructor used by dispatchCodex. It is
// overridable in tests (mirrors startACPSessionFn).
var startCodexSessionFn = startCodexSession

// codexPromptPreamble frames the task for a codex subagent.
//
// Unlike the ACP agents, codex needs no "<Task Completed>!" sentinel: the
// app-server sends an explicit turn/completed notification, so the session
// always knows when the turn is over. The anti-hallucination rules remain —
// they are about honesty, not termination.
func codexPromptPreamble(agentName, task string) string {
	return fmt.Sprintf("You are subagent[%s]. %s\n\n"+
		"ANTI-HALLUCINATION RULES (critical):\n"+
		"- Before claiming completion, run `git diff --name-only` and list the actual changed files.\n"+
		"- If the changed file list is empty, you have not delivered anything. Say so honestly.\n"+
		"- Never claim a build or test passes without running the actual command and pasting the output.\n"+
		"- Never claim a file exists that you did not create. Verify with `ls` or `git status`.\n"+
		"- Do not fabricate tool output. If a command failed, report the failure.",
		agentName, task)
}

// dispatchCodex starts a Codex app-server subagent and returns a *Process whose
// event channel is fed by the codex session, translated into the orchestrator's
// Event format.
//
// pi is not invoked for codex agents — the codex CLI is launched directly.
func dispatchCodex(ctx context.Context, opts SpawnOpts, agentName string) (*Process, error) {
	if opts.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	prompt := codexPromptPreamble(agentName, opts.Prompt)
	if strings.TrimSpace(opts.Instruction) != "" {
		prompt = opts.Instruction + "\n\n" + prompt
	}

	timeoutCfg := ResolveTimeout(opts.Timeout)
	procCtx, cancel := context.WithTimeout(ctx, timeoutCfg.Absolute)

	sess, err := startCodexSessionFn(procCtx, agentName, prompt, opts)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("starting %s codex session: %w", agentName, err)
	}

	proc := &Process{
		events: make(chan Event, 256),
		done:   make(chan struct{}),
		cancel: func() {
			_ = sess.Cancel()
			cancel()
		},
	}

	go pumpCodexSession(sess, proc, agentName)
	return proc, nil
}

// startCodexSession picks the sandbox and turn kind for agentName and starts a
// codex session.
//
// codex runs with workspace-write and a normal turn; codex-review runs
// read-only and uses review/start against the uncommitted changes. Codex's own
// model/effort defaults apply: opts.Model comes from pi's role config and names
// a pi model, which is meaningless to codex (same reasoning as startACPSession).
func startCodexSession(ctx context.Context, agentName, prompt string, opts SpawnOpts) (codexSession, error) {
	var sessOpts codex.SessionOpts
	switch agentName {
	case "codex":
		sessOpts = codex.SessionOpts{Sandbox: codex.SandboxWorkspaceWrite}
	case "codex-review":
		sessOpts = codex.SessionOpts{Sandbox: codex.SandboxReadOnly, Review: true}
	default:
		return nil, fmt.Errorf("unknown codex agent %q", agentName)
	}

	sessOpts.CWD = opts.WorkDir
	sessOpts.Prompt = prompt
	sessOpts.Env = append(ChildEnv(ConcurrencyFromEnv()), opts.Env...)

	sess, err := codex.NewSession(ctx, sessOpts)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// pumpCodexSession translates events from a codex session into orchestrator
// Events, captures the final result on proc, then closes proc.events and
// proc.done. The defer order is important: events must close before done so
// consumers ranging over Events() exit before Wait() returns.
func pumpCodexSession(sess codexSession, proc *Process, agentName string) {
	defer close(proc.done)
	defer close(proc.events)

	sentStart := false
	for ev := range sess.Events() {
		if !sentStart && ev.SessionID != "" {
			sendProcEvent(proc, Event{Type: "message_start", SessionID: ev.SessionID})
			sentStart = true
		}
		switch ev.Type {
		case codex.EventTypeMessage:
			sendProcEvent(proc, Event{Type: "text_delta", Content: ev.Content, SessionID: ev.SessionID})
		case codex.EventTypeProgress, codex.EventTypeTool:
			sendProcEvent(proc, Event{Type: "tool_call", Content: ev.Content, SessionID: ev.SessionID})
		case codex.EventTypeStderr:
			sendProcEvent(proc, Event{Type: "stderr", Content: ev.Content, SessionID: ev.SessionID})
		case codex.EventTypeError:
			sendProcEvent(proc, Event{Type: "error", Error: ev.Error, SessionID: ev.SessionID})
		default:
			sendProcEvent(proc, Event{Type: ev.Type, Content: ev.Content, SessionID: ev.SessionID})
		}
	}

	result := sess.Wait()

	if !sentStart && result.SessionID != "" {
		sendProcEvent(proc, Event{Type: "message_start", SessionID: result.SessionID})
	}

	proc.mu.Lock()
	proc.result = result.Result
	if result.Status == codex.StatusError {
		errMsg := strings.TrimSpace(result.Error)
		if stderrMsg := strings.TrimSpace(result.Stderr); stderrMsg != "" {
			if errMsg != "" {
				errMsg = fmt.Sprintf("%s\nstderr: %s", errMsg, stderrMsg)
			} else {
				errMsg = fmt.Sprintf("stderr: %s", stderrMsg)
			}
		}
		if errMsg == "" {
			errMsg = "subprocess failed"
		}
		proc.err = fmt.Errorf("%s codex %s", agentName, errMsg)
		proc.mu.Unlock()
		sendProcEvent(proc, Event{Type: "error", Error: proc.err.Error(), SessionID: result.SessionID})
	} else {
		proc.mu.Unlock()
	}

	sendProcEvent(proc, Event{Type: "message_end", SessionID: result.SessionID, StopReason: result.StopReason})
}
