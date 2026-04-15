package tools

import (
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/lsp"
)

func TestLSPTools_Count(t *testing.T) {
	mgr := lsp.NewManager(nil)
	defer mgr.Shutdown()

	tools, err := LSPTools(mgr)
	if err != nil {
		t.Fatalf("LSPTools: %v", err)
	}
	if len(tools) != 7 {
		t.Fatalf("expected 7 LSP tools, got %d", len(tools))
	}

	// Verify tool names.
	expected := map[string]bool{
		"lsp-diagnostics":      false,
		"lsp-definition":       false,
		"lsp-references":       false,
		"lsp-hover":            false,
		"lsp-symbols":          false,
		"lsp-workspace-symbol": false,
		"lsp-code-action":      false,
	}
	for _, tool := range tools {
		name := tool.Name()
		if _, ok := expected[name]; !ok {
			t.Errorf("unexpected tool name: %s", name)
		}
		expected[name] = true
	}
	for name, found := range expected {
		if !found {
			t.Errorf("missing tool: %s", name)
		}
	}
}

func TestLSPDiagnostics_NoServer(t *testing.T) {
	// Manager with all languages disabled — no server for any file.
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	input := LSPFileInput{File: "/tmp/test.go"}
	output, err := lspDiagnosticsHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error message for unsupported file type")
	}
	if output.File != "/tmp/test.go" {
		t.Errorf("expected file /tmp/test.go, got %s", output.File)
	}
}

func TestLSPDefinition_NoServer(t *testing.T) {
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	input := LSPPositionInput{File: "/tmp/test.go", Line: 10, Column: 5}
	output, err := lspDefinitionHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error for no server")
	}
}

func TestLSPReferences_NoServer(t *testing.T) {
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	input := LSPPositionInput{File: "/tmp/test.py", Line: 1, Column: 0}
	output, err := lspReferencesHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error for no server")
	}
}

func TestLSPHover_NoServer(t *testing.T) {
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	input := LSPPositionInput{File: "/tmp/test.rs", Line: 0, Column: 0}
	output, err := lspHoverHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error for no server")
	}
}

func TestLSPSymbols_NoServer(t *testing.T) {
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	input := LSPFileInput{File: "/tmp/test.ts"}
	output, err := lspSymbolsHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error for no server")
	}
}

func TestLSPTools_UnknownFileType(t *testing.T) {
	mgr := lsp.NewManager(nil)
	defer mgr.Shutdown()

	input := LSPFileInput{File: "/tmp/test.txt"}
	output, err := lspDiagnosticsHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error for unknown file type")
	}
}

