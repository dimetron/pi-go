package extension

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
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
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/notice"
)

var mcpConnectTimeout = 30 * time.Second

// interactiveOAuth gates the automatic re-login that answers a 401 from a
// remote MCP server. That flow opens a browser and waits up to
// mcpOAuthConnectTimeout for a human to approve it, which is only ever
// appropriate when a human is sitting in front of the process.
//
// It defaults to off, so print, JSON, RPC, socket and ACP runs keep the
// behavior they had: a rejected server is reported and skipped after the
// normal connect timeout, and nothing stalls a headless pipeline for ten
// minutes waiting on a browser nobody will see. runInteractive turns it on.
var interactiveOAuth atomic.Bool

// mcpOAuthListen binds the loopback callback server for the interactive
// authorization flow. It is a variable so tests can make binding fail and
// check that a headless run does not depend on it — a sandbox or container
// that forbids loopback listeners is exactly the case that matters.
var mcpOAuthListen = func() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}

// SetInteractiveOAuth enables or disables the automatic OAuth re-login for
// MCP servers that answer 401/403. Only a front end with a user and a browser
// in reach should enable it.
func SetInteractiveOAuth(enabled bool) { interactiveOAuth.Store(enabled) }

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
			notice.Notifyf("warning: MCP server %q skipped: %v", srv.Name, err)
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
	srv       MCPServerConfig
	transport *connTrackingTransport
	timeout   time.Duration

	// reconnect builds a second connection for the OAuth retry. It defaults
	// to newMCPToolset; tests replace it so the retry path can be exercised
	// without an authorization server to talk to.
	reconnect func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error)

	once   sync.Once
	tools  []tool.Tool
	failed bool
}

func (r *resilientToolset) Name() string { return r.name }

func (r *resilientToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	r.once.Do(func() {
		tools, err := r.listTools(ctx, r.inner, r.transport, r.timeout)
		// A remote server answering 401/403 is not broken, it is
		// unauthenticated — the one recoverable failure here. Re-authorize
		// and retry once before writing the server off.
		if err != nil && r.canReauthorize(err) {
			tools, err = r.reauthorize(ctx)
		}
		if err != nil {
			notice.Notifyf("%s", r.failureNotice(err))
			r.failed = true
			return
		}
		r.tools = r.deduplicateTools(tools)
	})
	if r.failed {
		return nil, nil
	}
	return r.tools, nil
}

// failureNotice renders the message shown when a server cannot be used.
//
// A URL whose base name is llms.txt gets a different message from the raw
// transport error. That name is a reserved convention (llmstxt.org) for a
// plain-text documentation index served over GET, so an entry pointing at one
// is almost certainly a docs source filed under the wrong config key — and the
// bare "Method Not Allowed" it produces says nothing about how to fix that.
// The diagnosis is only offered once the connection has actually failed, so a
// real MCP server that happens to live at such a path is never second-guessed.
func (r *resilientToolset) failureNotice(err error) string {
	if isLLMSDocsServer(r.srv) {
		return fmt.Sprintf("MCP server %q could not be reached (%v). That URL is an llms.txt "+
			"documentation index, not an MCP endpoint — it is already served by the fetch_docs "+
			"tool, so the entry can be removed from \"mcpServers\" and kept under "+
			"\"llms\": {\"sources\": [...]}.", r.name, err)
	}
	return fmt.Sprintf("warning: MCP server %q unavailable: %v", r.name, err)
}

// listTools runs one Tools() attempt under a timeout, so a server that accepts
// the connection but never answers "initialize" cannot stall the turn.
//
// The timeout is a separate timer rather than a derived context because the
// inner toolset needs the caller's agent.ReadonlyContext, and wrapping it in
// context.WithTimeout would erase that interface. The inner goroutine still
// honors the original ctx; on timeout we abandon it, close the connection
// underneath it so it can unblock, and drain it in the background.
func (r *resilientToolset) listTools(
	ctx agent.ReadonlyContext,
	inner tool.Toolset,
	transport *connTrackingTransport,
	timeout time.Duration,
) ([]tool.Tool, error) {
	type result struct {
		tools []tool.Tool
		err   error
	}
	if timeout == 0 {
		timeout = mcpConnectTimeout
	}
	ch := make(chan result, 1)
	timeoutCh := time.After(timeout)
	go func() {
		tools, err := inner.Tools(ctx)
		ch <- result{tools, err}
	}()

	select {
	case res := <-ch:
		return res.tools, res.err
	case <-timeoutCh:
		// Close the underlying MCP connection to kill any hung subprocess
		// and free the blocked goroutine.
		if transport != nil {
			transport.closeConn()
		}
		go func() {
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
			}
		}()
		return nil, fmt.Errorf("timed out after %v, skipping", timeout)
	}
}

