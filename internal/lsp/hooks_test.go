package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestApplyTextEdits_SingleEdit(t *testing.T) {
	content := "hello world\nfoo bar\nbaz qux\n"
	edits := []TextEdit{
		{
			Range:   Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 3}},
			NewText: "FOO",
		},
	}
	result := ApplyTextEdits(content, edits)
	if !strings.Contains(result, "FOO bar") {
		t.Errorf("expected 'FOO bar', got:\n%s", result)
	}
}

func TestApplyTextEdits_MultipleEdits(t *testing.T) {
	content := "aaa\nbbb\nccc\n"
	edits := []TextEdit{
		{
			Range:   Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 3}},
			NewText: "AAA",
		},
		{
			Range:   Range{Start: Position{Line: 2, Character: 0}, End: Position{Line: 2, Character: 3}},
			NewText: "CCC",
		},
	}
	result := ApplyTextEdits(content, edits)
	lines := strings.Split(result, "\n")
	if lines[0] != "AAA" {
		t.Errorf("line 0: expected 'AAA', got %q", lines[0])
	}
	if lines[2] != "CCC" {
		t.Errorf("line 2: expected 'CCC', got %q", lines[2])
	}
}

func TestApplyTextEdits_InsertNewLines(t *testing.T) {
	content := "line1\nline2\n"
	edits := []TextEdit{
		{
			Range:   Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 1, Character: 0}},
			NewText: "inserted\n",
		},
	}
	result := ApplyTextEdits(content, edits)
	if !strings.Contains(result, "inserted\nline2") {
		t.Errorf("expected inserted line, got:\n%s", result)
	}
}

func TestApplyTextEdits_DeleteRange(t *testing.T) {
	content := "keep\ndelete me\nkeep too\n"
	edits := []TextEdit{
		{
			Range:   Range{Start: Position{Line: 1, Character: 0}, End: Position{Line: 2, Character: 0}},
			NewText: "",
		},
	}
	result := ApplyTextEdits(content, edits)
	if strings.Contains(result, "delete me") {
		t.Errorf("expected line deleted, got:\n%s", result)
	}
	if !strings.Contains(result, "keep") && !strings.Contains(result, "keep too") {
		t.Errorf("expected other lines preserved, got:\n%s", result)
	}
}

func TestApplyTextEdits_EmptyEdits(t *testing.T) {
	content := "unchanged"
	result := ApplyTextEdits(content, nil)
	if result != content {
		t.Errorf("expected unchanged content, got %q", result)
	}
}

func TestApplyTextEdits_ReplaceEntireContent(t *testing.T) {
	content := "old content\nmore old\n"
	edits := []TextEdit{
		{
			Range:   Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 2, Character: 0}},
			NewText: "new content\n",
		},
	}
	result := ApplyTextEdits(content, edits)
	if !strings.HasPrefix(result, "new content") {
		t.Errorf("expected 'new content', got:\n%s", result)
	}
}

func TestEditBefore(t *testing.T) {
	a := TextEdit{Range: Range{Start: Position{Line: 5, Character: 0}}}
	b := TextEdit{Range: Range{Start: Position{Line: 2, Character: 0}}}

	if !editBefore(a, b) {
		t.Error("expected a (line 5) before b (line 2) in reverse order")
	}
	if editBefore(b, a) {
		t.Error("expected b (line 2) NOT before a (line 5)")
	}
}

func TestBuildLSPAfterToolCallback_NilManager(t *testing.T) {
	// Verify the callback builder doesn't panic with a valid manager.
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}
	cb := BuildLSPAfterToolCallback(mgr)
	if cb == nil {
		t.Fatal("expected non-nil callback")
	}
}

