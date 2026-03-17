package lsp

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestManager_NewWithDefaults(t *testing.T) {
	mgr := NewManager(nil)
	defer mgr.Shutdown()

	langs := mgr.Languages()
	if len(langs) != 4 {
		t.Errorf("expected 4 default languages, got %d", len(langs))
	}
	for _, name := range []string{"go", "typescript", "python", "rust"} {
		if _, ok := langs[name]; !ok {
			t.Errorf("missing default language %q", name)
		}
	}
}

func TestManager_ConfigOverride(t *testing.T) {
	cfg := &ManagerConfig{
		Languages: map[string]*LanguageConfig{
			"go": {
				Command:        "custom-gopls",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
				LanguageID:     "go",
			},
		},
	}

	mgr := NewManager(cfg)
	defer mgr.Shutdown()

	goCfg := mgr.Languages()["go"]
	if goCfg.Command != "custom-gopls" {
		t.Errorf("expected custom-gopls, got %s", goCfg.Command)
	}

	// Other languages should still be present.
	if _, ok := mgr.Languages()["typescript"]; !ok {
		t.Error("typescript config should still be present")
	}
}

func TestManager_DisabledLanguage(t *testing.T) {
	cfg := &ManagerConfig{
		Disabled: []string{"rust", "python"},
	}

	mgr := NewManager(cfg)
	defer mgr.Shutdown()

	langs := mgr.Languages()
	if _, ok := langs["rust"]; ok {
		t.Error("rust should be disabled")
	}
	if _, ok := langs["python"]; ok {
		t.Error("python should be disabled")
	}
	if _, ok := langs["go"]; !ok {
		t.Error("go should still be present")
	}
}

func TestManager_MissingServer(t *testing.T) {
	cfg := &ManagerConfig{
		Languages: map[string]*LanguageConfig{
			"fake": {
				Command:        "nonexistent-fake-server-binary-xyz",
				FileExtensions: []string{".fake"},
				RootMarkers:    []string{"fake.toml"},
				LanguageID:     "fake",
			},
		},
	}

	mgr := NewManager(cfg)
	defer mgr.Shutdown()

	// The fake server should not be available.
	if mgr.Available("fake") {
		t.Error("fake server should not be available")
	}

	// ServerFor should return (nil, nil) for unavailable server.
	srv, err := mgr.ServerFor("/tmp/test.fake")
	if err != nil {
		t.Errorf("expected nil error for missing server, got: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server for missing server binary")
	}
}

func TestManager_ServerForUnknownExtension(t *testing.T) {
	mgr := NewManager(nil)
	defer mgr.Shutdown()

	srv, err := mgr.ServerFor("/tmp/readme.txt")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server for .txt file")
	}
}

func TestManager_ServerLifecycle(t *testing.T) {
	// Create a mock-based manager to test server lifecycle without real LSP servers.
	// We test that the Server wrapper correctly calls the underlying client.

	handler := func(req Request) (json.RawMessage, *ResponseError) {
		switch req.Method {
		case "initialize":
			return json.RawMessage(`{"capabilities":{}}`), nil
		case "shutdown":
			return json.RawMessage(`null`), nil
		case "textDocument/hover":
			return json.RawMessage(`{"contents":{"kind":"markdown","value":"test hover"}}`), nil
		case "textDocument/definition":
			return json.RawMessage(`[{"uri":"file:///test.go","range":{"start":{"line":10,"character":0},"end":{"line":10,"character":5}}}]`), nil
		case "textDocument/references":
			return json.RawMessage(`[{"uri":"file:///a.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":3}}}]`), nil
		case "textDocument/documentSymbol":
			return json.RawMessage(`[{"name":"main","kind":12,"range":{"start":{"line":0,"character":0},"end":{"line":5,"character":1}},"selectionRange":{"start":{"line":0,"character":5},"end":{"line":0,"character":9}}}]`), nil
		case "textDocument/formatting":
			return json.RawMessage(`[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"newText":"fmt"}]`), nil
		}
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)

	srv := &Server{
		client:   client,
		language: "go",
		rootURI:  "file:///tmp/test",
		opened:   make(map[string]int),
	}

	// Create a temp file for ensureOpen to read.
	tmpFile := t.TempDir() + "/test.go"
	if err := writeTestFile(tmpFile, "package main\n"); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// Test Hover.
	hover, err := srv.Hover(ctx, tmpFile, 0, 0)
	if err != nil {
		t.Fatalf("Hover failed: %v", err)
	}
	if hover == nil || hover.Contents.Value != "test hover" {
		t.Errorf("unexpected hover result: %+v", hover)
	}

	// Test Definition.
	locs, err := srv.Definition(ctx, tmpFile, 0, 0)
	if err != nil {
		t.Fatalf("Definition failed: %v", err)
	}
	if len(locs) != 1 || locs[0].Range.Start.Line != 10 {
		t.Errorf("unexpected definition: %+v", locs)
	}

	// Test References.
	refs, err := srv.References(ctx, tmpFile, 0, 0)
	if err != nil {
		t.Fatalf("References failed: %v", err)
	}
	if len(refs) != 1 {
		t.Errorf("expected 1 reference, got %d", len(refs))
	}

	// Test Symbols.
	syms, err := srv.Symbols(ctx, tmpFile)
	if err != nil {
		t.Fatalf("Symbols failed: %v", err)
	}
	if len(syms) != 1 || syms[0].Name != "main" {
		t.Errorf("unexpected symbols: %+v", syms)
	}

	// Test Format.
	edits, err := srv.Format(ctx, tmpFile)
	if err != nil {
		t.Fatalf("Format failed: %v", err)
	}
	if len(edits) != 1 || edits[0].NewText != "fmt" {
		t.Errorf("unexpected edits: %+v", edits)
	}

	// Cleanup.
	srv.client.closed.Store(true)
	_ = srv.client.stdin.Close()
}