// canReauthorize reports whether err is worth answering with a fresh OAuth
// login.
//
// Three conditions must hold. A human must be present, since the flow blocks
// on a browser approval — see interactiveOAuth. Only remote servers qualify,
// because a stdio subprocess has no bearer token to renew. And the failure
// must be a genuine authorization failure, so a 404 or a DNS error never opens
// a browser window.
func (r *resilientToolset) canReauthorize(err error) bool {
	return interactiveOAuth.Load() && r.srv.URL != "" && isMCPAuthError(err)
}

// reauthorize answers a 401/403 by running the OAuth authorization-code flow
// and retrying the tool listing once.
//
// Two cases land here and both need the same treatment. A server configured
// without OAuth has no handler at all, so the SDK cannot recover on its own:
// it only re-authorizes when a handler is installed, and otherwise returns the
// 401 verbatim — which is what produced the bare "Unauthorized" warning. A
// server configured with OAuth may be replaying a cached token the provider
// has since revoked; the SDK would re-run the flow, but only after presenting
// those dead credentials again. Discarding the cached token first makes this a
// real re-login rather than a replay.
//
// The retry runs under mcpOAuthConnectTimeout because it contains a browser
// round-trip — open the URL, wait for approval, wait for the redirect. The
// normal 30s connect budget would abort mid-approval.
func (r *resilientToolset) reauthorize(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	notice.Notifyf("MCP server %q rejected the connection as unauthorized — re-running OAuth login.", r.name)

	// Drop cached credentials only when this connection actually presented
	// them — r.srv.OAuth is set for a configured server and for one buildMCPToolset
	// upgraded from the cache. Otherwise the refusal says nothing about the
	// stored token, and discarding it would throw away a working credential
	// and force a browser round-trip that was not needed. A cache that cannot
	// be cleared is not fatal: the flow still runs, it may just reuse the token.
	if r.srv.OAuth {
		if err := removeMCPOAuthToken(r.srv.Name, r.srv.URL); err != nil {
			notice.Notifyf("warning: could not clear cached OAuth token for MCP server %q: %v", r.name, err)
		}
	}

	// The refused connection is finished with either way — whether the
	// replacement connects, fails to list, or cannot be built at all. Close it
	// first, so no error path below can leave its HTTP/SSE session and the
	// goroutine draining it alive for the rest of the process.
	if r.transport != nil {
		r.transport.closeConn()
	}
	r.transport = nil

	srv := r.srv
	srv.OAuth = true
	srv.Headers = withoutAuthorization(srv.Headers)
	connect := r.reconnect
	if connect == nil {
		connect = newMCPToolset
	}
	inner, transport, err := connect(srv)
	if err != nil {
		return nil, fmt.Errorf("re-authorizing: %w", err)
	}

	tools, err := r.listTools(ctx, inner, transport, mcpOAuthConnectTimeout)
	if err != nil {
		// The replacement connected but could not list; it is abandoned here,
		// so it must be closed too.
		if transport != nil {
			transport.closeConn()
		}
		return nil, fmt.Errorf("after re-authorizing: %w", err)
	}
	// Adopt the authorized connection so later callers — and the timeout path
	// that closes a hung transport — act on the live one, not the connection
	// it replaced.
	r.inner, r.transport = inner, transport
	return tools, nil
}

