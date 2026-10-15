package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html/charset"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

const (
	// webFetchTimeout bounds one fetch, including redirects and the body read.
	// It matches the web_search and fetch_docs timeouts: a page that has not
	// answered by then is stalled, not slow.
	webFetchTimeout = 30 * time.Second

	// webFetchMaxBytes is the most of a response body that is read and returned.
	// 100 KiB is enough for a documentation page and stays under maxOutputBytes,
	// so the generic output truncator does not cut the same result again.
	// A longer body is cut and reported with truncated set.
	webFetchMaxBytes = 100 * 1024

	// webFetchMaxRedirects bounds how many redirects one fetch will follow.
	// Every hop is revalidated (scheme, userinfo, private host) before it is dialed.
	webFetchMaxRedirects = 5

	// webFetchUserAgent identifies pi-go to remote hosts. Some CDNs refuse
	// requests that arrive without one.
	webFetchUserAgent = "pi-go/1.0 (+https://github.com/dimetron/pi-go)"
)

// WebFetchInput is the argument for the web_fetch tool.
type WebFetchInput struct {
	// URL is the http or https page to fetch.
	URL string `json:"url"`
}

// WebFetchOutput is the result of a web_fetch call.
//
// HTML responses are converted to markdown before they land in Content.
// Plain text and JSON are returned unchanged. ContentType names that result,
// not the raw response header: a fetched HTML page is reported as text/markdown.
type WebFetchOutput struct {
	URL         string `json:"url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Content     string `json:"content,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Status      int    `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
}

// webFetchHTTPClient overrides the client used to fetch pages. Production
// leaves it nil. Tests that need a failing transport install their own.
var webFetchHTTPClient *http.Client

var (
	webFetchClientOnce sync.Once
	webFetchClient     *http.Client
)

// newWebFetchTool builds the web_fetch tool.
func newWebFetchTool() (tool.Tool, error) {
	kib := webFetchMaxBytes / 1024
	desc := fmt.Sprintf(`Fetch a public http or https URL and return it as readable text.

Use this to read a web page, a documentation page, or a JSON API response.
HTML is converted to markdown. Plain text and JSON are returned unchanged.
The response body is limited to %d KiB and the request times out after %s.
When the body is longer than that, it is cut and truncated is set.
Private, loopback, and link-local hosts are refused.`, kib, webFetchTimeout)
	return newTool("web_fetch", desc, func(ctx agent.Context, input WebFetchInput) (WebFetchOutput, error) {
		return runWebFetch(ctx, input)
	})
}

// runWebFetch validates the URL and fetches it.
//
// Operational failures are returned in WebFetchOutput.Error with a nil Go
// error, the same way web_search does, so the model sees the reason instead
// of a failed tool call.
func runWebFetch(ctx context.Context, input WebFetchInput) (WebFetchOutput, error) {
	u, err := parseWebFetchURL(input.URL)
	if err != nil {
		return WebFetchOutput{Error: err.Error()}, nil
	}
	// Reject private hosts before dialing so the model gets a clear reason.
	// The dialer checks the address it actually connects to as well: a name
	// that resolves to a public address here and a private one at connect
	// time (DNS rebinding) is still refused.
	if err := rejectPrivateHost(u.Hostname()); err != nil {
		return WebFetchOutput{Error: fmt.Sprintf("url rejected: %v", err)}, nil
	}
	return doWebFetch(ctx, u)
}

// parseWebFetchURL validates and normalizes a web_fetch URL.
// The fragment is dropped: it is never sent on the wire.
func parseWebFetchURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}
	if err := validateWebFetchURL(u); err != nil {
		return nil, err
	}
	u.Fragment = ""
	u.RawFragment = ""
	return u, nil
}

// validateWebFetchURL reports whether u is a fetchable http(s) URL.
func validateWebFetchURL(u *url.URL) error {
	if u == nil {
		return errors.New("url is required")
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return errors.New("only http and https URLs are allowed")
	}
	if u.Hostname() == "" {
		return errors.New("url has no host")
	}
	if u.User != nil {
		return errors.New("urls with userinfo are not allowed")
	}
	return nil
}

