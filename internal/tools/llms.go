package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/config"
)

// llmsFetchTimeout bounds the total time spent fetching a single llms.txt
// page. Documentation pages are small; anything slower is almost certainly a
// stalled connection or a denial-of-service attempt.
const llmsFetchTimeout = 30 * time.Second

// llmsMaxRedirects bounds how many redirects fetch_docs will follow. Every
// hop is revalidated by checkRedirectURL before being followed.
const llmsMaxRedirects = 5

// llmsMaxBytes caps the size of a fetched page. llms.txt indexes and their
// linked markdown pages are typically well under this; the cap prevents an
// oversized or malicious response from consuming unbounded context tokens.
const llmsMaxBytes = 2 * 1024 * 1024 // 2 MB

// llmsUserAgent identifies pi-go to remote documentation hosts. Some CDNs
// refuse requests without a UA.
const llmsUserAgent = "pi-go/1.0 (+https://github.com/dimetron/pi-go)"

// llmsClientOverride is a test-only override for the HTTP client used to
// fetch documentation. Production code leaves it nil and uses the toolset's
// default client. Tests that talk to httptest.NewTLSServer install
// srv.Client() so the test's self-signed cert is trusted.
var llmsClientOverride *http.Client

// llmsCacheCreateTemp creates the temporary file a cache entry is written to
// before being renamed into place. It is a variable so tests can hand back a
// file the write will fail on and check the failure path cleans up.
var llmsCacheCreateTemp = os.CreateTemp

// llmsCacheTTL bounds how old a cached page may be before it is considered
// stale. Revalidation with a 304 keeps a cache hit fast (small round trip, no
// body download); this TTL is the floor below which no revalidation is
// attempted at all — a page fetched moments ago is served straight from cache.
const llmsCacheTTL = 1 * time.Hour

// llmsCacheMaxAge bounds the age of an entry usable as a network-down fallback.
// A cached page older than this is not returned when the revalidation request
// fails, so a permanently dead or moved page is not served forever.
const llmsCacheMaxAge = 24 * time.Hour

// llmsCacheMaxEntries and llmsCacheMaxBytes bound the on-disk cache. fetch_docs
// is model-controlled and every distinct URL on an allowed host creates an
// entry of up to llmsMaxBytes, so without a budget the shared directory could
// grow without limit. When either bound is exceeded after a write, the
// least-recently-written entries are evicted until both hold again.
const (
	llmsCacheMaxEntries = 512
	llmsCacheMaxBytes   = 64 * 1024 * 1024 // 64 MB
)

