package webserver

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The terminal page loads ghostty-web from /static/vendor/. If the embed
// pattern ever stops covering the subdirectory, the page silently loses its
// terminal — the request 404s and nothing else fails.
func TestEmbeddedStatic_ServesVendoredGhosttyWeb(t *testing.T) {
	srv := httptest.NewServer(http.StripPrefix("/static/", staticAssetsFileServer("")))
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/static/vendor/ghostty-web.js")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the vendored bundle is not embedded", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want a JavaScript type", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	// The bundle inlines the WebAssembly module as a data URL; without it
	// init() falls through to ./ghostty-vt.wasm, which nothing serves.
	if !strings.Contains(string(body), "data:application/wasm;base64,") {
		t.Error("the vendored bundle no longer inlines the wasm — a separate " +
			"ghostty-vt.wasm must then be served from the site root, not /static/")
	}
	// The page imports these four names by name.
	for _, sym := range []string{"init", "Terminal", "FitAddon", "UrlRegexProvider"} {
		if !strings.Contains(string(body), sym) {
			t.Errorf("bundle does not export %q, which index.html imports", sym)
		}
	}
}

// MIT requires the license text to travel with the code.
func TestEmbeddedStatic_ShipsGhosttyWebLicense(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/vendor/ghostty-web.LICENSE")
	if err != nil {
		t.Fatalf("license not embedded: %v", err)
	}
	if !strings.Contains(string(data), "MIT License") {
		t.Error("vendored license does not look like the MIT license")
	}
}

// The terminal page must reference only the vendored bundle. A leftover CDN
// tag would mean the page still depends on the network and, worse, could load
// a second conflicting terminal implementation.
func TestIndexPage_UsesVendoredTerminalOnly(t *testing.T) {
	data, err := embeddedStaticFiles.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(data)

	// Match URLs, not prose: the comments legitimately mention xterm.js when
	// explaining what this replaced.
	for _, cdn := range []string{
		"cdn.jsdelivr.net/npm/xterm", // covers xterm and every xterm-addon-*
		"unpkg.com/xterm",
	} {
		if strings.Contains(page, cdn) {
			t.Errorf("index.html still loads %q from a CDN", cdn)
		}
	}
	// No terminal script may come from off-origin at all.
	for _, tag := range []string{`<script src="http`, `<script src='http`} {
		if strings.Contains(page, tag) {
			t.Errorf("index.html loads a script from off-origin (%q); the "+
				"terminal must come from the vendored bundle", tag)
		}
	}

	if !strings.Contains(page, `'/static/vendor/ghostty-web.js'`) {
		t.Error("index.html does not import the vendored ghostty-web bundle")
	}
	// init() must be awaited before a Terminal is constructed; the WASM module
	// does not exist until it resolves.
	if !strings.Contains(page, "await init()") {
		t.Error("index.html does not await init() before using Terminal")
	}
	if strings.Index(page, "await init()") > strings.Index(page, "new Terminal(") {
		t.Error("init() is awaited after Terminal is constructed")
	}
	if !strings.Contains(page, `<script type="module">`) {
		t.Error("index.html must use a module script to import the ESM bundle")
	}
}
