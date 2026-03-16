// Package rpc implements a Unix socket JSON-RPC server for pi-go.
// It accepts JSON-RPC requests and streams JSONL events back to clients,
// enabling editor/IDE integration.
package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/dimetron/pi-go/internal/agent"
)

// Request is a JSON-RPC request from the client.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	ID      any             `json:"id"`
}

// Response is a JSON-RPC response to the client.
type Response struct {
	JSONRPC string `json:"jsonrpc"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
	ID      any    `json:"id"`
}

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Event is a JSONL streaming event (same schema as JSON mode).
type Event struct {
	Type      string `json:"type"`
	Agent     string `json:"agent,omitempty"`
	Role      string `json:"role,omitempty"`
	Delta     string `json:"delta,omitempty"`
	Content   string `json:"content,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	ToolInput any    `json:"tool_input,omitempty"`
}

// PromptParams are the parameters for the "prompt" method.
type PromptParams struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty"`
}

// SessionResult is the result of session-related methods.
type SessionResult struct {
	SessionID string   `json:"session_id,omitempty"`
	Sessions  []string `json:"sessions,omitempty"`
}

// Config holds the RPC server configuration.
type Config struct {
	Agent      *agent.Agent
	SocketPath string
}

// Server is a Unix socket JSON-RPC server.
type Server struct {
	agent      *agent.Agent
	socketPath string
	listener   net.Listener
	wg         sync.WaitGroup
}

// NewServer creates a new RPC server.
func NewServer(cfg Config) *Server {
	return &Server{
		agent:      cfg.Agent,
		socketPath: cfg.SocketPath,
	}
}

// Run starts the server and blocks until shutdown.
// It listens on the Unix socket and handles client connections concurrently.
// Graceful shutdown occurs on SIGTERM or SIGINT, or when ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	// Remove stale socket file if it exists.
	_ = os.Remove(s.socketPath)

	var err error
	s.listener, err = net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.socketPath, err)
	}
	defer s.listener.Close()
	defer os.Remove(s.socketPath)

	fmt.Fprintf(os.Stderr, "pi-go: RPC server listening on %s\n", s.socketPath)

	// Handle graceful shutdown.
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	go func() {
		<-ctx.Done()
		s.listener.Close()
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				// Graceful shutdown — wait for active connections.
				s.wg.Wait()
				return nil
			default:
				return fmt.Errorf("accepting connection: %w", err)
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

// handleConn reads JSON-RPC requests from a connection and dispatches them.
// Each connection can send multiple requests (newline-delimited JSON).
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)

	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			// Connection closed or invalid JSON — just return.
			return
		}

		if req.JSONRPC == "" {
			req.JSONRPC = "2.0"
		}

		switch req.Method {
		case "prompt":
			s.handlePrompt(ctx, conn, enc, req)
		case "session.create":
			s.handleSessionCreate(ctx, enc, req)
		case "session.list":
			s.handleSessionList(ctx, enc, req)
		default:
			_ = enc.Encode(Response{
				JSONRPC: "2.0",
				Error:   &Error{Code: -32601, Message: "method not found: " + req.Method},
				ID:      req.ID,
			})
		}
	}
}

// handlePrompt processes a prompt request by running the agent and streaming events.
func (s *Server) handlePrompt(ctx context.Context, _ net.Conn, enc *json.Encoder, req Request) {
	var params PromptParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		_ = enc.Encode(Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: -32602, Message: "invalid params: " + err.Error()},
			ID:      req.ID,
		})
		return
	}

	if params.Text == "" {
		_ = enc.Encode(Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: -32602, Message: "params.text is required"},
			ID:      req.ID,
		})
		return
	}

	// Create a session if not provided.
	sessionID := params.SessionID
	if sessionID == "" {
		var err error
		sessionID, err = s.agent.CreateSession(ctx)
		if err != nil {
			_ = enc.Encode(Response{
				JSONRPC: "2.0",
				Error:   &Error{Code: -32000, Message: "creating session: " + err.Error()},
				ID:      req.ID,
			})
			return
		}
	}

	// Send initial response with session ID to acknowledge the request.
	_ = enc.Encode(Response{
		JSONRPC: "2.0",
		Result:  SessionResult{SessionID: sessionID},
		ID:      req.ID,
	})

	// Stream JSONL events (same schema as JSON mode).
	started := false

	for ev, err := range s.agent.Run(ctx, sessionID, params.Text) {
		if err != nil {
			_ = enc.Encode(Event{Type: "error", Content: err.Error()})
			break
		}
		if ev == nil || ev.Content == nil {
			continue
		}

		if !started {
			_ = enc.Encode(Event{
				Type:  "message_start",
				Agent: ev.Author,
				Role:  string(ev.Content.Role),
			})
			started = true
		}

		for _, part := range ev.Content.Parts {
			if part.Text != "" {
				_ = enc.Encode(Event{
					Type:  "text_delta",
					Agent: ev.Author,
					Delta: part.Text,
				})
			}
			if part.FunctionCall != nil {
				_ = enc.Encode(Event{
					Type:      "tool_call",
					Agent:     ev.Author,
					ToolName:  part.FunctionCall.Name,
					ToolInput: part.FunctionCall.Args,
				})
			}
			if part.FunctionResponse != nil {
				_ = enc.Encode(Event{
					Type:     "tool_result",
					Agent:    ev.Author,
					ToolName: part.FunctionResponse.Name,
					Content:  fmt.Sprintf("%v", part.FunctionResponse.Response),
				})
			}
		}
	}

	_ = enc.Encode(Event{Type: "message_end"})
}

// handleSessionCreate creates a new session and returns the ID.
func (s *Server) handleSessionCreate(ctx context.Context, enc *json.Encoder, req Request) {
	sessionID, err := s.agent.CreateSession(ctx)
	if err != nil {
		_ = enc.Encode(Response{
			JSONRPC: "2.0",
			Error:   &Error{Code: -32000, Message: "creating session: " + err.Error()},
			ID:      req.ID,
		})
		return
	}

	_ = enc.Encode(Response{
		JSONRPC: "2.0",
		Result:  SessionResult{SessionID: sessionID},
		ID:      req.ID,
	})
}

// handleSessionList is a placeholder for listing sessions.
func (s *Server) handleSessionList(_ context.Context, enc *json.Encoder, req Request) {
	// Session listing requires access to the session service directly.
	// For now, return an empty list. Full implementation needs service exposure.
	_ = enc.Encode(Response{
		JSONRPC: "2.0",
		Result:  SessionResult{Sessions: []string{}},
		ID:      req.ID,
	})
}