// withoutAuthorization copies headers without any Authorization entry.
//
// The OAuth retry must not carry the static credential that was just refused.
// headerRoundTripper is the client's outermost transport, so it runs after the
// SDK has set "Authorization: Bearer <fresh token>" on the request and would
// overwrite that token with the stale configured value — the retry would be
// rejected exactly like the first attempt. Every other header is kept: they
// carry routing and API metadata the server still needs.
//
// The comparison is canonicalized because HTTP header names are
// case-insensitive and http.Header.Set canonicalizes on the way out, so a
// config that spells the key "authorization" collides all the same.
func withoutAuthorization(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if http.CanonicalHeaderKey(k) == "Authorization" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isMCPAuthError reports whether err is the MCP transport's rendering of an
// HTTP 401 or 403.
//
// The test is textual because the SDK reports a non-2xx status by formatting
// http.StatusText into an error string rather than returning a typed error or
// exposing the status code, so there is nothing for errors.As to match. The
// OAuth error codes are matched too: a provider that returns a JSON body
// ({"error":"invalid_token"}) alongside the status puts both in one string.
func isMCPAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		strings.ToLower(http.StatusText(http.StatusUnauthorized)), // "unauthorized"
		strings.ToLower(http.StatusText(http.StatusForbidden)),    // "forbidden"
		"invalid_token",
		"invalid_grant",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
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
	// Credentials cached for this identity mean a previous run authorized this
	// server, whether the user asked for OAuth or an automatic re-login
	// upgraded it. Install the handler from the start so those credentials are
	// presented on the first request; without this the connection would go out
	// unauthenticated, be refused, and re-run the browser flow every launch.
	//
	// This applies in headless runs too, so a cached token still gets used
	// there. It cannot turn into a browser prompt: mcpOAuthCodeFetcher refuses
	// when no user is present, so a revoked token fails fast rather than
	// stalling the run on an approval nobody will see.
	if !srv.OAuth && srv.URL != "" && hasCachedMCPOAuthToken(srv.Name, srv.URL) {
		srv.OAuth = true
		// The static credential is what was refused when this server was
		// upgraded, and headerRoundTripper is the outermost transport, so
		// leaving it in place would overwrite the cached bearer token with the
		// refused value and produce another 401 — a browser flow every launch
		// interactively, and no connection at all headlessly.
		srv.Headers = withoutAuthorization(srv.Headers)
	}
	inner, tracked, err := newMCPToolset(srv)
	if err != nil {
		return nil, err
	}
	// The OAuth budget covers a browser round-trip — open, approve, redirect —
	// so it applies only when that round-trip can happen. A headless run never
	// waits on one (mcpOAuthCodeFetcher refuses immediately), so it keeps the
	// normal connect timeout and fails fast.
	timeout := mcpConnectTimeout
	if srv.OAuth && interactiveOAuth.Load() {
		timeout = mcpOAuthConnectTimeout
	}
	return &resilientToolset{
		inner:     inner,
		name:      srv.Name,
		srv:       srv,
		transport: tracked,
		timeout:   timeout,
	}, nil
}

// isLLMSDocsServer mirrors the config-load test, so a server list assembled by
// hand gets the same treatment as one that came from a config file: an entry
// that declares MCP-only configuration — a command, OAuth, custom headers — is
// taken at its word and dialed, whatever its URL looks like.
func isLLMSDocsServer(srv MCPServerConfig) bool {
	if srv.URL == "" || srv.Command != "" || srv.OAuth || len(srv.Headers) > 0 {
		return false
	}
	return config.IsLLMSDocsURL(srv.URL)
}

