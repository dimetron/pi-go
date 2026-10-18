package cli

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	a2aserver "github.com/dimetron/pi-go/internal/a2a/server"
	acpserver "github.com/dimetron/pi-go/internal/acp/server"
	"github.com/dimetron/pi-go/internal/kagent"
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

	// The A2A context id is the pi session id, and the transcript behind it
	// is persisted, so a conversation survives Substrate replacing the actor:
	// the next message on the same context resumes it from the durable dir.
	rt := acpserver.RuntimeConfig{
		Model:    model,
		BaseURL:  baseURL,
		Headers:  flagHeaders,
		Insecure: flagInsecure,
		System:   system,
		Version:  versionString(),
		// This server is the kagent harness, so the session gets the kagent
		// control-plane tools. They are inert unless KAGENT_API_URL is set,
		// which the kagent compiler injects.
		KagentTools: true,
	}
	if sessionSvc := openServerSessionStore(ctx, logger); sessionSvc != nil {
		rt.SessionService = sessionSvc
	}
	handler := acpserver.NewPromptHandler(rt)

	addr := flagA2AAddr
	if addr == "" {
		addr = ":" + os.Getenv("PORT")
		if os.Getenv("PORT") == "" {
			addr = ":8085"
		}
	}

	// Behind the kagent gateway the task state must live where the gateway
	// reads it, or a correct turn still fails with "task not found". The
	// controller address comes from the deployment; when it is absent this runs
	// as a standalone A2A server with an in-memory store.
	var tasks a2aserver.TaskStore
	if tasksURL := kagentTaskStoreAddress(); tasksURL != "" {
		store, err := kagent.New(kagent.Config{Address: tasksURL})
		if err != nil {
			return fmt.Errorf("a2a server: %w", err)
		}
		defer func() { _ = store.Close() }()
		tasks = store
	}

	if err := a2aserver.Serve(ctx, a2aserver.ServeConfig{
		Addr:      addr,
		ReadyAddr: flagA2AReadyAddr,
		Handler:   handler,
		TaskStore: tasks,
		Logger:    logger,
	}); err != nil {
		if f != nil {
			_, _ = io.WriteString(f, fmt.Sprintf("%v\n", err))
		}
		return fmt.Errorf("a2a server: %w", err)
	}
	return nil
}

// kagentTaskStoreAddress returns the controller gRPC address for task
// persistence, or "" to keep tasks in memory.
//
// KAGENT_TASK_STORE_ADDRESS is the only source, deliberately. It used to fall
// back to `control_plane_url` inside KAGENT_CONFIG_JSON, which could never
// resolve: that blob is the ADK agent config (model, instruction, session DB —
// see go/adk/pkg/config/config_materialize.go upstream) and has never carried
// a URL. The fallback read as "task persistence is automatic" when in fact
// nothing set the one variable that works.
//
// Persistence is opt-in because the service it targets is not universal:
// kagent's current gateway writes task state into its own Postgres from the
// runtime's A2A event stream (a2agateway/gateway.go storeEvent →
// StoreAgentInstanceTaskEvent) and does not serve kagent.runtime.v1
// TaskStoreService at all. Pointing this at such a controller turns a working
// turn into a failed RPC, so the address must be given explicitly, by a
// deployment that knows its controller serves it.
func kagentTaskStoreAddress() string {
	return strings.TrimSpace(os.Getenv("KAGENT_TASK_STORE_ADDRESS"))
}
