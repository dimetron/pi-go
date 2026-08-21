package subagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrSubagentTimeout marks a subagent that was stopped by one of its own time
// limits rather than by failing.
//
// It exists because the kill is indistinguishable from a crash at the point the
// caller reads it. os/exec's Wait prefers the *ExitError from Process.Wait over
// the context error — watch.err is only consulted when err == nil — and a
// SIGKILLed process always exits unsuccessfully. So context.DeadlineExceeded is
// discarded and every timeout surfaced as the bare text "signal: killed", which
// tells the reader nothing about which limit was hit or how to raise it.
var ErrSubagentTimeout = errors.New("subagent timeout")

// maxStderrCapture bounds the stderr retained for error reporting. The whole
// point of draining stderr concurrently is to unblock a chatty child, so the
// buffer must not become the new place the memory goes.
const maxStderrCapture = 64 * 1024

// timeoutHint names the knobs, because a subagent killed by a limit is exactly
// the moment the reader wants to know the limit is adjustable.
const timeoutHint = "raise it with PI_SUBAGENT_TIMEOUT_MS or a `timeout:` (milliseconds) key in the agent's frontmatter"

// SpawnOpts holds options for spawning a subagent process.
type SpawnOpts struct {
	AgentID     string   // Unique ID for this agent
	Model       string   // Model name to use
	WorkDir     string   // Working directory for the process
	Prompt      string   // Task prompt to send
	Instruction string   // System instruction for the subagent
	Timeout     int      // Timeout in milliseconds (0 = use default)
	Env         []string // Additional environment variables (merged with filtered process env)
	BaseURL     string   // LLM API base URL (passed as --url flag)
	Insecure    bool     // Skip TLS verification (passed as --insecure flag)
	Headers     []string // Extra HTTP headers (passed as --header flags)
	// LSP selects the child's language-server tool surface (passed as --lsp).
	// Empty leaves the flag off, so the child uses its own default. This is the
	// seam that lets a navigation-heavy agent run with the full LSP set while
	// the parent session pays for none of it.
	LSP string
}

// Spawner creates and manages subagent pi processes.
type Spawner struct {
	// PiBinary is the path to the pi binary. Defaults to os.Executable() when
	// created via NewSpawner(""), ensuring subagents match the parent version.
	PiBinary string
}

// NewSpawner creates a new Spawner. If piBinary is empty, uses the currently
// running executable path (via os.Executable) to ensure subagents run the same
// version as the parent process.
func NewSpawner(piBinary string) *Spawner {
	if piBinary == "" {
		if exe, err := os.Executable(); err == nil {
			piBinary = exe
		} else {
			piBinary = "pi"
		}
	}
	return &Spawner{PiBinary: piBinary}
}

// Process represents a running subagent pi process.
type Process struct {
	cmd    *exec.Cmd
	events chan Event
	done   chan struct{}
	cancel context.CancelFunc
	result string
	err    error
	mu     sync.Mutex
}

// Events returns a channel that receives streaming events from the subagent.
// The channel is closed when the process exits.
func (p *Process) Events() <-chan Event {
	return p.events
}

// Wait blocks until the process exits and returns the accumulated result or an error.
func (p *Process) Wait() (string, error) {
	<-p.done
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.result, p.err
}

// Cancel kills the subagent process.
func (p *Process) Cancel() {
	p.cancel()
}

// spawnArgs builds the child pi command line. Split out from Spawn so the flag
// wiring can be tested without starting a process — the --lsp value in
// particular decides how much context the child spends on tool declarations.
func spawnArgs(opts SpawnOpts) []string {
	args := []string{"--mode", "json"}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.BaseURL != "" {
		args = append(args, "--url", opts.BaseURL)
	}
	if opts.Insecure {
		args = append(args, "--insecure")
	}
	for _, h := range opts.Headers {
		args = append(args, "--header", h)
	}
	if opts.Instruction != "" {
		args = append(args, "--system", opts.Instruction)
	}
	if opts.LSP != "" {
		args = append(args, "--lsp", opts.LSP)
	}
	// The prompt is positional and must stay last.
	return append(args, opts.Prompt)
}