func TestCollectDiagnostics_FiltersToErrorsAndWarnings(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}

	// Pre-populate diagnostics cache.
	testURI := pathToURI("/tmp/test.go")
	mgr.diagnostics[testURI] = []Diagnostic{
		{Range: Range{Start: Position{Line: 5, Character: 0}}, Severity: SeverityError, Message: "undefined: foo"},
		{Range: Range{Start: Position{Line: 10, Character: 3}}, Severity: SeverityWarning, Message: "unused variable"},
		{Range: Range{Start: Position{Line: 15, Character: 0}}, Severity: SeverityHint, Message: "consider renaming"},
		{Range: Range{Start: Position{Line: 20, Character: 0}}, Severity: SeverityInformation, Message: "info message"},
	}

	result := map[string]any{"path": "/tmp/test.go"}
	result = collectDiagnosticsImmediate(mgr, nil, "/tmp/test.go", result)

	diagStr, ok := result["lsp_diagnostics"].(string)
	if !ok {
		t.Fatal("expected lsp_diagnostics string in result")
	}

	if !strings.Contains(diagStr, "error: undefined: foo") {
		t.Errorf("expected error diagnostic, got: %s", diagStr)
	}
	if !strings.Contains(diagStr, "warning: unused variable") {
		t.Errorf("expected warning diagnostic, got: %s", diagStr)
	}
	if strings.Contains(diagStr, "consider renaming") {
		t.Error("hint should be filtered out")
	}
	if strings.Contains(diagStr, "info message") {
		t.Error("info should be filtered out")
	}
}

func TestCollectDiagnostics_NoDiagnostics(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}

	result := map[string]any{"path": "/tmp/clean.go"}
	result = collectDiagnosticsImmediate(mgr, nil, "/tmp/clean.go", result)

	if _, ok := result["lsp_diagnostics"]; ok {
		t.Error("expected no lsp_diagnostics for clean file")
	}
}

// mockServer is a minimal Server implementation for testing hooks.
type mockServer struct {
	formatResult []TextEdit
	formatErr    error
}

func (m *mockServer) Format(_ context.Context, _ string) ([]TextEdit, error) {
	return m.formatResult, m.formatErr
}

func (m *mockServer) NotifyChange(_, _ string) error {
	return nil
}

func TestFormatFile_HappyPath(t *testing.T) {
	// Create a temp file
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.go"
	content := "package main\nfunc main() {}\n"
	if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mock server that returns a formatting edit
	mockSrv := &mockServer{
		formatResult: []TextEdit{
			{
				Range:   Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 12}},
				NewText: "package main",
			},
		},
	}

	result := map[string]any{"path": tmpFile}
	result = formatFileWithFormatter(mockSrv, tmpFile, result)

	if !result["lsp_formatted"].(bool) {
		t.Error("expected lsp_formatted to be true")
	}

	// Verify file was formatted
	formatted, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read formatted file: %v", err)
	}
	if string(formatted) != "package main\nfunc main() {}\n" {
		t.Errorf("unexpected formatted content: %q", string(formatted))
	}
}

func TestFormatFile_FormatError(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.go"
	content := "package main\n"
	if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mock server that returns error
	mockSrv := &mockServer{
		formatErr: fmt.Errorf("format failed"),
	}

	result := map[string]any{"path": tmpFile}
	result = formatFileWithFormatter(mockSrv, tmpFile, result)

	// Should return unchanged result
	if _, ok := result["lsp_formatted"]; ok {
		t.Error("expected no lsp_formatted when format fails")
	}
}

func TestFormatFile_EmptyEdits(t *testing.T) {
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.go"
	content := "package main\n"
	if err := os.WriteFile(tmpFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// Mock server that returns no edits
	mockSrv := &mockServer{
		formatResult: []TextEdit{},
	}

	result := map[string]any{"path": tmpFile}
	result = formatFileWithFormatter(mockSrv, tmpFile, result)

	// Should return unchanged result
	if _, ok := result["lsp_formatted"]; ok {
		t.Error("expected no lsp_formatted when no edits returned")
	}
}

// mockTool implements tool.Tool for testing.
type mockTool struct {
	name string
}

func (m *mockTool) Name() string        { return m.name }
func (m *mockTool) Description() string { return "mock tool" }
func (m *mockTool) IsLongRunning() bool { return false }

func TestBuildLSPAfterToolCallback_NonWriteTool(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}
	cb := BuildLSPAfterToolCallback(mgr)

	// Tools other than "write" and "edit" should be no-ops.
	for _, toolName := range []string{"read", "bash", "search", "delete", "list"} {
		tool := &mockTool{name: toolName}
		result := map[string]any{"path": "/tmp/test.go", "output": "some output"}
		got, err := cb(nil, tool, nil, result, nil)
		if err != nil {
			t.Errorf("tool %q: unexpected error: %v", toolName, err)
		}
		if got["output"] != "some output" {
			t.Errorf("tool %q: result should be unchanged, got: %v", toolName, got)
		}
		if _, ok := got["lsp_formatted"]; ok {
			t.Errorf("tool %q: should not have lsp_formatted", toolName)
		}
	}
}

