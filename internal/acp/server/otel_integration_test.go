//go:build integration

package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/dimetron/pi-go/internal/acp/server"
	"github.com/dimetron/pi-go/internal/otel"
)

// ---------------------------------------------------------------------------
// In-process OTLP collector (replaces Docker Jaeger for unit tests)
// ---------------------------------------------------------------------------

// inMemoryOTLPCollector is a minimal OTLP/HTTP receiver that captures span data
// in-memory so tests can inspect traces without Docker or a real collector backend.
type inMemoryOTLPCollector struct {
	mu  sync.Mutex
	in  []spanInfo
	srv *httptest.Server
}

type spanInfo struct {
	TraceID string
	SpanID  string
	Name    string
}

func newOTLPCollector(t *testing.T) *inMemoryOTLPCollector {
	c := &inMemoryOTLPCollector{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusOK)
			return
		}
		defer r.Body.Close()
		var batch struct {
			ResourceSpans []struct {
				ScopeSpans struct {
					Spans []struct {
						TraceID string `json:"traceId"`
						SpanID  string `json:"spanId"`
						Name    string `json:"name"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Logf("otlp collector decode: %v", err)
			return
		}
		c.mu.Lock()
		for _, rs := range batch.ResourceSpans {
			for _, ss := range rs.ScopeSpans.Spans {
				c.in = append(c.in, spanInfo{TraceID: ss.TraceID, SpanID: ss.SpanID, Name: ss.Name})
			}
		}
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	c.srv = httptest.NewServer(mux)
	return c
}

func (c *inMemoryOTLPCollector) URL() string { return c.srv.URL }

func (c *inMemoryOTLPCollector) Spans() []spanInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]spanInfo(nil), c.in...)
}

// ---------------------------------------------------------------------------
// ACP transport test helpers (replicated from agent_test.go since we are in server_test)
// ---------------------------------------------------------------------------

// capturingClient is the same mock used in agent_test.go.
type capturingClient struct {
	messages              atomic.Pointer[[]string]
	mu                    sync.Mutex
	availableCommandsSeen atomic.Int32
}

func (c *capturingClient) append(text string) {
	for {
		cur := c.messages.Load()
		var next []string
		if cur != nil {
			next = append(next, *cur...)
		}
		next = append(next, text)
		if c.messages.CompareAndSwap(cur, &next) {
			return
		}
	}
}

func (c *capturingClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	if n.Update.AgentMessageChunk != nil {
		if blk := n.Update.AgentMessageChunk.Content; blk.Text != nil {
			c.append(blk.Text.Text)
		}
	}
	return nil
}

func (c *capturingClient) RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	return acp.RequestPermissionResponse{}, nil
}
func (c *capturingClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, nil
}
func (c *capturingClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, nil
}
func (c *capturingClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, nil
}
func (c *capturingClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, nil
}
func (c *capturingClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, nil
}
func (c *capturingClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, nil
}
func (c *capturingClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, nil
}

// pipePair returns cross-wired io.ReadWriteClosers simulating a bidirectional ACP transport.
func pipePair() (clientRW, agentRW *pipeRW) {
	c2aR, c2aW := io.Pipe()
	a2cR, a2cW := io.Pipe()
	clientRW = &pipeRW{r: a2cR, w: c2aW}
	agentRW = &pipeRW{r: c2aR, w: a2cW}
	return
}

type pipeRW struct {
	r *io.PipeReader
	w *io.PipeWriter
}

func (p *pipeRW) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeRW) Write(b []byte) (int, error) { return p.w.Write(b) }
func (p *pipeRW) Close() error {
	_ = p.r.Close()
	return p.w.Close()
}

// wireAgent wraps the agent with an AgentSideConnection on the given pipe.
func wireAgent(t *testing.T, a *server.Agent, rw *pipeRW) func() {
	conn := acp.NewAgentSideConnection(a, rw, rw)
	a.SetAgentConnection(conn)
	return func() { _ = rw.Close() }
}

// ---------------------------------------------------------------------------
// .env helper
// ---------------------------------------------------------------------------

func withEnvDot(t *testing.T, vals map[string]string) (cleanup func()) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	path := filepath.Join(home, ".pi-go", ".env")

	var orig string
	if data, err := os.ReadFile(path); err == nil {
		orig = string(data)
	}

	var lines []string
	for k, v := range vals {
		lines = append(lines, k+"="+v)
	}
	_ = os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644)

	return func() {
		if orig != "" {
			_ = os.WriteFile(path, []byte(orig), 0644)
		} else {
			os.Remove(path)
		}
	}
}

// ---------------------------------------------------------------------------
// Test
// ---------------------------------------------------------------------------

// TestACPWithOTELAllSpansShareTraceID exercises the full ACP Initialize → NewSession → Prompt
// flow with OTEL tracing enabled and an in-process OTLP collector. It verifies that
// every span produced during the session shares the same trace ID — i.e., that all work
// done for one ACP session is captured under one parent trace, not scattered across
// independent traces.
func TestACPWithOTELAllSpansShareTraceID(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping OTEL integration test in -short mode")
	}

	// 1. Start in-process OTLP collector (replaces Docker Jaeger).
	collector := newOTLPCollector(t)
	defer collector.srv.Close()

	// 2. Override ~/.pi-go/.env so otel.Tracer() exports to our collector.
	cleanupEnv := withEnvDot(t, map[string]string{
		"OTEL_TRACES_EXPORTER":        "otlp",
		"OTEL_EXPORTER_OTLP_ENDPOINT": collector.URL(),
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http",
		"OTEL_SERVICE_NAME":           "acp-server-test",
	})
	defer cleanupEnv()

	// otel.Tracer() lazy-init on first call. We check IsEnabled to confirm
	// the exporter is active; if false the .env wasn't picked up.
	if !otel.IsEnabled() {
		t.Skip("OTEL not enabled — set OTEL_TRACES_EXPORTER=otlp in ~/.pi-go/.env")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 3. Build agent with EchoPromptHandler (simplest handler that still exercises
	// the full server → Agent → Prompt trace chain).
	a := &server.Agent{Handler: server.EchoPromptHandler}

	// 4. Wire the pipe transport (same pattern as agent_test.go).
	clientRW, agentRW := pipePair()
	defer clientRW.Close()
	defer wireAgent(t, a, agentRW)()

	captures := &capturingClient{}
	clientConn := acp.NewClientSideConnection(captures, clientRW, clientRW)

	// 5. Execute ACP flow. Each call should produce spans that are children of
	// a single parent span started by the server entry points.
	if _, err := clientConn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersion(acp.ProtocolVersionNumber),
		ClientInfo:      &acp.Implementation{Name: "test-otel-client"},
	}); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	sessResp, err := clientConn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acp.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	promptResp, err := clientConn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessResp.SessionId,
		Prompt:    []acp.ContentBlock{acp.TextBlock("hello otel")},
	})
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if promptResp.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("StopReason = %q, want end_turn", promptResp.StopReason)
	}

	// 6. Give the OTLP batch processor time to flush.
	time.Sleep(1 * time.Second)

	// 7. Verify trace cohesion: every span must share the same trace ID.
	spans := collector.Spans()
	if len(spans) == 0 {
		t.Fatal("no spans captured — OTEL may not have exported. " +
			"Verify OTEL_TRACES_EXPORTER=otlp is set and collector URL is reachable.")
	}

	traceIDs := make(map[string]int)
	for _, s := range spans {
		if s.TraceID == "" {
			continue
		}
		traceIDs[s.TraceID]++
	}

	if len(traceIDs) > 1 {
		t.Errorf("found %d distinct trace IDs — all ACP session spans must share one trace:\n", len(traceIDs))
		for tid, count := range traceIDs {
			t.Logf("  traceID=%s  spanCount=%d", tid, count)
		}
	}

	var names []string
	for _, s := range spans {
		names = append(names, s.Name)
	}
	t.Logf("captured %d spans in %d trace(s): %v", len(spans), len(traceIDs), names)
}
