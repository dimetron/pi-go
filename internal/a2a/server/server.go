// Package server implements an HTTP/gRPC A2A (Agent-to-Agent) server for pi.
//
// It mirrors the ACP server in internal/acp/server: the same pi runtime
// (PromptHandler) is exposed over the A2A protocol instead of ACP. The server
// serves an agent card at /.well-known/agent-card.json and accepts JSON-RPC
// A2A message requests on the root path, plus gRPC A2A on the same port
// (HTTP/2 with application/grpc content type), matching the kagent Substrate
// runtime contract.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2agrpc "github.com/a2aproject/a2a-go/v2/a2agrpc/v1"
	a2apb "github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	a2ataskstore "github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"

	acpserver "github.com/dimetron/pi-go/internal/acp/server"
)

// ServeConfig configures a server.Serve run.
type ServeConfig struct {
	// Addr is the HTTP/gRPC listen address. Default ":8085".
	Addr string
	// ReadyAddr is the readiness listen address. Default ":8081".
	ReadyAddr string
	// Handler processes one A2A prompt turn. Required.
	Handler acpserver.PromptHandler
	// CWD is the working directory for sessions. Empty means current dir.
	CWD string
	// Logger receives diagnostic output. Optional.
	Logger *slog.Logger
}

// Serve runs pi as an A2A agent over HTTP/gRPC and returns when ctx is
// canceled. Serve does not close the listeners; callers own them.
func Serve(ctx context.Context, cfg ServeConfig) error {
	if cfg.Handler == nil {
		return fmt.Errorf("a2a serve: handler is required")
	}
	addr := cfg.Addr
	if addr == "" {
		addr = ":8085"
	}
	readyAddr := cfg.ReadyAddr
	if readyAddr == "" {
		readyAddr = ":8081"
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

	// In-memory task store so GetTask/SubscribeToTask and continuation
	// messages keep working for stateless runtime turn processing. The seed
	// interceptor mirrors kagent's Go ADK: the kagent gateway assigns the
	// TaskID and forwards the message, so the runtime must seed the task in
	// its local store before the handler runs, or the executor rejects the
	// message with "task not found".
	tasks := a2ataskstore.NewInMemory(&a2ataskstore.InMemoryStoreConfig{
		Authenticator: a2asrv.NewTaskStoreAuthenticator(),
	})
	handler := a2asrv.NewHandler(executor,
		a2asrv.WithCapabilityChecks(&a2a.AgentCapabilities{Streaming: true, ExtendedAgentCard: true}),
		a2asrv.WithExtendedAgentCard(card),
		a2asrv.WithTaskStore(tasks),
		a2asrv.WithLogger(logger),
		a2asrv.WithCallInterceptors(&seedTaskInterceptor{store: tasks}),
	)

	// gRPC A2A server (kagent gateway dials the runtime over gRPC).
	grpcServer := grpc.NewServer()
	a2agrpc.NewHandler(handler).RegisterWith(grpcServer)
	healthServer := health.NewServer()
	healthServer.SetServingStatus(a2apb.A2AService_ServiceDesc.ServiceName, grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// HTTP mux: JSON-RPC A2A + card + health; gRPC requests are routed by
	// content type (HTTP/2 application/grpc), matching kagent's A2AServer.
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(handler))
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handlerMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
			grpcServer.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{Handler: handlerMux, Protocols: protocols}
	errCh := make(chan error, 1)
	go func() {
		logger.Log(ctx, slog.LevelInfo, "a2a-server: listening", "addr", ln.Addr().String())
		errCh <- srv.Serve(ln)
	}()

	// Readiness server on ReadyAddr (Substrate probes /readyz:8081).
	readyLn, err := net.Listen("tcp", readyAddr)
	if err != nil {
		_ = srv.Close()
		return fmt.Errorf("a2a serve: listen ready %s: %w", readyAddr, err)
	}
	readySrv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
	})}
	readyErrCh := make(chan error, 1)
	go func() {
		logger.Log(ctx, slog.LevelInfo, "a2a-server: ready listening", "addr", readyLn.Addr().String())
		readyErrCh <- readySrv.Serve(readyLn)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = readySrv.Shutdown(shutdownCtx)
		grpcServer.GracefulStop()
		return ctx.Err()
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("a2a serve: %w", err)
		}
		return nil
	case err := <-readyErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("a2a serve ready: %w", err)
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

		// When the gateway pre-seeds the task (seedTaskInterceptor), the
		// manager already holds a stored task and rejects a bare Message
		// event ("message not allowed after task was stored"). When no task
		// was stored, the manager requires the first event to be a Task.
		// Emit the same sequence kagent's ADK executor and a2a-go's own
		// exec executor emit: Task (if needed) → Working → streamed
		// artifacts → Completed.
		if execCtx.StoredTask == nil {
			if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
				return
			}
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}

		// Run the prompt turn in a goroutine and stream its ACP session
		// updates back as A2A artifact events. The adapter.Stream classifies
		// ADK events (thought vs text vs tool calls) and calls the updater,
		// which pushes A2A events onto a channel this iterator drains and
		// yields — so thinking and tool calls reach the UI as they happen,
		// not in one artifact at the end of the turn.
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		updater := newA2AUpdater(runCtx, execCtx)

		type turnResult struct {
			result acpserver.PromptResult
			err    error
		}
		resultCh := make(chan turnResult, 1)
		go func() {
			var res turnResult
			defer func() {
				if r := recover(); r != nil {
					e.logger.Log(ctx, slog.LevelError, "a2a-server: prompt panic",
						"task", execCtx.TaskID, "panic", r)
					res = turnResult{err: fmt.Errorf("handler panicked: %v", r)}
				}
				close(updater.events)
				resultCh <- res
			}()
			res.result, res.err = e.handler(runCtx, acpserver.PromptTurn{
				SessionID: execCtx.ContextID,
				CWD:       e.cwd,
				Prompt:    prompt,
				Updater:   updater,
			})
			// Settle the artifact left open by the last text chunk: the
			// closing frame replaces it with the whole text and marks it
			// the last chunk, matching what kagent's ADK runtime emits.
			if res.err == nil {
				if err := updater.closeTextArtifacts(runCtx); err != nil {
					e.logger.Log(ctx, slog.LevelWarn, "a2a-server: closing artifacts",
						"task", execCtx.TaskID, "err", err)
				}
			}
		}()

		for {
			select {
			case ev, ok := <-updater.events:
				if !ok {
					res := <-resultCh
					if res.err != nil {
						e.logger.Log(ctx, slog.LevelError, "a2a-server: prompt failed",
							"task", execCtx.TaskID, "err", res.err)
						yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed,
							a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(res.err.Error()))), nil)
						return
					}
					// The reply was already streamed as chunks; a final
					// artifact would duplicate it. Emit one only when the
					// turn produced text without streaming it.
					if !updater.streamedText() && strings.TrimSpace(res.result.FinalText) != "" {
						if !yield(a2a.NewArtifactEvent(execCtx, a2a.NewTextPart(res.result.FinalText)), nil) {
							return
						}
					}
					if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, nil), nil) {
						return
					}
					return
				}
				if !yield(ev, nil) {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}
}

