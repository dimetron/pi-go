package lsp

import (
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
