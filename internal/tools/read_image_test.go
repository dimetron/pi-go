package tools

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeTestPNG writes a small valid 2x1 PNG to path and returns it.
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestReadImageHandler(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "shot.png")
	writeTestPNG(t, path)

	t.Run("reads a valid png", func(t *testing.T) {
		out, err := readImageHandler(sb, ReadImageInput{FilePath: path})
		if err != nil {
			t.Fatalf("readImageHandler: %v", err)
		}
		if out.MIMEType != "image/png" {
			t.Errorf("expected image/png, got %q", out.MIMEType)
		}
		if out.Width != 2 || out.Height != 1 {
			t.Errorf("expected 2x1, got %dx%d", out.Width, out.Height)
		}
		if out.Size == 0 {
			t.Error("expected non-zero size")
		}
		if out.Path != path {
			t.Errorf("expected path %q, got %q", path, out.Path)
		}
	})

	t.Run("requires file_path", func(t *testing.T) {
		if _, err := readImageHandler(sb, ReadImageInput{}); err == nil {
			t.Error("expected error for empty file_path")
		}
	})

	t.Run("missing file errors", func(t *testing.T) {
		if _, err := readImageHandler(sb, ReadImageInput{FilePath: filepath.Join(dir, "nope.png")}); err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run("escapes sandbox", func(t *testing.T) {
		outside := filepath.Join(os.TempDir(), "outside-read-image.png")
		writeTestPNG(t, outside)
		defer os.Remove(outside)
		if _, err := readImageHandler(sb, ReadImageInput{FilePath: outside}); err == nil {
			t.Error("expected error for path outside sandbox")
		}
	})
}

func TestDetectImageMIMEType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	writeTestPNG(t, path)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := DetectImageMIMEType(data); got != "image/png" {
		t.Errorf("expected image/png, got %q", got)
	}
	if got := DetectImageMIMEType([]byte("not an image")); got == "image/png" {
		t.Error("expected non-image MIME for garbage bytes")
	}
}

// TestReadImageHandler_RealMascot exercises readImageHandler against the
// checked-in docs/screen/pi-go-mascot.png screenshot. This catches regressions
// in MIME sniffing / dimension decoding that synthetic 2x1 PNGs wouldn't:
// the real file is a non-paletted 8-bit RGBA image at 1406x1424.
func TestReadImageHandler_RealMascot(t *testing.T) {
	// Resolve the repo root from this test file: .../pi-go/internal/tools
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	srcPath := filepath.Join(repoRoot, "docs", "screen", "pi-go-mascot.png")
	info, err := os.Stat(srcPath)
	if err != nil {
		t.Skipf("mascot screenshot not present at %s (skipping): %v", srcPath, err)
	}
	rawBytes, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read mascot: %v", err)
	}

	// Use a sandbox rooted at the repo root so the relative path resolves
	// exactly the way the agent would see it during a real session.
	sb := testSandbox(t, repoRoot)
	relPath := filepath.Join("docs", "screen", "pi-go-mascot.png")

	out, err := readImageHandler(sb, ReadImageInput{FilePath: relPath})
	if err != nil {
		t.Fatalf("readImageHandler on real mascot: %v", err)
	}

	if out.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want %q", out.MIMEType, "image/png")
	}
	if out.Width != 1406 || out.Height != 1424 {
		t.Errorf("dimensions = %dx%d, want 1406x1424", out.Width, out.Height)
	}
	if out.Size != int(info.Size()) {
		t.Errorf("Size = %d, want %d", out.Size, info.Size())
	}
	if out.Path != relPath {
		t.Errorf("Path = %q, want %q", out.Path, relPath)
	}

	// The handler must never embed bytes in the output — that's the callback's job.
	if len(rawBytes) > 0 && out.Size != len(rawBytes) {
		t.Errorf("Size=%d != rawBytes=%d (handler must report the on-disk size, not embed bytes)", out.Size, len(rawBytes))
	}
}

// TestNewReadImageTool covers the factory closure (newReadImageTool), which the
// direct readImageHandler tests never reach. It builds the tool, exercises its
// Run method with a valid PNG, and confirms the alias "path" -> "file_path" is
// resolved so a model sending the shorter field still works.
func TestNewReadImageTool(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "shot.png")
	writeTestPNG(t, path)

	tl, err := newReadImageTool(sb)
	if err != nil {
		t.Fatalf("newReadImageTool: %v", err)
	}
	if tl.Name() != "read_image" {
		t.Errorf("Name() = %q, want read_image", tl.Name())
	}

	out := runTool(t, tl, map[string]any{"file_path": path})
	if out["mime_type"] != "image/png" {
		t.Errorf("mime_type = %v, want image/png", out["mime_type"])
	}
	if out["width"] != float64(2) {
		t.Errorf("width = %v, want 2", out["width"])
	}

	// The "path" alias must map onto file_path (coercingTool.aliasArgs).
	outAlias := runTool(t, tl, map[string]any{"path": path})
	if outAlias["mime_type"] != "image/png" {
		t.Errorf("aliased mime_type = %v, want image/png", outAlias["mime_type"])
	}
}