func TestManager_DiagnosticCache(t *testing.T) {
	mgr := NewManager(nil)
	defer mgr.Shutdown()

	// Manually populate diagnostics cache.
	mgr.mu.Lock()
	mgr.diagnostics["file:///test.go"] = []Diagnostic{
		{Message: "undefined: foo", Severity: SeverityError},
	}
	mgr.mu.Unlock()

	diags := mgr.CachedDiagnostics("file:///test.go")
	if len(diags) != 1 || diags[0].Message != "undefined: foo" {
		t.Errorf("unexpected cached diagnostics: %+v", diags)
	}

	// Non-existent file returns nil.
	diags = mgr.CachedDiagnostics("file:///nonexistent.go")
	if diags != nil {
		t.Errorf("expected nil for uncached file, got %+v", diags)
	}
}

func TestServer_NotifyChange(t *testing.T) {
	handler := func(req Request) (json.RawMessage, *ResponseError) {
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)

	srv := &Server{
		client:   client,
		language: "go",
		rootURI:  "file:///tmp",
		opened:   make(map[string]int),
	}

	// Mark a file as opened using the same URI the code generates.
	uri := fileURI("/tmp/test.go")
	srv.mu.Lock()
	srv.opened[uri] = 1
	srv.mu.Unlock()

	err := srv.NotifyChange("/tmp/test.go", "package main\nfunc main() {}\n")
	if err != nil {
		t.Fatalf("NotifyChange failed: %v", err)
	}

	// Version should have incremented.
	srv.mu.Lock()
	ver := srv.opened[uri]
	srv.mu.Unlock()
	if ver != 2 {
		t.Errorf("expected version 2, got %d", ver)
	}

	srv.client.closed.Store(true)
	_ = srv.client.stdin.Close()
}

func TestFileURI(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/tmp/test.go", "file:///tmp/test.go"},
		{"/home/user/project/main.rs", "file:///home/user/project/main.rs"},
	}
	for _, tt := range tests {
		got := pathToURI(tt.path)
		if got != tt.want {
			t.Errorf("pathToURI(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestParseLocations(t *testing.T) {
	// Null result.
	locs, err := parseLocations(json.RawMessage(`null`))
	if err != nil || locs != nil {
		t.Errorf("null: got locs=%v, err=%v", locs, err)
	}

	// Array of locations.
	locs, err = parseLocations(json.RawMessage(`[{"uri":"file:///a.go","range":{"start":{"line":1,"character":0},"end":{"line":1,"character":5}}}]`))
	if err != nil || len(locs) != 1 {
		t.Errorf("array: got locs=%v, err=%v", locs, err)
	}

	// Single location.
	locs, err = parseLocations(json.RawMessage(`{"uri":"file:///b.go","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":3}}}`))
	if err != nil || len(locs) != 1 || locs[0].Range.Start.Line != 2 {
		t.Errorf("single: got locs=%v, err=%v", locs, err)
	}
}

func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func TestServer_Diagnostics(t *testing.T) {
	handler := func(req Request) (json.RawMessage, *ResponseError) {
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)
	tmpFile := t.TempDir() + "/test.go"
	if err := writeTestFile(tmpFile, "package main\n"); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		client:   client,
		language: "go",
		rootURI:  "file:///tmp/test",
		opened:   make(map[string]int),
	}

	// Test Diagnostics - should return empty as diagnostics are pushed async
	ctx := context.Background()
	diags, err := srv.Diagnostics(ctx, tmpFile)
	if err != nil {
		t.Fatalf("Diagnostics failed: %v", err)
	}
	if len(diags) != 0 {
		t.Errorf("expected empty diagnostics, got %d", len(diags))
	}

	// Cleanup
	srv.client.closed.Store(true)
	_ = srv.client.stdin.Close()
}

func TestServer_Close(t *testing.T) {
	handler := func(req Request) (json.RawMessage, *ResponseError) {
		if req.Method == "shutdown" {
			return json.RawMessage(`null`), nil
		}
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)
	tmpFile := t.TempDir() + "/test.go"
	if err := writeTestFile(tmpFile, "package main\n"); err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		client:   client,
		language: "go",
		rootURI:  "file:///tmp/test",
		opened:   map[string]int{fileURI(tmpFile): 1},
	}

	// Close should send didClose for all opened files and close the client
	err := srv.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify client was closed
	if !srv.client.closed.Load() {
		t.Error("expected client to be closed")
	}
}