// newMCPToolset builds one MCP connection: the transport for the configured
// endpoint, wrapped in connection tracking so a hung one can be closed. It is
// separate from buildMCPToolset so the OAuth retry can construct a second,
// authorized connection for a server whose first attempt was refused, without
// rebuilding the resilient wrapper that owns the once-only listing state.
func newMCPToolset(srv MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
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
				return nil, nil, fmt.Errorf("MCP server %q: %w", srv.Name, err)
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
		return nil, nil, fmt.Errorf("MCP server %q has neither command nor URL", srv.Name)
	}

	// Wrap with connection tracking so we can close the connection on timeout.
	tracked := &connTrackingTransport{inner: transport}

	ts, err := mcptoolset.New(mcptoolset.Config{
		Transport: tracked,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("creating MCP toolset: %w", err)
	}
	return ts, tracked, nil
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
	// A headless run never completes an authorization flow — mcpOAuthCodeFetcher
	// refuses before a browser opens — so it needs no callback server. Binding
	// one anyway would make a sandbox or container that forbids loopback
	// listeners fail handler construction, and the server would be skipped
	// without its cached token ever being presented. That token is the whole
	// reason a handler is installed there.
	if !interactiveOAuth.Load() {
		return newMCPOAuthTokenOnlyHandler(name, serverURL)
	}

	// Pick a free loopback port for the callback server.
	listener, err := mcpOAuthListen()
	if err != nil {
		return nil, fmt.Errorf("starting OAuth callback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port //nolint:errcheck // TCP listener guaranteed
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	// resultChan is buffered so the browser's callback handler never blocks,
	// even if it fires twice or after the flow has moved on.
	resultChan := make(chan mcpOAuthCallbackResult, 1)

	// The callback server lives as long as the handler: the SDK re-runs the
	// authorization flow (and so the fetcher) whenever the server answers 401
	// — a cached token that was revoked, say — or asks for step-up scopes,
	// and the redirect URI registered with the authorization server carries
	// this port, so it cannot be rebound later.
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", mcpOAuthCallbackHandler(resultChan))
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(listener) }()

	fetcher := mcpOAuthCodeFetcher(name, resultChan, browser.Open)

	// Reuse a persisted token from a previous session so the browser flow
	// runs only once per mcpOAuthTokenTTL. Nil when nothing usable is cached,
	// which triggers a normal interactive authorization.
	cachedTS := loadMCPOAuthTokenSource(name, serverURL)

	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL:                     redirectURL,
		DynamicClientRegistrationConfig: mcpDynamicClientRegistration(redirectURL),
		AuthorizationCodeFetcher:        fetcher,
		RequestRefreshToken:             true,
		InitialTokenSource:              cachedTS,
		// Persist each freshly authorized token along with the OAuth config
		// needed to refresh it next session.
		NewTokenSource: mcpOAuthNewTokenSource(name, serverURL),
	})
	if err != nil {
		_ = srv.Close()
		return nil, fmt.Errorf("creating OAuth handler: %w", err)
	}
	return handler, nil
}

// mcpDynamicClientRegistration describes pi-go to an authorization server for
// RFC 7591 dynamic client registration, so no client ID has to be pre-shared.
func mcpDynamicClientRegistration(redirectURL string) *auth.DynamicClientRegistrationConfig {
	return &auth.DynamicClientRegistrationConfig{
		Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName:              "pi-go",
			RedirectURIs:            []string{redirectURL},
			GrantTypes:              []string{"authorization_code"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		},
	}
}

// newMCPOAuthTokenOnlyHandler builds a handler that can present and refresh
// stored credentials but can never obtain new ones.
//
// It binds nothing and opens nothing. The redirect URL is a placeholder that is
// never reached, because the fetcher refuses before any authorization request
// is made — which is the correct behavior with no user present: a valid cached
// token still authenticates the connection, and a revoked one fails fast with a
// message saying to run pi interactively once.
func newMCPOAuthTokenOnlyHandler(name, serverURL string) (auth.OAuthHandler, error) {
	const redirectURL = "http://127.0.0.1/callback"
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL: redirectURL,
		// Required by the constructor even though no flow can run here: the
		// fetcher refuses before registration would ever be attempted.
		DynamicClientRegistrationConfig: mcpDynamicClientRegistration(redirectURL),
		AuthorizationCodeFetcher:        mcpOAuthCodeFetcher(name, nil, browser.Open),
		RequestRefreshToken:             true,
		InitialTokenSource:              loadMCPOAuthTokenSource(name, serverURL),
		NewTokenSource:                  mcpOAuthNewTokenSource(name, serverURL),
	})
	if err != nil {
		return nil, fmt.Errorf("creating OAuth handler: %w", err)
	}
	return handler, nil
}