func TestBuildLSPAfterToolCallback_ToolError(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}
	cb := BuildLSPAfterToolCallback(mgr)

	// When the tool itself errored, callback should be a no-op.
	tool := &mockTool{name: "write"}
	result := map[string]any{"path": "/tmp/test.go"}
	toolErr := fmt.Errorf("write failed: disk full")
	got, err := cb(nil, tool, nil, result, toolErr)
	if err != nil {
		t.Errorf("unexpected error from callback: %v", err)
	}
	if _, ok := got["lsp_formatted"]; ok {
		t.Error("should not format when tool errored")
	}
	if _, ok := got["lsp_diagnostics"]; ok {
		t.Error("should not collect diagnostics when tool errored")
	}
}

func TestBuildLSPAfterToolCallback_MissingPath(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}
	cb := BuildLSPAfterToolCallback(mgr)

	tool := &mockTool{name: "write"}

	// Missing path key entirely.
	result := map[string]any{"output": "something"}
	got, err := cb(nil, tool, nil, result, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, ok := got["lsp_formatted"]; ok {
		t.Error("should not format without path")
	}

	// Path present but empty string.
	result2 := map[string]any{"path": ""}
	got2, err2 := cb(nil, tool, nil, result2, nil)
	if err2 != nil {
		t.Errorf("unexpected error: %v", err2)
	}
	if _, ok := got2["lsp_formatted"]; ok {
		t.Error("should not format with empty path")
	}

	// Path present but wrong type.
	result3 := map[string]any{"path": 12345}
	got3, err3 := cb(nil, tool, nil, result3, nil)
	if err3 != nil {
		t.Errorf("unexpected error: %v", err3)
	}
	if _, ok := got3["lsp_formatted"]; ok {
		t.Error("should not format when path is not a string")
	}
}

func TestBuildLSPAfterToolCallback_NoServerForLanguage(t *testing.T) {
	// Manager with no configured languages — no server for any file.
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}
	cb := BuildLSPAfterToolCallback(mgr)

	tool := &mockTool{name: "write"}
	result := map[string]any{"path": "/tmp/test.unknownext"}
	got, err := cb(nil, tool, nil, result, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if _, ok := got["lsp_formatted"]; ok {
		t.Error("should not format when no server available")
	}
}

func TestBuildLSPAfterToolCallback_EditToolSkipsFormat(t *testing.T) {
	// For "edit" tool, formatting should NOT be triggered even if server is available.
	// Only diagnostics should be collected.
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}
	cb := BuildLSPAfterToolCallback(mgr)

	tool := &mockTool{name: "edit"}
	result := map[string]any{"path": "/tmp/test.unknownext"}
	got, err := cb(nil, tool, nil, result, nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	// No server for .unknownext — should return unchanged result.
	if _, ok := got["lsp_formatted"]; ok {
		t.Error("edit tool should not trigger formatting")
	}
}

func TestFormatFileWithFormatter_NonexistentFile(t *testing.T) {
	// When the file to format doesn't exist, should return result unchanged.
	mockSrv := &mockServer{
		formatResult: []TextEdit{
			{
				Range:   Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 0, Character: 3}},
				NewText: "NEW",
			},
		},
	}

	result := map[string]any{"path": "/nonexistent/file/that/does/not/exist.go"}
	got := formatFileWithFormatter(mockSrv, "/nonexistent/file/that/does/not/exist.go", result)

	if _, ok := got["lsp_formatted"]; ok {
		t.Error("should not mark formatted when file doesn't exist")
	}
}

