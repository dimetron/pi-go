package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connect wires a real client to the real server over an in-memory transport.
// The session-backed tools (ping, log, roots, sample, elicit) call *back* into
// the client mid-request, so they cannot be exercised by invoking the handler
// functions directly — they need a live peer that answers those callbacks.
func connect(t *testing.T, opts *mcp.ClientOptions, roots ...*mcp.Root) *mcp.ClientSession {
	t.Helper()

	serverTr, clientTr := mcp.NewInMemoryTransports()

	server := newServer()
	serverSession, err := server.Connect(t.Context(), serverTr, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, opts)
	if len(roots) > 0 {
		client.AddRoots(roots...)
	}

	clientSession, err := client.Connect(t.Context(), clientTr, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name})
	if err != nil {
		t.Fatalf("CallTool(%q): %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%q) returned a tool error: %s", name, contentText(res))
	}
	return res
}

// pingingTool pings the client from inside the tool call; it fails if the
// round-trip does not come back.
func TestPingTool(t *testing.T) {
	cs := connect(t, nil)
	callTool(t, cs, "ping")
}

// loggingTool sends a log notification to the client mid-call.
func TestLogTool(t *testing.T) {
	got := make(chan mcp.LoggingLevel, 1)
	cs := connect(t, &mcp.ClientOptions{
		LoggingMessageHandler: func(_ context.Context, req *mcp.LoggingMessageRequest) {
			select {
			case got <- req.Params.Level:
			default:
			}
		},
	})

	callTool(t, cs, "log")

	select {
	case level := <-got:
		if level != "error" {
			t.Errorf("log level = %q, want error", level)
		}
	default:
		// The notification is asynchronous; the tool succeeding is the contract
		// under test, so do not fail if it has not landed yet.
	}
}

// rootsTool asks the client for its roots and joins them into "name:uri".
func TestRootsTool(t *testing.T) {
	cs := connect(t, nil,
		&mcp.Root{Name: "repo", URI: "file:///work/repo"},
		&mcp.Root{Name: "docs", URI: "file:///work/docs"},
	)

	res := callTool(t, cs, "roots")

	text := contentText(res)
	for _, want := range []string{"repo:file:///work/repo", "docs:file:///work/docs"} {
		if !strings.Contains(text, want) {
			t.Errorf("roots output %q missing %q", text, want)
		}
	}
}

// samplingTool asks the client to generate a message and echoes the content back.
func TestSamplingTool(t *testing.T) {
	cs := connect(t, &mcp.ClientOptions{
		CreateMessageHandler: func(context.Context, *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			return &mcp.CreateMessageResult{
				Model:   "test-model",
				Role:    "assistant",
				Content: &mcp.TextContent{Text: "sampled reply"},
			}, nil
		},
	})

	res := callTool(t, cs, "sample")

	if text := contentText(res); !strings.Contains(text, "sampled reply") {
		t.Errorf("sampling output = %q, want the client's reply echoed back", text)
	}
}

// The elicitation tools ask the client to fill in a form / visit a URL.
func TestElicitTools(t *testing.T) {
	opts := &mcp.ClientOptions{
		Capabilities: &mcp.ClientCapabilities{
			Elicitation: &mcp.ElicitationCapabilities{
				Form: &mcp.FormElicitationCapabilities{},
				URL:  &mcp.URLElicitationCapabilities{},
			},
		},
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			// URL-mode elicitation carries no RequestedSchema: the user is sent
			// to a page instead of filling in a form here. Returning form content
			// for it fails schema validation, so only the form flow gets content.
			if req.Params.RequestedSchema == nil {
				return &mcp.ElicitResult{Action: "accept"}, nil
			}
			return &mcp.ElicitResult{
				Action:  "accept",
				Content: map[string]any{"random": "abc123"},
			}, nil
		},
	}

	t.Run("form", func(t *testing.T) {
		cs := connect(t, opts)
		res := callTool(t, cs, "elicit (form)")
		if text := contentText(res); !strings.Contains(text, "abc123") {
			t.Errorf("form elicitation output = %q, want the submitted value", text)
		}
	})

	t.Run("url", func(t *testing.T) {
		cs := connect(t, opts)
		callTool(t, cs, "elicit (url)")
	})
}

// The plain tools still work through a real session.
func TestGreetTools(t *testing.T) {
	cs := connect(t, nil)

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("CallTool(greet): %v", err)
	}
	if text := contentText(res); !strings.Contains(text, "world") {
		t.Errorf("greet output = %q, want it to greet world", text)
	}
}

// newServer must register every tool, prompt and resource the harness advertises.
func TestNewServer_RegistersEverything(t *testing.T) {
	cs := connect(t, nil)

	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range tools.Tools {
		got[tl.Name] = true
	}
	for _, want := range []string{"greet", "ping", "log", "sample", "roots", "elicit (form)", "elicit (url)"} {
		if !got[want] {
			t.Errorf("tool %q not registered", want)
		}
	}

	prompts, err := cs.ListPrompts(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(prompts.Prompts) == 0 {
		t.Error("no prompts registered")
	}

	resources, err := cs.ListResources(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources.Resources) == 0 {
		t.Error("no resources registered")
	}
}

func contentText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestMainFunc(t *testing.T) {
	oldStdin := os.Stdin
	oldStdout := os.Stdout
	defer func() {
		os.Stdin = oldStdin
		os.Stdout = oldStdout
	}()

	rStdin, wStdin, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_ = wStdin.Close()
	os.Stdin = rStdin

	rStdout, wStdout, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wStdout

	main()

	_ = wStdout.Close()
	_ = rStdout.Close()
	_ = rStdin.Close()
}

// The /gc endpoint forces a few GC cycles so a heap profile scraped afterwards
// reports only reachable memory.
func TestPprofMux_GCEndpoint(t *testing.T) {
	rec := httptest.NewRecorder()
	pprofMux().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/gc", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "GC'ed") {
		t.Errorf("body = %q, want it to confirm the GC ran", rec.Body.String())
	}
}

// The streamable HTTP transport must actually serve the MCP endpoint.
func TestHTTPHandler_Serves(t *testing.T) {
	srv := httptest.NewServer(httpHandler(newServer()))
	defer srv.Close()

	// A GET without the streaming headers is rejected by the SDK, which is
	// enough to prove the handler is wired and responding rather than 404ing.
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		t.Errorf("status = 404; the MCP handler is not mounted")
	}
}

// serve over stdio must return when its context is canceled, rather than
// hanging forever.
func TestServe_StdioStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = serve(ctx, newServer(), "", "")
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after its context was canceled")
	}
}
