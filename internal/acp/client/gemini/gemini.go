// Package gemini provides an ACP client for Google Gemini CLI via the Gemini
// CLI subprocess adapter. The ACP session lifecycle — connection wiring, stderr
// streaming, event translation and result capture — is provided by the shared
// client package; this package only resolves the Gemini binary and builds its
// ACP-mode argument list.
package gemini

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

// BinaryName is the expected name of the Gemini CLI binary.
const BinaryName = "gemini"

// rpcTimeout caps Initialize and NewSession against a hung subprocess. See
// claudecode for the rationale; 60s is enough for a normal startup but short
// enough to surface a missing API key or missing binary quickly.
const rpcTimeout = 60 * time.Second

// DefaultBinaryPaths lists common installation locations for gemini CLI.
var DefaultBinaryPaths = []string{
	"gemini",
	".local/bin/gemini",
	"/usr/local/bin/gemini",
	"/usr/bin/gemini",
}

// Runner launches Gemini CLI via the gemini subprocess adapter
// and manages the ACP client-side connection.
type Runner struct {
	// ClientInfo identifies this client to the agent.
	ClientInfo acp.Implementation

	// Binary is the path to the gemini binary.
	// If empty, uses the first found in DefaultBinaryPaths.
	Binary string

	// Logger for connection debugging.
	Logger *slog.Logger

	// ExtraEnv is additional environment variables to pass to the subprocess.
	ExtraEnv []string
}

// RunRequest describes a Gemini CLI prompt turn.
type RunRequest struct {
	Prompt    string   // Prompt to send to Gemini CLI
	SessionID string   // Optional session ID to resume
	CWD       string   // Working directory
	Env       []string // Additional environment variables
	Command   []string // Optional command override for testing (defaults to gemini)

	// Options for Gemini CLI
	Model   string // Optional model override (e.g., "gemini-2.5-flash")
	Sandbox string // Optional sandbox mode (e.g., "no-sandbox", "docker")
	Debug   bool   // Enable debug output
}

// envACPGeminiCmd is the environment variable that overrides the Gemini ACP command.
// Format: "binary arg1 arg2 ..." or just "binary" (args default to "--acp").
const envACPGeminiCmd = "PI_ACP_GEMINI_CMD"

// Start launches the Gemini CLI subprocess and begins the ACP flow, delegating
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

// resolveCommand determines the binary and argument list for the Gemini ACP
// subprocess, honoring (in order) an explicit Binary, a test Command override,
// the PI_ACP_GEMINI_CMD env override, and finally DefaultBinaryPaths.
func (r Runner) resolveCommand(req RunRequest) (string, []string, error) {
	binary := r.Binary
	var cmdArgs []string

	// When req.Command is supplied (test override) honor it verbatim — no
	// --acp injection and no optional flag appending, so helper processes
	// can be exercised without the real gemini CLI argument shape. This
	// mirrors claude/cursor behavior.
	if binary == "" && len(req.Command) > 0 {
		binary = req.Command[0]
		cmdArgs = append([]string(nil), req.Command[1:]...)
		return binary, cmdArgs, nil
	}

	if binary == "" {
		if envCmd := os.Getenv(envACPGeminiCmd); envCmd != "" {
			// PI_ACP_GEMINI_CMD overrides the default binary. Parse
			// "binary arg1 arg2 ..." from the env var; a bare binary
			// falls back to the default --acp subcommand.
			parts := strings.Fields(envCmd)
			if len(parts) > 0 {
				binary = parts[0]
				if len(parts) > 1 {
					cmdArgs = parts[1:]
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
	if len(cmdArgs) == 0 {
		cmdArgs = []string{"--acp"}
	}
	if req.Model != "" {
		cmdArgs = append(cmdArgs, "--model", req.Model)
	}
	if req.Sandbox != "" {
		cmdArgs = append(cmdArgs, "--sandbox", req.Sandbox)
	}
	if req.Debug {
		cmdArgs = append(cmdArgs, "--debug")
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