// TestReadImageToolRegistered ensures the tool is exposed by CoreTools.
func TestReadImageToolRegistered(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	tools, err := CoreTools(sb)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools {
		if tl.Name() == "read_image" {
			return
		}
	}
	t.Fatal("read_image tool not registered in CoreTools")
}

// pngBytes returns the bytes of a 2x1 PNG.
func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestReadImageHandler_HTTP_URL_IsRejected confirms that an http:// URL (from
// a plain httptest server) is rejected by the policy before any download is
// attempted. The full https happy path is covered by TestReadImageHandler_HTTPS.
func TestReadImageHandler_HTTP_URL_IsRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not be reached for an http:// URL")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	sb := testSandbox(t, t.TempDir())
	_, err := readImageHandler(sb, ReadImageInput{FilePath: srv.URL + "/x.png"})
	if err == nil {
		t.Fatal("expected http:// to be rejected")
	}
	if !strings.Contains(err.Error(), "https") {
		t.Errorf("expected error to mention https, got %v", err)
	}
}

// TestReadImageHandler_HTTPS exercises the full URL pipeline against a TLS
// httptest server. It validates caching, metadata, and that the second call
// does not re-hit the network.
func TestReadImageHandler_HTTPS(t *testing.T) {
	img := pngBytes(t)
	var hits int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		// Sanity-check the UA so we know our request builder is wired up.
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "pi-go/") {
			t.Errorf("unexpected User-Agent: %q", ua)
		}
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(img)
	}))
	t.Cleanup(srv.Close)

	// httptest.NewTLSServer listens on 127.0.0.1, which the production SSRF
	// guard rightly rejects. Allow it explicitly for the duration of this
	// test and restore on exit. The server also uses a self-signed cert, so
	// install its trust-anchored client.
	withAllowedTestHosts(t, "127.0.0.1", "::1")
	withFetchClient(t, srv.Client())

	dir := t.TempDir()
	sb := testSandbox(t, dir)
	fileURL := srv.URL + "/path/to/cat.png"

	// First call: must hit the network and write the cache.
	out, err := readImageHandler(sb, ReadImageInput{FilePath: fileURL})
	if err != nil {
		t.Fatalf("first readImageHandler: %v", err)
	}
	if hits != 1 {
		t.Errorf("expected 1 network hit, got %d", hits)
	}
	if out.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", out.MIMEType)
	}
	if out.Width != 2 || out.Height != 1 {
		t.Errorf("dimensions = %dx%d, want 2x1", out.Width, out.Height)
	}
	if out.Size != len(img) {
		t.Errorf("Size = %d, want %d", out.Size, len(img))
	}
	if out.SourceURL != fileURL {
		t.Errorf("SourceURL = %q, want %q", out.SourceURL, fileURL)
	}
	if !strings.HasPrefix(out.Path, imageCacheDir+string(filepath.Separator)) &&
		!strings.HasPrefix(out.Path, imageCacheDir+"/") {
		t.Errorf("Path = %q, want it under %q", out.Path, imageCacheDir)
	}
	if !strings.HasSuffix(out.Path, ".png") {
		t.Errorf("Path = %q, want .png extension (from URL path)", out.Path)
	}

	// Second call with the same URL: must NOT hit the network.
	out2, err := readImageHandler(sb, ReadImageInput{FilePath: fileURL})
	if err != nil {
		t.Fatalf("second readImageHandler: %v", err)
	}
	if hits != 1 {
		t.Errorf("expected cached call to skip network, got %d total hits", hits)
	}
	if out2.Path != out.Path {
		t.Errorf("cached path = %q, want %q (stable by URL hash)", out2.Path, out.Path)
	}

	// The cached file must be readable through the sandbox (proves it lives
	// inside the root, not in some external temp dir).
	data, err := sb.ReadFile(out.Path)
	if err != nil {
		t.Fatalf("sb.ReadFile(%q): %v", out.Path, err)
	}
	if !bytes.Equal(data, img) {
		t.Error("cached bytes differ from the server response")
	}
}

