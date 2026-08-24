package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
)

// maxImageBytes caps the size of an image read_image will accept. Screenshots
// are typically well under this; the cap prevents an oversized or malicious
// file from consuming unbounded vision-context tokens when injected.
const maxImageBytes = 20 * 1024 * 1024 // 20 MB

// imageFetchTimeout bounds the total time spent fetching a remote image. Most
// legitimate image hosts respond in well under this; anything slower is almost
// certainly a stalled connection or a denial-of-service attempt.
const imageFetchTimeout = 30 * time.Second

// imageCacheDir is the sandbox-relative directory where remote images are
// cached. It lives under .pi-go/ alongside config.json, mcp.json, and .env
// so the agent's writable state is co-located (and the whole .pi-go/ is
// already covered by the project's .gitignore). The file name is a content
// hash so identical URLs resolve to the same cached file within a session.
const imageCacheDir = ".pi-go/cache/read_image"

// readImageUserAgent identifies pi-go to remote image hosts. Some CDNs
// (Cloudflare, hotlink-protected buckets) refuse requests without a UA.
const readImageUserAgent = "pi-go/1.0 (+https://github.com/dimetron/pi-go)"

// allowedImageSchemes is the set of URL schemes read_image will fetch. http://
// is deliberately excluded: vision fetches should always go over TLS so that
// the response cannot be tampered with by an on-path attacker. If a caller
// really needs plaintext (e.g. a local proxy), they should set up TLS.
var allowedImageSchemes = map[string]bool{"https": true}

// allowPrivateHosts is a test-only escape hatch for the SSRF guard. Production
// code leaves it nil and every private/link-local/loopback destination is
// rejected. Tests against httptest.NewTLSServer set this so the test loopback
// listener is reachable.
var allowPrivateHosts []string

// fetchClientOverride is a test-only override for the HTTP client used to
// download remote images. Production code leaves it nil and uses a default
// client with the system trust store. Tests that talk to httptest.NewTLSServer
// install srv.Client() so the test's self-signed cert is trusted.
var fetchClientOverride *http.Client

// ReadImageInput defines the parameters for the read_image tool.
type ReadImageInput struct {
	// The path or URL of the image to read.
	//
	// Accepted forms:
	//   - An absolute or sandbox-relative path to a local file.
	//   - An https:// URL pointing to a remote image (fetched to the
	//     session cache, then handled like a local file).
	//
	// http:// URLs are rejected. Remote hosts are resolved to IPs and
	// loopback / private / link-local / multicast addresses are blocked to
	// prevent SSRF.
	FilePath string `json:"file_path"`
}

// ReadImageOutput contains metadata about the read image. The image bytes are
// deliberately NOT returned here: embedding them would bloat the text tool
// result and count them as tokens. A BeforeModelCallback re-reads the file and
// injects the bytes as an InlineData part that a vision model can actually see.
//
// When the input was a URL, Path contains the cached local file path (a
// content-addressed entry under .pi-go-image-cache/) and SourceURL is the
// original URL the caller provided. This makes the response self-describing
// without bloating the text tool result.
type ReadImageOutput struct {
	// The path that was read. For URL inputs, this is the cached local file.
	Path string `json:"path"`
	// The detected MIME type (e.g. image/png).
	MIMEType string `json:"mime_type"`
	// The image width in pixels (0 if undetectable).
	Width int `json:"width"`
	// The image height in pixels (0 if undetectable).
	Height int `json:"height"`
	// The file size in bytes.
	Size int `json:"size"`
	// The original URL when the input was a remote URL, otherwise empty.
	SourceURL string `json:"source_url,omitempty"`
}

func newReadImageTool(sb *Sandbox) (tool.Tool, error) {
	return newTool("read_image", `Read an image and make it visible to the model (vision).

Use this after a screenshot has been saved to disk, or to inspect a remote image by URL.

Required: file_path — either an absolute/sandbox-relative path to a local image, or an https:// URL to a remote image.

URL fetching is gated by a conservative egress policy: only https, only public hosts. http://, loopback, private networks, link-local, and multicast destinations are all rejected.`, func(_ agent.Context, input ReadImageInput) (ReadImageOutput, error) {
		return readImageHandler(sb, input)
	}, map[string]string{"path": "file_path"})
}

