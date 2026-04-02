package subagent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SpawnOpts holds options for spawning a subagent process.
type SpawnOpts struct {
	AgentID     string   // Unique ID for this agent
	Model       string   // Model name to use
	WorkDir     string   // Working directory for the process
	Prompt      string   // Task prompt to send
	Instruction string   // System instruction for the subagent
	Timeout     int      // Timeout in milliseconds (0 = use default)
	Env         []string // Additional environment variables (merged with filtered process env)
}

// Spawner creates and manages subagent pi processes.
type Spawner struct {
	// PiBinary is the path to the pi binary. Defaults to "pi" (from PATH).
	PiBinary string
}

// NewSpawner creates a new Spawner. If piBinary is empty, uses "pi" from PATH.
func NewSpawner(piBinary string) *Spawner {
	if piBinary == "" {
		piBinary = "pi"
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
	if opts.Instruction != "" {
		args = append(args, "--system", opts.Instruction)
	}
	args = append(args, opts.Prompt)

	cmd := exec.CommandContext(procCtx, s.PiBinary, args...)

	// Set up environment: filtered process env + additional env vars.
	baseEnv := FilterEnv(nil)
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

	// Reader goroutine: parse JSONL from stdout, send events.
	go func() {
		defer close(proc.done)
		defer close(proc.events)

		var resultBuilder strings.Builder
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // up to 1MB lines

		for scanner.Scan() {
			line := scanner.Text()
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
				proc.sendEvent(Event{Type: "tool_call", Content: ev.ToolName})
			case "tool_result":
				proc.sendEvent(Event{Type: "tool_result", Content: ev.Content})
			case "message_start":
				proc.sendEvent(Event{Type: "message_start"})
			case "message_end":
				proc.sendEvent(Event{Type: "message_end"})
			default:
				proc.sendEvent(Event{Type: ev.Type, Content: ev.Delta + ev.Content})
			}
		}

		// Capture stderr for error reporting.
		stderrScanner := bufio.NewScanner(stderr)
		var stderrBuf strings.Builder
		for stderrScanner.Scan() {
			stderrBuf.WriteString(stderrScanner.Text())
			stderrBuf.WriteByte('\n')
		}

		// Wait for process exit.
		waitErr := cmd.Wait()

		proc.mu.Lock()
		proc.result = resultBuilder.String()
		if waitErr != nil {
			stderrStr := strings.TrimSpace(stderrBuf.String())
			if stderrStr != "" {
				proc.err = fmt.Errorf("pi process failed: %w: %s", waitErr, stderrStr)
			} else {
				proc.err = fmt.Errorf("pi process failed: %w", waitErr)
			}
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
}
