package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
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

// newLLMSCacheTestServer starts a TLS server that counts requests and returns
// a body on 200, plus a toolset with caching enabled into a temp dir.
func newLLMSCacheTestServer(t *testing.T) (*httptest.Server, *LLMSToolset, *int32) {
	t.Helper()
	var requests int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Write([]byte("# Cached Docs"))
	}))
	t.Cleanup(srv.Close)

	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	// A not-yet-existing subdirectory, as ~/.pi-go/llms-cache is on first
	// use, so the tests exercise the directory creation path too.
	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{
			{Name: "test", URL: srv.URL + "/llms.txt"},
		},
	}, filepath.Join(t.TempDir(), "llms-cache"))
	return srv, ts, &requests
}

func TestLLMSFetchDocsCachesSecondFetch(t *testing.T) {
	srv, ts, requests := newLLMSCacheTestServer(t)
	u := srv.URL + "/page.md"

	out, err := ts.FetchDocs(context.Background(), u)
	if err != nil || out.Error != "" {
		t.Fatalf("first FetchDocs() = (%q, %v), want nil error", out.Error, err)
	}

	// A second fetch within llmsCacheTTL must be served from disk: no HTTP call.
	out, err = ts.FetchDocs(context.Background(), u)
	if err != nil {
		t.Fatalf("second FetchDocs() error = %v", err)
	}
	if !strings.Contains(out.Content, "Cached Docs") {
		t.Fatalf("expected cached content, got %q", out.Content)
	}
	if got := atomic.LoadInt32(requests); got != 1 {
		t.Fatalf("expected 1 HTTP request on cached hit, got %d", got)
	}
}

func TestLLMSFetchCacheRevalidatesStale(t *testing.T) {
	var requests int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		if n == 2 && r.Header.Get("If-None-Match") == `"v1"` {
			// Revalidation request: server confirms the copy is current.
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write([]byte("Revalidated Docs"))
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())

	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	}, t.TempDir())
	u, err := url.Parse(srv.URL + "/page.md")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	if _, err := ts.FetchDocs(context.Background(), u.String()); err != nil {
		t.Fatalf("first FetchDocs() error = %v", err)
	}

	// Age the cached entry past llmsCacheTTL so the next read revalidates.
	e := ts.cacheRead(u)
	e.FetchedAt = time.Now().Add(-2 * time.Hour).Unix()
	ts.cacheWrite(e)

	if _, err := ts.FetchDocs(context.Background(), u.String()); err != nil {
		t.Fatalf("second FetchDocs() error = %v", err)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("expected 2 HTTP requests (initial + revalidate), got %d", got)
	}
}

func TestLLMSFetchCacheFallsBackOnNetworkError(t *testing.T) {
	// First fetch populates the cache; the second uses a transport that always
	// fails, and must return the cached copy rather than an error.
	srv, ts, _ := newLLMSCacheTestServer(t)
	u := srv.URL + "/page.md"

	out, err := ts.FetchDocs(context.Background(), u)
	if err != nil || out.Error != "" {
		t.Fatalf("initial FetchDocs() = (%q, %v), want nil error", out.Error, err)
	}
	// Age the entry past llmsCacheTTL: a fresh entry would be served from
	// disk without ever reaching the failing transport, and the test would
	// pass without exercising the fallback.
	ageLLMSCacheEntry(t, ts, u, 2*time.Hour)

	withLLMSClient(t, &http.Client{Transport: errorTransport{}})
	out, err = ts.FetchDocs(context.Background(), u)
	if err != nil {
		t.Fatalf("FetchDocs() error = %v", err)
	}
	if !strings.Contains(out.Content, "Cached Docs") {
		t.Fatalf("expected cached content on network failure, got %q", out.Content)
	}
}

func TestNewLLMSCachedToolsetUsesDefaultCacheDir(t *testing.T) {
	// The voice agent, one-shot CLI, and piagent all build the toolset through
	// NewLLMSCachedToolset, so they must all share one cache directory. Pinning
	// that here guards against a regression that silently disables the shared
	// cache for one mode.
	home := t.TempDir()
	testenv.SetHome(t, home)
	ts := NewLLMSCachedToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "adk", URL: "https://adk.dev/llms.txt"}},
	})
	if ts.cacheDir == "" {
		t.Fatal("NewLLMSCachedToolset must enable caching")
	}
	if ts.cacheDir != LLMSDefaultCacheDir() {
		t.Fatalf("cacheDir = %q, want %q", ts.cacheDir, LLMSDefaultCacheDir())
	}
	if want := filepath.Join(home, ".pi-go", "llms-cache"); ts.cacheDir != want {
		t.Fatalf("cacheDir = %q, want %q (under the test HOME)", ts.cacheDir, want)
	}
}