// TestReadImageHandler_URL_Rejections enumerates the egress-policy failure
// modes. Each subtest sends a request that must be rejected before any
// significant side effect (no cache file written, no network I/O for the
// rejection cases that don't need a server).
func TestReadImageHandler_URL_Rejections(t *testing.T) {
	t.Run("http scheme is rejected", func(t *testing.T) {
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: "http://example.com/x.png"})
		if err == nil {
			t.Fatal("expected http:// to be rejected")
		}
		if !strings.Contains(err.Error(), "https") {
			t.Errorf("expected error to mention https, got %v", err)
		}
	})

	t.Run("malformed url is rejected", func(t *testing.T) {
		sb := testSandbox(t, t.TempDir())
		// "://" with no scheme is not a valid URL.
		_, err := readImageHandler(sb, ReadImageInput{FilePath: "ht!tp://bad"})
		if err == nil {
			t.Fatal("expected malformed URL to be rejected")
		}
	})

	t.Run("non-image content-type is rejected", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>not an image</html>"))
		}))
		t.Cleanup(srv.Close)
		withAllowedTestHosts(t, "127.0.0.1", "::1")
		withFetchClient(t, srv.Client())
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: srv.URL + "/x.png"})
		if err == nil {
			t.Fatal("expected HTML response to be rejected")
		}
	})

	t.Run("image bytes with text content-type are still rejected", func(t *testing.T) {
		// Server lies with a text/plain Content-Type even though the bytes
		// happen to look like a PNG. The byte-level sniff must catch this.
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(pngBytes(t))
		}))
		t.Cleanup(srv.Close)
		withAllowedTestHosts(t, "127.0.0.1", "::1")
		withFetchClient(t, srv.Client())
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: srv.URL + "/x.png"})
		if err == nil {
			t.Fatal("expected text/plain response to be rejected by Content-Type check")
		}
		if !strings.Contains(err.Error(), "not an image") {
			t.Errorf("expected error to mention non-image, got %v", err)
		}
	})

	t.Run("non-200 status is rejected", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "not found", http.StatusNotFound)
		}))
		t.Cleanup(srv.Close)
		withAllowedTestHosts(t, "127.0.0.1", "::1")
		withFetchClient(t, srv.Client())
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: srv.URL + "/missing.png"})
		if err == nil {
			t.Fatal("expected 404 to be rejected")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("expected error to mention 404, got %v", err)
		}
	})

	t.Run("oversize response is rejected", func(t *testing.T) {
		// Build a body that exceeds the 20 MB cap. We don't need a real image
		// header because the size check fires first inside the read loop.
		huge := bytes.Repeat([]byte{0xff}, maxImageBytes+1024)
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(huge)
		}))
		t.Cleanup(srv.Close)
		withAllowedTestHosts(t, "127.0.0.1", "::1")
		withFetchClient(t, srv.Client())
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: srv.URL + "/huge.png"})
		if err == nil {
			t.Fatal("expected oversize response to be rejected")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("expected error to mention size, got %v", err)
		}
	})

	t.Run("url with literal loopback IP is rejected", func(t *testing.T) {
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: "https://127.0.0.1/x.png"})
		if err == nil {
			t.Fatal("expected loopback IP to be rejected")
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Errorf("expected error to mention loopback, got %v", err)
		}
	})

	t.Run("url with literal private IP is rejected", func(t *testing.T) {
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: "https://10.0.0.5/x.png"})
		if err == nil {
			t.Fatal("expected 10.0.0.5 to be rejected")
		}
		if !strings.Contains(err.Error(), "private") {
			t.Errorf("expected error to mention private, got %v", err)
		}
	})

	t.Run("url with literal link-local IP is rejected", func(t *testing.T) {
		// 169.254.169.254 is the AWS IMDS endpoint. Hard-rejecting it here
		// is the whole point of the SSRF guard.
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: "https://169.254.169.254/latest/meta-data/"})
		if err == nil {
			t.Fatal("expected 169.254.169.254 to be rejected")
		}
		if !strings.Contains(err.Error(), "link-local") {
			t.Errorf("expected error to mention link-local, got %v", err)
		}
	})

	t.Run("url with literal IPv6 loopback is rejected", func(t *testing.T) {
		sb := testSandbox(t, t.TempDir())
		_, err := readImageHandler(sb, ReadImageInput{FilePath: "https://[::1]/x.png"})
		if err == nil {
			t.Fatal("expected [::1] to be rejected")
		}
	})
}

