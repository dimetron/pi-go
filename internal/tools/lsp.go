package tools

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/dimetron/pi-go/internal/lsp"
	"google.golang.org/adk/tool"
)

// lspFileAliases maps common LLM parameter name mistakes to canonical names.
// LLMs frequently send "file_path" or "path" instead of "file".
var lspFileAliases = map[string]string{"file_path": "file", "path": "file"}

// --- Input/Output types ---

// LSPFileInput is shared input for tools that take only a file path.
type LSPFileInput struct {
	File string `json:"file"`
}

// LSPPositionInput is shared input for tools that take a file + position.
type LSPPositionInput struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// LSPDiagnosticsOutput is the output of the lsp-diagnostics tool.
type LSPDiagnosticsOutput struct {
	File        string            `json:"file"`
	Diagnostics []DiagnosticEntry `json:"diagnostics"`
	Error       string            `json:"error,omitempty"`
}

// DiagnosticEntry is a single diagnostic for tool output.
type DiagnosticEntry struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Source   string `json:"source,omitempty"`
}

// LSPLocationsOutput is the output for definition/references tools.
type LSPLocationsOutput struct {
	Locations []LocationEntry `json:"locations"`
	Error     string          `json:"error,omitempty"`
}

// LocationEntry is a single location for tool output.
type LocationEntry struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// LSPHoverOutput is the output of the lsp-hover tool.
type LSPHoverOutput struct {
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// LSPSymbolsOutput is the output of the lsp-symbols tool.
type LSPSymbolsOutput struct {
	File    string        `json:"file"`
	Symbols []SymbolEntry `json:"symbols"`
	Error   string        `json:"error,omitempty"`
}

// SymbolEntry is a single symbol for tool output.
type SymbolEntry struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Line    int    `json:"line"`
	EndLine int    `json:"end_line"`
}

// --- Tool constructors ---

func newLSPDiagnosticsTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-diagnostics",
		`Get LSP diagnostics (errors, warnings) for a file.

Returns compiler errors, type errors, and warnings from the language server.
Use this to check a file for errors without editing it, or to get more detail
than the automatic diagnostics provided after write/edit.`,
		func(ctx tool.Context, input LSPFileInput) (LSPDiagnosticsOutput, error) {
			return lspDiagnosticsHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPDefinitionTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-definition",
		`Go to definition of a symbol at a given position.

Returns the file and line where a function, type, variable, or other symbol
is defined. Line and column are 0-based.`,
		func(ctx tool.Context, input LSPPositionInput) (LSPLocationsOutput, error) {
			return lspDefinitionHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPReferencesTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-references",
		`Find all references to a symbol at a given position.

Returns all locations where the symbol at the given position is referenced,
including the declaration. Line and column are 0-based.`,
		func(ctx tool.Context, input LSPPositionInput) (LSPLocationsOutput, error) {
			return lspReferencesHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPHoverTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-hover",
		`Get type information and documentation for a symbol at a given position.

Returns the type signature and documentation for the symbol under the cursor.
Line and column are 0-based.`,
		func(ctx tool.Context, input LSPPositionInput) (LSPHoverOutput, error) {
			return lspHoverHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPSymbolsTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-symbols",
		`List all symbols (functions, types, variables) in a file.

Returns an overview of the file's structure including function definitions,
type declarations, constants, and variables with their line ranges.`,
		func(ctx tool.Context, input LSPFileInput) (LSPSymbolsOutput, error) {
			return lspSymbolsHandler(ctx, mgr, input)
		}, lspFileAliases)
}

// LSPTools returns the 5 explicit LSP ADK tools.
func LSPTools(mgr *lsp.Manager) ([]tool.Tool, error) {
	builders := []func(*lsp.Manager) (tool.Tool, error){
		newLSPDiagnosticsTool,
		newLSPDefinitionTool,
		newLSPReferencesTool,
		newLSPHoverTool,
		newLSPSymbolsTool,
	}

	result := make([]tool.Tool, 0, len(builders))
	for _, b := range builders {
		t, err := b(mgr)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, nil
}

// --- Handlers ---

func getServerOrSkip(mgr *lsp.Manager, file string) (*lsp.Server, string) {
	srv, err := mgr.ServerFor(file)
	if err != nil {
		return nil, fmt.Sprintf("language server error: %v", err)
	}
	if srv == nil {
		ext := filepath.Ext(file)
		return nil, fmt.Sprintf("no language server configured for %s files", ext)
	}
	return srv, ""
}

func lspDiagnosticsHandler(_ tool.Context, mgr *lsp.Manager, input LSPFileInput) (LSPDiagnosticsOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPDiagnosticsOutput{File: input.File, Error: errMsg}, nil
	}

	// Trigger didOpen/didChange to prompt diagnostics push.
	_, _ = srv.Diagnostics(context.Background(), input.File)

	// Read cached diagnostics.
	uri := fileURI(input.File)
	cached := mgr.CachedDiagnostics(uri)

	entries := make([]DiagnosticEntry, 0, len(cached))
	for _, d := range cached {
		entries = append(entries, DiagnosticEntry{
			Line:     d.Range.Start.Line,
			Column:   d.Range.Start.Character,
			Severity: d.SeverityString(),
			Message:  d.Message,
			Source:   d.Source,
		})
	}

	return LSPDiagnosticsOutput{
		File:        input.File,
		Diagnostics: entries,
	}, nil
}

func lspDefinitionHandler(_ tool.Context, mgr *lsp.Manager, input LSPPositionInput) (LSPLocationsOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPLocationsOutput{Error: errMsg}, nil
	}

	locs, err := srv.Definition(context.Background(), input.File, input.Line, input.Column)
	if err != nil {
		return LSPLocationsOutput{Error: err.Error()}, nil
	}

	return LSPLocationsOutput{Locations: convertLocations(locs)}, nil
}

func lspReferencesHandler(_ tool.Context, mgr *lsp.Manager, input LSPPositionInput) (LSPLocationsOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPLocationsOutput{Error: errMsg}, nil
	}

	locs, err := srv.References(context.Background(), input.File, input.Line, input.Column)
	if err != nil {
		return LSPLocationsOutput{Error: err.Error()}, nil
	}

	return LSPLocationsOutput{Locations: convertLocations(locs)}, nil
}

