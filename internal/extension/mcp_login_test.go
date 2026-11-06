package extension

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// A stdio server has no bearer token, so login must refuse before touching the
// network. Refusing early is what keeps `pi mcp login` from looking like a hang
// on a server that was never going to open a browser.
func TestLoginMCP_RejectsStdioServer(t *testing.T) {
	setTestHome(t)

	err := LoginMCP(context.Background(), MCPServerConfig{Name: "filesystem", Command: "npx"})
	if err == nil {
		t.Fatal("expected an error for a stdio server")
	}
	if !strings.Contains(err.Error(), "no url") {
		t.Errorf("err = %v, want it to explain that a stdio server has no url", err)
	}
}

// A server that needs no credentials never issues a token, and reporting success
// there would tell the user their credentials are cached when they are not. The
// command needs a distinguishable failure so it can say so.
//
// The server here is a real MCP server rather than a canned initialize response:
// a hand-written reply can satisfy the handshake and then leave the SDK waiting
// on a protocol detail, which would test the timeout instead of the diagnosis.
func TestLoginMCP_ReportsServerThatNeedsNoAuthorization(t *testing.T) {
	setTestHome(t)

	var mu sync.Mutex
	var authHeaders []string
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "open", Version: "1"}, nil)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		nil,
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		mu.Unlock()
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	err := LoginMCP(context.Background(), MCPServerConfig{Name: "open", URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error: the server issued no token")
	}
	if !strings.Contains(err.Error(), "no token was cached") {
		t.Errorf("err = %v, want it to say no token was cached", err)
	}

	mu.Lock()
	defer mu.Unlock()
	for _, h := range authHeaders {
		if h != "" {
			t.Errorf("sent Authorization %q to a server with no cached credentials", h)
		}
	}
}

// Working credentials already on disk must be reported as reuse, not re-minted:
// the whole point of the sentinel is that `pi mcp login` on an authorized server
// says so instead of opening a browser the user does not need.
//
// The server below accepts exactly the seeded token and refuses everything else,
// which is what makes "the connection succeeded" mean "the cached token was
// presented and honored".
func TestLoginMCP_ReusesWorkingCachedToken(t *testing.T) {
	setTestHome(t)

	const (
		name  = "guarded"
		token = "seeded-access-token"
	)
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "guarded", Version: "1"}, nil)
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		nil,
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: token, RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(name, srv.URL, cfg, tok); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	err := LoginMCP(context.Background(), MCPServerConfig{Name: name, URL: srv.URL})
	if !errors.Is(err, ErrMCPLoginReusedToken) {
		t.Fatalf("err = %v, want ErrMCPLoginReusedToken", err)
	}
}

// Login is interactive by definition, but it must not leave the process-wide
// flag on: a later headless run in the same process would then wait on a browser
// nobody is watching.
func TestLoginMCP_RestoresInteractiveFlag(t *testing.T) {
	setTestHome(t)
	before := interactiveOAuth.Load()

	_ = LoginMCP(context.Background(), MCPServerConfig{Name: "stdiopath", Command: "npx"})

	if got := interactiveOAuth.Load(); got != before {
		t.Errorf("interactiveOAuth = %v after login, want it restored to %v", got, before)
	}
}

// HasCachedMCPToken is what `pi mcp list` reports, so it must agree with the
// cache the next session reads: present after a save, absent after a removal.
func TestHasCachedMCPToken(t *testing.T) {
	setTestHome(t)
	const (
		name = "srv"
		url  = "https://mcp.example.com/mcp"
	)

	if HasCachedMCPToken(name, url) {
		t.Fatal("reported credentials before any were stored")
	}

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "cached", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken(name, url, cfg, tok); err != nil {
		t.Fatalf("seeding token: %v", err)
	}
	if !HasCachedMCPToken(name, url) {
		t.Error("did not report stored credentials")
	}

	// A different URL is a different identity: a token minted for one endpoint
	// must never be reported as valid for another.
	if HasCachedMCPToken(name, "https://elsewhere.example/mcp") {
		t.Error("reported credentials for a different endpoint")
	}

	if err := removeMCPOAuthToken(name, url); err != nil {
		t.Fatalf("removing token: %v", err)
	}
	if HasCachedMCPToken(name, url) {
		t.Error("still reported credentials after removal")
	}
}