func TestLLMSDefaultCacheDirEmptyWithoutHome(t *testing.T) {
	// No usable home directory must disable caching rather than point the
	// cache at a relative path or fail construction.
	testenv.UnsetHome(t)
	if got := LLMSDefaultCacheDir(); got != "" {
		t.Fatalf("LLMSDefaultCacheDir() = %q, want empty without a home directory", got)
	}
	ts := NewLLMSCachedToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "adk", URL: "https://adk.dev/llms.txt"}},
	})
	if ts.cacheDir != "" {
		t.Fatalf("cacheDir = %q, want caching disabled without a home directory", ts.cacheDir)
	}
}

// ageLLMSCacheEntry rewrites the cached entry for rawURL so it was fetched
// `by` ago, forcing the next FetchDocs to revalidate (or, past
// llmsCacheMaxAge, to refuse the entry as a fallback).
func ageLLMSCacheEntry(t *testing.T, ts *LLMSToolset, rawURL string, by time.Duration) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	e := ts.cacheRead(u)
	if e == nil {
		t.Fatalf("no cache entry for %s", rawURL)
	}
	e.FetchedAt = time.Now().Add(-by).Unix()
	ts.cacheWrite(e)
}

func TestLLMSFetchCacheStaleReplacedBy200(t *testing.T) {
	// When revalidation returns a fresh 200 the new body must replace the
	// cached one, and the conditional headers must have been sent.
	var requests int32
	var sawConditional atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&requests, 1)
		if r.Header.Get("If-None-Match") == `"v1"` && r.Header.Get("If-Modified-Since") == "Mon, 02 Jan 2006 15:04:05 GMT" {
			sawConditional.Store(true)
		}
		w.Header().Set("ETag", fmt.Sprintf(`"v%d"`, n))
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		fmt.Fprintf(w, "Docs v%d", n)
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())
	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	}, t.TempDir())
	u := srv.URL + "/page.md"

	out, _ := ts.FetchDocs(context.Background(), u)
	if out.Content != "Docs v1" {
		t.Fatalf("first fetch = %q, want %q", out.Content, "Docs v1")
	}
	ageLLMSCacheEntry(t, ts, u, 2*time.Hour)

	out, _ = ts.FetchDocs(context.Background(), u)
	if out.Content != "Docs v2" {
		t.Fatalf("revalidated fetch = %q, want the replaced body %q", out.Content, "Docs v2")
	}
	if !sawConditional.Load() {
		t.Fatal("revalidation request did not carry If-None-Match/If-Modified-Since")
	}
	// The replacement is what the cache now serves, with no further request.
	out, _ = ts.FetchDocs(context.Background(), u)
	if out.Content != "Docs v2" || atomic.LoadInt32(&requests) != 2 {
		t.Fatalf("third fetch = %q after %d requests, want %q from cache after 2", out.Content, requests, "Docs v2")
	}
}

func TestLLMSFetchCacheFallbackRefusedPastMaxAge(t *testing.T) {
	// An entry older than llmsCacheMaxAge is not a valid network-down
	// fallback: the caller sees the fetch error instead of a day-old page.
	srv, ts, _ := newLLMSCacheTestServer(t)
	u := srv.URL + "/page.md"
	if out, _ := ts.FetchDocs(context.Background(), u); out.Error != "" {
		t.Fatalf("initial fetch error: %s", out.Error)
	}
	ageLLMSCacheEntry(t, ts, u, 25*time.Hour)

	withLLMSClient(t, &http.Client{Transport: errorTransport{}})
	out, _ := ts.FetchDocs(context.Background(), u)
	if !strings.Contains(out.Error, "fetch failed") {
		t.Fatalf("expected 'fetch failed' past max age, got content=%q error=%q", out.Content, out.Error)
	}
}

