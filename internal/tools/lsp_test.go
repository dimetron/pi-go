package tools

import (
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
	if len(tools) != 5 {
		t.Fatalf("expected 5 LSP tools, got %d", len(tools))
	}

	// Verify tool names.
	expected := map[string]bool{
		"lsp-diagnostics": false,
		"lsp-definition":  false,
		"lsp-references":  false,
		"lsp-hover":       false,
		"lsp-symbols":     false,
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