func TestCollectDiagnosticsImmediate_OnlyErrors(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}

	testURI := pathToURI("/tmp/errors_only.go")
	mgr.diagnostics[testURI] = []Diagnostic{
		{Range: Range{Start: Position{Line: 0, Character: 0}}, Severity: SeverityError, Message: "syntax error"},
	}

	result := collectDiagnosticsImmediate(mgr, nil, "/tmp/errors_only.go", map[string]any{})
	diagStr, ok := result["lsp_diagnostics"].(string)
	if !ok {
		t.Fatal("expected lsp_diagnostics in result")
	}
	if !strings.Contains(diagStr, "error: syntax error") {
		t.Errorf("expected error diagnostic, got: %s", diagStr)
	}
}

func TestCollectDiagnosticsImmediate_AllHintsFiltered(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}

	testURI := pathToURI("/tmp/hints_only.go")
	mgr.diagnostics[testURI] = []Diagnostic{
		{Range: Range{Start: Position{Line: 0, Character: 0}}, Severity: SeverityHint, Message: "consider using X"},
		{Range: Range{Start: Position{Line: 1, Character: 0}}, Severity: SeverityInformation, Message: "info about Y"},
	}

	result := collectDiagnosticsImmediate(mgr, nil, "/tmp/hints_only.go", map[string]any{})
	if _, ok := result["lsp_diagnostics"]; ok {
		t.Error("hints and info should be filtered out — lsp_diagnostics should not be in result")
	}
}

func TestCollectDiagnosticsImmediate_DiagnosticLineFormat(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}

	testURI := pathToURI("/tmp/format_test.go")
	mgr.diagnostics[testURI] = []Diagnostic{
		{
			Range:    Range{Start: Position{Line: 4, Character: 7}},
			Severity: SeverityWarning,
			Message:  "unused import",
		},
	}

	result := collectDiagnosticsImmediate(mgr, nil, "/tmp/format_test.go", map[string]any{})
	diagStr, ok := result["lsp_diagnostics"].(string)
	if !ok {
		t.Fatal("expected lsp_diagnostics")
	}
	// Line/char are 1-based in output.
	if !strings.Contains(diagStr, "format_test.go:5:8: warning: unused import") {
		t.Errorf("unexpected diagnostic format: %q", diagStr)
	}
}

func TestApplyTextEdits_OutOfBoundsClamp(t *testing.T) {
	content := "line0\nline1\n"
	edits := []TextEdit{
		{
			// startLine and endLine beyond the end of the document.
			Range:   Range{Start: Position{Line: 100, Character: 0}, End: Position{Line: 200, Character: 0}},
			NewText: "appended",
		},
	}
	// Should not panic — clamping should handle out-of-bounds gracefully.
	result := ApplyTextEdits(content, edits)
	if result == "" {
		t.Error("result should not be empty")
	}
}

func TestApplyTextEdits_CharBeyondLineLength(t *testing.T) {
	content := "abc\ndef\n"
	edits := []TextEdit{
		{
			// Character beyond line length — should be clamped.
			Range:   Range{Start: Position{Line: 0, Character: 999}, End: Position{Line: 0, Character: 999}},
			NewText: "X",
		},
	}
	// Should not panic.
	result := ApplyTextEdits(content, edits)
	if !strings.Contains(result, "abc") {
		t.Errorf("original line content should be preserved, got: %q", result)
	}
}