func TestLLMSFetchCacheFallsBackOn5xx(t *testing.T) {
	// A transient upstream 5xx on revalidation serves the reasonably fresh
	// cached copy; without a cached copy the status is reported.
	var fail atomic.Bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte("Good Docs"))
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())
	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	}, t.TempDir())
	u := srv.URL + "/page.md"

	fail.Store(true)
	if out, _ := ts.FetchDocs(context.Background(), u); !strings.Contains(out.Error, "unexpected status 502") {
		t.Fatalf("uncached 5xx: got content=%q error=%q, want unexpected status 502", out.Content, out.Error)
	}

	fail.Store(false)
	if out, _ := ts.FetchDocs(context.Background(), u); out.Content != "Good Docs" {
		t.Fatalf("populate: got content=%q error=%q", out.Content, out.Error)
	}
	ageLLMSCacheEntry(t, ts, u, 2*time.Hour)

	fail.Store(true)
	if out, _ := ts.FetchDocs(context.Background(), u); out.Content != "Good Docs" {
		t.Fatalf("5xx with cache: got content=%q error=%q, want cached body", out.Content, out.Error)
	}
	ageLLMSCacheEntry(t, ts, u, 25*time.Hour)
	if out, _ := ts.FetchDocs(context.Background(), u); !strings.Contains(out.Error, "unexpected status 502") {
		t.Fatalf("5xx past max age: got content=%q error=%q, want the status error", out.Content, out.Error)
	}
}

func TestLLMSFetchCache304WithoutEntry(t *testing.T) {
	// A server answering 304 to an unconditional request has nothing to
	// reuse; that must be an explicit error, not empty content.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())
	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	}, t.TempDir())
	out, _ := ts.FetchDocs(context.Background(), srv.URL+"/page.md")
	if !strings.Contains(out.Error, "304") {
		t.Fatalf("got content=%q error=%q, want a 304-without-cache error", out.Content, out.Error)
	}
}