func (e *piExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

// seedTaskInterceptor seeds a gateway-assigned task in the local in-memory
// store before an A2A SendMessage is processed. kagent's gateway creates the
// task in its own database and assigns the TaskID; the runtime's local store
// then sees a continuation it has never heard of and would reject it. The
// stored task (in InputRequired/AuthRequired state) travels in the message
// metadata; when present, this interceptor replays it into the local store.
type seedTaskInterceptor struct {
	a2asrv.PassthroughCallInterceptor
	store a2ataskstore.Store
}

// takenStoredTask is the kagent private continuation metadata key.
const storedTaskMetadataKey = "https://kagent.dev/internal/stored-task/v1"

// takeStoredTask consumes the kagent gateway's stored-task metadata from a
// message, mirroring kagent's go/api/a2a.TakeStoredTask. Returns nil when the
// key is absent.
func takeStoredTask(message *a2a.Message) (*a2a.Task, error) {
	if message == nil || message.Metadata == nil {
		return nil, nil
	}
	raw, ok := message.Metadata[storedTaskMetadataKey]
	if !ok {
		return nil, nil
	}
	delete(message.Metadata, storedTaskMetadataKey)
	stateMap, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid stored task state")
	}
	taskState, _ := stateMap["state"].(string)
	switch a2a.TaskState(taskState) {
	case a2a.TaskStateInputRequired, a2a.TaskStateAuthRequired:
	default:
		return nil, fmt.Errorf("invalid stored task state %q", taskState)
	}
	var statusMessage *a2a.Message
	if rawMessage, ok := stateMap["message"]; ok && rawMessage != nil {
		encoded, err := json.Marshal(rawMessage)
		if err != nil {
			return nil, fmt.Errorf("decode stored status message: %w", err)
		}
		statusMessage = &a2a.Message{}
		if err := json.Unmarshal(encoded, statusMessage); err != nil {
			return nil, fmt.Errorf("decode stored status message: %w", err)
		}
	}
	return &a2a.Task{
		ID:        message.TaskID,
		ContextID: message.ContextID,
		Status:    a2a.TaskStatus{State: a2a.TaskState(taskState), Message: statusMessage},
	}, nil
}

// Before implements a2asrv.CallInterceptor.
func (i *seedTaskInterceptor) Before(ctx context.Context, _ *a2asrv.CallContext, req *a2asrv.Request) (context.Context, any, error) {
	if req == nil {
		return ctx, nil, nil
	}
	send, ok := req.Payload.(*a2a.SendMessageRequest)
	if !ok || send.Message == nil || send.Message.TaskID == "" {
		return ctx, nil, nil
	}
	storedTask, err := takeStoredTask(send.Message)
	if err != nil {
		return ctx, nil, err
	}
	if _, err := i.store.Get(ctx, send.Message.TaskID); err == nil {
		return ctx, nil, nil
	} else if !errors.Is(err, a2a.ErrTaskNotFound) {
		return ctx, nil, fmt.Errorf("load actor task: %w", err)
	}
	if storedTask == nil {
		storedTask = a2a.NewSubmittedTask(send.Message, send.Message)
	}
	storedTask.ID = send.Message.TaskID
	storedTask.ContextID = send.Message.ContextID
	if _, err := i.store.Create(ctx, storedTask); err != nil && !errors.Is(err, a2ataskstore.ErrTaskAlreadyExists) {
		return ctx, nil, fmt.Errorf("seed actor task: %w", err)
	}
	return ctx, nil, nil
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

// buildCard returns the agent card advertised by the server. When the kagent
// compiler injects KAGENT_AGENT_CARD_JSON, that card is used verbatim (it
// carries the compiled template identity); otherwise a built-in card with one
// skill is used so clients that reject empty skill lists (a2a-go's pbconv) can
// still parse it.
func buildCard(addr string) *a2a.AgentCard {
	if raw := strings.TrimSpace(os.Getenv("KAGENT_AGENT_CARD_JSON")); raw != "" {
		var card a2a.AgentCard
		if err := json.Unmarshal([]byte(raw), &card); err == nil {
			return &card
		}
	}
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
