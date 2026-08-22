package tools

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

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

// adkToolset returns a toolset configured with a single adk.dev source.
func adkToolset() *LLMSToolset {
	return NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{
			{Name: "adk", URL: "https://adk.dev/llms.txt"},
		},
	})
}

func TestLLMSFetchDocs(t *testing.T) {
	srv, ts := newLLMSTestServer(t, "# Test Docs\n\nSome content")
	out, err := ts.FetchDocs(context.Background(), srv.URL+"/page.md")
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

func TestLLMSFetchDocsEmptyURL(t *testing.T) {
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "   ")
	if !strings.Contains(out.Error, "url is required") {
		t.Fatalf("expected 'url is required', got %q", out.Error)
	}
}

func TestLLMSFetchDocsParseError(t *testing.T) {
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "://bad")
	if !strings.Contains(out.Error, "parse url") {
		t.Fatalf("expected 'parse url', got %q", out.Error)
	}
}

func TestLLMSFetchDocsNoHost(t *testing.T) {
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "https:///path")
	if !strings.Contains(out.Error, "url has no host") {
		t.Fatalf("expected 'url has no host', got %q", out.Error)
	}
}

func TestLLMSFetchDocsRejectsPrivateHost(t *testing.T) {
	// No withAllowedTestHosts here: 127.0.0.1 must be rejected by the SSRF guard.
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "https://127.0.0.1/page.md")
	if !strings.Contains(out.Error, "url rejected") {
		t.Fatalf("expected 'url rejected', got %q", out.Error)
	}
}

func TestLLMSFetchDocsDisallowedHost(t *testing.T) {
	// Use a host that resolves to a public IP so the SSRF guard passes and the
	// host allow-list check is what rejects it. localhost is private, so use a
	// clearly-public-but-unreachable host; the allow-list check runs before any
	// network dial.
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "https://example.com/page.md")
	if !strings.Contains(out.Error, "not an allowed documentation source") {
		t.Fatalf("expected host rejection, got %q", out.Error)
	}
}

func TestLLMSFetchDocsHTTPError(t *testing.T) {
	// A client whose Do always fails exercises the "fetch failed" branch.
	withLLMSClient(t, &http.Client{Transport: errorTransport{}})
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "https://adk.dev/page.md")
	if !strings.Contains(out.Error, "fetch failed") {
		t.Fatalf("expected 'fetch failed', got %q", out.Error)
	}
}

func TestLLMSFetchDocsNon200(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	})
	out, _ := ts.FetchDocs(context.Background(), srv.URL+"/page.md")
	if !strings.Contains(out.Error, "unexpected status 500") {
		t.Fatalf("expected 'unexpected status 500', got %q", out.Error)
	}
}

func TestLLMSHostAllowed(t *testing.T) {
	ts := adkToolset()
	if !ts.hostAllowed("adk.dev") {
		t.Fatal("expected adk.dev to be allowed")
	}
	if ts.hostAllowed("evil.com") {
		t.Fatal("expected evil.com to be rejected")
	}
}

func TestLLMSHostAllowedSkipsInvalidSource(t *testing.T) {
	// A source with an unparseable URL must be skipped, not panic.
	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{
			{Name: "bad", URL: "://not-a-url"},
			{Name: "adk", URL: "https://adk.dev/llms.txt"},
		},
	})
	if !ts.hostAllowed("adk.dev") {
		t.Fatal("expected adk.dev to be allowed despite invalid source")
	}
}

func TestLLMSRejectsNonHTTPS(t *testing.T) {
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "http://adk.dev/page.md")
	if !strings.Contains(out.Error, "https") {
		t.Fatalf("expected https rejection, got %q", out.Error)
	}
}

func TestLLMSBuildDescription(t *testing.T) {
	withSources := buildLLMSDescription([]config.LLMSSource{
		{Name: "adk", URL: "https://adk.dev/llms.txt"},
		{Name: "langchain", URL: "https://python.langchain.com/llms.txt"},
	})
	if !strings.Contains(withSources, "adk") || !strings.Contains(withSources, "langchain") {
		t.Fatalf("expected both source names in description, got %q", withSources)
	}

	without := buildLLMSDescription(nil)
	if !strings.Contains(without, "No llms.txt sources configured") {
		t.Fatalf("expected 'No llms.txt sources configured', got %q", without)
	}
}

func TestLLMSToolsetName(t *testing.T) {
	ts := NewLLMSToolset(nil)
	if ts.Name() != "llms" {
		t.Fatalf("Name() = %q, want 'llms'", ts.Name())
	}
}

func TestLLMSToolsetTools(t *testing.T) {
	ts := adkToolset()
	ctx := &mockReadonlyContext{Context: context.Background()}
	tools, err := ts.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("Tools() returned %d tools, want 1", len(tools))
	}
}

func TestLLMSToolInvoke(t *testing.T) {
	// Exercise the full tool.Tool Run path (functiontool factory closure),
	// not just the direct handler.
	srv, ts := newLLMSTestServer(t, "# Invoked Docs")
	ctx := &mockReadonlyContext{Context: context.Background()}
	tools, err := ts.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("Tools() returned %d tools, want 1", len(tools))
	}

	// The tool is a coercingTool wrapping the functiontool; runTool invokes it
	// through the Run interface, covering the factory closure.
	m := runTool(t, tools[0], map[string]any{"url": srv.URL + "/page.md"})
	content, _ := m["content"].(string)
	if !strings.Contains(content, "Invoked Docs") {
		t.Fatalf("expected content to contain 'Invoked Docs', got %q", content)
	}
}

// errorTransport is a RoundTripper that always fails, to exercise the
// "fetch failed" branch without a real network.
type errorTransport struct{}

