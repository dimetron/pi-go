package subagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// Spawn starts a pi subprocess in JSON mode and returns a Process handle for streaming events.
func (s *Spawner) Spawn(ctx context.Context, opts SpawnOpts) (*Process, error) {
	if opts.Prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	// Resolve timeout configuration (applies defaults if not set).
	timeoutCfg := ResolveTimeout(opts.Timeout)
	procCtx, cancel := context.WithTimeout(ctx, timeoutCfg.Absolute)

	// Build command arguments.
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
	args = append(args, opts.Prompt)

	cmd := exec.CommandContext(procCtx, s.PiBinary, args...)

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

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("creating stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("creating stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("starting pi process: %w", err)
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
	var (
		stderrMu   sync.Mutex
		stderrBuf  strings.Builder
		stderrDone = make(chan struct{})
	)
	go func() {
		defer close(stderrDone)
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			stderrMu.Lock()
			if stderrBuf.Len() < maxStderrCapture {
				stderrBuf.WriteString(sc.Text())
				stderrBuf.WriteByte('\n')
			}
			stderrMu.Unlock()
		}
	}()

	// Feed stdout lines to the reader below rather than scanning inline, so the
	// reader can wait on "a line arrived" and "nothing has arrived in a while"
	// at the same time. A bare `for scanner.Scan()` can only block.
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 256*1024), 1024*1024) // up to 1MB lines
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	// Reader goroutine: parse JSONL from stdout, send events.
	go func() {
		defer close(proc.done)
		defer close(proc.events)

		var resultBuilder strings.Builder

		// The inactivity timer is what separates "slow" from "wedged". Without
		// it the absolute cap is the only limit, so a long but productive agent
		// is killed on the same rule as one that has hung — which is precisely
		// the failure this fixes.
		idle := NewInactivityTimer(timeoutCfg.Inactivity)
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
					proc.sendEvent(Event{Type: "text_delta", Content: line})
					continue
				}

				switch ev.Type {
				case "text_delta":
					resultBuilder.WriteString(ev.Delta)
					proc.sendEvent(Event{Type: "text_delta", Content: ev.Delta})
				case "tool_call":
					proc.sendEvent(Event{Type: "tool_call", Content: ev.ToolName, ToolArgs: ev.ToolInput})
				case "tool_result":
					proc.sendEvent(Event{Type: "tool_result", Content: ev.Content})
				case "message_start":
					proc.sendEvent(Event{Type: "message_start", SessionID: ev.SessionID})
				case "message_end":
					proc.sendEvent(Event{Type: "message_end"})
				default:
					proc.sendEvent(Event{Type: ev.Type, Content: ev.Delta + ev.Content})
				}

			case <-idle.C():
				timedOutIdle = true
				cancel()          // kills the process group; stdout closes, so lines drains
				for range lines { //nolint:revive // drain so the scanner goroutine can exit
				}
				break read
			}
		}

		// Both pipes must be at EOF before Wait, and stderr is wanted for the
		// error message below.
		<-stderrDone

		// Wait for process exit.
		waitErr := cmd.Wait()

		proc.mu.Lock()
		proc.result = resultBuilder.String()

		stderrMu.Lock()
		stderrStr := strings.TrimSpace(stderrBuf.String())
		stderrMu.Unlock()

		switch {
		case timedOutIdle:
			proc.err = fmt.Errorf("pi subagent produced no output for %s: %w (%s)",
				timeoutCfg.Inactivity, ErrSubagentTimeout, timeoutHint)
		case errors.Is(procCtx.Err(), context.DeadlineExceeded):
			proc.err = fmt.Errorf("pi subagent exceeded its %s time limit: %w (%s)",
				timeoutCfg.Absolute, ErrSubagentTimeout, timeoutHint)
		case waitErr != nil && stderrStr != "":
			proc.err = fmt.Errorf("pi process failed: %w: %s", waitErr, stderrStr)
		case waitErr != nil:
			proc.err = fmt.Errorf("pi process failed: %w", waitErr)
		}
		if proc.err != nil {
			proc.sendEvent(Event{Type: "error", Error: proc.err.Error()})
		}
		proc.mu.Unlock()
	}()

	return proc, nil
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
