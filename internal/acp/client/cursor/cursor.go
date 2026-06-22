// Package cursor provides an ACP client for Cursor CLI via its `agent acp`
// subprocess (https://cursor.com/docs/cli/acp). The ACP session lifecycle —
// connection wiring, stderr streaming, event translation and result capture —
// is provided by the shared client package; this package only resolves the
// agent binary and builds its ACP-mode argument list.
package cursor

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

// rpcTimeout caps Initialize and NewSession against a hung subprocess. See
// claudecode for the rationale; 60s is enough for a normal startup but short
// enough to surface a missing API key or missing binary quickly.
const rpcTimeout = 60 * time.Second

// BinaryName is the canonical name of the Cursor CLI binary. The official
// installer creates both `agent` (preferred) and `cursor-agent` (legacy alias)
// as symlinks to ~/.local/share/cursor-agent/versions/<v>/cursor-agent.
const BinaryName = "agent"

// ACPSubcommand is the subcommand that puts Cursor CLI into ACP mode.
const ACPSubcommand = "acp"

// DefaultBinaryPaths lists common installation locations for the Cursor
// CLI binary, in priority order. The Cursor installer places the canonical
// `agent` binary at $HOME/.local/bin/agent; `cursor-agent` is kept as a
// legacy fallback for older installs.
var DefaultBinaryPaths = []string{
	"agent",
	"cursor-agent",
	".local/bin/agent",
	".local/bin/cursor-agent",
	"/usr/local/bin/agent",
	"/usr/local/bin/cursor-agent",
	"/usr/bin/agent",
	"/usr/bin/cursor-agent",
}

// Runner launches Cursor CLI in ACP mode and manages the client-side
// connection. Authentication is taken from the environment
// (CURSOR_API_KEY / CURSOR_AUTH_TOKEN) unless explicitly overridden.
type Runner struct {
	// ClientInfo identifies this client to the agent.
	ClientInfo acp.Implementation

	// Binary is the path to the agent (or legacy `cursor-agent`) binary.
	// If empty, uses the first found in DefaultBinaryPaths.
	Binary string

	// Logger for connection debugging.
	Logger *slog.Logger

	// ExtraEnv is additional environment variables to pass to the subprocess.
	ExtraEnv []string
}

// RunRequest describes a Cursor CLI prompt turn.
type RunRequest struct {
	Prompt    string   // Prompt to send to Cursor CLI
	SessionID string   // Optional session ID to resume
	CWD       string   // Working directory
	Env       []string // Additional environment variables
	Command   []string // Optional command override for testing (defaults to agent acp)

	// Cursor-specific options
	APIKey    string // Optional; if set, passed as --api-key. Prefer CURSOR_API_KEY env var.
	AuthToken string // Optional; if set, passed as --auth-token. Prefer CURSOR_AUTH_TOKEN env var.
	Endpoint  string // Optional API endpoint override (e.g. https://api2.cursor.sh).
	Model     string // Optional model hint (informational; Cursor picks per-task).
	Debug     bool   // Enable verbose/debug output if supported by the CLI.
}

// envACPCursorCmd is the environment variable that overrides the Cursor ACP command.
// Format: "binary arg1 arg2 ..." or just "binary" (args default to "acp").
const envACPCursorCmd = "PI_ACP_CURSOR_CMD"

// Start launches the Cursor CLI subprocess and begins the ACP flow, delegating
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

// resolveCommand determines the binary and argument list for the Cursor ACP
// subprocess, honoring (in order) an explicit Binary, a test Command override,
// the PI_ACP_CURSOR_CMD env override, and finally DefaultBinaryPaths.
func (r Runner) resolveCommand(req RunRequest) (string, []string, error) {
	binary := r.Binary
	var cmdArgs []string

	envCmd := os.Getenv(envACPCursorCmd)
	switch {
	case binary != "":
		found, err := findBinary([]string{binary})
		if err != nil {
			return "", nil, fmt.Errorf("finding %s: %w", BinaryName, err)
		}
		binary = found
		cmdArgs = buildArgs(req, true)
	case len(req.Command) > 0:
		binary = req.Command[0]
		// Caller supplied a full argv; honor it verbatim so tests can point
		// at a helper process without having the `acp` subcommand injected.
		cmdArgs = append([]string(nil), req.Command[1:]...)
	case envCmd != "":
		// PI_ACP_CURSOR_CMD overrides the default binary.
		// Parse "binary arg1 arg2 ..." from the env var.
		parts := strings.Fields(envCmd)
		if len(parts) > 0 {
			binary = parts[0]
			// If only one word (just the binary), use default "acp" subcommand.
			// Otherwise use the remaining parts as args.
			if len(parts) > 1 {
				cmdArgs = parts[1:]
			} else {
				cmdArgs = buildArgs(req, true)
			}
		}
	default:
		found, err := findBinary(DefaultBinaryPaths)
		if err != nil {
			return "", nil, fmt.Errorf("finding %s: %w", BinaryName, err)
		}
		binary = found
		cmdArgs = buildArgs(req, true)
	}
	return binary, cmdArgs, nil
}

// buildArgs constructs the CLI argument list for launching Cursor in ACP mode.
// When includeSubcommand is false the caller has already provided a command
// that should be invoked verbatim (e.g. a test helper process).
func buildArgs(req RunRequest, includeSubcommand bool) []string {
	var args []string
	if req.Endpoint != "" {
		args = append(args, "-e", req.Endpoint)
	}
	if req.APIKey != "" {
		args = append(args, "--api-key", req.APIKey)
	}
	if req.AuthToken != "" {
		args = append(args, "--auth-token", req.AuthToken)
	}
	if includeSubcommand {
		args = append(args, ACPSubcommand)
	}
	return args
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