// buildCommand wires the child pi command: its arguments, environment, working
// directory and the platform attributes that make canceling procCtx kill the
// whole process group rather than just the immediate child.
func (s *Spawner) buildCommand(procCtx context.Context, opts SpawnOpts) *exec.Cmd {
	cmd := exec.CommandContext(procCtx, s.PiBinary, spawnArgs(opts)...)

	// Set up environment: filtered process env + additional env vars. The
	// child's concurrency budget is this process's share, not a copy of it —
	// see ChildEnv.
	baseEnv := ChildEnv(ConcurrencyFromEnv())
	if len(opts.Env) > 0 {
		cmd.Env = append(baseEnv, opts.Env...)
	} else {
		cmd.Env = baseEnv
	}

	// Ensure the process and its children are killed on cancel.
	setPlatformAttrs(cmd)
	cmd.WaitDelay = 3 * time.Second
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	return cmd
}

// startPiProcess opens the child's stdout and stderr pipes and starts it.
//
// Canceling the process context stays the caller's job: on error nothing was
// started, so there is no process to tear down here.
func startPiProcess(cmd *exec.Cmd) (io.ReadCloser, io.ReadCloser, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting pi process: %w", err)
	}
	return stdout, stderr, nil
}

// Spawn starts a pi subprocess in JSON mode and returns a Process handle for streaming events.
func (s *Spawner) Spawn(ctx context.Context, opts SpawnOpts) (*Process, error) {
	if opts.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	// Resolve timeout configuration (applies defaults if not set).
	//
	// procCtx and its cancel deliberately stay in this scope: the process
	// outlives Spawn, so cancel is handed to the Process (Process.Cancel) and
	// to the reader goroutine, never deferred here.
	timeoutCfg := ResolveTimeout(opts.Timeout)
	procCtx, cancel := context.WithTimeout(ctx, timeoutCfg.Absolute)

	cmd := s.buildCommand(procCtx, opts)

	stdout, stderr, err := startPiProcess(cmd)
	if err != nil {
		cancel()
		return nil, err
	}

	proc := &Process{
		cmd:    cmd,
		events: make(chan Event, 64),
		done:   make(chan struct{}),
		cancel: cancel,
	}

	// Drain stderr concurrently with stdout.
	//
	// These are two independent pipes with their own kernel buffers. Reading
	// them in sequence — stdout to EOF, then stderr — deadlocks the moment the
	// child writes more than a pipe buffer (~64KB) to stderr: the child blocks
	// in write(2), so it never closes stdout, so the parent never finishes the
	// stdout scan and never reaches the stderr read. The child then sits there
	// until a timeout kills it, and because stderr was never drained the error
	// arrives with no diagnostic text attached at all.
	stderrCap, stderrDone := drainStderr(stderr)

	// Feed stdout lines to the reader below rather than scanning inline, so the
	// reader can wait on "a line arrived" and "nothing has arrived in a while"
	// at the same time. A bare `for scanner.Scan()` can only block.
	lines := scanLines(stdout)

	// Reader goroutine: parse JSONL from stdout, send events.
	go func() {
		defer close(proc.done)
		defer close(proc.events)

		result, timedOutIdle := proc.readEvents(lines, cancel, timeoutCfg.Inactivity)

		// Both pipes must be at EOF before Wait, and stderr is wanted for the
		// error message below.
		<-stderrDone

		// Wait for process exit.
		waitErr := cmd.Wait()

		proc.mu.Lock()
		proc.result = result
		proc.err = spawnFailure(timeoutCfg, timedOutIdle, procCtx.Err(), waitErr, stderrCap.String())
		if proc.err != nil {
			proc.sendEvent(Event{Type: "error", Error: proc.err.Error()})
		}
		proc.mu.Unlock()
	}()

	return proc, nil
}

// stderrCapture accumulates a child's stderr, bounded at maxStderrCapture. The
// draining goroutine writes while the reader goroutine reads, so it carries its
// own lock.
type stderrCapture struct {
	mu  sync.Mutex
	buf strings.Builder
}

// writeLine appends a line and its newline, dropping everything past the cap.
func (c *stderrCapture) writeLine(line string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.buf.Len() < maxStderrCapture {
		c.buf.WriteString(line)
		c.buf.WriteByte('\n')
	}
}

// String returns the captured stderr with surrounding whitespace trimmed.
func (c *stderrCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.TrimSpace(c.buf.String())
}

// drainStderr consumes r in the background into a bounded buffer. The returned
// channel is closed once r reaches EOF, which is the signal that the pipe is
// drained and cmd.Wait may run.
func drainStderr(r io.Reader) (*stderrCapture, <-chan struct{}) {
	capture := &stderrCapture{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			capture.writeLine(sc.Text())
		}
	}()
	return capture, done
}

