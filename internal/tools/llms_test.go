package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
)

// newLLMSTestServer starts an httptest TLS server serving a fixed body and
// returns the server plus a toolset configured to allow its host.
func newLLMSTestServer(t *testing.T, body string) (*httptest.Server, *LLMSToolset) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	// httptest.NewTLSServer listens on 127.0.0.1, which the production SSRF
	// guard rejects. Allow it for the duration of the test.
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{
			{Name: "test", URL: srv.URL + "/llms.txt"},
		},
	})
	return srv, ts
}

// withLLMSClient overrides the HTTP client used to fetch documentation for
// the duration of the test.
func withLLMSClient(t *testing.T, c *http.Client) {
	t.Helper()
	prev := llmsClientOverride
	llmsClientOverride = c
	t.Cleanup(func() { llmsClientOverride = prev })
}

func TestLLMSFetchDocs(t *testing.T) {
	srv, ts := newLLMSTestServer(t, "# Test Docs\n\nSome content")
	out, err := ts.fetchDocs(context.Background(), srv.URL+"/page.md")
	if err != nil {
		t.Fatalf("fetchDocs() error = %v", err)
	}
	if out.Error != "" {
		t.Fatalf("fetchDocs() error = %q", out.Error)
	}
	if !strings.Contains(out.Content, "Test Docs") {
		t.Fatalf("expected content to contain 'Test Docs', got %q", out.Content)
	}
}

func TestLLMSHostAllowed(t *testing.T) {
	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{
			{Name: "adk", URL: "https://adk.dev/llms.txt"},
		},
	})
	if !ts.hostAllowed("adk.dev") {
		t.Fatal("expected adk.dev to be allowed")
	}
	if ts.hostAllowed("evil.com") {
		t.Fatal("expected evil.com to be rejected")
	}
}

func TestLLMSRejectsNonHTTPS(t *testing.T) {
	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{
			{Name: "adk", URL: "https://adk.dev/llms.txt"},
		},
	})
	out, _ := ts.fetchDocs(context.Background(), "http://adk.dev/page.md")
	if !strings.Contains(out.Error, "https") {
		t.Fatalf("expected https rejection, got %q", out.Error)
	}
}
