package extension

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"golang.org/x/oauth2"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/notice"
)

// captureNotices installs a sink that records notices for the duration of the
// test, so assertions can check what the user was actually told and nothing
// leaks to os.Stderr and into a TUI frame.
func captureNotices(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var got []string
	prev := notice.SetSink(func(msg string) {
		mu.Lock()
		got = append(got, msg)
		mu.Unlock()
	})
	t.Cleanup(func() { notice.SetSink(prev) })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), got...)
	}
}

// allowInteractiveOAuth enables the browser re-login for the duration of a
// test. It is off by default so headless runs are never blocked on an approval
// nobody will see, which means every test of the retry path must opt in.
func allowInteractiveOAuth(t *testing.T) {
	t.Helper()
	SetInteractiveOAuth(true)
	t.Cleanup(func() { SetInteractiveOAuth(false) })
}

func TestIsMCPAuthError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			// The shape the streamable client actually produces: the SDK
			// formats http.StatusText into the message.
			name: "401 from the transport",
			err:  fmt.Errorf(`failed to init MCP session: calling "initialize": sending "initialize": Unauthorized`),
			want: true,
		},
		{"403", fmt.Errorf("sending \"initialize\": Forbidden"), true},
		{"oauth error code", fmt.Errorf(`{"error":"invalid_token"}`), true},
		{"expired refresh token", fmt.Errorf("oauth2: cannot fetch token: invalid_grant"), true},
		{"405 is not an auth failure", fmt.Errorf("sending \"initialize\": Method Not Allowed"), false},
		{"transport failure", fmt.Errorf("dial tcp: connection refused"), false},
		{"crash", fmt.Errorf("MCP server crashed: EOF"), false},
	}
	for _, tc := range tests {
		if got := isMCPAuthError(tc.err); got != tc.want {
			t.Errorf("%s: isMCPAuthError(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}

// unauthorizedToolset fails the way a remote MCP server that wants a bearer
// token does: the transport surfaces the 401 as "Unauthorized".
type unauthorizedToolset struct{ calls int }

func (u *unauthorizedToolset) Name() string { return "unauthorized" }
func (u *unauthorizedToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	u.calls++
	return nil, fmt.Errorf(`failed to init MCP session: calling "initialize": Unauthorized`)
}

func TestResilientToolset_ReauthorizesOn401(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	notices := captureNotices(t)

	authorized := &successToolset{tools: []tool.Tool{&namedTool{nameVal: "list-benchmarks"}}}
	var gotSrv MCPServerConfig
	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  "openrouter",
		srv:   MCPServerConfig{Name: "openrouter", URL: "https://mcp.example.com/mcp"},
		reconnect: func(srv MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			gotSrv = srv
			return authorized, nil, nil
		},
	}

	tools, err := rt.Tools(nil)
	if err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name() != "list-benchmarks" {
		t.Fatalf("tools = %v, want the authorized connection's tools", tools)
	}
	if rt.failed {
		t.Error("toolset marked failed after a successful re-authorization")
	}
	if !gotSrv.OAuth {
		t.Error("retry did not enable OAuth; the SDK only re-authorizes when a handler is installed")
	}
	if gotSrv.URL != "https://mcp.example.com/mcp" {
		t.Errorf("retry targeted %q, want the configured endpoint", gotSrv.URL)
	}
	// The live connection must be the authorized one, not the refused one.
	if rt.inner != tool.Toolset(authorized) {
		t.Error("toolset did not adopt the authorized connection")
	}
	if !hasNoticeContaining(notices(), "re-running OAuth login") {
		t.Errorf("user was not told a re-login was happening; notices: %v", notices())
	}
}

// A cached token the provider has since revoked must be discarded, or the
// "re-login" replays the same dead credentials.
func TestResilientToolset_ReauthorizeClearsCachedToken(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	captureNotices(t)

	const (
		name = "openrouter"
		url  = "https://mcp.example.com/mcp"
	)
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "revoked", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(name, url, cfg, tok); err != nil {
		t.Fatalf("seeding the token cache: %v", err)
	}
	path, err := mcpOAuthTokenFile(name, url)
	if err != nil {
		t.Fatalf("mcpOAuthTokenFile: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("cached token was not written: %v", err)
	}

	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  name,
		// OAuth is on because buildMCPToolset upgrades a server with cached
		// credentials, so this connection presented the revoked token.
		srv: MCPServerConfig{Name: name, URL: url, OAuth: true},
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			return &successToolset{tools: []tool.Tool{&namedTool{nameVal: "t"}}}, nil, nil
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("revoked token still cached at %s (stat err %v)", path, err)
	}
}

// A stdio server has no bearer token to renew, so a 401-looking message from
// one must never open a browser.
func TestResilientToolset_NoReauthorizeForCommandServer(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	notices := captureNotices(t)

	reconnected := false
	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  "local",
		srv:   MCPServerConfig{Name: "local", Command: "some-server"},
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			reconnected = true
			return nil, nil, nil
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if reconnected {
		t.Error("ran the OAuth flow for a stdio server")
	}
	if !rt.failed {
		t.Error("expected the server to be marked failed")
	}
	if hasNoticeContaining(notices(), "re-running OAuth login") {
		t.Errorf("announced an OAuth re-login for a stdio server; notices: %v", notices())
	}
}

// A non-auth failure must be reported as-is, without a browser round-trip.
func TestResilientToolset_NoReauthorizeForOtherErrors(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	notices := captureNotices(t)

	reconnected := false
	rt := &resilientToolset{
		inner: &failingToolset{},
		name:  "broken",
		srv:   MCPServerConfig{Name: "broken", URL: "https://mcp.example.com/mcp"},
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			reconnected = true
			return nil, nil, nil
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if reconnected {
		t.Error("re-authorized a server that failed for a non-auth reason")
	}
	if !hasNoticeContaining(notices(), "unavailable") {
		t.Errorf("failure was not reported; notices: %v", notices())
	}
}

// The retry runs once. If it is also refused, the server is reported failed
// rather than looping through the authorization flow again.
func TestResilientToolset_ReauthorizeFailureIsTerminal(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	notices := captureNotices(t)

	second := &unauthorizedToolset{}
	calls := 0
	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  "openrouter",
		srv:   MCPServerConfig{Name: "openrouter", URL: "https://mcp.example.com/mcp"},
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			calls++
			return second, nil, nil
		},
	}

	tools, err := rt.Tools(nil)
	if err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if len(tools) != 0 {
		t.Errorf("tools = %v, want none", tools)
	}
	if calls != 1 {
		t.Errorf("reconnect called %d times, want exactly 1", calls)
	}
	if !rt.failed {
		t.Error("expected the server to be marked failed")
	}
	if !hasNoticeContaining(notices(), "after re-authorizing") {
		t.Errorf("failure did not say the retry was the thing that failed; notices: %v", notices())
	}
}

func TestResilientToolset_ReconnectErrorIsReported(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	notices := captureNotices(t)

	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  "openrouter",
		srv:   MCPServerConfig{Name: "openrouter", URL: "https://mcp.example.com/mcp"},
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			return nil, nil, fmt.Errorf("no authorization server advertised")
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if !rt.failed {
		t.Error("expected the server to be marked failed")
	}
	if !hasNoticeContaining(notices(), "no authorization server advertised") {
		t.Errorf("reconnect error was swallowed; notices: %v", notices())
	}
}

// A docs index configured as an MCP server is still dialed — the base name
// cannot prove it is not a real endpoint — but when the connection fails the
// message must explain what the entry actually is, not just relay a 405.
func TestResilientToolset_DiagnosesLLMSDocsIndexOnFailure(t *testing.T) {
	notices := captureNotices(t)

	rt := &resilientToolset{
		inner: &methodNotAllowedToolset{},
		name:  "adk-docs-mcp",
		srv:   MCPServerConfig{Name: "adk-docs-mcp", URL: "https://adk.dev/llms.txt"},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if !rt.failed {
		t.Error("expected the server to be marked failed")
	}
	msgs := notices()
	if len(msgs) != 1 {
		t.Fatalf("notices = %v, want exactly one", msgs)
	}
	for _, want := range []string{"adk-docs-mcp", "fetch_docs", "llms", "sources", "Method Not Allowed"} {
		if !strings.Contains(msgs[0], want) {
			t.Errorf("notice %q does not mention %q", msgs[0], want)
		}
	}
}

// A server that declares MCP-only configuration gets the plain failure
// message, never the docs-index diagnosis.
func TestResilientToolset_NoLLMSDiagnosisForConfiguredEndpoint(t *testing.T) {
	notices := captureNotices(t)

	rt := &resilientToolset{
		inner: &methodNotAllowedToolset{},
		name:  "authed",
		srv: MCPServerConfig{
			Name:    "authed",
			URL:     "https://example.com/llms.txt",
			Headers: map[string]string{"Authorization": "Bearer k"},
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if hasNoticeContaining(notices(), "fetch_docs") {
		t.Errorf("diagnosed a configured MCP endpoint as a docs index; notices: %v", notices())
	}
	if !hasNoticeContaining(notices(), "unavailable") {
		t.Errorf("failure was not reported; notices: %v", notices())
	}
}

// A docs index is dialed like any other server; nothing is skipped on a name.
func TestBuildMCPToolsets_DialsLLMSDocsIndex(t *testing.T) {
	toolsets, err := BuildMCPToolsets([]MCPServerConfig{
		{Name: "adk-docs-mcp", URL: "https://adk.dev/llms.txt"},
		{Name: "openrouter", URL: "https://mcp.example.com/mcp"},
	})
	if err != nil {
		t.Fatalf("BuildMCPToolsets: %v", err)
	}
	if len(toolsets) != 2 {
		t.Fatalf("built %d toolsets, want 2; nothing may be dropped on a URL base name", len(toolsets))
	}
}

// methodNotAllowedToolset fails the way an llms.txt index does when something
// POSTs "initialize" at it.
type methodNotAllowedToolset struct{}

func (m *methodNotAllowedToolset) Name() string { return "405" }
func (m *methodNotAllowedToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return nil, fmt.Errorf(`failed to init MCP session: calling "initialize": Method Not Allowed`)
}

func hasNoticeContaining(msgs []string, want string) bool {
	for _, m := range msgs {
		if strings.Contains(m, want) {
			return true
		}
	}
	return false
}

// Guard against the cache file format drifting out from under
// removeMCPOAuthToken: it must be removing the same file saveMCPOAuthToken
// wrote, not a path that happens not to exist.
func TestRemoveMCPOAuthToken(t *testing.T) {
	setTestHome(t)

	const (
		name = "srv"
		url  = "https://a.example.com/mcp"
	)
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	if err := saveMCPOAuthToken(name, url, cfg, &oauth2.Token{AccessToken: "a"}); err != nil {
		t.Fatalf("saveMCPOAuthToken: %v", err)
	}
	path, err := mcpOAuthTokenFile(name, url)
	if err != nil {
		t.Fatalf("mcpOAuthTokenFile: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the cached token: %v", err)
	}
	var stored mcpOAuthToken
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("cached token is not the expected document: %v", err)
	}

	if err := removeMCPOAuthToken(name, url); err != nil {
		t.Fatalf("removeMCPOAuthToken: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("token still present after removal (stat err %v)", err)
	}
	// Removing again is success: the goal is that nothing remains cached.
	if err := removeMCPOAuthToken(name, url); err != nil {
		t.Errorf("removing an absent token returned %v, want nil", err)
	}
}

func TestWithoutAuthorization(t *testing.T) {
	tests := []struct {
		name string
		in   map[string]string
		want map[string]string
	}{
		{"nil", nil, nil},
		{"empty", map[string]string{}, nil},
		{"only authorization", map[string]string{"Authorization": "Bearer stale"}, nil},
		{
			// HTTP header names are case-insensitive and http.Header.Set
			// canonicalizes, so a lowercase spelling collides all the same.
			name: "lowercase spelling",
			in:   map[string]string{"authorization": "Bearer stale", "X-Title": "pi"},
			want: map[string]string{"X-Title": "pi"},
		},
		{
			name: "other headers survive",
			in:   map[string]string{"Authorization": "Bearer stale", "HTTP-Referer": "https://pi.go", "X-Title": "pi"},
			want: map[string]string{"HTTP-Referer": "https://pi.go", "X-Title": "pi"},
		},
	}
	for _, tc := range tests {
		got := withoutAuthorization(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for k, v := range tc.want {
			if got[k] != v {
				t.Errorf("%s: got[%q] = %q, want %q", tc.name, k, got[k], v)
			}
		}
	}
}

func TestWithoutAuthorizationDoesNotMutateInput(t *testing.T) {
	in := map[string]string{"Authorization": "Bearer stale", "X-Title": "pi"}
	withoutAuthorization(in)
	if in["Authorization"] != "Bearer stale" {
		t.Error("withoutAuthorization mutated the caller's header map; it is the loaded config")
	}
}

// headerRoundTripper is the client's outermost transport, so it runs after the
// SDK has set the freshly issued bearer token on the request. Carrying the
// refused static credential into the retry would overwrite that token and the
// retry would be rejected exactly like the first attempt.
func TestResilientToolset_ReauthorizeDropsStaleAuthorizationHeader(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	captureNotices(t)

	var gotSrv MCPServerConfig
	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  "openrouter",
		srv: MCPServerConfig{
			Name:    "openrouter",
			URL:     "https://mcp.example.com/mcp",
			Headers: map[string]string{"Authorization": "Bearer revoked", "X-Title": "pi"},
		},
		reconnect: func(srv MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			gotSrv = srv
			return &successToolset{tools: []tool.Tool{&namedTool{nameVal: "t"}}}, nil, nil
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}

	if _, ok := gotSrv.Headers["Authorization"]; ok {
		t.Error("retry carried the refused Authorization header; it would overwrite the new OAuth token")
	}
	if gotSrv.Headers["X-Title"] != "pi" {
		t.Errorf("retry dropped an unrelated header: %v", gotSrv.Headers)
	}
	// The loaded config must be left intact for anything else reading it.
	if rt.srv.Headers["Authorization"] != "Bearer revoked" {
		t.Error("re-authorization mutated the server's configured headers")
	}
}

// Print, JSON, RPC, socket and ACP runs have no user at a browser. A 401 there
// must be reported and skipped after the normal connect timeout, exactly as it
// was before automatic re-login existed — never met with a browser window and
// a ten-minute wait that would stall a headless pipeline.
func TestResilientToolset_NoReauthorizeWhenNonInteractive(t *testing.T) {
	setTestHome(t)
	notices := captureNotices(t)
	// Deliberately no allowInteractiveOAuth: this is the default state.

	reconnected := false
	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  "openrouter",
		srv:   MCPServerConfig{Name: "openrouter", URL: "https://mcp.example.com/mcp"},
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			reconnected = true
			return nil, nil, nil
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if reconnected {
		t.Error("opened a browser re-login in a headless run")
	}
	if !rt.failed {
		t.Error("expected the server to be marked failed")
	}
	if hasNoticeContaining(notices(), "re-running OAuth login") {
		t.Errorf("announced an OAuth re-login with no user present; notices: %v", notices())
	}
	if !hasNoticeContaining(notices(), "Unauthorized") {
		t.Errorf("failure was not reported; notices: %v", notices())
	}
}

// An entry that declares configuration only a real MCP endpoint needs is
// dialed whatever its URL looks like, so the llms.txt name heuristic can
// never silently remove a working server.
func TestBuildMCPToolsets_KeepsConfiguredServerAtLLMSPath(t *testing.T) {
	notices := captureNotices(t)

	toolsets, err := BuildMCPToolsets([]MCPServerConfig{
		{Name: "authed", URL: "https://example.com/llms.txt", Headers: map[string]string{"Authorization": "Bearer k"}},
		{Name: "oauthed", URL: "https://example.com/llms-full.txt", OAuth: true},
	})
	if err != nil {
		t.Fatalf("BuildMCPToolsets: %v", err)
	}
	if len(toolsets) != 2 {
		t.Fatalf("built %d toolsets, want 2; a configured MCP endpoint was rerouted on its path alone", len(toolsets))
	}
	if hasNoticeContaining(notices(), "fetch_docs") {
		t.Errorf("rerouted a server that declares MCP-only config; notices: %v", notices())
	}
}

// A connection that is replaced or abandoned must be closed, or its HTTP/SSE
// session and the goroutine draining it stay alive for the process lifetime.
func TestResilientToolset_ReauthorizeClosesReplacedConnection(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	captureNotices(t)

	old := &connTrackingTransport{inner: &countingTransport{}}
	old.conn = &closeCountingConn{}
	replacement := &connTrackingTransport{inner: &countingTransport{}}
	replacement.conn = &closeCountingConn{}

	rt := &resilientToolset{
		inner:     &unauthorizedToolset{},
		name:      "openrouter",
		srv:       MCPServerConfig{Name: "openrouter", URL: "https://mcp.example.com/mcp"},
		transport: old,
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			// The retry also fails, so its connection is abandoned too.
			return &unauthorizedToolset{}, replacement, nil
		},
	}
	oldConn := old.conn.(*closeCountingConn)
	newConn := replacement.conn.(*closeCountingConn)

	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if oldConn.closes() == 0 {
		t.Error("the refused connection was replaced without being closed")
	}
	if newConn.closes() == 0 {
		t.Error("the abandoned retry connection was not closed")
	}
}

type countingTransport struct{}

func (c *countingTransport) Connect(context.Context) (mcp.Connection, error) {
	return &closeCountingConn{}, nil
}

// closeCountingConn is a minimal mcp.Connection that records Close calls.
type closeCountingConn struct {
	mu sync.Mutex
	n  int
}

func (c *closeCountingConn) Read(context.Context) (jsonrpc.Message, error) { return nil, io.EOF }
func (c *closeCountingConn) Write(context.Context, jsonrpc.Message) error  { return io.EOF }
func (c *closeCountingConn) SessionID() string                             { return "" }
func (c *closeCountingConn) Close() error {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return nil
}
func (c *closeCountingConn) closes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// A cached token means a previous run authorized this server, so the handler
// must be installed from the start. Without that the connection goes out
// unauthenticated, is refused, and the browser flow runs on every launch.
func TestBuildMCPToolset_UpgradesServerWithCachedToken(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)

	const (
		name = "openrouter"
		url  = "https://mcp.example.com/mcp"
	)
	srv := MCPServerConfig{Name: name, URL: url}

	ts, err := buildMCPToolset(srv)
	if err != nil {
		t.Fatalf("buildMCPToolset: %v", err)
	}
	if rt := ts.(*resilientToolset); rt.srv.OAuth {
		t.Fatal("enabled OAuth for a server with no cached credentials")
	}

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "cached", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(name, url, cfg, tok); err != nil {
		t.Fatalf("seeding the token cache: %v", err)
	}

	ts, err = buildMCPToolset(srv)
	if err != nil {
		t.Fatalf("buildMCPToolset: %v", err)
	}
	rt := ts.(*resilientToolset)
	if !rt.srv.OAuth {
		t.Error("cached credentials did not enable OAuth; the browser flow would repeat every launch")
	}
	// The browser round-trip budget applies to any OAuth connection, including
	// one upgraded implicitly, or an approval would be cut off at 30s.
	if rt.timeout != mcpOAuthConnectTimeout {
		t.Errorf("timeout = %v, want the OAuth budget %v", rt.timeout, mcpOAuthConnectTimeout)
	}
	// The caller's config must not be mutated by the upgrade.
	if srv.OAuth {
		t.Error("buildMCPToolset mutated the caller's server config")
	}
}

// A server that never presented a cached token learns nothing from a 401 about
// that token, so a credential stored for it must survive the re-login.
func TestResilientToolset_ReauthorizeKeepsUnusedCachedToken(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	captureNotices(t)

	const (
		name = "openrouter"
		url  = "https://mcp.example.com/mcp"
	)
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	if err := saveMCPOAuthToken(name, url, cfg, &oauth2.Token{AccessToken: "unused"}); err != nil {
		t.Fatalf("seeding the token cache: %v", err)
	}
	path, err := mcpOAuthTokenFile(name, url)
	if err != nil {
		t.Fatalf("mcpOAuthTokenFile: %v", err)
	}

	rt := &resilientToolset{
		inner: &unauthorizedToolset{},
		name:  name,
		srv:   MCPServerConfig{Name: name, URL: url}, // OAuth off: nothing was presented
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			return &successToolset{tools: []tool.Tool{&namedTool{nameVal: "t"}}}, nil, nil
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("discarded a cached token the refused connection never sent: %v", err)
	}
}

// A headless run still presents cached credentials — that is the point of the
// upgrade — but must not adopt the browser budget, since no browser opens.
func TestBuildMCPToolset_CachedTokenKeepsFastTimeoutWhenHeadless(t *testing.T) {
	setTestHome(t)
	// Deliberately no allowInteractiveOAuth.

	const (
		name = "openrouter"
		url  = "https://mcp.example.com/mcp"
	)
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "cached", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(name, url, cfg, tok); err != nil {
		t.Fatalf("seeding the token cache: %v", err)
	}

	ts, err := buildMCPToolset(MCPServerConfig{Name: name, URL: url})
	if err != nil {
		t.Fatalf("buildMCPToolset: %v", err)
	}
	rt := ts.(*resilientToolset)
	if !rt.srv.OAuth {
		t.Error("cached credentials were not presented in a headless run")
	}
	if rt.timeout != mcpConnectTimeout {
		t.Errorf("timeout = %v, want the fast budget %v; nothing waits on a browser here",
			rt.timeout, mcpConnectTimeout)
	}
}

// A headless run must not depend on binding a loopback listener. A sandbox or
// container that forbids one would otherwise fail handler construction and
// skip the server without ever presenting the cached token that is the only
// reason a handler is installed there at all.
func TestNewMCPOAuthHandler_HeadlessNeedsNoListener(t *testing.T) {
	setTestHome(t)
	// Deliberately no allowInteractiveOAuth.
	refuseLoopbackListen(t)

	h, err := newMCPOAuthHandler("srv", "https://mcp.example.com/mcp")
	if err != nil {
		t.Fatalf("newMCPOAuthHandler: %v", err)
	}
	if h == nil {
		t.Fatal("no handler returned; a cached token could never be presented")
	}
}

// The interactive handler does need one — it has to receive the redirect — so
// a bind failure there is a real error rather than something to paper over.
func TestNewMCPOAuthHandler_InteractiveRequiresListener(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	refuseLoopbackListen(t)

	if _, err := newMCPOAuthHandler("srv", "https://mcp.example.com/mcp"); err == nil {
		t.Fatal("expected an error when the callback listener cannot bind")
	} else if !strings.Contains(err.Error(), "callback listener") {
		t.Errorf("error %q does not name the listener", err)
	}
}

// A server whose cached token is presented headlessly still builds when
// loopback binding is impossible.
func TestBuildMCPToolset_HeadlessCachedTokenSurvivesNoListener(t *testing.T) {
	setTestHome(t)
	refuseLoopbackListen(t)

	const (
		name = "openrouter"
		url  = "https://mcp.example.com/mcp"
	)
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "cached", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(name, url, cfg, tok); err != nil {
		t.Fatalf("seeding the token cache: %v", err)
	}

	ts, err := buildMCPToolset(MCPServerConfig{Name: name, URL: url})
	if err != nil {
		t.Fatalf("buildMCPToolset: %v; the cached token would never be presented", err)
	}
	if !ts.(*resilientToolset).srv.OAuth {
		t.Error("cached credentials were not presented")
	}
}

// refuseLoopbackListen makes the callback listener fail to bind, standing in
// for a sandbox that forbids loopback servers.
func refuseLoopbackListen(t *testing.T) {
	t.Helper()
	prev := mcpOAuthListen
	mcpOAuthListen = func() (net.Listener, error) {
		return nil, fmt.Errorf("operation not permitted")
	}
	t.Cleanup(func() { mcpOAuthListen = prev })
}

// The refused connection must be closed even when the replacement cannot be
// built at all, not only when it connects and then fails.
func TestResilientToolset_ReauthorizeClosesRefusedConnectionOnSetupFailure(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)
	captureNotices(t)

	old := &connTrackingTransport{}
	old.conn = &closeCountingConn{}
	oldConn := old.conn.(*closeCountingConn)

	rt := &resilientToolset{
		inner:     &unauthorizedToolset{},
		name:      "openrouter",
		srv:       MCPServerConfig{Name: "openrouter", URL: "https://mcp.example.com/mcp"},
		transport: old,
		reconnect: func(MCPServerConfig) (tool.Toolset, *connTrackingTransport, error) {
			return nil, nil, fmt.Errorf("starting OAuth callback listener: permission denied")
		},
	}
	if _, err := rt.Tools(nil); err != nil {
		t.Fatalf("Tools() returned an error: %v", err)
	}
	if !rt.failed {
		t.Error("expected the server to be marked failed")
	}
	if oldConn.closes() == 0 {
		t.Error("refused connection leaked when the replacement could not be built")
	}
}

// A server upgraded from a static credential must not carry that credential
// forward on the next launch: headerRoundTripper runs last and would overwrite
// the cached bearer token, producing another 401 every time.
func TestBuildMCPToolset_CachedUpgradeDropsStaticAuthorization(t *testing.T) {
	setTestHome(t)
	allowInteractiveOAuth(t)

	const (
		name = "openrouter"
		url  = "https://mcp.example.com/mcp"
	)
	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "cached", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(name, url, cfg, tok); err != nil {
		t.Fatalf("seeding the token cache: %v", err)
	}

	srv := MCPServerConfig{
		Name:    name,
		URL:     url,
		Headers: map[string]string{"Authorization": "Bearer refused", "X-Title": "pi"},
	}
	ts, err := buildMCPToolset(srv)
	if err != nil {
		t.Fatalf("buildMCPToolset: %v", err)
	}
	rt := ts.(*resilientToolset)
	if !rt.srv.OAuth {
		t.Fatal("cached credentials did not enable OAuth")
	}
	if _, ok := rt.srv.Headers["Authorization"]; ok {
		t.Error("carried the refused Authorization header; it would overwrite the cached token")
	}
	if rt.srv.Headers["X-Title"] != "pi" {
		t.Errorf("dropped an unrelated header: %v", rt.srv.Headers)
	}
	if srv.Headers["Authorization"] != "Bearer refused" {
		t.Error("mutated the caller's configured headers")
	}
}