func TestLLMSFetchCacheIgnoresBadEntries(t *testing.T) {
	// Corrupt JSON, an entry recorded for a different URL, and an oversized
	// body are all treated as a miss: the page is fetched again and the bad
	// file replaced.
	srv, ts, requests := newLLMSCacheTestServer(t)
	rawURL := srv.URL + "/page.md"
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	path := ts.cachePath(u)

	cases := []struct {
		name string
		data []byte
	}{
		{"corrupt", []byte("{not json")},
		{"other-url", mustJSON(t, llmsCacheEntry{URL: srv.URL + "/other.md", Body: "Wrong Page", FetchedAt: time.Now().Unix()})},
		{"oversized", mustJSON(t, llmsCacheEntry{URL: rawURL, Body: strings.Repeat("x", llmsMaxBytes+1), FetchedAt: time.Now().Unix()})},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(ts.cacheDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if got := ts.cacheRead(u); got != nil {
				t.Fatalf("cacheRead returned %+v for a %s entry, want nil", got, tc.name)
			}
			out, _ := ts.FetchDocs(context.Background(), rawURL)
			if out.Content != "# Cached Docs" {
				t.Fatalf("got content=%q error=%q, want a fresh fetch", out.Content, out.Error)
			}
			if got := atomic.LoadInt32(requests); got != int32(i+1) {
				t.Fatalf("expected %d HTTP requests after refetch, got %d", i+1, got)
			}
			if got := ts.cacheRead(u); got == nil || got.Body != "# Cached Docs" {
				t.Fatalf("bad entry was not replaced by the fresh fetch: %+v", got)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLLMSFetchCacheDisabledWritesNothing(t *testing.T) {
	// NewLLMSToolset (no cache dir) must never touch the disk and must refetch
	// every time.
	srv, _, requests := newLLMSCacheTestServer(t)
	ts := NewLLMSToolset(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	})
	u := srv.URL + "/page.md"
	for i := 1; i <= 2; i++ {
		if out, _ := ts.FetchDocs(context.Background(), u); out.Content != "# Cached Docs" {
			t.Fatalf("fetch %d: got content=%q error=%q", i, out.Content, out.Error)
		}
	}
	if got := atomic.LoadInt32(requests); got != 2 {
		t.Fatalf("expected 2 HTTP requests with caching disabled, got %d", got)
	}
	pu, _ := url.Parse(u)
	if got := ts.cacheRead(pu); got != nil {
		t.Fatalf("cacheRead with caching disabled = %+v, want nil", got)
	}
	ts.cacheWrite(&llmsCacheEntry{URL: u, Body: "x"}) // must be a no-op, not a panic
}

func TestLLMSFetchCacheWriteIsPrivateAndAtomic(t *testing.T) {
	// The shared cache directory is created 0700 and entries 0600, and no
	// temp file is left behind after a write.
	srv, ts, _ := newLLMSCacheTestServer(t)
	u := srv.URL + "/page.md"
	if out, _ := ts.FetchDocs(context.Background(), u); out.Error != "" {
		t.Fatalf("fetch error: %s", out.Error)
	}
	entries, err := os.ReadDir(ts.cacheDir)
	if err != nil {
		t.Fatalf("read cache dir: %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("cache dir = %v, want exactly one .json entry and no temp files", names)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	di, err := os.Stat(ts.cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("cache dir perm = %o, want 0700", got)
	}
	fi, err := os.Stat(filepath.Join(ts.cacheDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("cache file perm = %o, want 0600", got)
	}
}

func TestLLMSFetchCacheKeyIncludesQuery(t *testing.T) {
	// Two URLs that differ only in query string are distinct cache entries.
	var requests int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		fmt.Fprintf(w, "q=%s", r.URL.RawQuery)
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())
	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	}, t.TempDir())

	a, _ := ts.FetchDocs(context.Background(), srv.URL+"/page.md?v=1")
	b, _ := ts.FetchDocs(context.Background(), srv.URL+"/page.md?v=2")
	if a.Content != "q=v=1" || b.Content != "q=v=2" {
		t.Fatalf("got %q and %q, want distinct bodies per query", a.Content, b.Content)
	}
	if got := atomic.LoadInt32(&requests); got != 2 {
		t.Fatalf("expected 2 HTTP requests for 2 distinct URLs, got %d", got)
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

func TestLLMSCacheWriteFailuresAreIgnored(t *testing.T) {
	// Cache writes are best-effort: an unparsable URL or an uncreatable cache
	// directory (here, a path below a regular file) must neither panic nor
	// leave anything behind.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ts := NewLLMSToolsetWithCache(nil, filepath.Join(blocker, "llms-cache"))
	ts.cacheWrite(&llmsCacheEntry{URL: "https://adk.dev/page.md", Body: "x", FetchedAt: time.Now().Unix()})
	if _, err := os.Stat(ts.cacheDir); err == nil {
		t.Fatal("cache dir below a file: directory exists, want nothing created")
	}

	ts = NewLLMSToolsetWithCache(nil, filepath.Join(t.TempDir(), "llms-cache"))
	ts.cacheWrite(&llmsCacheEntry{URL: "https://adk.dev/\x7f", Body: "x"})
	if _, err := os.Stat(ts.cacheDir); !os.IsNotExist(err) {
		t.Fatalf("unparsable URL: cache dir was created (stat err = %v), want nothing written", err)
	}
}

func TestLLMSFetchCacheNoFallbackOn4xx(t *testing.T) {
	// A 4xx on revalidation is the origin's verdict on the page (gone, moved,
	// access revoked): the cached body must not be served as current.
	var status atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := int(status.Load()); code != 0 {
			w.WriteHeader(code)
			return
		}
		w.Write([]byte("Good Docs"))
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())
	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	}, filepath.Join(t.TempDir(), "llms-cache"))
	u := srv.URL + "/page.md"
	if out, _ := ts.FetchDocs(context.Background(), u); out.Content != "Good Docs" {
		t.Fatalf("populate: got content=%q error=%q", out.Content, out.Error)
	}
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone} {
		ageLLMSCacheEntry(t, ts, u, 2*time.Hour)
		status.Store(int32(code))
		out, _ := ts.FetchDocs(context.Background(), u)
		if out.Content != "" || !strings.Contains(out.Error, fmt.Sprintf("unexpected status %d", code)) {
			t.Fatalf("%d: got content=%q error=%q, want the status error and no cached body", code, out.Content, out.Error)
		}
	}
}

func TestLLMSFetchCacheIgnoresFragment(t *testing.T) {
	// page.md#a and page.md#b are the same resource on the wire and share one
	// cache entry; the request itself carries no fragment.
	var requests int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		if r.URL.Fragment != "" || strings.Contains(r.RequestURI, "#") {
			t.Errorf("fragment reached the server: %q", r.RequestURI)
		}
		w.Write([]byte("Sectioned Docs"))
	}))
	t.Cleanup(srv.Close)
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withLLMSClient(t, srv.Client())
	ts := NewLLMSToolsetWithCache(&config.LLMSConfig{
		Sources: []config.LLMSSource{{Name: "test", URL: srv.URL + "/llms.txt"}},
	}, filepath.Join(t.TempDir(), "llms-cache"))

	for _, frag := range []string{"#install", "#usage", ""} {
		out, _ := ts.FetchDocs(context.Background(), srv.URL+"/page.md"+frag)
		if out.Content != "Sectioned Docs" {
			t.Fatalf("%q: got content=%q error=%q", frag, out.Content, out.Error)
		}
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("expected 1 HTTP request for three fragments of one page, got %d", got)
	}
	entries, err := os.ReadDir(ts.cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache dir has %d entries (err %v), want exactly 1", len(entries), err)
	}
	pu, _ := url.Parse(srv.URL + "/page.md#install")
	if e := ts.cacheRead(pu); e != nil {
		t.Fatalf("cacheRead with a fragment found an entry keyed by the fragment: %+v", e)
	}
}