func readImageHandler(sb *Sandbox, input ReadImageInput) (ReadImageOutput, error) {
	if input.FilePath == "" {
		return ReadImageOutput{}, fmt.Errorf("file_path is required")
	}
	if sb == nil {
		return ReadImageOutput{}, fmt.Errorf("sandbox is not initialized")
	}

	path := input.FilePath
	sourceURL := ""
	if isHTTPURL(path) {
		resolved, err := fetchImageToCache(sb, path)
		if err != nil {
			return ReadImageOutput{}, fmt.Errorf("fetching image: %w", err)
		}
		path = resolved
		sourceURL = input.FilePath
	}

	info, err := sb.Stat(path)
	if err != nil {
		return ReadImageOutput{}, fmt.Errorf("reading image: %w", err)
	}
	if info.Size() > maxImageBytes {
		return ReadImageOutput{}, fmt.Errorf("image too large: %d bytes exceeds %d byte limit", info.Size(), maxImageBytes)
	}
	data, err := sb.ReadFile(path)
	if err != nil {
		return ReadImageOutput{}, fmt.Errorf("reading image: %w", err)
	}
	width, height := decodeImageSize(data)
	return ReadImageOutput{
		Path:      path,
		MIMEType:  DetectImageMIMEType(data),
		Width:     width,
		Height:    height,
		Size:      len(data),
		SourceURL: sourceURL,
	}, nil
}

// DetectImageMIMEType sniffs the image's MIME type from its bytes. Returns
// "application/octet-stream" if it cannot be determined.
func DetectImageMIMEType(data []byte) string {
	return http.DetectContentType(data)
}

