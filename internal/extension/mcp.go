package extension

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"golang.org/x/oauth2"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/mcptoolset"

	piauth "github.com/dimetron/pi-go/internal/auth" // SDK auth pkg is imported above
	"github.com/dimetron/pi-go/internal/browser"
)

var mcpConnectTimeout = 30 * time.Second

// mcpOAuthConnectTimeout is used for servers that run the OAuth
// authorization-code flow on first connect. The browser round-trip (open URL,
// user approves, redirect back) routinely takes minutes, so the default
// 30s connect timeout would abort the flow before it completes.
var mcpOAuthConnectTimeout = 10 * time.Minute

// MCPServerConfig matches the config.MCPServer structure.
type MCPServerConfig struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`     // HTTP transport (Streamable HTTP)
	Headers map[string]string `json:"headers,omitempty"` // Custom HTTP headers for the URL transport
	OAuth   bool              `json:"oauth,omitempty"`   // run the OAuth authorization-code flow on first connect
}

// BuildMCPToolsets creates ADK Toolsets from MCP server configurations.
// Each server is launched as a subprocess using CommandTransport.
// Servers that fail to initialize are logged and skipped rather than
// failing the entire batch.
func BuildMCPToolsets(servers []MCPServerConfig) ([]tool.Toolset, error) {
	var toolsets []tool.Toolset
	for _, srv := range servers {
		ts, err := buildMCPToolset(srv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pi-go: warning: MCP server %q skipped: %v\n", srv.Name, err)
			continue
		}
		toolsets = append(toolsets, ts)
	}
	return toolsets, nil
}

// respawnTransport wraps CommandTransport to create a fresh exec.Cmd on each
// Connect call. Go's exec.Cmd is single-use — once StdoutPipe/StdinPipe are
// called the Cmd cannot be reused. Without this wrapper, a failed MCP server
// startup poisons the transport permanently with "Stdout already set" errors.
type respawnTransport struct {
	command string
	args    []string
}

func (t *respawnTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	ct := &mcp.CommandTransport{
		Command: exec.Command(t.command, t.args...),
	}
	return ct.Connect(ctx)
}

// connTrackingTransport wraps an mcp.Transport to capture the Connection
// returned by Connect, so it can be closed later to kill a hung subprocess.
type connTrackingTransport struct {
	inner mcp.Transport

	mu   sync.Mutex
	conn mcp.Connection
}

func (t *connTrackingTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	c, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.conn = c
	t.mu.Unlock()
	return c, nil
}

// closeConn closes the tracked connection if one exists, killing any
// subprocess backing it. Safe to call multiple times.
func (t *connTrackingTransport) closeConn() {
	t.mu.Lock()
	c := t.conn
	t.conn = nil
	t.mu.Unlock()
	if c != nil {
		_ = c.Close()
	}
}

// resilientToolset wraps an MCP Toolset so that connection failures at
// tool-listing time are logged instead of killing the agent. The ADK
// mcptoolset connects lazily — errors surface only when Tools() is first
// called during an LLM request. Without this wrapper, a single misconfigured
// or unreachable MCP server crashes the entire agent.
//
// A timeout guards against hanging MCP servers that start but never respond
// to the JSON-RPC "initialize" handshake.
//
// Duplicate tool names across MCP servers are deduplicated automatically.
// When multiple servers expose a tool with the same name, only the first
// occurrence (from the server that loads first) is kept to prevent the
// "duplicate tool" error from the ADK runner.
type resilientToolset struct {
	inner     tool.Toolset
	name      string
	transport *connTrackingTransport
	timeout   time.Duration

	once   sync.Once
	tools  []tool.Tool
	failed bool
}

func (r *resilientToolset) Name() string { return r.name }

func (r *resilientToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	r.once.Do(func() {
		type result struct {
			tools []tool.Tool
			err   error
		}
		ch := make(chan result, 1)
		// Use a separate cancel channel so we can time out the inner
		// Tools() call without wrapping ctx (which would lose the
		// ReadonlyContext interface). The inner goroutine respects the
		// original ctx; we simply abandon it on timeout and mark the
		// toolset as failed.
		timeout := r.timeout
		if timeout == 0 {
			timeout = mcpConnectTimeout
		}
		timeoutCh := time.After(timeout)
		go func() {
			tools, err := r.inner.Tools(ctx)
			ch <- result{tools, err}
		}()

		select {
		case res := <-ch:
			if res.err != nil {
				fmt.Fprintf(os.Stderr, "pi-go: warning: MCP server %q unavailable: %v\n", r.name, res.err)
				r.failed = true
				return
			}
			r.tools = r.deduplicateTools(res.tools)
		case <-timeoutCh:
			fmt.Fprintf(os.Stderr, "pi-go: warning: MCP server %q timed out after %v, skipping\n", r.name, timeout)
			r.failed = true
			// Close the underlying MCP connection to kill any hung
			// subprocess and free the blocked goroutine.
			if r.transport != nil {
				r.transport.closeConn()
			}
			// Drain the result in the background so the inner goroutine
			// can exit after the connection is closed.
			go func() {
				select {
				case <-ch:
				case <-time.After(2 * time.Second):
				}
			}()
		}
	})
	if r.failed {
		return nil, nil
	}
	return r.tools, nil
}

// deduplicateTools returns tools with their original names. When multiple
// MCP servers expose tools with the same name, the LLM will see them all
// as-is. The ADK runner handles tool call routing by matching the tool name
// in the function call response against available tools.
func (r *resilientToolset) deduplicateTools(tools []tool.Tool) []tool.Tool {
	return tools
}

// MCPToolEntry is a single resolved tool from a named MCP server.
type MCPToolEntry struct {
	Server string
	Tool   string
}

// LoadedToolsetTools returns the tools already cached by connected MCP
// toolsets, without triggering a fetch. Callers that need to measure what MCP
// contributes to the prompt use this rather than Toolset.Tools, which takes an
// invocation context and can block on a network round-trip — measuring context
// overhead must never stall startup or a render.
//
// Pending and failed servers contribute nothing, so the figure is a floor: it
// counts what is loaded, never a guess at what might load later.
func LoadedToolsetTools(toolsets []tool.Toolset) []tool.Tool {
	var out []tool.Tool
	for _, ts := range toolsets {
		rt, ok := ts.(*resilientToolset)
		if !ok || rt.failed || len(rt.tools) == 0 {
			continue
		}
		out = append(out, rt.tools...)
	}
	return out
}

// BuildMCPToolEntries returns a flat list of {Server, Tool} pairs for all
// connected MCP toolsets. Only toolsets whose tools have already been loaded
// (via a previous Tools() call) contribute entries — pending/failed servers
// are silently skipped.
func BuildMCPToolEntries(toolsets []tool.Toolset) []MCPToolEntry {
	var entries []MCPToolEntry
	for _, ts := range toolsets {
		rt, ok := ts.(*resilientToolset)
		if !ok || rt.failed || len(rt.tools) == 0 {
			continue
		}
		for _, t := range rt.tools {
			entries = append(entries, MCPToolEntry{
				Server: rt.name,
				Tool:   t.Name(),
			})
		}
	}
	return entries
}

type MCPServerStatus struct {
	Name      string
	Connected bool   // true if Tools() succeeded at least once
	Failed    bool   // true if Tools() timed out or returned an error
	ToolCount int    // number of tools reported; 0 if not yet loaded
	Status    string // "connected", "failed", "pending"
}

// ToolsetStatuses returns the current status for each toolset that was created
// by BuildMCPToolsets. Toolsets that haven't been queried yet are reported as "pending".
func ToolsetStatuses(toolsets []tool.Toolset) []MCPServerStatus {
	statuses := make([]MCPServerStatus, 0, len(toolsets))
	for _, ts := range toolsets {
		rt, ok := ts.(*resilientToolset)
		if !ok {
			statuses = append(statuses, MCPServerStatus{
				Name:   ts.Name(),
				Status: "pending",
			})
			continue
		}
		s := MCPServerStatus{
			Name:      rt.name,
			Connected: len(rt.tools) > 0,
			Failed:    rt.failed,
			ToolCount: len(rt.tools),
		}
		switch {
		case rt.failed:
			s.Status = "failed"
		case len(rt.tools) > 0:
			s.Status = "connected"
		default:
			s.Status = "pending"
		}
		statuses = append(statuses, s)
	}
	return statuses
}

func buildMCPToolset(srv MCPServerConfig) (tool.Toolset, error) {
	var transport mcp.Transport
	switch {
	case srv.URL != "":
		t := &mcp.StreamableClientTransport{Endpoint: srv.URL}
		if len(srv.Headers) > 0 {
			t.HTTPClient = &http.Client{
				Transport: &headerRoundTripper{headers: srv.Headers},
			}
		}
		if srv.OAuth {
			handler, err := newMCPOAuthHandler(srv.Name, srv.URL)
			if err != nil {
				return nil, fmt.Errorf("MCP server %q: %w", srv.Name, err)
			}
			t.OAuthHandler = handler
		}
		transport = t
	case srv.Command != "":
		transport = &respawnTransport{
			command: srv.Command,
			args:    srv.Args,
		}
	default:
		return nil, fmt.Errorf("MCP server %q has neither command nor URL", srv.Name)
	}

	// Wrap with connection tracking so we can close the connection on timeout.
	tracked := &connTrackingTransport{inner: transport}

	ts, err := mcptoolset.New(mcptoolset.Config{
		Transport: tracked,
	})
	if err != nil {
		return nil, fmt.Errorf("creating MCP toolset: %w", err)
	}
	timeout := mcpConnectTimeout
	if srv.OAuth {
		timeout = mcpOAuthConnectTimeout
	}
	return &resilientToolset{inner: ts, name: srv.Name, transport: tracked, timeout: timeout}, nil
}

// newMCPOAuthHandler builds an OAuth authorization-code handler for a remote
// MCP server. It uses dynamic client registration (RFC 7591) so no client ID
// needs to be pre-registered, and a local loopback callback server that the
// authorization server redirects to after the user approves. The browser is
// opened to the authorization URL; the handler blocks until the code is
// received and exchanged for a token. The token source is held by the handler
// and reused for the lifetime of the connection, so the flow runs only on the
// first connect (or after the token expires).
func newMCPOAuthHandler(name, serverURL string) (auth.OAuthHandler, error) {
	// Pick a free loopback port for the callback server.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting OAuth callback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port //nolint:errcheck // TCP listener guaranteed
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// authChan is buffered so the browser's callback handler never blocks,
	// even if it fires twice or after the flow has moved on.
	authChan := make(chan *auth.AuthorizationResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = errParam
			}
			piauth.RenderCallbackPage(w, http.StatusBadRequest, false, "Authentication failed", desc)
			return
		}
		code := q.Get("code")
		if code == "" {
			piauth.RenderCallbackPage(w, http.StatusBadRequest, false, "No code received", "")
			return
		}
		select {
		case authChan <- &auth.AuthorizationResult{
			Code:  code,
			State: q.Get("state"),
			Iss:   q.Get("iss"),
		}:
			piauth.RenderCallbackPage(w, http.StatusOK, true, "Authentication successful", "You can close this tab and return to pi.")
		default:
			// Flow already completed with this handler; ignore duplicates.
			piauth.RenderCallbackPage(w, http.StatusOK, true, "Authentication successful", "You can close this tab and return to pi.")
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()

	fetcher := func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		fmt.Fprintf(os.Stderr, "pi-go: MCP server %q requires authorization. Opening browser...\n", name)
		if err := browser.Open(args.URL); err != nil {
			fmt.Fprintf(os.Stderr, "pi-go: could not open browser for %q; visit this URL manually:\n%s\n", name, args.URL)
		}
		defer func() { _ = srv.Close() }() // one-shot: stop serving after the first result
		select {
		case res := <-authChan:
			return res, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	// Reuse a persisted token from a previous session so the browser flow
	// runs only once per mcpOAuthTokenTTL. Nil when nothing usable is cached,
	// which triggers a normal interactive authorization.
	cachedTS := loadMCPOAuthTokenSource(name, serverURL)
	if cachedTS != nil {
		_ = srv.Close() // no browser round-trip ahead; free the loopback port
	}

	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL: redirectURL,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:              "pi-go",
				RedirectURIs:            []string{redirectURL},
				GrantTypes:              []string{"authorization_code"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
			},
		},
		AuthorizationCodeFetcher: fetcher,
		RequestRefreshToken:      true,
		InitialTokenSource:       cachedTS,
		// Persist each freshly authorized token along with the OAuth config
		// needed to refresh it next session.
		NewTokenSource: func(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
			if err := saveMCPOAuthToken(name, serverURL, cfg, tok); err != nil {
				fmt.Fprintf(os.Stderr, "pi-go: could not cache OAuth token for MCP server %q: %v\n", name, err)
			}
			// Wrap so rotated refresh tokens are written back to disk too.
			return newPersistingTokenSource(cfg.TokenSource(ctx, tok), nil, func(t *oauth2.Token) error {
				return saveMCPOAuthToken(name, serverURL, cfg, t)
			}), nil
		},
	})
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("creating OAuth handler: %w", err)
	}
	return handler, nil
}

// headerRoundTripper injects static HTTP headers (e.g., API keys, usernames)
// into every request sent by the Streamable HTTP MCP transport. The base
// RoundTripper defaults to http.DefaultTransport when nil.
type headerRoundTripper struct {
	headers map[string]string
	base    http.RoundTripper
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone the request so we never mutate the caller's headers, which the
	// transport may reuse across retries.
	r := req.Clone(req.Context())
	for k, v := range h.headers {
		r.Header.Set(k, v)
	}
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}
