package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"google.golang.org/adk/tool"
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
	return newTool("bash", "Execute a shell command and return its output. Commands run in a bash shell. Use for system operations, running tests, building code, git operations, etc.", func(ctx tool.Context, input BashInput) (BashOutput, error) {
		return bashHandler(sb, ctx, input)
	})
}

func bashHandler(sb *Sandbox, ctx tool.Context, input BashInput) (BashOutput, error) {
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

	// Use background context if tool.Context is nil (e.g. in unit tests)
	var parentCtx = context.Background()
	if ctx != nil {
		parentCtx = ctx
	}
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
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if cmdCtx.Err() == context.DeadlineExceeded {
			return BashOutput{
				Stdout:   redactSecrets(truncateOutput(stdout.String())),
				Stderr:   "command timed out",
				ExitCode: -1,
			}, nil
		} else {
			return BashOutput{}, fmt.Errorf("executing command: %w", err)
		}
	}

	return BashOutput{
		Stdout:   redactSecrets(truncateOutput(stdout.String())),
		Stderr:   redactSecrets(truncateOutput(stderr.String())),
		ExitCode: exitCode,
	}, nil
}
