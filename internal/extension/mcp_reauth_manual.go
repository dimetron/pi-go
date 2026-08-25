package extension

import (
	"context"
	"fmt"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/session"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/genai"
)

// mcpReadonlyCtx is a minimal agent.ReadonlyContext for driving the OAuth flow
// outside a live agent turn. Only the embedded context.Context is used by the
// MCP transport during Connect; the remaining methods are never called on that
// path, so they return zero values.
type mcpReadonlyCtx struct {
	context.Context
}

func (mcpReadonlyCtx) UserContent() *genai.Content          { return nil }
func (mcpReadonlyCtx) InvocationID() string                 { return "" }
func (mcpReadonlyCtx) AgentName() string                    { return "" }
func (mcpReadonlyCtx) ReadonlyState() session.ReadonlyState { return nil }
func (mcpReadonlyCtx) UserID() string                       { return "" }
func (mcpReadonlyCtx) AppName() string                      { return "" }
func (mcpReadonlyCtx) SessionID() string                    { return "" }
func (mcpReadonlyCtx) Branch() string                       { return "" }

var _ agent.ReadonlyContext = mcpReadonlyCtx{}

// ReauthorizeMCP forces a fresh OAuth authorization for a remote MCP server,
// discarding any cached token and running the browser flow. It returns a
// rebuilt, authorized toolset, or an error if the flow fails.
//
// This is the manual escape hatch for a server whose auto re-login did not
// start (or whose cached credential was revoked): it is independent of the
// heuristics in canReauthorize and always runs the browser flow. Stdio servers
// have no bearer token to renew and are rejected.
func ReauthorizeMCP(ctx context.Context, srv MCPServerConfig) (tool.Toolset, error) {
	if srv.URL == "" {
		return nil, fmt.Errorf("MCP server %q is not a remote server; nothing to re-authorize", srv.Name)
	}
	if err := removeMCPOAuthToken(srv.Name, srv.URL); err != nil {
		return nil, fmt.Errorf("clearing cached OAuth token for %q: %w", srv.Name, err)
	}
	srv.OAuth = true
	srv.Headers = withoutAuthorization(srv.Headers)
	ts, err := buildMCPToolset(srv)
	if err != nil {
		return nil, fmt.Errorf("building MCP toolset for %q: %w", srv.Name, err)
	}
	// Trigger the lazy connect, which runs the OAuth browser flow and lists the
	// server's tools. On failure the resilient toolset marks itself failed and
	// returns (nil, nil), so check that state explicitly.
	if _, err := ts.Tools(mcpReadonlyCtx{Context: ctx}); err != nil {
		return nil, fmt.Errorf("authorizing MCP server %q: %w", srv.Name, err)
	}
	if rt, ok := ts.(*resilientToolset); ok && rt.failed {
		return nil, fmt.Errorf("authorizing MCP server %q: connection failed", srv.Name)
	}
	return ts, nil
}
