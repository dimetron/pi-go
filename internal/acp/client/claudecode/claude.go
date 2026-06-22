// Package claudecode provides an ACP client for Claude Code via the
// @agentclientprotocol/claude-agent-acp subprocess adapter. The ACP session
// lifecycle — connection wiring, stderr streaming, event translation and result
// capture — is provided by the shared client package; this package only resolves
// the launcher binary and builds its command line.
package claudecode

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
	client "github.com/dimetron/pi-go/internal/acp/client"
)

// BinaryName is the preferred launcher for the Claude Code ACP subprocess adapter.
const BinaryName = "bunx"

// rpcTimeout caps how long Initialize and NewSession may block on the
// subprocess. Without a cap, a hung subprocess (missing API key, npm install
// in progress, no TTY for interactive auth, etc.) makes the call wait for the
// orchestrator's absolute timeout — typically 10 minutes — producing "stuck"
// subagents in the parent UI. 60s is comfortably more than a normal startup
// yet short enough to surface real problems quickly.
const rpcTimeout = 60 * time.Second

// DefaultCommand is the default command used to launch Claude Code ACP.
var DefaultCommand = []string{"bunx", "-y", "@agentclientprotocol/claude-agent-acp@latest"}

// DefaultBinaryPaths lists common installation locations for the launcher binary.
var DefaultBinaryPaths = []string{
	"bunx",
	"/opt/homebrew/bin/bunx",
	"/usr/local/bin/bunx",
	"/usr/bin/bunx",
}

// Runner launches Claude Code via the claude-agent-acp subprocess adapter
// and manages the ACP client-side connection.
type Runner struct {
	// ClientInfo identifies this client to the agent.
	ClientInfo acp.Implementation

	// Binary is the path to the claude-agent-acp binary.
	// If empty, uses the first found in DefaultBinaryPaths.
	Binary string

	// Logger for connection debugging.
	Logger *slog.Logger

	// ExtraEnv is additional environment variables to pass to the subprocess.
	ExtraEnv []string
}

// RunRequest describes a Claude Code prompt turn.
type RunRequest struct {
	Prompt    string   // Prompt to send to Claude Code
	SessionID string   // Optional session ID to resume
	CWD       string   // Working directory
	Env       []string // Additional environment variables
	Command   []string // Optional command override for testing (defaults to claude-agent-acp)
}

// envACPClaudeCmd is the environment variable that overrides the Claude ACP command.
// Format: "binary arg1 arg2 ..." or just "binary" (args come from DefaultCommand).
const envACPClaudeCmd = "PI_ACP_CLAUDE_CMD"

// Start launches the Claude Code subprocess and begins the ACP flow, delegating
// the session lifecycle to the shared client runner.
func (r Runner) Start(ctx context.Context, req RunRequest) (*client.RunningSession, error) {
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	binary, cmdArgs, err := r.resolveCommand(req)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, binary, cmdArgs...)
	runner := client.Runner{ClientInfo: r.ClientInfo, Logger: r.Logger, ExtraEnv: r.ExtraEnv}
	return runner.StartCommand(ctx, cmd, shared.RunRequest{
		Prompt:     req.Prompt,
		SessionID:  req.SessionID,
		CWD:        req.CWD,
		Env:        req.Env,
		RPCTimeout: rpcTimeout,
	})
}

// resolveCommand determines the launcher binary and argument list for the
// Claude Code ACP subprocess, honoring (in order) an explicit Binary, a test
// Command override, the PI_ACP_CLAUDE_CMD env override, and finally
// DefaultBinaryPaths with DefaultCommand arguments.
func (r Runner) resolveCommand(req RunRequest) (string, []string, error) {
	binary := r.Binary
	cmdArgs := []string{}
	if binary == "" {
		switch {
		case len(req.Command) > 0:
			binary = req.Command[0]
			if len(req.Command) > 1 {
				cmdArgs = req.Command[1:]
			}
		case os.Getenv(envACPClaudeCmd) != "":
			// PI_ACP_CLAUDE_CMD overrides the default launcher entirely.
			// Parse "binary arg1 arg2 ..." from the env var.
			parts := strings.Fields(os.Getenv(envACPClaudeCmd))
			if len(parts) > 0 {
				binary = parts[0]
				cmdArgs = parts[1:]
			}
		default:
			found, err := findBinary(DefaultBinaryPaths)
			if err != nil {
				return "", nil, fmt.Errorf("finding %s: %w", BinaryName, err)
			}
			binary = found
			cmdArgs = append(cmdArgs, DefaultCommand[1:]...)
		}
	}
	return binary, cmdArgs, nil
}

// findBinary returns the first existing entry in paths, resolving bare names
// via PATH and absolute/relative paths via stat.
func findBinary(paths []string) (string, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		if filepath.IsAbs(path) || strings.HasPrefix(path, ".") {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
			continue
		}
		if fullPath, err := exec.LookPath(path); err == nil {
			return fullPath, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH or default locations", BinaryName)
}
