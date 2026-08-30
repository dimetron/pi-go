// Package server implements an HTTP A2A (Agent-to-Agent) server for pi.
//
// It mirrors the ACP server in internal/acp/server: the same pi runtime
// (PromptHandler) is exposed over the A2A protocol instead of ACP. The server
// serves an agent card at /.well-known/agent-card.json and accepts JSON-RPC
// A2A message requests on the root path.
package server

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

// ServeConfig configures a server.Serve run.
type ServeConfig struct {
	// Addr is the HTTP listen address. Default ":8085".
	Addr string
	// Handler processes one A2A prompt turn. Required.
	Handler acpserver.PromptHandler
	// CWD is the working directory for sessions. Empty means current dir.
	CWD string
	// Logger receives diagnostic output. Optional.
	Logger *slog.Logger
}

// Serve runs pi as an A2A agent over HTTP and returns when ctx is canceled.
// Serve does not close the listener; callers own it.
func Serve(ctx context.Context, cfg ServeConfig) error {
	if cfg.Handler == nil {
		return fmt.Errorf("a2a serve: handler is required")
	}
	addr := cfg.Addr
	if addr == "" {
		addr = ":8085"
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("a2a serve: listen %s: %w", addr, err)
	}

	card := buildCard(ln.Addr().String())
	executor := &piExecutor{handler: cfg.Handler, cwd: cfg.CWD, logger: logger}
	handler := a2asrv.NewHandler(executor,
		a2asrv.WithCapabilityChecks(&a2a.AgentCapabilities{Streaming: true, ExtendedAgentCard: true}),
		a2asrv.WithExtendedAgentCard(card),
		a2asrv.WithLogger(logger),
	)

	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: mux}
	errCh := make(chan error, 1)
	go func() {
		logger.Log(ctx, slog.LevelInfo, "a2a-server: listening", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("a2a serve: %w", err)
		}
		return nil
	}
}

// piExecutor implements a2asrv.AgentExecutor by running one pi prompt turn per
// A2A SendMessage. The A2A context ID is used as the pi session ID so follow-up
// messages sharing a context reuse the same pi session and conversation history.
type piExecutor struct {
	handler acpserver.PromptHandler
	cwd     string
	logger  *slog.Logger
}

func (e *piExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		prompt := extractText(execCtx.Message)
		if strings.TrimSpace(prompt) == "" {
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed,
				a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("empty prompt"))), nil)
			return
		}

		result, err := e.handler(ctx, acpserver.PromptTurn{
			SessionID: execCtx.ContextID,
			CWD:       e.cwd,
			Prompt:    prompt,
			Updater:   nil,
		})
		if err != nil {
			e.logger.Log(ctx, slog.LevelError, "a2a-server: prompt failed",
				"task", execCtx.TaskID, "err", err)
			yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed,
				a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(err.Error()))), nil)
			return
		}

		if !yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(result.FinalText)), nil) {
			return
		}
	}
}

func (e *piExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

// extractText concatenates the text of every non-empty content part of a
// message. Returns "" for nil messages or messages with no text.
func extractText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Parts {
		if s := part.Text(); s != "" {
			sb.WriteString(s)
		}
	}
	return sb.String()
}

// buildCard returns the agent card advertised by the server. The card carries
// one skill so clients that reject empty skill lists (a2a-go's pbconv) can
// still parse it.
func buildCard(addr string) *a2a.AgentCard {
	url := "http://" + addr
	return &a2a.AgentCard{
		Name:        "pi-go",
		Description: "pi-go coding agent",
		Version:     "1.0.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(url, a2a.TransportProtocolJSONRPC),
		},
		Capabilities: a2a.AgentCapabilities{
			Streaming:         true,
			ExtendedAgentCard: true,
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Skills: []a2a.AgentSkill{{
			ID:          "pi-go",
			Name:        "pi-go",
			Description: "Coding and Kubernetes assistant",
			Tags:        []string{"coding", "kubernetes"},
		}},
	}
}
