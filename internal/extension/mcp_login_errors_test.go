package extension

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/oauth2"
)

// A URL the transport cannot dial must be reported as a login failure naming the
// server, not as a generic error: the user typed that name into the command.
func TestLoginMCP_ReportsConnectFailure(t *testing.T) {
	setTestHome(t)

	// A closed listener: the port refuses connections deterministically.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	err := LoginMCP(context.Background(), MCPServerConfig{Name: "dead", URL: url})
	if err == nil {
		t.Fatal("expected a connect error")
	}
	if !strings.Contains(err.Error(), `"dead"`) {
		t.Errorf("err = %v, want it to name the server", err)
	}
}

// A server that completes the handshake but rejects the tool listing must fail
// login: connecting successfully proves nothing if the token it issued is not
// usable, and reporting success there would cache credentials that fail on the
// first real tool call.
//
// The server is a real MCP server with its tools/list method replaced, so the
// handshake is genuine and only the listing fails — a canned initialize response
// would fail the handshake instead and test the wrong branch.
func TestLoginMCP_ReportsListToolsFailure(t *testing.T) {
	setTestHome(t)

	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "half", Version: "1"}, nil)
	mcpSrv.AddTool(&mcp.Tool{
		Name:        "ping",
		Description: "unused",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Refuse only the listing request; everything else is served normally.
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(body))
		if bytes.Contains(body, []byte(`"tools/list"`)) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"listing unavailable"}`))
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: "seeded", RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken("half", srv.URL, cfg, tok); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	err := LoginMCP(context.Background(), MCPServerConfig{Name: "half", URL: srv.URL})
	if err == nil {
		t.Fatal("expected an error when the tool listing fails")
	}
	if !strings.Contains(err.Error(), `"half"`) {
		t.Errorf("err = %v, want it to name the server", err)
	}
	if errors.Is(err, ErrMCPLoginReusedToken) {
		t.Error("reported success even though the listing failed")
	}
}

// Handler construction can fail before any request is sent — a sandbox that
// forbids a loopback listener does exactly that. Login must report it as a login
// failure naming the server, not as a transport error the user cannot place.
func TestLoginMCP_ReportsHandlerSetupFailure(t *testing.T) {
	setTestHome(t)
	refuseLoopbackListen(t)

	err := LoginMCP(context.Background(), MCPServerConfig{Name: "sandboxed", URL: "https://mcp.example.com/mcp"})
	if err == nil {
		t.Fatal("expected a handler setup error")
	}
	if !strings.Contains(err.Error(), `"sandboxed"`) || !strings.Contains(err.Error(), "preparing OAuth login") {
		t.Errorf("err = %v, want it to name the server and the stage", err)
	}
}

// Headers configured on the server must travel with login's connection too: a
// server that gates on a custom header would otherwise be refused as
// unauthorized when it simply never received its header.
func TestLoginMCP_PresentsConfiguredHeaders(t *testing.T) {
	setTestHome(t)

	const token = "seeded-access-token"
	var seen []string
	mcpSrv := mcp.NewServer(&mcp.Implementation{Name: "headered", Version: "1"}, nil)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpSrv }, nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("X-Api-Key"))
		if r.Header.Get("X-Api-Key") != "abc-123" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"missing header"}`))
			return
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	cfg := &oauth2.Config{ClientID: "cid", Endpoint: oauth2.Endpoint{TokenURL: "https://as.example.com/token"}}
	tok := &oauth2.Token{AccessToken: token, RefreshToken: "r", Expiry: time.Now().Add(time.Hour)}
	if err := saveMCPOAuthToken("headered", srv.URL, cfg, tok); err != nil {
		t.Fatalf("seeding token: %v", err)
	}

	err := LoginMCP(context.Background(), MCPServerConfig{
		Name:    "headered",
		URL:     srv.URL,
		Headers: map[string]string{"X-Api-Key": "abc-123"},
	})
	if !errors.Is(err, ErrMCPLoginReusedToken) {
		t.Fatalf("err = %v, want ErrMCPLoginReusedToken", err)
	}
	for _, h := range seen {
		if h != "abc-123" {
			t.Errorf("server saw X-Api-Key = %q, want the configured value", h)
		}
	}
}

// loginOutcome has four cases and two of them cannot be reached end to end
// without an authorization server: a fresh login mints a token, and only a server
// that issues one produces that. The table covers all four so the ordinary
// success case is asserted rather than assumed.
func TestLoginOutcome(t *testing.T) {
	tests := []struct {
		name     string
		before   string
		hadToken bool
		after    string
		cached   bool
		want     error
	}{
		{
			name:   "server issued no token",
			cached: false,
			want:   errLoginNoToken,
		},
		{
			name:     "cached token was presented and accepted",
			before:   "100/24",
			hadToken: true,
			after:    "100/24",
			cached:   true,
			want:     ErrMCPLoginReusedToken,
		},
		{
			name:     "freshly authorized",
			hadToken: false,
			after:    "200/24",
			cached:   true,
			want:     nil,
		},
		{
			// A stale cached token replaced by a new one: the fingerprints differ,
			// so this is a fresh login even though a token existed beforehand.
			name:     "stale cached token replaced",
			before:   "100/24",
			hadToken: true,
			after:    "300/24",
			cached:   true,
			want:     nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := loginOutcome("srv", tc.before, tc.hadToken, tc.after, tc.cached)
			switch {
			case tc.want == nil && err != nil:
				t.Fatalf("err = %v, want nil", err)
			case errors.Is(tc.want, ErrMCPLoginReusedToken) && !errors.Is(err, ErrMCPLoginReusedToken):
				t.Fatalf("err = %v, want ErrMCPLoginReusedToken", err)
			case errors.Is(tc.want, errLoginNoToken) && !errors.Is(err, errLoginNoToken):
				t.Fatalf("err = %v, want errLoginNoToken", err)
			}
		})
	}
}