// decodeImageSize returns the image's width and height in pixels. Returns
// 0,0 if the format cannot be decoded by the standard library.
func decodeImageSize(data []byte) (int, int) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// isHTTPURL reports whether s is an http:// or https:// URL. The check is
// deliberately cheap (no URL parsing) and used as a routing hint only — the
// real validation happens in fetchImageToCache.
func isHTTPURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// fetchImageToCache downloads a remote image and stores it in the sandbox's
// image cache, returning the sandbox-relative path of the cached file. The
// file name is derived from the SHA-256 of the URL so identical requests share
// a single cache entry and a single network round-trip within a session.
//
// Egress policy:
//   - Only https:// URLs are accepted.
//   - The URL's host is resolved to its IPs; loopback, private, link-local,
//     and multicast destinations are rejected before any TCP connection.
//   - The body is capped at maxImageBytes via io.LimitReader.
//   - The response Content-Type (if present) must advertise an image MIME.
func fetchImageToCache(sb *Sandbox, rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	if !allowedImageSchemes[strings.ToLower(u.Scheme)] {
		return "", fmt.Errorf("scheme %q not allowed (only https)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("url has no host")
	}

	// Resolve the host and reject any address that is not a public IP. This
	// must happen before we dial, otherwise DNS rebinding or a CNAME can
	// point a "public" hostname at a private IP after the lookup.
	if err := rejectPrivateHost(u.Hostname()); err != nil {
		return "", err
	}

	cachePath, err := cachedImagePath(sb, rawURL, u)
	if err != nil {
		return "", err
	}

	// If the file already exists, skip the network round-trip entirely.
	if _, statErr := sb.Stat(cachePath); statErr == nil {
		return cachePath, nil
	}

	if err := downloadToCache(sb, rawURL, cachePath); err != nil {
		return "", err
	}
	return cachePath, nil
}

// cachedImagePath returns the sandbox-relative path where the image at rawURL
// should be cached. The file extension is derived from the URL's path; the
// base name is the SHA-256 of the URL itself so re-fetches are stable.
func cachedImagePath(sb *Sandbox, rawURL string, u *url.URL) (string, error) {
	sum := sha256.Sum256([]byte(rawURL))
	name := hex.EncodeToString(sum[:]) + extFromURL(u)
	rel := filepath.ToSlash(filepath.Join(imageCacheDir, name))
	// Resolve to confirm the path stays inside the sandbox; this is defense
	// in depth in case imageCacheDir is ever changed to something user-controlled.
	if _, _, err := sb.resolveToRoot(rel); err != nil {
		return "", fmt.Errorf("resolve cache path: %w", err)
	}
	return rel, nil
}

// extFromURL picks a file extension for the cached image based on the URL's
// path component. It prefers explicit image extensions and falls back to .img
// for anything ambiguous, so that downstream sniffing always has bytes to
// work with.
func extFromURL(u *url.URL) string {
	last := u.Path
	if i := strings.LastIndex(last, "/"); i >= 0 {
		last = last[i+1:]
	}
	if dot := strings.LastIndex(last, "."); dot >= 0 && dot < len(last)-1 {
		ext := strings.ToLower(last[dot:])
		switch ext {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".tiff", ".tif":
			return ext
		}
	}
	return ".img"
}

// downloadToCache performs the actual HTTP GET, enforces size and content-type
// limits, and writes the body into the sandbox. Any write that would push the
// file past maxImageBytes is aborted.
func downloadToCache(sb *Sandbox, rawURL, cachePath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), imageFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", readImageUserAgent)
	req.Header.Set("Accept", "image/png, image/jpeg, image/gif, image/webp, image/*;q=0.8, */*;q=0.1")

	client := fetchClientOverride
	if client == nil {
		client = &http.Client{Timeout: imageFetchTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http %s", resp.Status)
	}

	// Defense in depth: reject anything that doesn't claim to be an image.
	// We still run byte-level sniffing on the saved file, but if the server
	// is honest, this avoids downloading a 20 MB HTML error page.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		mt, _, _ := mime.ParseMediaType(ct)
		if !strings.HasPrefix(mt, "image/") {
			return fmt.Errorf("content-type %q is not an image", ct)
		}
	}

	// LimitReader caps the body at maxImageBytes+1 so the excess is detectable
	// as a read error rather than a silently-truncated file.
	limited := io.LimitReader(resp.Body, maxImageBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > maxImageBytes {
		return fmt.Errorf("image too large: exceeds %d byte limit", maxImageBytes)
	}
	if len(data) == 0 {
		return errors.New("empty response body")
	}

	// Final byte-level sniff — saves a non-image (e.g. a 200 OK with an
	// image/png Content-Type but HTML bytes) from being misread downstream.
	if detected := DetectImageMIMEType(data); !strings.HasPrefix(detected, "image/") {
		return fmt.Errorf("downloaded content is not an image (detected %q)", detected)
	}

	if err := sb.WriteFile(cachePath, data, 0o644); err != nil {
		return fmt.Errorf("write cache file: %w", err)
	}
	return nil
}

// rejectPrivateHost resolves hostname and rejects any address that fails the
// public-IP check. Returns nil for an empty hostname (caller should have
// caught that) and for the unspecified address 0.0.0.0 / :: (also unsafe).
func rejectPrivateHost(hostname string) error {
	if hostname == "" {
		return errors.New("url has no host")
	}
	// Literal IP shortcut — net.LookupAddr would round-trip through DNS.
	if ip := net.ParseIP(hostname); ip != nil {
		err := rejectPrivateIP(ip)
		if err == nil || isAllowedPrivateHost(ip) {
			return nil
		}
		return err
	}
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", hostname, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("no addresses for %s", hostname)
	}
	for _, ip := range ips {
		if err := rejectPrivateIP(ip); err != nil {
			if isAllowedPrivateHost(ip) {
				continue
			}
			return fmt.Errorf("%s resolves to private address: %w", hostname, err)
		}
	}
	return nil
}

// isAllowedPrivateHost reports whether ip was explicitly allow-listed for
// tests. It is a no-op in production because allowPrivateHosts is nil.
func isAllowedPrivateHost(ip net.IP) bool {
	if len(allowPrivateHosts) == 0 {
		return false
	}
	for _, allowed := range allowPrivateHosts {
		if allowed == "" {
			continue
		}
		if ip.Equal(net.ParseIP(allowed)) {
			return true
		}
	}
	return false
}

// rejectPrivateIP rejects loopback, private, link-local, multicast, and
// unspecified addresses. This is the core SSRF guard.
func rejectPrivateIP(ip net.IP) error {
	if ip == nil {
		return errors.New("nil ip")
	}
	if ip.IsUnspecified() {
		return errors.New("unspecified address (0.0.0.0 or ::)")
	}
	if ip.IsLoopback() {
		return errors.New("loopback address")
	}
	if ip.IsPrivate() {
		return errors.New("private address")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return errors.New("link-local address")
	}
	if ip.IsMulticast() {
		return errors.New("multicast address")
	}
	if ip.IsInterfaceLocalMulticast() {
		return errors.New("interface-local multicast address")
	}
	return nil
}