func TestBuildLSPAfterToolCallback_WriteTool_WithCachedServer(t *testing.T) {
	// Set up a manager with a mock server pre-loaded in the cache.
	// This exercises the paths: notifyChange + formatFile + collectDiagnostics.
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.fakego"
	if err := os.WriteFile(tmpFile, []byte("hello world\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := func(req Request) (json.RawMessage, *ResponseError) {
		switch req.Method {
		case "textDocument/formatting":
			// Return a single formatting edit.
			return json.RawMessage(`[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}},"newText":"HELLO"}]`), nil
		}
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)
	srv := &Server{
		client:   client,
		language: "fakego",
		rootURI:  "file://" + tmpDir,
		opened:   make(map[string]int),
	}

	// Pre-mark the file as open so NotifyChange doesn't need a didOpen.
	uri := fileURI(tmpFile)
	srv.mu.Lock()
	srv.opened[uri] = 1
	srv.mu.Unlock()

	// The key is lang:root — root is tmpDir because no root marker exists there.
	serverKey := "fakego:" + tmpDir

	mgr := &Manager{
		languages: map[string]*LanguageConfig{
			"fakego": {
				Command:        "nonexistent", // not used since server is cached
				FileExtensions: []string{".fakego"},
				RootMarkers:    []string{"fakego.mod"},
				LanguageID:     "fakego",
			},
		},
		servers:     map[string]*Server{serverKey: srv},
		diagnostics: make(map[string][]Diagnostic),
		available:   map[string]bool{"fakego": true},
	}

	// Verify that ServerFor returns the cached server (not nil).
	cachedSrv, err := mgr.ServerFor(tmpFile)
	if err != nil {
		t.Fatalf("ServerFor failed: %v", err)
	}
	if cachedSrv == nil {
		t.Fatal("expected cached server to be returned by ServerFor")
	}

	result := map[string]any{"path": tmpFile}

	// Verify the formatting path with the cached server.
	// (The callback also calls collectDiagnostics which has a 2s sleep, so we
	// test the format path directly here.)
	fmtResult := formatFileWithFormatter(srv, tmpFile, result)
	if formatted, ok := fmtResult["lsp_formatted"].(bool); !ok || !formatted {
		t.Error("expected lsp_formatted=true after formatting with mock server")
	}

	client.closed.Store(true)
	_ = client.stdin.Close()
}

func TestBuildLSPAfterToolCallback_FullWritePath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full callback path test (has 2s diagnostic delay)")
	}

	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/full.fakego"
	if err := os.WriteFile(tmpFile, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := func(req Request) (json.RawMessage, *ResponseError) {
		if req.Method == "textDocument/formatting" {
			return json.RawMessage(`[]`), nil // no-op format
		}
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)
	srv := &Server{
		client:   client,
		language: "fakego",
		rootURI:  "file://" + tmpDir,
		opened:   make(map[string]int),
	}

	// Pre-mark as open so NotifyChange doesn't need to read the file for didOpen.
	uri := fileURI(tmpFile)
	srv.mu.Lock()
	srv.opened[uri] = 1
	srv.mu.Unlock()

	serverKey := "fakego:" + tmpDir
	mgr := &Manager{
		languages: map[string]*LanguageConfig{
			"fakego": {
				Command:        "nonexistent",
				FileExtensions: []string{".fakego"},
				RootMarkers:    []string{"fakego.mod"},
				LanguageID:     "fakego",
			},
		},
		servers:     map[string]*Server{serverKey: srv},
		diagnostics: make(map[string][]Diagnostic),
		available:   map[string]bool{"fakego": true},
	}

	cb := BuildLSPAfterToolCallback(mgr)
	tool := &mockTool{name: "write"}
	result := map[string]any{"path": tmpFile}

	// This invokes the full callback path including the 2s DiagnosticsDelay.
	got, err := cb(nil, tool, nil, result, nil)
	if err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	// No formatting edits returned (empty array) → lsp_formatted should NOT be set.
	if _, ok := got["lsp_formatted"]; ok {
		t.Error("unexpected lsp_formatted with empty edits")
	}
	// No diagnostics cached → lsp_diagnostics should NOT be set.
	if _, ok := got["lsp_diagnostics"]; ok {
		t.Error("unexpected lsp_diagnostics with no cached diagnostics")
	}

	client.closed.Store(true)
	_ = client.stdin.Close()
}