func (errorTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

// errorBodyTransport returns a 200 response whose body fails on read, to
// exercise the "read body" error branch.
type errorBodyTransport struct{}

func (errorBodyTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(errReader{}),
		Header:     make(http.Header),
	}, nil
}

// errReader is an io.Reader that always fails.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestLLMSFetchDocsBodyReadError(t *testing.T) {
	withLLMSClient(t, &http.Client{Transport: errorBodyTransport{}})
	ts := adkToolset()
	out, _ := ts.FetchDocs(context.Background(), "https://adk.dev/page.md")
	if !strings.Contains(out.Error, "read body") {
		t.Fatalf("expected 'read body', got %q", out.Error)
	}
}

var _ tool.Tool = (*coercingTool)(nil)
var _ agent.Context = mockToolCtx{}

func TestLLMSFetchDocsTruncated(t *testing.T) {
	// A body larger than llmsMaxBytes must be an explicit error, not a
	// silently truncated success.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, llmsMaxBytes+1024))
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	})
	out, _ := ts.FetchDocs(context.Background(), srv.URL+"/big.md")
	if out.Error == "" {
		t.Fatal("expected size error for oversized body")
	}
	if !strings.Contains(out.Error, "byte limit") {
		t.Fatalf("expected 'byte limit' error, got %q", out.Error)
	}
}

func TestLLMSRedirectToDisallowedHostRejected(t *testing.T) {
	// An allowed host must not be able to bounce the client to a disallowed
	// destination via a redirect. The target here is a public hostname that is
	// not a configured source; CheckRedirect rejects it before any dial, so
	// the test needs no network. (Two httptest servers cannot model this:
	// both listen on 127.0.0.1, and the allowlist compares hostnames only.)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/evil.md", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	})
	out, _ := ts.FetchDocs(context.Background(), srv.URL+"/start.md")
	if out.Content != "" {
		t.Fatalf("redirected content must not be returned, got %q", out.Content)
	}
	if !strings.Contains(out.Error, "fetch failed") {
		t.Fatalf("expected redirect rejection surfaced as fetch failure, got %q", out.Error)
	}
}

func TestLLMSRedirectToAllowedHostFollowed(t *testing.T) {
	// Same-host redirects (the common case for doc sites) still work.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start.md" {
			http.Redirect(w, r, "/page.md", http.StatusFound)
			return
		}
		w.Write([]byte("# Redirected Docs"))
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	})
	out, err := ts.FetchDocs(context.Background(), srv.URL+"/start.md")
	if err != nil {
		t.Fatalf("FetchDocs() error = %v", err)
	}
	if out.Error != "" {
		t.Fatalf("same-host redirect should be followed, got %q", out.Error)
	}
	if !strings.Contains(out.Content, "Redirected Docs") {
		t.Fatalf("expected redirected content, got %q", out.Content)
	}
}

func TestLLMSBuildDescriptionIncludesURLs(t *testing.T) {
	desc := buildLLMSDescription([]config.LLMSSource{
		{Name: "adk", URL: "https://adk.dev/llms.txt"},
	})
	if !strings.Contains(desc, "https://adk.dev/llms.txt") {
		t.Fatalf("expected source URL in description so the model can bootstrap, got %q", desc)
	}
}

func TestLLMSSources(t *testing.T) {
	want := []config.LLMSSource{
		{Name: "adk", URL: "https://adk.dev/llms.txt"},
		{Name: "langchain", URL: "https://python.langchain.com/llms.txt"},
	}
	ts := NewLLMSToolset(&config.LLMSConfig{Sources: want})
	got := ts.Sources()
	if len(got) != len(want) {
		t.Fatalf("Sources() returned %d sources, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Sources()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	if NewLLMSToolset(nil).Sources() != nil {
		t.Fatal("Sources() on a nil config should be nil")
	}
}

func TestLLMSCheckRedirectURL(t *testing.T) {
	// Exercise every branch of the CheckRedirect hook directly, without a
	// server: the hook is what keeps an open redirect on an allowed host from
	// defeating the https, SSRF, and allowlist guards.
	ts := adkToolset()
	mustURL := func(raw string) *url.URL {
		t.Helper()
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		return u
	}
	hops := func(n int) []*http.Request {
		via := make([]*http.Request, n)
		for i := range via {
			via[i] = &http.Request{URL: mustURL("https://adk.dev/hop")}
		}
		return via
	}

	cases := []struct {
		name    string
		target  string
		via     []*http.Request
		wantErr string // empty means the hop must be allowed
	}{
		{name: "same allowed host", target: "https://adk.dev/page.md", via: hops(1)},
		{name: "too many redirects", target: "https://adk.dev/page.md", via: hops(llmsMaxRedirects), wantErr: "redirects"},
		{name: "downgrade to http", target: "http://adk.dev/page.md", via: hops(1), wantErr: "https"},
		{name: "private host", target: "https://127.0.0.1/page.md", via: hops(1), wantErr: "url rejected"},
		{name: "disallowed host", target: "https://example.com/page.md", via: hops(1), wantErr: "not an allowed documentation source"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ts.checkRedirectURL(mustURL(tc.target), tc.via)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkRedirectURL() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkRedirectURL() = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestLLMSRedirectLimitEnforced(t *testing.T) {
	// A redirect loop on an allowed host must stop at llmsMaxRedirects rather
	// than spin until the client timeout.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop.md", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	})
	out, _ := ts.FetchDocs(context.Background(), srv.URL+"/loop.md")
	if out.Content != "" {
		t.Fatalf("looping redirect must not return content, got %q", out.Content)
	}
	if !strings.Contains(out.Error, "fetch failed") || !strings.Contains(out.Error, "redirects") {
		t.Fatalf("expected redirect-limit failure, got %q", out.Error)
	}
}