func lspHoverHandler(_ tool.Context, mgr *lsp.Manager, input LSPPositionInput) (LSPHoverOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPHoverOutput{Error: errMsg}, nil
	}

	result, err := srv.Hover(context.Background(), input.File, input.Line, input.Column)
	if err != nil {
		return LSPHoverOutput{Error: err.Error()}, nil
	}
	if result == nil {
		return LSPHoverOutput{Content: "no hover information available"}, nil
	}

	return LSPHoverOutput{Content: result.Contents.Value}, nil
}

func lspSymbolsHandler(_ tool.Context, mgr *lsp.Manager, input LSPFileInput) (LSPSymbolsOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPSymbolsOutput{File: input.File, Error: errMsg}, nil
	}

	symbols, err := srv.Symbols(context.Background(), input.File)
	if err != nil {
		return LSPSymbolsOutput{File: input.File, Error: err.Error()}, nil
	}

	entries := flattenSymbols(symbols, nil)

	return LSPSymbolsOutput{
		File:    input.File,
		Symbols: entries,
	}, nil
}

// --- Helpers ---

// fileURI converts a file path to a file:// URI (matching lsp package convention).
func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return u.String()
}

func convertLocations(locs []lsp.Location) []LocationEntry {
	entries := make([]LocationEntry, 0, len(locs))
	for _, loc := range locs {
		file := uriToPath(loc.URI)
		entries = append(entries, LocationEntry{
			File:   file,
			Line:   loc.Range.Start.Line,
			Column: loc.Range.Start.Character,
		})
	}
	return entries
}

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return uri
	}
	if u.Scheme == "file" {
		return filepath.FromSlash(u.Path)
	}
	return uri
}

func flattenSymbols(symbols []lsp.DocumentSymbol, out []SymbolEntry) []SymbolEntry {
	for _, s := range symbols {
		out = append(out, SymbolEntry{
			Name:    s.Name,
			Kind:    symbolKindName(s.Kind),
			Line:    s.Range.Start.Line,
			EndLine: s.Range.End.Line,
		})
		if len(s.Children) > 0 {
			out = flattenSymbols(s.Children, out)
		}
	}
	return out
}

func symbolKindName(kind int) string {
	switch kind {
	case lsp.SymbolKindFile:
		return "file"
	case lsp.SymbolKindModule:
		return "module"
	case lsp.SymbolKindNamespace:
		return "namespace"
	case lsp.SymbolKindPackage:
		return "package"
	case lsp.SymbolKindClass:
		return "class"
	case lsp.SymbolKindMethod:
		return "method"
	case lsp.SymbolKindProperty:
		return "property"
	case lsp.SymbolKindField:
		return "field"
	case lsp.SymbolKindConstructor:
		return "constructor"
	case lsp.SymbolKindEnum:
		return "enum"
	case lsp.SymbolKindInterface:
		return "interface"
	case lsp.SymbolKindFunction:
		return "function"
	case lsp.SymbolKindVariable:
		return "variable"
	case lsp.SymbolKindConstant:
		return "constant"
	case lsp.SymbolKindStruct:
		return "struct"
	default:
		return fmt.Sprintf("kind(%d)", kind)
	}
}