func TestSymbolKindName(t *testing.T) {
	tests := []struct {
		kind int
		want string
	}{
		{lsp.SymbolKindFile, "file"},
		{lsp.SymbolKindFunction, "function"},
		{lsp.SymbolKindMethod, "method"},
		{lsp.SymbolKindStruct, "struct"},
		{lsp.SymbolKindInterface, "interface"},
		{lsp.SymbolKindVariable, "variable"},
		{lsp.SymbolKindConstant, "constant"},
		{lsp.SymbolKindClass, "class"},
		{lsp.SymbolKindField, "field"},
		{999, "kind(999)"},
	}
	for _, tt := range tests {
		got := symbolKindName(tt.kind)
		if got != tt.want {
			t.Errorf("symbolKindName(%d) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestConvertLocations(t *testing.T) {
	locs := []lsp.Location{
		{
			URI:   "file:///tmp/foo.go",
			Range: lsp.Range{Start: lsp.Position{Line: 10, Character: 5}},
		},
		{
			URI:   "file:///tmp/bar.go",
			Range: lsp.Range{Start: lsp.Position{Line: 20, Character: 0}},
		},
	}

	entries := convertLocations(locs)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].File != "/tmp/foo.go" {
		t.Errorf("expected /tmp/foo.go, got %s", entries[0].File)
	}
	if entries[0].Line != 10 || entries[0].Column != 5 {
		t.Errorf("expected line=10 col=5, got line=%d col=%d", entries[0].Line, entries[0].Column)
	}
	if entries[1].File != "/tmp/bar.go" {
		t.Errorf("expected /tmp/bar.go, got %s", entries[1].File)
	}
}

func TestFlattenSymbols(t *testing.T) {
	symbols := []lsp.DocumentSymbol{
		{
			Name:  "MyStruct",
			Kind:  lsp.SymbolKindStruct,
			Range: lsp.Range{Start: lsp.Position{Line: 5}, End: lsp.Position{Line: 15}},
			Children: []lsp.DocumentSymbol{
				{
					Name:  "MyMethod",
					Kind:  lsp.SymbolKindMethod,
					Range: lsp.Range{Start: lsp.Position{Line: 7}, End: lsp.Position{Line: 10}},
				},
			},
		},
		{
			Name:  "MyFunc",
			Kind:  lsp.SymbolKindFunction,
			Range: lsp.Range{Start: lsp.Position{Line: 20}, End: lsp.Position{Line: 30}},
		},
	}

	entries := flattenSymbols(symbols, nil)
	if len(entries) != 3 {
		t.Fatalf("expected 3 flattened symbols, got %d", len(entries))
	}
	if entries[0].Name != "MyStruct" || entries[0].Kind != "struct" {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[1].Name != "MyMethod" || entries[1].Kind != "method" {
		t.Errorf("entry[1] = %+v", entries[1])
	}
	if entries[2].Name != "MyFunc" || entries[2].Kind != "function" {
		t.Errorf("entry[2] = %+v", entries[2])
	}
	if entries[0].Line != 5 || entries[0].EndLine != 15 {
		t.Errorf("entry[0] line range = %d-%d, want 5-15", entries[0].Line, entries[0].EndLine)
	}
}

func TestURIToPath(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"file:///tmp/foo.go", "/tmp/foo.go"},
		{"file:///home/user/bar.py", "/home/user/bar.py"},
		{"https://example.com", "https://example.com"},
	}
	for _, tt := range tests {
		got := uriToPath(tt.uri)
		if got != tt.want {
			t.Errorf("uriToPath(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestFileURI(t *testing.T) {
	tests := []struct {
		path    string
		wantPfx string
	}{
		{"/tmp/foo.go", "file:///tmp/foo.go"},
		{"/home/user/project/main.go", "file:///home/user/project/main.go"},
	}
	for _, tt := range tests {
		got := fileURI(tt.path)
		if got != tt.wantPfx {
			t.Errorf("fileURI(%q) = %q, want %q", tt.path, got, tt.wantPfx)
		}
	}
}

func TestFileURI_RelativePath(t *testing.T) {
	// Relative paths are converted to absolute
	got := fileURI("relative/path.go")
	if len(got) < 8 || got[:7] != "file://" {
		t.Errorf("fileURI(relative) = %q, want file:// prefix", got)
	}
}

func TestConvertDiagnostics(t *testing.T) {
	diags := []lsp.Diagnostic{
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: 10, Character: 5},
			},
			Severity: 1,
			Message:  "undefined: foo",
			Source:   "gopls",
		},
		{
			Range: lsp.Range{
				Start: lsp.Position{Line: 20, Character: 0},
			},
			Severity: 2,
			Message:  "unused variable",
			Source:   "gopls",
		},
	}
	entries := convertDiagnostics(diags)

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Line != 10 {
		t.Errorf("line = %d, want 10", entries[0].Line)
	}
	if entries[0].Column != 5 {
		t.Errorf("column = %d, want 5", entries[0].Column)
	}
	if entries[0].Message != "undefined: foo" {
		t.Errorf("message = %q", entries[0].Message)
	}
	if entries[0].Source != "gopls" {
		t.Errorf("source = %q", entries[0].Source)
	}
}

func TestConvertDiagnostics_Empty(t *testing.T) {
	entries := convertDiagnostics(nil)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestExtractHoverContent_Nil(t *testing.T) {
	got := extractHoverContent(nil)
	if got != "no hover information available" {
		t.Errorf("got %q", got)
	}
}

func TestExtractHoverContent_WithResult(t *testing.T) {
	result := &lsp.HoverResult{
		Contents: lsp.MarkupContent{Value: "func Foo() int"},
	}
	got := extractHoverContent(result)
	if got != "func Foo() int" {
		t.Errorf("got %q", got)
	}
}

func TestGetServerOrSkipForLanguage_NotAvailable(t *testing.T) {
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	srv, errMsg := getServerOrSkipForLanguage(mgr, "go")
	if srv != nil {
		t.Error("expected nil server")
	}
	if errMsg == "" {
		t.Fatal("expected error message for unavailable language")
	}
	if errMsg != "go not available" {
		t.Errorf("got error %q, want 'go not available'", errMsg)
	}
}

func TestGetServerOrSkipForLanguage_NoExtensions(t *testing.T) {
	// Create a manager and mock a language without file extensions
	mgr := lsp.NewManager(nil)
	defer mgr.Shutdown()

	// Test with a language that exists but has no extensions
	// This would require internal knowledge of how Languages() works
	// For now, test the no-server path via getServerOrSkip
	srv, errMsg := getServerOrSkip(mgr, "/tmp/test.txt")
	if srv != nil {
		t.Error("expected nil server for unknown file type")
	}
	if errMsg == "" {
		t.Fatal("expected error for unknown file type")
	}
}

func TestFormatDiagnosticsForDisplay_Empty(t *testing.T) {
	got := formatDiagnosticsForDisplay("/tmp/test.go", nil)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFormatDiagnosticsForDisplay_OnlyErrors(t *testing.T) {
	// The condition is d.Severity > SeverityWarning (2), so:
	// - SeverityWarning (2) is shown (2 > 2 is false)
	// - SeverityError (1) is shown (1 > 2 is false)
	// - SeverityInformation (3) and SeverityHint (4) are filtered
	diags := []lsp.Diagnostic{
		{
			Range:    lsp.Range{Start: lsp.Position{Line: 1}},
			Severity: lsp.SeverityError, // 1 - shown
			Message:  "error message",
		},
		{
			Range:    lsp.Range{Start: lsp.Position{Line: 2}},
			Severity: lsp.SeverityWarning, // 2 - shown
			Message:  "warning message",
		},
		{
			Range:    lsp.Range{Start: lsp.Position{Line: 3}},
			Severity: lsp.SeverityInformation, // 3 - filtered
			Message:  "info message",
		},
	}
	got := formatDiagnosticsForDisplay("/tmp/test.go", diags)
	// Errors and warnings are shown, info is filtered
	if got == "" {
		t.Error("expected formatted output for errors/warnings")
	}
	if !strings.Contains(got, "error message") {
		t.Errorf("should contain error, got %q", got)
	}
	if !strings.Contains(got, "warning message") {
		t.Errorf("should contain warning, got %q", got)
	}
	if strings.Contains(got, "info message") {
		t.Error("should not contain info (filtered)")
	}
}

func TestFormatDiagnosticsForDisplay_OnlyErrorsFiltered(t *testing.T) {
	// Diagnostics with severity > SeverityWarning (2) are filtered out.
	// This includes SeverityInformation (3) and SeverityHint (4).
	diags := []lsp.Diagnostic{
		{
			Range:    lsp.Range{Start: lsp.Position{Line: 1}},
			Severity: lsp.SeverityInformation, // 3 - filtered OUT (3 > 2)
			Message:  "info message",
		},
		{
			Range:    lsp.Range{Start: lsp.Position{Line: 2}},
			Severity: lsp.SeverityHint, // 4 - filtered OUT (4 > 2)
			Message:  "hint message",
		},
	}
	got := formatDiagnosticsForDisplay("/tmp/test.go", diags)
	if got != "" {
		t.Errorf("got %q, want empty (info/hint filtered)", got)
	}
}

func TestFormatDiagnosticsForDisplay_ZeroSeverity(t *testing.T) {
	diags := []lsp.Diagnostic{
		{
			Range:    lsp.Range{Start: lsp.Position{Line: 0}},
			Severity: 0, // 0 < 2, so shown
			Message:  "info message",
		},
	}
	got := formatDiagnosticsForDisplay("/tmp/test.go", diags)
	// Severity 0 is shown (0 < 2)
	if got == "" {
		t.Error("expected output for severity 0")
	}
}

func TestLSPCodeAction_NoServer(t *testing.T) {
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	input := LSPCodeActionInput{
		File:      "/tmp/test.go",
		StartLine: 10, StartCol: 0,
		EndLine: 20, EndCol: 10,
	}
	output, err := lspCodeActionHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error for no server")
	}
	if output.File != "/tmp/test.go" {
		t.Errorf("File = %q, want '/tmp/test.go'", output.File)
	}
}

func TestLSPWorkspaceSymbol_NoServerAvailable(t *testing.T) {
	mgr := lsp.NewManager(&lsp.ManagerConfig{
		Disabled: []string{"go", "typescript", "python", "rust"},
	})
	defer mgr.Shutdown()

	input := LSPWorkspaceSymbolInput{Query: "main"}
	output, err := lspWorkspaceSymbolHandler(nil, mgr, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output.Error == "" {
		t.Fatal("expected error for no available servers")
	}
	if output.Error != "no language server available" {
		t.Errorf("got error %q", output.Error)
	}
}

func TestLSPWorkspaceSymbolInput(t *testing.T) {
	input := LSPWorkspaceSymbolInput{Query: "findThis"}
	if input.Query != "findThis" {
		t.Errorf("Query = %q, want 'findThis'", input.Query)
	}
}

func TestLSPCodeActionInput(t *testing.T) {
	input := LSPCodeActionInput{
		File:      "/tmp/test.go",
		StartLine: 5, StartCol: 10,
		EndLine: 10, EndCol: 20,
	}
	if input.File != "/tmp/test.go" {
		t.Errorf("File = %q", input.File)
	}
	if input.StartLine != 5 || input.StartCol != 10 {
		t.Errorf("Start = %d:%d, want 5:10", input.StartLine, input.StartCol)
	}
	if input.EndLine != 10 || input.EndCol != 20 {
		t.Errorf("End = %d:%d, want 10:20", input.EndLine, input.EndCol)
	}
}

func TestLSPWorkspaceSymbolOutput(t *testing.T) {
	output := LSPWorkspaceSymbolOutput{
		Symbols: []WorkspaceSymbolEntry{
			{Name: "Main", Kind: "function", File: "/tmp/main.go", Line: 10, Column: 0},
		},
	}
	if len(output.Symbols) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(output.Symbols))
	}
	if output.Symbols[0].Name != "Main" {
		t.Errorf("Name = %q", output.Symbols[0].Name)
	}
}

func TestLSPCodeActionOutput(t *testing.T) {
	output := LSPCodeActionOutput{
		File: "/tmp/test.go",
		Actions: []ActionEntry{
			{Title: "Add import", Kind: "quickfix", IsPreferred: true},
		},
	}
	if len(output.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(output.Actions))
	}
	if output.Actions[0].Title != "Add import" {
		t.Errorf("Title = %q", output.Actions[0].Title)
	}
	if !output.Actions[0].IsPreferred {
		t.Error("IsPreferred should be true")
	}
}