// llmsCacheEntry is the on-disk form of one cached page.
type llmsCacheEntry struct {
	URL          string `json:"url"`
	Body         string `json:"body"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	FetchedAt    int64  `json:"fetched_at"`
}

// age reports how long ago the entry was fetched or last revalidated. It is
// computed from the timestamp stored inside the entry, not the file's mtime,
// so it does not depend on filesystem timestamp granularity.
func (e *llmsCacheEntry) age() time.Duration {
	return time.Since(time.Unix(e.FetchedAt, 0))
}

// LLMSInput defines the parameters for the fetch_docs tool.
type LLMSInput struct {
	// URL is the documentation URL to fetch. It must be an https:// URL on a
	// host that hosts one of the configured llms.txt sources.
	URL string `json:"url,omitempty"`
}

// LLMSOutput is the result of a fetch_docs call.
type LLMSOutput struct {
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// LLMSToolset implements tool.Toolset for llms.txt documentation sources.
type LLMSToolset struct {
	sources  []config.LLMSSource
	client   *http.Client
	cacheDir string // directory for the on-disk fetch cache; empty disables caching
}

// NewLLMSToolset creates a new llms.txt toolset from configuration.
func NewLLMSToolset(cfg *config.LLMSConfig) *LLMSToolset {
	return NewLLMSToolsetWithCache(cfg, "")
}

// NewLLMSToolsetWithCache creates a new llms.txt toolset that caches fetched
// pages under dir. When dir is empty, caching is disabled.
func NewLLMSToolsetWithCache(cfg *config.LLMSConfig, dir string) *LLMSToolset {
	ts := &LLMSToolset{cacheDir: dir}
	if cfg != nil {
		ts.sources = cfg.Sources
	}
	// Redirects are re-run through the same https/private-host/host-allowlist
	// checks as the original request; otherwise an open redirect on an allowed
	// host could reach arbitrary destinations, defeating both guards.
	ts.client = &http.Client{
		Timeout:       llmsFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return ts.checkRedirectURL(req.URL, via) },
	}
	return ts
}

// NewLLMSCachedToolset creates a new llms.txt toolset whose fetch cache lives
// under the shared pi-go documentation cache directory (~/.pi-go/llms-cache).
// Every mode that wires fetch_docs — the piagent, the one-shot CLI, and the
// interactive TUI (which the voice agent drives) — shares this one directory,
// so a page fetched by one is a cache hit for the others. Caching degrades to
// off when no usable home directory exists.
func NewLLMSCachedToolset(cfg *config.LLMSConfig) *LLMSToolset {
	return NewLLMSToolsetWithCache(cfg, LLMSDefaultCacheDir())
}

// LLMSDefaultCacheDir returns the shared directory used to cache fetched
// documentation pages, or "" when no usable home directory can be found. An
// empty result disables the fetch cache rather than failing construction: doc
// fetching is a best effort feature.
func LLMSDefaultCacheDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi-go", "llms-cache")
}

// checkRedirectURL validates one redirect hop. It is the CheckRedirect hook
// for the toolset's HTTP client and is exported-for-test via unit tests.
func (t *LLMSToolset) checkRedirectURL(u *url.URL, via []*http.Request) error {
	if len(via) >= llmsMaxRedirects {
		return fmt.Errorf("stopped after %d redirects", llmsMaxRedirects)
	}
	if u.Scheme != "https" {
		return errors.New("only https:// URLs are allowed")
	}
	if err := rejectPrivateHost(u.Hostname()); err != nil {
		return fmt.Errorf("url rejected: %w", err)
	}
	// Redirects are held to the same rule as the original request, so a source
	// restricted to one URL cannot be widened by a hop the server chooses.
	if !t.urlAllowed(u) {
		return fmt.Errorf("host %q is not an allowed documentation source", u.Hostname())
	}
	return nil
}

// Name returns the name of the toolset.
func (t *LLMSToolset) Name() string {
	return "llms"
}

// Sources returns the configured llms.txt documentation sources.
func (t *LLMSToolset) Sources() []config.LLMSSource {
	return t.sources
}

// Tools returns the llms.txt tools available.
func (t *LLMSToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return LLMSTools(t), nil
}

// LLMSTools returns the llms.txt tools built from the toolset.
func LLMSTools(ts *LLMSToolset) []tool.Tool {
	t, err := NewLLMSToolFromSet(ts)
	if err != nil {
		return nil
	}
	return []tool.Tool{t}
}

// NewLLMSToolFromSet creates the fetch_docs ADK tool from a toolset.
func NewLLMSToolFromSet(ts *LLMSToolset) (tool.Tool, error) {
	desc := buildLLMSDescription(ts.sources)

	return newTool("fetch_docs", desc,
		func(ctx agent.Context, input LLMSInput) (LLMSOutput, error) {
			return ts.FetchDocs(ctx, input.URL)
		},
	)
}

// parseLLMSURL validates and normalizes a fetch_docs URL: https only, must
// have a host, and the fragment (#section) is dropped — it is never sent on
// the wire, so page.md#a and page.md#b are the same resource and should share
// one cache entry instead of each downloading a copy.
func parseLLMSURL(rawURL string) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("url is required")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if u.Scheme != "https" {
		return nil, errors.New("only https:// URLs are allowed")
	}
	if u.Host == "" {
		return nil, errors.New("url has no host")
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

// conditionalHeaders sets revalidation headers on req for entry: the server
// answers 304 and we reuse the cached body, saving the full download.
func conditionalHeaders(req *http.Request, entry *llmsCacheEntry) {
	if entry == nil {
		return
	}
	if entry.ETag != "" {
		req.Header.Set("If-None-Match", entry.ETag)
	}
	if entry.LastModified != "" {
		req.Header.Set("If-Modified-Since", entry.LastModified)
	}
}

// fetchLLMSURL builds, sends, and converts the response of a documentation fetch
// cached entry for revalidation (304) and stale-while-error fallback.
//
// A cached copy younger than llmsCacheMaxAge is served on network failure or
// an upstream 5xx: both are transient. A 4xx (401/403/404/410, …) is the
// origin's verdict on the page itself — removed, moved, or access revoked —
// so the cached body must not be passed off as current; only the status is
// reported.
func (t *LLMSToolset) fetchLLMSURL(ctx context.Context, u *url.URL, entry *llmsCacheEntry) (LLMSOutput, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return LLMSOutput{Error: fmt.Sprintf("build request: %v", err)}, nil
	}
	req.Header.Set("User-Agent", llmsUserAgent)
	conditionalHeaders(req, entry)

	client := t.client
	if llmsClientOverride != nil {
		// The test override supplies only transport/TLS settings; the
		// toolset's redirect policy must survive the swap, or the SSRF and
		// allowlist guards would not apply to redirect hops in tests (and
		// nowhere else — production always uses t.client).
		c := *llmsClientOverride
		c.CheckRedirect = t.client.CheckRedirect
		client = &c
	}
	resp, err := client.Do(req)
	if err != nil {
		if entry != nil && entry.age() < llmsCacheMaxAge {
			return LLMSOutput{Content: entry.Body}, nil
		}
		return LLMSOutput{Error: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		// Server confirms the cached copy is still current.
		if entry != nil {
			entry.FetchedAt = time.Now().Unix()
			t.cacheWrite(entry)
			return LLMSOutput{Content: entry.Body}, nil
		}
		return LLMSOutput{Error: "unexpected 304 without a cached entry"}, nil

	case http.StatusOK:
		// Read one byte past the cap so an oversized body is detected rather
		// than silently truncated into a successful-looking result.
		body, err := io.ReadAll(io.LimitReader(resp.Body, llmsMaxBytes+1))
		if err != nil {
			return LLMSOutput{Error: fmt.Sprintf("read body: %v", err)}, nil
		}
		if len(body) > llmsMaxBytes {
			return LLMSOutput{Error: fmt.Sprintf("response exceeds %d byte limit", llmsMaxBytes)}, nil
		}
		// Cache the fresh body so the next read revalidates instead of
		// downloading it again. Caching is best-effort: a write failure just
		// means the next call re-fetches.
		t.cacheWrite(&llmsCacheEntry{
			URL:          u.String(),
			Body:         string(body),
			ETag:         resp.Header.Get("ETag"),
			LastModified: resp.Header.Get("Last-Modified"),
			FetchedAt:    time.Now().Unix(),
		})
		return LLMSOutput{Content: string(body)}, nil

	default:
		if resp.StatusCode >= 500 && entry != nil && entry.age() < llmsCacheMaxAge {
			return LLMSOutput{Content: entry.Body}, nil
		}
		return LLMSOutput{Error: fmt.Sprintf("unexpected status %d", resp.StatusCode)}, nil
	}
}

// FetchDocs fetches a documentation URL, restricted to hosts that host one of
// the configured llms.txt sources. The response is returned as-is (llms.txt
// and its linked pages are markdown, so no HTML conversion is needed).
//
// It is exported so the voice agent can drive the same allow-list and SSRF
// guards for its own fetch_docs tool rather than reimplementing them; it is
// otherwise the body behind the ADK fetch_docs tool.
func (t *LLMSToolset) FetchDocs(ctx context.Context, rawURL string) (LLMSOutput, error) {
	u, err := parseLLMSURL(rawURL)
	if err != nil {
		return LLMSOutput{Error: err.Error()}, nil
	}

	// SSRF guard: reject loopback, private, link-local, multicast, and
	// unspecified destinations before dialing.
	if err := rejectPrivateHost(u.Hostname()); err != nil {
		return LLMSOutput{Error: fmt.Sprintf("url rejected: %v", err)}, nil
	}

	// Restrict to configured llms.txt sources: a whole host for one the user
	// configured, the exact URL for one pi-go inferred.
	if !t.urlAllowed(u) {
		return LLMSOutput{Error: fmt.Sprintf("host %q is not an allowed documentation source", u.Hostname())}, nil
	}

	entry := t.cacheRead(u)
	if entry != nil && entry.age() < llmsCacheTTL {
		return LLMSOutput{Content: entry.Body}, nil
	}

	return t.fetchLLMSURL(ctx, u, entry)
}

// cachePath maps a documentation URL to its on-disk cache file. The key is a
// hash of the full URL (scheme, host, path and query) so distinct pages and
// query strings are disambiguated while the file name stays filesystem-safe
// on every platform: lowercase hex plus a fixed extension.
func (t *LLMSToolset) cachePath(u *url.URL) string {
	sum := sha256.Sum256([]byte(u.String()))
	return filepath.Join(t.cacheDir, hex.EncodeToString(sum[:])+".json")
}

// cacheRead loads a cached entry for u, or nil when caching is disabled or the
// entry is missing, corrupt, or does not describe u. Cache failures degrade to
// nil so the caller falls through to a network fetch.
//
// The entry's recorded URL must match the requested one and its body must be
// within llmsMaxBytes: the cache directory is shared and writable by the user,
// so a stray or tampered file must not be able to answer for a different URL
// or hand the model an oversized body the network path would have refused.
func (t *LLMSToolset) cacheRead(u *url.URL) *llmsCacheEntry {
	if t.cacheDir == "" {
		return nil
	}
	data, err := os.ReadFile(t.cachePath(u))
	if err != nil {
		return nil
	}
	var e llmsCacheEntry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil
	}
	if e.URL != u.String() || len(e.Body) > llmsMaxBytes {
		return nil
	}
	return &e
}

// cacheWrite persists an entry, creating the cache directory as needed. Cache
// write failures are ignored: they cost a future re-fetch, not correctness.
//
// The entry is written to a temporary file and renamed into place so a
// concurrent reader in another pi-go process (the directory is shared by every
// mode) never observes a partially written file. The directory is created
// 0700 and the files 0600: the content is public documentation, but the
// directory lives under the user's home and should not be world-readable.
func (t *LLMSToolset) cacheWrite(e *llmsCacheEntry) {
	if t.cacheDir == "" {
		return
	}
	u, err := url.Parse(e.URL)
	if err != nil {
		return
	}
	// Marshaling cannot fail here: the entry is plain strings and an int64
	// (invalid UTF-8 in a body is coerced, not rejected).
	data, _ := json.Marshal(e)
	if err := os.MkdirAll(t.cacheDir, 0o700); err != nil {
		return
	}
	dst := t.cachePath(u)
	tmp, err := llmsCacheCreateTemp(t.cacheDir, filepath.Base(dst)+".*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr != nil || cerr != nil {
		_ = os.Remove(tmpName)
		return
	}
	_ = os.Chmod(tmpName, 0o600)
	// os.Rename replaces an existing destination on every platform (on
	// Windows it uses MoveFileEx with REPLACE_EXISTING), but can still fail
	// there if another process has the destination open at that instant. The
	// write is best-effort, so just discard the temp file in that case.
	if err := os.Rename(tmpName, dst); err != nil {
		_ = os.Remove(tmpName)
		return
	}
	t.cacheEvict()
}

// cacheEvict enforces llmsCacheMaxEntries and llmsCacheMaxBytes over the
// cache directory by deleting the least-recently-written entries first. It
// runs after every write, which only happens after a network round trip, so
// the directory scan is cheap relative to the fetch it follows. Removal is
// best-effort: a file another process holds open (Windows) or has already
// removed is simply skipped.
func (t *LLMSToolset) cacheEvict() {
	dirEntries, err := os.ReadDir(t.cacheDir)
	if err != nil {
		return
	}
	type cacheFile struct {
		name  string
		size  int64
		mtime time.Time
	}
	var files []cacheFile
	var total int64
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		files = append(files, cacheFile{name: de.Name(), size: info.Size(), mtime: info.ModTime()})
		total += info.Size()
	}
	if len(files) <= llmsCacheMaxEntries && total <= llmsCacheMaxBytes {
		return
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mtime.Before(files[j].mtime) })
	remaining := len(files)
	for _, f := range files {
		if remaining <= llmsCacheMaxEntries && total <= llmsCacheMaxBytes {
			return
		}
		if err := os.Remove(filepath.Join(t.cacheDir, f.name)); err != nil && !os.IsNotExist(err) {
			continue // still on disk; it keeps counting against the budget
		}
		remaining--
		total -= f.size
	}
}

// hostAllowed reports whether hostname hosts one of the configured llms.txt
// sources. The comparison is on the hostname (port stripped) so a source
// configured as https://adk.dev/llms.txt allows fetching any page on adk.dev.
func (t *LLMSToolset) hostAllowed(hostname string) bool {
	for _, s := range t.sources {
		if s.ExactURLOnly {
			continue // widened access is not what such a source grants
		}
		u, err := url.Parse(s.URL)
		if err != nil {
			continue
		}
		if strings.EqualFold(u.Hostname(), hostname) {
			return true
		}
	}
	return false
}

// urlAllowed reports whether target may be fetched. A configured source grants
// its whole host, which is what makes an llms.txt index useful — the index
// exists to be followed to the pages it links. A source pi-go inferred from a
// URL's file name grants only that one URL: the name is a guess, and a wrong
// guess must not hand the model a whole host it was never configured to reach.
func (t *LLMSToolset) urlAllowed(target *url.URL) bool {
	if t.hostAllowed(target.Hostname()) {
		return true
	}
	for _, s := range t.sources {
		if !s.ExactURLOnly {
			continue
		}
		u, err := url.Parse(s.URL)
		if err != nil {
			continue
		}
		// EscapedPath, not Path: url.Parse decodes %2F to "/" in Path, so
		// /a%2Fb/llms.txt and /a/b/llms.txt compare equal there while being
		// different resources on the wire.
		//
		// The query is part of the identity too: ?resource=other can select a
		// different document entirely, so ignoring it would let any variant
		// of the path through. An inferred source never carries one —
		// config.IsLLMSDocsURL refuses query-bearing URLs — so in practice
		// this requires the request to have none either.
		if strings.EqualFold(u.Hostname(), target.Hostname()) &&
			u.Scheme == target.Scheme && u.Port() == target.Port() &&
			u.EscapedPath() == target.EscapedPath() && u.RawQuery == target.RawQuery {
			return true
		}
	}
	return false
}

// buildLLMSDescription generates a dynamic tool description listing the
// configured documentation sources.
func buildLLMSDescription(sources []config.LLMSSource) string {
	var sb strings.Builder
	sb.WriteString("Fetch documentation from a configured llms.txt source. ")
	sb.WriteString("Parameters: url (the https:// URL to fetch). ")
	sb.WriteString("Only hosts that host a configured llms.txt source are allowed. ")
	if len(sources) == 0 {
		sb.WriteString("No llms.txt sources configured.")
		return sb.String()
	}
	sb.WriteString("Available sources: ")
	names := make([]string, 0, len(sources))
	for _, s := range sources {
		names = append(names, s.Name+" ("+s.URL+")")
	}
	sb.WriteString(strings.Join(names, ", "))
	sb.WriteString(".")
	return sb.String()
}