func TestLLMSCacheEvictsOldestPastBudget(t *testing.T) {
	// Past the entry budget the least-recently-written files go first, and
	// the total-bytes budget is enforced the same way.
	dir := filepath.Join(t.TempDir(), "llms-cache")
	ts := NewLLMSToolsetWithCache(nil, dir)
	base := time.Now().Add(-time.Hour)
	writeAged := func(i int, body string, age time.Duration) string {
		t.Helper()
		rawURL := fmt.Sprintf("https://adk.dev/p%d.md", i)
		ts.cacheWrite(&llmsCacheEntry{URL: rawURL, Body: body, FetchedAt: time.Now().Unix()})
		pu, _ := url.Parse(rawURL)
		when := base.Add(-age)
		if err := os.Chtimes(ts.cachePath(pu), when, when); err != nil {
			t.Fatal(err)
		}
		return rawURL
	}
	// llmsCacheMaxEntries entries, the lowest-numbered the oldest; all fit.
	for i := 0; i < llmsCacheMaxEntries; i++ {
		writeAged(i, "x", time.Duration(llmsCacheMaxEntries-i)*time.Second)
	}
	if n := countLLMSCacheFiles(t, dir); n != llmsCacheMaxEntries {
		t.Fatalf("before eviction: %d files, want %d", n, llmsCacheMaxEntries)
	}
	// One more (newest) evicts exactly the oldest, p0.
	newest := writeAged(llmsCacheMaxEntries, "x", 0)
	if n := countLLMSCacheFiles(t, dir); n != llmsCacheMaxEntries {
		t.Fatalf("after one over budget: %d files, want %d", n, llmsCacheMaxEntries)
	}
	p0, _ := url.Parse("https://adk.dev/p0.md")
	if ts.cacheRead(p0) != nil {
		t.Fatal("oldest entry p0 survived eviction")
	}
	p1, _ := url.Parse("https://adk.dev/p1.md")
	if ts.cacheRead(p1) == nil {
		t.Fatal("second-oldest entry p1 was evicted, want only the oldest gone")
	}
	nu, _ := url.Parse(newest)
	if ts.cacheRead(nu) == nil {
		t.Fatal("the entry just written was evicted")
	}

	// Bytes budget: a few large, old entries over llmsCacheMaxBytes in total
	// get trimmed oldest-first until the total fits, in a fresh directory.
	dir = filepath.Join(t.TempDir(), "llms-cache")
	ts = NewLLMSToolsetWithCache(nil, dir)
	big := strings.Repeat("y", llmsMaxBytes) // 2 MB per entry
	n := llmsCacheMaxBytes/llmsMaxBytes + 1  // one past the budget
	for i := 0; i < n; i++ {
		writeAged(i, big, time.Duration(n-i)*time.Second)
	}
	if got := countLLMSCacheFiles(t, dir); got >= n {
		t.Fatalf("bytes budget: %d files remain of %d, want at least one evicted", got, n)
	}
	if ts.cacheRead(p0) != nil {
		t.Fatal("bytes budget: oldest entry p0 survived eviction")
	}
}

func countLLMSCacheFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// requirePOSIXPerms skips tests that rely on permission bits being enforced:
// Windows ignores them and root bypasses them.
func requirePOSIXPerms(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
}

