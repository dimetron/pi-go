// Package agy provides an ACP client for Google Antigravity via its
// standalone ACP server binary (`agy_acp_server`). The ACP session lifecycle —
// connection wiring, stderr streaming, event translation and result capture —
// is provided by the shared client package; this package only resolves the
// Antigravity ACP server binary and builds its argument list.
//
// The command shape mirrors the `antigravity-acp` entry in the Agent Client
// Protocol registry (https://github.com/agentclientprotocol/registry):
// the platform archive ships a single executable (`agy_acp_server.par`, or
// `agy_acp_server.exe` on Windows) that speaks ACP over stdio, taking
// `--uid=` on Linux and no arguments elsewhere.
//
// Unlike claude/gemini/cursor/copilot, the binary is not the vendor's
// interactive CLI: the `agy` CLI has no ACP mode. Install the ACP server with
// scripts/install-agy-acp.sh, which extracts it to ~/.pi-go/acp/agy.
package agy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"

	shared "github.com/dimetron/pi-go/internal/acp"
	client "github.com/dimetron/pi-go/internal/acp/client"
)

// BinaryName is the name of the Antigravity ACP server executable for the
// running platform.
var BinaryName = binaryName()

// rpcTimeout caps Initialize and NewSession against a hung subprocess. See
// claudecode for the rationale; 60s is enough for a normal startup but short
// enough to surface a missing login or missing binary quickly.
const rpcTimeout = 60 * time.Second

// DefaultBinaryPaths lists the locations searched for the Antigravity ACP
// server, in order. The first entry is the directory install-agy-acp.sh extracts
// the registry archive into.
var DefaultBinaryPaths = defaultBinaryPaths()

// DefaultArgs are the platform-specific arguments the registry entry declares
// for the ACP server. Linux builds require an explicit (empty) --uid.
var DefaultArgs = defaultArgs()

// Runner launches the Antigravity ACP server and manages the client-side
// connection.
//
// The server does not inherit the agy CLI's login: it refuses session/new
// until an auth method is selected in ~/.gemini/antigravity-acp/settings.json
// ("auth": {"type": "oauth-personal" | "gemini-api-key" | "oauth-business" |
// "agent-platform"}), and oauth-personal additionally needs a one-time browser
// login. That first login is interactive, so it must be done by hand before a
// subagent turn will run.
type Runner struct {
	// ClientInfo identifies this client to the agent.
	ClientInfo acp.Implementation

	// Binary is the path to the agy ACP server binary.
	// If empty, uses the first found in DefaultBinaryPaths.
	Binary string

	// Logger for connection debugging.
	Logger *slog.Logger

	// ExtraEnv is additional environment variables to pass to the subprocess.
	ExtraEnv []string
}

// RunRequest describes an Antigravity prompt turn.
type RunRequest struct {
	Prompt    string   // Prompt to send to Antigravity
	SessionID string   // Optional session ID to resume
	CWD       string   // Working directory
	Env       []string // Additional environment variables
	Command   []string // Optional command override for testing (defaults to the ACP server)
}

// envACPAgyCmd is the environment variable that overrides the Antigravity ACP
// command. Format: "binary arg1 arg2 ..." or just "binary" (args default to
// the platform's DefaultArgs).
const envACPAgyCmd = "PI_ACP_AGY_CMD"

// Start launches the Antigravity ACP server and begins the ACP flow,
// delegating the session lifecycle to the shared client runner.
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

// resolveCommand determines the binary and argument list for the Antigravity
// ACP subprocess, honoring (in order) an explicit Binary, a test Command
// override, the PI_ACP_AGY_CMD env override, and finally DefaultBinaryPaths.
func (r Runner) resolveCommand(req RunRequest) (string, []string, error) {
	binary := r.Binary
	var cmdArgs []string

	// When req.Command is supplied (test override) honor it verbatim — no
	// default flag injection — so helper processes can be exercised without
	// the real ACP server argument shape. This mirrors the other adapters.
	if binary == "" && len(req.Command) > 0 {
		binary = req.Command[0]
		cmdArgs = append([]string(nil), req.Command[1:]...)
		return binary, cmdArgs, nil
	}

	if binary == "" {
		if envCmd := os.Getenv(envACPAgyCmd); envCmd != "" {
			// PI_ACP_AGY_CMD overrides the default binary. Parse
			// "binary arg1 arg2 ..." from the env var; a bare binary
			// falls back to the platform's default arguments.
			parts := strings.Fields(envCmd)
			if len(parts) > 0 {
				binary = parts[0]
				if len(parts) > 1 {
					return binary, parts[1:], nil
				}
			}
		} else {
			found, err := findBinary(DefaultBinaryPaths)
			if err != nil {
				return "", nil, fmt.Errorf("finding %s: %w", BinaryName, err)
			}
			binary = found
		}
	}
	return binary, append(cmdArgs, DefaultArgs...), nil
}

// binaryName returns the ACP server executable name for the running platform,
// matching the "cmd" field of the registry entry.
func binaryName() string {
	if runtime.GOOS == "windows" {
		return "agy_acp_server.exe"
	}
	return "agy_acp_server.par"
}

// defaultArgs returns the platform-specific argument list from the registry
// entry. Only the Linux builds declare arguments.
func defaultArgs() []string {
	if runtime.GOOS == "linux" {
		return []string{"--uid="}
	}
	return nil
}

// defaultBinaryPaths returns the search order for the ACP server binary: the
// pi-go install directory first, then Antigravity's own directory, then the
// usual bin directories and PATH.
func defaultBinaryPaths() []string {
	name := binaryName()
	paths := []string{name}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths,
			filepath.Join(home, ".pi-go", "acp", "agy", name),
			filepath.Join(home, ".antigravity", "acp", name),
			filepath.Join(home, ".local", "bin", name),
		)
	}
	return append(paths, filepath.Join("/usr/local/bin", name))
}

// findBinary returns the first existing entry in paths, resolving bare names
// via PATH and absolute/relative paths via stat.
func findBinary(paths []string) (string, error) {
	for _, path := range paths {
		if path == "" {
			continue
		}
		// Check if it's a full path that exists
		if filepath.IsAbs(path) || strings.HasPrefix(path, ".") {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
			continue
		}
		// Try using LookPath for commands in PATH
		if fullPath, err := exec.LookPath(path); err == nil {
			return fullPath, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH or default locations", BinaryName)
}