func doWebFetch(ctx context.Context, u *url.URL) (WebFetchOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	// The dialer pins the IP of this host. A later dial to a different address
	// is the proxy (or a redirect hop already checked in CheckRedirect).
	ctx = context.WithValue(ctx, webFetchOriginKey{}, u.Hostname())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return WebFetchOutput{Error: fmt.Sprintf("build request: %v", err)}, nil
	}
	req.Header.Set("User-Agent", webFetchUserAgent)
	req.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain, text/markdown, application/json;q=0.9, */*;q=0.1")

	resp, err := activeWebFetchClient().Do(req)
	if err != nil {
		return WebFetchOutput{Error: fmt.Sprintf("fetch failed: %v", err)}, nil
	}
	defer func() { _ = resp.Body.Close() }()

	final := u
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL
	}
	ct := resp.Header.Get("Content-Type")
	mt := mediaTypeOf(ct)
	out := WebFetchOutput{URL: final.String(), Status: resp.StatusCode, ContentType: mt}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		out.Error = fmt.Sprintf("http %s", resp.Status)
		return out, nil
	}
	if rejectedMedia(mt) {
		out.Error = fmt.Sprintf("unsupported content type %q", mt)
		return out, nil
	}

	body, truncated, err := readLimited(resp.Body, webFetchMaxBytes)
	if err != nil {
		out.Error = fmt.Sprintf("read body: %v", err)
		return out, nil
	}

	rendered, errMsg := renderWebBody(ct, body, final)
	if errMsg != "" {
		out.Error = errMsg
		return out, nil
	}
	out.ContentType = rendered.contentType
	out.Content = rendered.content
	out.Truncated = truncated || rendered.truncated
	return out, nil
}

// readLimited reads at most n bytes. The extra byte detects a body that
// continues past the cap without buffering the rest.
func readLimited(r io.Reader, n int) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(n)+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > n {
		return data[:n], true, nil
	}
	return data, false, nil
}

func activeWebFetchClient() *http.Client {
	if webFetchHTTPClient != nil {
		return webFetchHTTPClient
	}
	webFetchClientOnce.Do(func() {
		webFetchClient = newWebFetchClient()
	})
	return webFetchClient
}

func newWebFetchClient() *http.Client {
	var tr *http.Transport
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		tr = base.Clone()
	} else {
		tr = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	// DialContext is the SSRF check that sees the IP actually connected.
	// Clone keeps ProxyFromEnvironment, so a configured proxy still applies;
	// the URL is also checked before the request and on every redirect.
	tr.DialContext = webFetchDialContext
	tr.TLSHandshakeTimeout = 10 * time.Second
	tr.ResponseHeaderTimeout = 15 * time.Second
	return &http.Client{
		Timeout:       webFetchTimeout,
		Transport:     tr,
		CheckRedirect: checkWebFetchRedirect,
	}
}

// checkWebFetchRedirect is the client's redirect policy. The hop count, the
// scheme, and the destination address are all checked again: a public page
// must not be able to bounce the fetch onto a loopback or link-local host.
func checkWebFetchRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= webFetchMaxRedirects {
		return fmt.Errorf("stopped after %d redirects", webFetchMaxRedirects)
	}
	if req == nil || req.URL == nil {
		return errors.New("redirect has no url")
	}
	if err := validateWebFetchURL(req.URL); err != nil {
		return err
	}
	if err := rejectPrivateHost(req.URL.Hostname()); err != nil {
		return fmt.Errorf("redirect rejected: %w", err)
	}
	return nil
}

// webFetchOriginKey carries the page host the dialer must pin. DialContext is
// also used to reach an HTTP proxy, and that address is not the page.
type webFetchOriginKey struct{}

// webFetchDialContext resolves addr and dials only an address that passes the
// private-host check. Connecting to the checked IP, rather than the name,
// closes the gap where a second DNS answer differs from the one just allowed.
//
// When addr is not the page host, it is the proxy (or a redirect target whose
// name CheckRedirect already accepted). That connection is dialed directly so
// a proxy on a loopback or private address still works.
func webFetchDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	if origin, ok := ctx.Value(webFetchOriginKey{}).(string); ok && origin != "" && !strings.EqualFold(origin, host) {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		return dialer.DialContext(ctx, network, addr)
	}
	ips, err := resolveWebFetchHost(ctx, host)
	if err != nil {
		return nil, err
	}

	var dialErr error
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	for _, ip := range ips {
		if err := rejectPrivateIP(ip); err != nil && !isAllowedPrivateHost(ip) {
			dialErr = fmt.Errorf("%s: %w", ip, err)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err != nil {
			dialErr = err
			continue
		}
		return conn, nil
	}
	if dialErr == nil {
		return nil, fmt.Errorf("no usable address for %s", host)
	}
	return nil, dialErr
}

func resolveWebFetchHost(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	resolved, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("no addresses for %s", host)
	}
	ips := make([]net.IP, len(resolved))
	for i, r := range resolved {
		ips[i] = r.IP
	}
	return ips, nil
}

// renderedBody is the text web_fetch returns and the content type that describes it.
type renderedBody struct {
	content     string
	contentType string
	truncated   bool
}

// renderWebBody turns a fetched body into the text handed to the model.
// The bool on the caller tracks whether the read itself was cut; this reports
// a further cut when charset conversion or markdown rendering exceeds the cap.
func renderWebBody(contentType string, body []byte, page *url.URL) (renderedBody, string) {
	mt := mediaTypeOf(contentType)
	if mt == "" || mt == "application/octet-stream" {
		mt = sniffWebBody(body)
		if mt == "" {
			if strings.TrimSpace(contentType) == "" {
				return renderedBody{}, "unsupported content type"
			}
			return renderedBody{}, fmt.Sprintf("unsupported content type %q", strings.TrimSpace(contentType))
		}
	}
	if rejectedMedia(mt) {
		return renderedBody{}, fmt.Sprintf("unsupported content type %q", mt)
	}

	header := contentType
	if mediaTypeOf(header) == "" {
		header = mt
	}
	text := decodeBody(header, body)

	outType := mt
	if isHTMLMedia(mt) {
		text = htmlToMarkdown(text, page)
		outType = "text/markdown"
	}
	truncated := false
	if len(text) > webFetchMaxBytes {
		text = utf8SafeCut(text, webFetchMaxBytes)
		truncated = true
	}
	return renderedBody{content: text, contentType: outType, truncated: truncated}, ""
}

func mediaTypeOf(contentType string) string {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ""
	}
	return strings.ToLower(mt)
}

func isHTMLMedia(mt string) bool {
	return mt == "text/html" || mt == "application/xhtml+xml"
}

func isPassThrough(mt string) bool {
	switch mt {
	case "text/plain", "text/markdown", "text/x-markdown",
		"application/json", "text/json", "application/ld+json",
		"application/xml", "text/xml",
		"application/javascript", "text/javascript":
		return true
	default:
		return strings.HasPrefix(mt, "text/") || strings.HasSuffix(mt, "+json") || strings.HasSuffix(mt, "+xml")
	}
}

func rejectedMedia(mt string) bool {
	if mt == "" || mt == "application/octet-stream" {
		return false
	}
	return !isHTMLMedia(mt) && !isPassThrough(mt)
}

// sniffWebBody classifies a body whose Content-Type is missing or generic.
// An empty result means the bytes are not something the tool should return.
func sniffWebBody(body []byte) string {
	if len(body) == 0 {
		return "text/plain"
	}
	sample := body
	if len(sample) > 512 {
		sample = sample[:512]
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return ""
	}
	trim := strings.TrimSpace(string(sample))
	if trim == "" {
		return "text/plain"
	}
	lower := strings.ToLower(trim)
	if looksLikeHTML(lower) {
		return "text/html"
	}
	if trim[0] == '{' || trim[0] == '[' {
		return "application/json"
	}
	if mostlyText(sample) {
		return "text/plain"
	}
	return ""
}

func looksLikeHTML(lower string) bool {
	for _, prefix := range []string{"<!doctype", "<html", "<head", "<body", "<p", "<div", "<span", "<a ", "<a>", "<h1", "<table", "<ul", "<ol"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// mostlyText reports whether sample is text the model can read.
// High bytes count only when they form valid UTF-8, so a binary payload that
// happens to avoid NUL is still refused.
func mostlyText(sample []byte) bool {
	if len(sample) == 0 {
		return true
	}
	end := len(sample)
	if !utf8.Valid(sample) {
		// The sniff window can split a rune. A short incomplete tail is not binary.
		for end > 0 && len(sample)-end < utf8.UTFMax {
			end--
			if utf8.Valid(sample[:end]) {
				break
			}
		}
		if end == 0 || !utf8.Valid(sample[:end]) {
			return false
		}
	}
	controls, runes := 0, 0
	for _, r := range string(sample[:end]) {
		runes++
		if r == '\t' || r == '\n' || r == '\r' || r == '\uFEFF' {
			continue
		}
		if unicode.IsControl(r) {
			controls++
		}
	}
	return runes > 0 && controls*10 <= runes
}

// decodeBody converts body to UTF-8 using the charset in contentType.
// HTML with no charset in the header is presniffed for a meta charset.
func decodeBody(contentType string, body []byte) string {
	if len(body) == 0 {
		return ""
	}
	reader, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return strings.ToValidUTF8(string(body), "\uFFFD")
	}
	decoded, err := io.ReadAll(reader)
	if err != nil || len(decoded) == 0 {
		return strings.ToValidUTF8(string(body), "\uFFFD")
	}
	if !utf8.Valid(decoded) {
		return strings.ToValidUTF8(string(decoded), "\uFFFD")
	}
	return string(decoded)
}
