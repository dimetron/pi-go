package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"

	"github.com/dimetron/pi-go/internal/otel"
)

const defaultBashTimeout = 120 * time.Second

// BashInput defines the parameters for the bash tool.
type BashInput struct {
	// The shell command to execute.
	Command string `json:"command"`
	// Optional timeout in milliseconds. Default: 120000 (2 minutes). Max: 600000 (10 minutes).
	Timeout int `json:"timeout,omitempty"`
}

// BashOutput contains the result of executing a shell command.
type BashOutput struct {
	// Standard output from the command.
	Stdout string `json:"stdout"`
	// Standard error from the command.
	Stderr string `json:"stderr"`
	// Exit code of the command.
	ExitCode int `json:"exit_code"`
}

func newBashTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("bash", "Execute a shell command and return its output. Commands run in a bash shell. Use for system operations, running tests, building code, git operations, etc.", func(ctx agent.ToolContext, input BashInput) (BashOutput, error) {
		return bashHandler(sb, ctx, input)
	})
}

func bashHandler(sb *Sandbox, ctx agent.ToolContext, input BashInput) (BashOutput, error) {
	if input.Command == "" {
		return BashOutput{}, fmt.Errorf("command is required")
	}

	timeout := defaultBashTimeout
	if input.Timeout > 0 {
		timeout = time.Duration(input.Timeout) * time.Millisecond
		if timeout > 10*time.Minute {
			timeout = 10 * time.Minute
		}
	}

	// Use background context if agent.ToolContext is nil (e.g. in unit tests)
	var parentCtx = context.Background()
	if ctx != nil {
		parentCtx = ctx
	}

	parentCtx, span := otel.Tracer("pi-go").Start(parentCtx, "tool.bash")
	span.SetAttributes(
		attribute.String("bash.command", input.Command),
		attribute.String("bash.cwd", sb.Dir()),
	)
	defer span.End()

	cmdCtx, cancel := context.WithTimeout(parentCtx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "bash", "-c", input.Command)
	cmd.Dir = sb.Dir()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
			span.SetAttributes(
				attribute.Int("bash.exit_code", -1),
				attribute.Int("bash.stdout_len", stdout.Len()),
				attribute.Int("bash.stderr_len", stderr.Len()),
			)
			return BashOutput{
				Stdout:   redactSecrets(truncateOutput(stdout.String())),
				Stderr:   redactSecrets(truncateOutput("command timed out\n" + stderr.String())),
				ExitCode: -1,
			}, nil
		} else {
			span.RecordError(err)
			return BashOutput{}, fmt.Errorf("executing command: %w", err)
		}
	}

	// SIGPIPE (exit 141 = signal 13 + 128) is benign — the consumer closed the pipe
	// before the producer finished writing. Treat it as success.
	if exitCode == 141 {
		exitCode = 0
	}

	span.SetAttributes(
		attribute.Int("bash.exit_code", exitCode),
		attribute.Int("bash.stdout_len", stdout.Len()),
		attribute.Int("bash.stderr_len", stderr.Len()),
	)
	return BashOutput{
		Stdout:   redactSecrets(truncateOutput(stdout.String())),
		Stderr:   redactSecrets(truncateOutput(stderr.String())),
		ExitCode: exitCode,
	}, nil
}
