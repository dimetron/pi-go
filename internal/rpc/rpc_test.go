package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

// mockLLM returns a fixed text response.
type mockLLM struct {
	response string
}

func (m *mockLLM) Name() string { return "mock-model" }

func (m *mockLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		resp := &model.LLMResponse{
			Content: genai.NewContentFromText(m.response, genai.RoleModel),
		}
		yield(resp, nil)
	}
}

func newTestAgent(t *testing.T, response string) *agent.Agent {
	t.Helper()
	ag, err := agent.New(agent.Config{
		Model:       &mockLLM{response: response},
		Instruction: "Test agent",
	})
	if err != nil {
		t.Fatalf("creating agent: %v", err)
	}
	return ag
}

func TestServerPromptRoundtrip(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	ag := newTestAgent(t, "Hello from RPC!")

	srv := NewServer(Config{
		Agent:      ag,
		SocketPath: socketPath,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in background.
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	waitForSocket(t, socketPath)

	// Connect client.
	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	defer conn.Close()

	// Send prompt request.
	enc := json.NewEncoder(conn)
	_ = enc.Encode(Request{
		JSONRPC: "2.0",
		Method:  "prompt",
		Params:  json.RawMessage(`{"text":"hello"}`),
		ID:      1,
	})

	// Read responses: first is JSON-RPC ack, then events.
	dec := json.NewDecoder(conn)

	// 1. JSON-RPC response with session ID.
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// 2. message_start event.
	var ev Event
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("decoding event: %v", err)
	}
	if ev.Type != "message_start" {
		t.Errorf("expected message_start, got %s", ev.Type)
	}

	// 3. text_delta event.
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("decoding event: %v", err)
	}
	if ev.Type != "text_delta" {
		t.Errorf("expected text_delta, got %s", ev.Type)
	}
	if ev.Delta != "Hello from RPC!" {
		t.Errorf("expected 'Hello from RPC!', got %q", ev.Delta)
	}

	// 4. message_end event.
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("decoding event: %v", err)
	}
	if ev.Type != "message_end" {
		t.Errorf("expected message_end, got %s", ev.Type)
	}

	cancel()
}

