package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/spf13/cobra"

	a2aserver "github.com/dimetron/pi-go/internal/a2a/server"
	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

func newA2AServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "a2a",
		Short: "Run pi as an A2A agent over HTTP",
		Long: `Run pi as an A2A (Agent-to-Agent) agent that communicates with an
external A2A client over HTTP JSON-RPC. Use this when another agent or a kagent
harness drives pi through the A2A protocol. The server serves an agent card at
/.well-known/agent-card.json and accepts A2A message requests on the root path.
The server returns when the process receives SIGINT.`,
		Args: cobra.NoArgs,
		RunE: runA2AServer,
	}
	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use for A2A prompt handling")
	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the LLM API endpoint")
	cmd.Flags().StringArrayVar(&flagHeaders, "header", nil, "Extra HTTP header for LLM requests (key=value, repeatable)")
	if f := cmd.Flags().Lookup("header"); f != nil {
		f.NoOptDefVal = ""
	}
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification for LLM API calls")
	cmd.Flags().StringVar(&flagA2AAddr, "addr", "", "HTTP/gRPC listen address (default $PORT or :8085)")
	cmd.Flags().StringVar(&flagA2AReadyAddr, "ready-addr", ":8081", "Readiness listen address")
	return cmd
}

func runA2AServer(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	// Create error log file for a2a-server failures in $HOME/.pi-go/sessions/.
	logDir, err := os.UserHomeDir()
	if err == nil {
		logDir = filepath.Join(logDir, ".pi-go", "sessions")
	}
	errFile := filepath.Join(logDir, "a2a-server.err.log")
	if err == nil {
		_ = os.MkdirAll(logDir, 0o755)
	}
	f, err := os.OpenFile(errFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		defer f.Close()
	}

	// Build a logger that writes INFO+ to the err log file for crash RCA.
	var logger *slog.Logger
	if f != nil {
		logger = slog.New(&logHandler{f: f})
	}

	model := flagModel
	if model == "" {
		model = os.Getenv("PI_MODEL")
	}
	if model == "" {
		model = "glm-5.2:cloud"
	}

	baseURL := flagURL
	if baseURL == "" {
		baseURL = os.Getenv("PI_BASE_URL")
	}
	system := flagSystem
	if system == "" {
		system = os.Getenv("PI_SYSTEM")
	}

	handler := acpserver.NewPromptHandler(acpserver.RuntimeConfig{
		Model:    model,
		BaseURL:  baseURL,
		Headers:  flagHeaders,
		Insecure: flagInsecure,
		System:   system,
	})

	addr := flagA2AAddr
	if addr == "" {
		addr = ":" + os.Getenv("PORT")
		if os.Getenv("PORT") == "" {
			addr = ":8085"
		}
	}

	if err := a2aserver.Serve(ctx, a2aserver.ServeConfig{
		Addr:      addr,
		ReadyAddr: flagA2AReadyAddr,
		Handler:   handler,
		Logger:    logger,
	}); err != nil {
		if f != nil {
			_, _ = io.WriteString(f, fmt.Sprintf("%v\n", err))
		}
		return fmt.Errorf("a2a server: %w", err)
	}
	return nil
}