func TestManager_Shutdown(t *testing.T) {
	// Create a manager with a mock server
	handler := func(req Request) (json.RawMessage, *ResponseError) {
		if req.Method == "initialize" {
			return json.RawMessage(`{"capabilities":{}}`), nil
		}
		if req.Method == "shutdown" {
			return json.RawMessage(`null`), nil
		}
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)
	tmpFile := t.TempDir() + "/test.go"
	if err := writeTestFile(tmpFile, "package main\n"); err != nil {
		t.Fatal(err)
	}

	// Manually create a server and add it to manager
	srv := &Server{
		client:   client,
		language: "go",
		rootURI:  "file:///tmp",
		opened:   make(map[string]int),
	}

	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     map[string]*Server{"go:tmp": srv},
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}

	// Shutdown should close all servers
	mgr.Shutdown()

	// Verify server was closed
	if !srv.client.closed.Load() {
		t.Error("expected client to be closed after Shutdown")
	}

	// Verify servers map was cleared
	if len(mgr.servers) != 0 {
		t.Errorf("expected servers map to be empty, got %d entries", len(mgr.servers))
	}
}

func TestManager_ServerFor_AvailableServer(t *testing.T) {
	// This tests ServerFor when the server is available and starts successfully
	cfg := &ManagerConfig{
		Languages: map[string]*LanguageConfig{
			"testlang": {
				Command:        "/bin/echo", // Binary exists but won't work as LSP
				FileExtensions: []string{".test"},
				RootMarkers:    []string{"test.toml"},
				LanguageID:     "testlang",
			},
		},
	}

	mgr := NewManager(cfg)
	defer mgr.Shutdown()

	// The echo command exists but won't respond to LSP protocol
	// ServerFor should return (nil, error) because it can't start a real LSP server
	srv, err := mgr.ServerFor("/tmp/test.test")
	// We expect an error because echo isn't a real LSP server
	if err == nil && srv != nil {
		t.Error("expected error or nil server for non-LSP command")
	}
}

func TestManager_ServerFor_DisabledLanguage(t *testing.T) {
	cfg := &ManagerConfig{
		Disabled: []string{"go"},
	}

	mgr := NewManager(cfg)
	defer mgr.Shutdown()

	// go is disabled, so ServerFor should return (nil, nil)
	srv, err := mgr.ServerFor("/tmp/test.go")
	if err != nil {
		t.Errorf("expected no error for disabled language, got: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server for disabled language")
	}
}

func TestManager_ServerFor_NoRootMarker(t *testing.T) {
	// Create a temp dir without go.mod
	tmpDir := t.TempDir()

	// Use a command that definitely doesn't exist
	cfg := &ManagerConfig{
		Languages: map[string]*LanguageConfig{
			"go": {
				Command:        "nonexistent-lsp-server-that-does-not-exist",
				FileExtensions: []string{".go"},
				RootMarkers:    []string{"go.mod"},
				LanguageID:     "go",
			},
		},
	}

	mgr := NewManager(cfg)
	defer mgr.Shutdown()

	// Since the binary doesn't exist, ServerFor should return (nil, nil)
	srv, err := mgr.ServerFor(tmpDir + "/test.go")
	if err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
	if srv != nil {
		t.Error("expected nil server (binary not found)")
	}
}