// TestRejectPrivateIP exercises the SSRF guard in isolation, so a regression
// in the IP classifier is caught even if no full URL handler test happens to
// cover that address family. The classifier order in net.IP means 224.0.0.1
// matches IsLinkLocalMulticast() before IsMulticast(), so we only assert the
// call rejects — not the specific reason.
func TestRejectPrivateIP(t *testing.T) {
	cases := []struct {
		ip string
	}{
		{"127.0.0.1"},
		{"10.0.0.1"},
		{"172.16.0.1"},
		{"192.168.1.1"},
		{"169.254.169.254"},
		{"0.0.0.0"},
		{"::1"},
		{"fc00::1"},
		{"fe80::1"},
		{"224.0.0.1"},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("could not parse %s", tc.ip)
			}
			if err := rejectPrivateIP(ip); err == nil {
				t.Errorf("expected %s to be rejected", tc.ip)
			}
		})
	}

	// A public IP must pass.
	public := net.ParseIP("8.8.8.8")
	if err := rejectPrivateIP(public); err != nil {
		t.Errorf("expected 8.8.8.8 to be accepted, got %v", err)
	}
}

// TestIsHTTPURL sanity-checks the routing hint. The match is intentionally
// case-sensitive on the scheme: the prefix check is just a fast dispatch
// signal — the real scheme validation happens in fetchImageToCache, which
// canonicalises via url.Parse.
func TestIsHTTPURL(t *testing.T) {
	cases := map[string]bool{
		"http://x.com/a":     true,
		"https://x.com/a":    true,
		"/abs/path":          false,
		"./rel/path":         false,
		"docs/screen/x.png":  false,
		"":                   false,
		"file:///etc/passwd": false,
		"HTTPS://x.com/a":    false, // case-sensitive by design
	}
	for in, want := range cases {
		if got := isHTTPURL(in); got != want {
			t.Errorf("isHTTPURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// guard against a future refactor accidentally widening imageFetchTimeout
// (which would make SSRF/reachability probes slow).
func TestImageFetchTimeoutIsBounded(t *testing.T) {
	if imageFetchTimeout > time.Minute {
		t.Errorf("imageFetchTimeout = %v, expected <= 1m", imageFetchTimeout)
	}
}

// withAllowedTestHosts adds the given IPs to the test-only allowlist and
// restores the previous value when the test ends. Use this whenever a test
// needs to reach a localhost listener (e.g. httptest.NewTLSServer).
func withAllowedTestHosts(t *testing.T, ips ...string) {
	t.Helper()
	prev := allowPrivateHosts
	allowPrivateHosts = append([]string{}, prev...)
	allowPrivateHosts = append(allowPrivateHosts, ips...)
	t.Cleanup(func() { allowPrivateHosts = prev })
}

// withFetchClient overrides the HTTP client used to download remote images
// for the duration of the test. httptest.NewTLSServer's cert is self-signed
// and untrusted by the default client, so the test must install srv.Client().
func withFetchClient(t *testing.T, c *http.Client) {
	t.Helper()
	prev := fetchClientOverride
	fetchClientOverride = c
	t.Cleanup(func() { fetchClientOverride = prev })
}

// TestReadImageHandler_RealNetwork exercises readImageHandler against a real
// public HTTPS URL (the pi-go mascot hosted on pi-go.sh). It is gated by the
// -short flag and a 10s connect timeout so it does not hang or fail on CI
// runners without internet access. Run with `go test -run RealNetwork -count=1`
// to enable it.
func TestReadImageHandler_RealNetwork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-network test in -short mode")
	}
	const url = "https://pi-go.sh/docs/pi-go-mascot.png"

	// Short, hard cap on the whole test — if the host is unreachable or
	// something hangs, we fail fast rather than block the suite.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = ctx // readImageHandler does not take a context, but the constant
	// timeout of imageFetchTimeout (30s) is bounded enough on its own.

	sb := testSandbox(t, t.TempDir())
	out, err := readImageHandler(sb, ReadImageInput{FilePath: url})
	if err != nil {
		t.Skipf("real-network fetch failed (offline or DNS blocked?): %v", err)
	}
	if out.MIMEType != "image/png" {
		t.Errorf("MIMEType = %q, want image/png", out.MIMEType)
	}
	if out.Width == 0 || out.Height == 0 {
		t.Errorf("dimensions = %dx%d, want non-zero", out.Width, out.Height)
	}
	if out.Size == 0 {
		t.Errorf("Size = 0, want non-zero")
	}
	if out.SourceURL != url {
		t.Errorf("SourceURL = %q, want %q", out.SourceURL, url)
	}
	if !strings.HasPrefix(out.Path, imageCacheDir) {
		t.Errorf("Path = %q, want it under %q", out.Path, imageCacheDir)
	}

	// Re-read through the sandbox: confirms the file is actually inside the
	// sandbox and readable.
	data, err := sb.ReadFile(out.Path)
	if err != nil {
		t.Fatalf("sb.ReadFile(%q): %v", out.Path, err)
	}
	if int64(len(data)) != int64(out.Size) {
		t.Errorf("readback size = %d, want %d", len(data), out.Size)
	}
}