// mcpOAuthCallbackResult is what the loopback callback hands to the waiting
// fetcher: either the authorization result or the error the authorization
// server redirected back with (e.g. the user denied access).
type mcpOAuthCallbackResult struct {
	res *auth.AuthorizationResult
	err error
}

// mcpOAuthCallbackHandler serves the loopback redirect endpoint of the
// authorization-code flow. It renders the pi-go.sh-styled result page and
// delivers the outcome — code or provider error — exactly once on resultChan;
// a second redirect after the flow has consumed the result is answered with
// the same page but otherwise ignored, so a stale browser tab can never block
// or re-trigger the flow.
func mcpOAuthCallbackHandler(resultChan chan<- mcpOAuthCallbackResult) http.HandlerFunc {
	deliver := func(r mcpOAuthCallbackResult) {
		select {
		case resultChan <- r:
		default:
			// Flow already has a pending result; ignore duplicates.
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errParam := q.Get("error"); errParam != "" {
			desc := q.Get("error_description")
			if desc == "" {
				desc = errParam
			}
			deliver(mcpOAuthCallbackResult{err: fmt.Errorf("OAuth error: %s", desc)})
			piauth.RenderCallbackPage(w, http.StatusBadRequest, false, "Authentication failed", desc)
			return
		}
		code := q.Get("code")
		if code == "" {
			deliver(mcpOAuthCallbackResult{err: fmt.Errorf("no authorization code received")})
			piauth.RenderCallbackPage(w, http.StatusBadRequest, false, "No code received", "")
			return
		}
		deliver(mcpOAuthCallbackResult{res: &auth.AuthorizationResult{
			Code:  code,
			State: q.Get("state"),
			Iss:   q.Get("iss"),
		}})
		piauth.RenderCallbackPage(w, http.StatusOK, true, "Authentication successful", "You can close this tab and return to pi.")
	}
}

// mcpOAuthCodeFetcher returns the AuthorizationCodeFetcher for the SDK
// handler: it opens the authorization URL with openURL (browser.Open in
// production; injectable so tests never launch anything), then blocks until
// the callback delivers an outcome on resultChan or ctx is done. Any outcome
// left over from an earlier flow (a late duplicate redirect, say) is dropped
// first so it cannot be mistaken for this flow's result; the SDK additionally
// checks the returned state against the one it issued.
func mcpOAuthCodeFetcher(
	name string,
	resultChan <-chan mcpOAuthCallbackResult,
	openURL func(string) error,
) func(context.Context, *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
	return func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
		// The SDK calls this itself whenever a request comes back 401/403, so
		// it is the last place that can stop a browser opening with nobody to
		// see it. A handler is installed in headless runs too — cached
		// credentials are worth presenting there — but the flow that needs a
		// human must fail fast instead of blocking the run.
		if !interactiveOAuth.Load() {
			return nil, fmt.Errorf("MCP server %q needs authorization, and there is no user to grant it; "+
				"run pi interactively once to authorize it", name)
		}
		select {
		case <-resultChan: // drop stale outcome from a previous flow
		default:
		}
		notice.Notifyf("MCP server %q requires authorization. Opening browser...", name)
		if err := openURL(args.URL); err != nil {
			notice.Notifyf("could not open browser for %q; visit this URL manually:\n%s", name, args.URL)
		}
		select {
		case r := <-resultChan:
			return r.res, r.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// mcpOAuthNewTokenSource returns the NewTokenSource hook for the SDK handler.
// It persists each freshly authorized token for the server identity and wraps
// the refreshing source so rotated tokens are written back to disk too. A
// failed cache write is reported and otherwise ignored: the in-memory source
// still works for this session, and the next one simply re-authorizes.
func mcpOAuthNewTokenSource(name, serverURL string) func(context.Context, *oauth2.Config, *oauth2.Token) (oauth2.TokenSource, error) {
	return func(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
		if err := saveMCPOAuthToken(name, serverURL, cfg, tok); err != nil {
			notice.Notifyf("could not cache OAuth token for MCP server %q: %v", name, err)
		}
		return newPersistingTokenSource(cfg.TokenSource(ctx, tok), nil, func(t *oauth2.Token) error {
			return saveMCPOAuthToken(name, serverURL, cfg, t)
		}), nil
	}
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