func TestServerSessionCreate(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	ag := newTestAgent(t, "ok")

	srv := NewServer(Config{
		Agent:      ag,
		SocketPath: socketPath,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	_ = enc.Encode(Request{
		JSONRPC: "2.0",
		Method:  "session.create",
		ID:      "create-1",
	})

	dec := json.NewDecoder(conn)
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// Result should contain a session ID.
	result, _ := json.Marshal(resp.Result)
	var sr SessionResult
	_ = json.Unmarshal(result, &sr)
	if sr.SessionID == "" {
		t.Error("expected non-empty session ID")
	}

	cancel()
}

func TestServerMethodNotFound(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	ag := newTestAgent(t, "ok")

	srv := NewServer(Config{
		Agent:      ag,
		SocketPath: socketPath,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	_ = enc.Encode(Request{
		JSONRPC: "2.0",
		Method:  "unknown.method",
		ID:      42,
	})

	dec := json.NewDecoder(conn)
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", resp.Error.Code)
	}

	cancel()
}

func TestServerPromptMissingText(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	ag := newTestAgent(t, "ok")

	srv := NewServer(Config{
		Agent:      ag,
		SocketPath: socketPath,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	_ = enc.Encode(Request{
		JSONRPC: "2.0",
		Method:  "prompt",
		Params:  json.RawMessage(`{"text":""}`),
		ID:      1,
	})

	dec := json.NewDecoder(conn)
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if resp.Error == nil {
		t.Fatal("expected error for empty text")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("expected code -32602, got %d", resp.Error.Code)
	}

	cancel()
}

func TestServerConcurrentSessions(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	ag := newTestAgent(t, "concurrent response")

	srv := NewServer(Config{
		Agent:      ag,
		SocketPath: socketPath,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()
	waitForSocket(t, socketPath)

	// Launch 3 concurrent clients.
	results := make(chan string, 3)
	for i := 0; i < 3; i++ {
		go func(id int) {
			conn, err := net.Dial("unix", socketPath)
			if err != nil {
				results <- fmt.Sprintf("client %d dial error: %v", id, err)
				return
			}
			defer conn.Close()

			enc := json.NewEncoder(conn)
			_ = enc.Encode(Request{
				JSONRPC: "2.0",
				Method:  "prompt",
				Params:  json.RawMessage(fmt.Sprintf(`{"text":"hello %d"}`, id)),
				ID:      id,
			})

			dec := json.NewDecoder(conn)

			// Read response + events until message_end.
			var resp Response
			if err := dec.Decode(&resp); err != nil {
				results <- fmt.Sprintf("client %d decode error: %v", id, err)
				return
			}
			if resp.Error != nil {
				results <- fmt.Sprintf("client %d error: %s", id, resp.Error.Message)
				return
			}

			gotEnd := false
			for {
				var ev Event
				if err := dec.Decode(&ev); err != nil {
					break
				}
				if ev.Type == "message_end" {
					gotEnd = true
					break
				}
			}

			if gotEnd {
				results <- fmt.Sprintf("client %d ok", id)
			} else {
				results <- fmt.Sprintf("client %d missing message_end", id)
			}
		}(i)
	}

	// Collect results.
	for i := 0; i < 3; i++ {
		result := <-results
		if result[len(result)-2:] != "ok" {
			t.Errorf("unexpected result: %s", result)
		}
	}

	cancel()
}

func TestServerGracefulShutdown(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	ag := newTestAgent(t, "ok")

	srv := NewServer(Config{
		Agent:      ag,
		SocketPath: socketPath,
	})

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run(ctx) }()

	waitForSocket(t, socketPath)

	// Cancel context triggers graceful shutdown.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("server error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down in time")
	}

	// Socket file should be cleaned up.
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("socket file not cleaned up")
	}
}

func TestServerPromptWithSessionID(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "test.sock")
	ag := newTestAgent(t, "session response")

	srv := NewServer(Config{
		Agent:      ag,
		SocketPath: socketPath,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Run(ctx) }()
	waitForSocket(t, socketPath)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dialing: %v", err)
	}
	defer conn.Close()

	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)

	// First, create a session.
	_ = enc.Encode(Request{
		JSONRPC: "2.0",
		Method:  "session.create",
		ID:      1,
	})

	var createResp Response
	if err := dec.Decode(&createResp); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	result, _ := json.Marshal(createResp.Result)
	var sr SessionResult
	_ = json.Unmarshal(result, &sr)

	// Use that session ID for a prompt.
	_ = enc.Encode(Request{
		JSONRPC: "2.0",
		Method:  "prompt",
		Params:  json.RawMessage(fmt.Sprintf(`{"text":"hello","session_id":"%s"}`, sr.SessionID)),
		ID:      2,
	})

	var promptResp Response
	if err := dec.Decode(&promptResp); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if promptResp.Error != nil {
		t.Fatalf("unexpected error: %+v", promptResp.Error)
	}

	// Verify the session ID is echoed back.
	result, _ = json.Marshal(promptResp.Result)
	var sr2 SessionResult
	_ = json.Unmarshal(result, &sr2)
	if sr2.SessionID != sr.SessionID {
		t.Errorf("session ID mismatch: got %s, want %s", sr2.SessionID, sr.SessionID)
	}

	// Drain remaining events.
	for {
		var ev Event
		if err := dec.Decode(&ev); err != nil {
			break
		}
		if ev.Type == "message_end" {
			break
		}
	}

	cancel()
}

// waitForSocket waits for the socket file to appear.
func waitForSocket(t *testing.T, path string) {
	t.Helper()
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("socket %s did not appear", path)
}