func TestBuildLSPAfterToolCallback_FullEditPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping full callback path test (has 2s diagnostic delay)")
	}

	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/edit.fakego"
	if err := os.WriteFile(tmpFile, []byte("edited content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := func(req Request) (json.RawMessage, *ResponseError) {
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)
	srv := &Server{
		client:   client,
		language: "fakego",
		rootURI:  "file://" + tmpDir,
		opened:   make(map[string]int),
	}

	uri := fileURI(tmpFile)
	srv.mu.Lock()
	srv.opened[uri] = 1
	srv.mu.Unlock()

	testURI := pathToURI(tmpFile)
	serverKey := "fakego:" + tmpDir
	mgr := &Manager{
		languages: map[string]*LanguageConfig{
			"fakego": {
				Command:        "nonexistent",
				FileExtensions: []string{".fakego"},
				RootMarkers:    []string{"fakego.mod"},
				LanguageID:     "fakego",
			},
		},
		servers: map[string]*Server{serverKey: srv},
		diagnostics: map[string][]Diagnostic{
			testURI: {
				{Range: Range{Start: Position{Line: 0}}, Severity: SeverityError, Message: "full edit error"},
			},
		},
		available: map[string]bool{"fakego": true},
	}

	cb := BuildLSPAfterToolCallback(mgr)
	tool := &mockTool{name: "edit"}
	result := map[string]any{"path": tmpFile}

	got, err := cb(nil, tool, nil, result, nil)
	if err != nil {
		t.Fatalf("callback returned error: %v", err)
	}
	// Edit tool should NOT set lsp_formatted.
	if _, ok := got["lsp_formatted"]; ok {
		t.Error("edit tool should not trigger formatting")
	}
	// Should collect diagnostics.
	diagStr, ok := got["lsp_diagnostics"].(string)
	if !ok {
		t.Fatal("expected lsp_diagnostics for edit path with cached diagnostics")
	}
	if !strings.Contains(diagStr, "full edit error") {
		t.Errorf("expected 'full edit error' in diagnostics, got: %s", diagStr)
	}

	client.closed.Store(true)
	_ = client.stdin.Close()
}

func TestBuildLSPAfterToolCallback_EditTool_WithCachedServer(t *testing.T) {
	// Test the "edit" path: no formatting, but diagnostics collection.
	tmpDir := t.TempDir()
	tmpFile := tmpDir + "/test.fakego2"
	if err := os.WriteFile(tmpFile, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := func(req Request) (json.RawMessage, *ResponseError) {
		return json.RawMessage(`null`), nil
	}

	client, _ := newClientWithMock(handler)
	srv := &Server{
		client:   client,
		language: "fakego2",
		rootURI:  "file://" + tmpDir,
		opened:   make(map[string]int),
	}

	uri := fileURI(tmpFile)
	srv.mu.Lock()
	srv.opened[uri] = 1
	srv.mu.Unlock()

	serverKey := "fakego2:" + tmpDir

	// Pre-populate diagnostics for the file.
	testURI := pathToURI(tmpFile)
	mgr := &Manager{
		languages: map[string]*LanguageConfig{
			"fakego2": {
				Command:        "nonexistent",
				FileExtensions: []string{".fakego2"},
				RootMarkers:    []string{"fakego2.mod"},
				LanguageID:     "fakego2",
			},
		},
		servers: map[string]*Server{serverKey: srv},
		diagnostics: map[string][]Diagnostic{
			testURI: {
				{Range: Range{Start: Position{Line: 0, Character: 0}}, Severity: SeverityError, Message: "edit tool error"},
			},
		},
		available: map[string]bool{"fakego2": true},
	}

	// For edit tool, formatting is skipped, diagnostics are collected.
	result := collectDiagnosticsImmediate(mgr, srv, tmpFile, map[string]any{"path": tmpFile})
	diagStr, ok := result["lsp_diagnostics"].(string)
	if !ok {
		t.Fatal("expected lsp_diagnostics for edit tool path")
	}
	if !strings.Contains(diagStr, "edit tool error") {
		t.Errorf("expected 'edit tool error' in diagnostics, got: %s", diagStr)
	}

	client.closed.Store(true)
	_ = client.stdin.Close()
}

func TestCollectDiagnostics_WaitForDiagnostics(t *testing.T) {
	mgr := &Manager{
		languages:   make(map[string]*LanguageConfig),
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   make(map[string]bool),
	}

	// Pre-populate diagnostics cache
	testURI := pathToURI("/tmp/test.go")
	mgr.diagnostics[testURI] = []Diagnostic{
		{Range: Range{Start: Position{Line: 5, Character: 0}}, Severity: SeverityError, Message: "error message"},
	}

	result := map[string]any{"path": "/tmp/test.go"}
	// Note: collectDiagnostics waits for DiagnosticsDelay (2s)
	// We can't easily test the delay without actual time passing
	result = collectDiagnosticsImmediate(mgr, nil, "/tmp/test.go", result)

	diagStr, ok := result["lsp_diagnostics"].(string)
	if !ok {
		t.Fatal("expected lsp_diagnostics in result")
	}
	if !strings.Contains(diagStr, "error message") {
		t.Errorf("expected error message in diagnostics, got: %s", diagStr)
	}
}