// scanLines feeds r's lines to a buffered channel in the background, closing it
// at EOF. Lines up to 1MB are tolerated; anything longer ends the scan.
func scanLines(r io.Reader) <-chan string {
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 256*1024), 1024*1024) // up to 1MB lines
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	return lines
}

// readEvents forwards the child's JSONL stdout as events until the stream ends
// or the child goes quiet for longer than inactivity. It returns the
// accumulated assistant text and whether the inactivity limit fired.
//
// On inactivity it cancels the process group itself and drains lines so the
// scanner goroutine can exit; the caller must go straight to cmd.Wait.
func (p *Process) readEvents(lines <-chan string, cancel context.CancelFunc, inactivity time.Duration) (string, bool) {
	var resultBuilder strings.Builder

	// The inactivity timer is what separates "slow" from "wedged". Without
	// it the absolute cap is the only limit, so a long but productive agent
	// is killed on the same rule as one that has hung — which is precisely
	// the failure this fixes.
	idle := NewInactivityTimer(inactivity)
	defer idle.Stop()

	timedOutIdle := false

read:
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				break read
			}
			idle.Reset()
			if line == "" {
				continue
			}

			var ev jsonEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				// Non-JSON output; emit as text.
				p.sendEvent(Event{Type: "text_delta", Content: line})
				continue
			}

			if ev.Type == "text_delta" {
				resultBuilder.WriteString(ev.Delta)
			}
			p.sendEvent(ev.toEvent())

		case <-idle.C():
			timedOutIdle = true
			cancel()          // kills the process group; stdout closes, so lines drains
			for range lines { //nolint:revive // drain so the scanner goroutine can exit
			}
			break read
		}
	}

	return resultBuilder.String(), timedOutIdle
}

// spawnFailure classifies how the child ended into the error Wait reports.
// A nil result means it exited cleanly.
//
// A timeout is reported ahead of the exit status because it also arrives as a
// signal kill — the process group is SIGKILLed — and would otherwise be
// indistinguishable from a crash.
func spawnFailure(cfg TimeoutConfig, timedOutIdle bool, procCtxErr, waitErr error, stderrStr string) error {
	switch {
	case timedOutIdle:
		return fmt.Errorf("pi subagent produced no output for %s: %w (%s)",
			cfg.Inactivity, ErrSubagentTimeout, timeoutHint)
	case errors.Is(procCtxErr, context.DeadlineExceeded):
		return fmt.Errorf("pi subagent exceeded its %s time limit: %w (%s)",
			cfg.Absolute, ErrSubagentTimeout, timeoutHint)
	case waitErr != nil && stderrStr != "":
		return fmt.Errorf("pi process failed: %w: %s", waitErr, stderrStr)
	case waitErr != nil:
		return fmt.Errorf("pi process failed: %w", waitErr)
	}
	return nil
}

// sendEvent sends an event to the channel without blocking.
func (p *Process) sendEvent(ev Event) {
	select {
	case p.events <- ev:
	default:
		// Channel full; drop event to avoid blocking.
	}
}

// jsonEvent mirrors the JSONL event format from pi --mode json (see internal/cli/cli.go).
type jsonEvent struct {
	Type      string `json:"type"`
	Agent     string `json:"agent,omitempty"`
	Role      string `json:"role,omitempty"`
	Delta     string `json:"delta,omitempty"`
	Content   string `json:"content,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolInput any    `json:"tool_input,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

// toEvent maps a decoded child event onto the Event forwarded to the
// orchestrator. Each known type carries a different field across, which is why
// this is a switch and not a struct copy; an unknown type keeps its name and
// whichever of delta/content the child populated.
func (ev jsonEvent) toEvent() Event {
	switch ev.Type {
	case "text_delta":
		return Event{Type: "text_delta", Content: ev.Delta}
	case "tool_call":
		return Event{Type: "tool_call", Content: ev.ToolName, ToolArgs: ev.ToolInput}
	case "tool_result":
		return Event{Type: "tool_result", Content: ev.Content}
	case "message_start":
		return Event{Type: "message_start", SessionID: ev.SessionID}
	case "message_end":
		return Event{Type: "message_end"}
	default:
		return Event{Type: ev.Type, Content: ev.Delta + ev.Content}
	}
}
