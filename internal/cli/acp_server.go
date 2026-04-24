package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	acp "github.com/coder/acp-go-sdk"
	"github.com/spf13/cobra"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
	pisession "github.com/dimetron/pi-go/internal/session"
)

func newACPServerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "acp-server",
		Short: "Run pi as an ACP agent over stdio",
		Long: `Run pi as an ACP (Agent Client Protocol) agent that communicates with an
external ACP client over stdin/stdout. Use this when another tool drives pi
through the ACP protocol. The server returns when the peer disconnects or the
process receives SIGINT.`,
		Args: cobra.NoArgs,
		RunE: runACPServer,
	}
	cmd.Flags().StringVar(&flagModel, "model", "", "LLM model to use for ACP prompt handling")
	cmd.Flags().StringVar(&flagURL, "url", "", "Alternative base URL for the LLM API endpoint")
	cmd.Flags().StringArrayVar(&flagHeaders, "header", nil, "Extra HTTP header for LLM requests (key=value, repeatable)")
	if f := cmd.Flags().Lookup("header"); f != nil {
		f.NoOptDefVal = ""
	}
	cmd.Flags().BoolVar(&flagInsecure, "insecure", false, "Skip TLS certificate verification for LLM API calls")
	return cmd
}

func runACPServer(cmd *cobra.Command, _ []string) error {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt)
	defer stop()

	// Create error log file for acp-server failures in $HOME/.pi-go/sessions/.
	logDir, err := os.UserHomeDir()
	if err == nil {
		logDir = filepath.Join(logDir, ".pi-go", "sessions")
	}
	errFile := filepath.Join(logDir, "acp-server.err.log")
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

	sessionSvc, err := pisession.NewFileService(logDir)
	if err != nil && f != nil {
		_, _ = io.WriteString(f, fmt.Sprintf("session service: %v\n", err))
	}

	model := flagModel
	if model == "" {
		model = "minimax-m2.7:cloud"
	}

	agent := &acpserver.Agent{
		AgentInfo:                 acp.Implementation{Name: "pi-go", Version: Version},
		AvailableCommandsResolver: acpserver.DiscoverAvailableCommands,
		Handler: acpserver.NewPromptHandler(acpserver.RuntimeConfig{
			Model:          model,
			BaseURL:        flagURL,
			Headers:        flagHeaders,
			Insecure:       flagInsecure,
			System:         flagSystem,
			SessionService: sessionSvc,
		}),
		Logger:         logger,
		SessionService: sessionSvc,
	}
	if err := acpserver.Serve(ctx, acpserver.ServeConfig{
		Agent: agent,
		In:    os.Stdin,
		Out:   os.Stdout,
	}); err != nil {
		if f != nil {
			_, _ = io.WriteString(f, fmt.Sprintf("%v\n", err))
		}
		return fmt.Errorf("acp server: %w", err)
	}
	return nil
}

// logHandler writes slog records to a file.
type logHandler struct {
	f io.Writer
}

func (h *logHandler) Handle(_ context.Context, r slog.Record) error {
	_, err := fmt.Fprintf(h.f, "%s\t%s\t%s\n", r.Time.Format("2006-01-02T15:04:05.000"), r.Level, r.Message)
	return err
}

func (h *logHandler) WithAttrs(_ []slog.Attr) slog.Handler         { return h }
func (h *logHandler) WithGroup(_ string) slog.Handler              { return h }
func (h *logHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