func TestLLMSCacheWriteSkipsUnwritableDir(t *testing.T) {
	// An existing cache directory that cannot be written to (CreateTemp
	// fails) is a silent no-op, not an error or a panic.
	requirePOSIXPerms(t)
	dir := filepath.Join(t.TempDir(), "llms-cache")
	if err := os.MkdirAll(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	ts := NewLLMSToolsetWithCache(nil, dir)
	ts.cacheWrite(&llmsCacheEntry{URL: "https://adk.dev/page.md", Body: "x", FetchedAt: time.Now().Unix()})
	if n := countLLMSCacheFiles(t, dir); n != 0 {
		t.Fatalf("%d files written into a read-only cache dir, want 0", n)
	}
}

func TestLLMSCacheWriteRenameFailureLeavesNoTempFile(t *testing.T) {
	// If the entry cannot be renamed into place (here the destination is a
	// directory), the temp file is removed rather than left to accumulate.
	dir := filepath.Join(t.TempDir(), "llms-cache")
	ts := NewLLMSToolsetWithCache(nil, dir)
	u, _ := url.Parse("https://adk.dev/page.md")
	if err := os.MkdirAll(ts.cachePath(u), 0o700); err != nil {
		t.Fatal(err)
	}
	ts.cacheWrite(&llmsCacheEntry{URL: u.String(), Body: "x", FetchedAt: time.Now().Unix()})
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("cache dir = %v, want only the blocking directory (no temp file left)", names)
	}
	if ts.cacheRead(u) != nil {
		t.Fatal("cacheRead returned an entry for a path that is a directory")
	}
}

func TestLLMSCacheEvictEdgeCases(t *testing.T) {
	// A missing cache directory is a no-op; non-entry files and
	// subdirectories in the cache directory are neither counted nor removed.
	ts := NewLLMSToolsetWithCache(nil, filepath.Join(t.TempDir(), "never-created"))
	ts.cacheEvict() // must not panic

	dir := filepath.Join(t.TempDir(), "llms-cache")
	ts = NewLLMSToolsetWithCache(nil, dir)
	if err := os.MkdirAll(filepath.Join(dir, "subdir.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "stray.tmp")
	if err := os.WriteFile(stray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts.cacheWrite(&llmsCacheEntry{URL: "https://adk.dev/page.md", Body: "x", FetchedAt: time.Now().Unix()})
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("stray non-entry file was touched by eviction: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "subdir.json")); err != nil {
		t.Fatalf("subdirectory was touched by eviction: %v", err)
	}
	if n := countLLMSCacheFiles(t, dir); n != 3 {
		t.Fatalf("cache dir has %d entries, want 3 (entry, stray file, subdir)", n)
	}
}

func TestLLMSCacheEvictToleratesUnremovableEntries(t *testing.T) {
	// An entry that cannot be removed (read-only directory) keeps counting
	// against the budget and eviction moves on without failing.
	requirePOSIXPerms(t)
	dir := filepath.Join(t.TempDir(), "llms-cache")
	ts := NewLLMSToolsetWithCache(nil, dir)
	for i := 0; i <= llmsCacheMaxEntries; i++ { // one over budget
		rawURL := fmt.Sprintf("https://adk.dev/p%d.md", i)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		pu, _ := url.Parse(rawURL)
		// Write directly so the per-write eviction does not run yet.
		data := mustJSON(t, llmsCacheEntry{URL: rawURL, Body: "x", FetchedAt: time.Now().Unix()})
		if err := os.WriteFile(ts.cachePath(pu), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	ts.cacheEvict()
	if n := countLLMSCacheFiles(t, dir); n != llmsCacheMaxEntries+1 {
		t.Fatalf("%d entries after eviction in a read-only dir, want all %d untouched", n, llmsCacheMaxEntries+1)
	}
}

func TestLLMSCacheWriteFailedWriteLeavesNoTempFile(t *testing.T) {
	// If writing the temp file fails, it is removed and no entry appears.
	// The seam hands cacheWrite a read-only handle so the write errors.
	prev := llmsCacheCreateTemp
	t.Cleanup(func() { llmsCacheCreateTemp = prev })
	llmsCacheCreateTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		name := f.Name()
		if err := f.Close(); err != nil {
			return nil, err
		}
		return os.Open(name) // O_RDONLY: Write fails, Close succeeds
	}
	dir := filepath.Join(t.TempDir(), "llms-cache")
	ts := NewLLMSToolsetWithCache(nil, dir)
	u, _ := url.Parse("https://adk.dev/page.md")
	ts.cacheWrite(&llmsCacheEntry{URL: u.String(), Body: "x", FetchedAt: time.Now().Unix()})
	if n := countLLMSCacheFiles(t, dir); n != 0 {
		t.Fatalf("%d files left after a failed temp-file write, want 0 (no temp file, no entry)", n)
	}
	if ts.cacheRead(u) != nil {
		t.Fatal("cacheRead returned an entry after a failed write")
	}
}
