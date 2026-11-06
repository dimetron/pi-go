package extension

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpLoginTimeout bounds `pi mcp login` end to end. It is the OAuth budget, not
// the connect budget: the command exists to sit and wait while a human opens a
// browser, approves, and is redirected back. One authorization round-trip plus a
// token exchange fits comfortably; a user who walks away hits it and gets a
// clear error rather than a hung terminal.
const mcpLoginTimeout = 5 * time.Minute

// LoginMCP authorizes a remote MCP server ahead of a session and caches the
// resulting token, so the first agent request does not have to open a browser.
//
// This is the piece `pi mcp login` needs and nothing else provides: the OAuth
// handler built by newMCPOAuthHandler only authorizes in reaction to a 401 from
// a live connection, which means a fresh terminal — or an agent run — is the
// first place the flow can happen. Logging in separately makes that step
// deliberate: the user sees the browser open, and a headless run later finds
// usable credentials already on disk.
//
// Completion is detected by watching the token cache rather than by reading the
// handler's token source. The handler exposes TokenSource, but it returns the
// cached source from the previous session until Authorize has swapped in a new
// one — so polling it would report success before the browser flow even started.
// Observing the file means "usable credentials exist now" is answered by the same
// record the next session will consult.
func LoginMCP(ctx context.Context, srv MCPServerConfig) error {
	if srv.URL == "" {
		return fmt.Errorf("MCP server %q has no url; a stdio server has no bearer token to authorize", srv.Name)
	}

	// The flow can only complete with a user present, and mcpOAuthCodeFetcher
	// refuses without one. Enabling it here is scoped to this call: login is
	// interactive by definition, and leaving the flag on would make an
	// unrelated later run pop a browser.
	prev := interactiveOAuth.Swap(true)
	defer interactiveOAuth.Store(prev)

	handler, err := newMCPOAuthHandler(srv.Name, srv.URL)
	if err != nil {
		return fmt.Errorf("preparing OAuth login for %q: %w", srv.Name, err)
	}
	if _, ok := handler.(*mcpTokenOnlyHandler); ok {
		// Only reachable if a future change skips the interactive branch above.
		return fmt.Errorf("OAuth login for %q requires an interactive terminal", srv.Name)
	}

	// Whatever is cached now is either about to be replaced or is already
	// valid. Discarding it first would force a needless browser round-trip
	// when the token is fine, so it is left alone; the connection below reports
	// whether it works.
	before, hadToken := cachedTokenFingerprint(srv)

	connectCtx, cancel := context.WithTimeout(ctx, mcpLoginTimeout)
	defer cancel()

	transport := &mcp.StreamableClientTransport{
		Endpoint:     srv.URL,
		OAuthHandler: handler,
	}
	if len(srv.Headers) > 0 {
		transport.HTTPClient = &http.Client{
			Transport: &headerRoundTripper{headers: srv.Headers},
		}
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "pi-go", Version: "dev"}, nil)
	session, err := client.Connect(connectCtx, transport, nil)
	if err != nil {
		return fmt.Errorf("authorizing MCP server %q: %w", srv.Name, err)
	}
	// Listing also proves the token the server just issued is usable, rather
	// than merely on disk. The session is closed immediately: login authorizes,
	// it does not run tools.
	defer func() { _ = session.Close() }()
	if _, err := session.ListTools(connectCtx, nil); err != nil {
		return fmt.Errorf("authorizing MCP server %q: %w", srv.Name, err)
	}

	after, ok := cachedTokenFingerprint(srv)
	if !ok {
		return fmt.Errorf("connected to %q but no token was cached; the server may not require authorization", srv.Name)
	}
	if hadToken && after == before {
		// A cached token was presented and accepted, so nothing new was issued.
		// That is a success for the user's purpose and is reported as reuse
		// rather than as a fresh login.
		return ErrMCPLoginReusedToken
	}
	return nil
}

// ErrMCPLoginReusedToken reports that login found working cached credentials and
// did not need to run the browser flow. It is a success, exported as a sentinel
// so the command can say "already authorized" without treating it as a failure.
var ErrMCPLoginReusedToken = errors.New("already authorized")

// HasCachedMCPToken reports whether usable credentials are already stored for a
// remote server. It is what `pi mcp list` reads to say whether a server is
// authorized, and it answers with the same cache record the next session
// consults — a token that has aged past mcpOAuthTokenTTL is reported absent,
// because it will not be presented.
func HasCachedMCPToken(server, serverURL string) bool {
	return hasCachedMCPOAuthToken(server, serverURL)
}

// cachedTokenFingerprint identifies the currently cached credentials for a
// server identity without exposing them: the saved-at timestamp and the token
// length change whenever a new token is stored, which is all that is needed to
// tell "reused" from "freshly issued". An empty string means nothing is cached.
func cachedTokenFingerprint(srv MCPServerConfig) (string, bool) {
	st, _, ok := readMCPOAuthRecord(srv.Name, srv.URL)
	if !ok || st.AccessToken == "" {
		return "", false
	}
	return fmt.Sprintf("%d/%d", st.SavedAt.UnixNano(), len(st.AccessToken)), true
}
