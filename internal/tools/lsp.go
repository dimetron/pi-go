package tools

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/lsp"
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
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
}

// LSPDiagnosticsOutput is the output of the lsp-diagnostics tool.
type LSPDiagnosticsOutput struct {
	File           string            `json:"file"`
	Diagnostics    []DiagnosticEntry `json:"diagnostics"`
	LSPDiagnostics string            `json:"lsp_diagnostics"` // styled string for display (compatible with formatToolResult)
	Error          string            `json:"error,omitempty"`
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

// LSPWorkspaceSymbolInput is input for workspace symbol search.
type LSPWorkspaceSymbolInput struct {
	Query string `json:"query"`
}

// LSPWorkspaceSymbolOutput is output for workspace symbol search.
type LSPWorkspaceSymbolOutput struct {
	Symbols []WorkspaceSymbolEntry `json:"symbols"`
	Error   string                 `json:"error,omitempty"`
}

// WorkspaceSymbolEntry is a single workspace symbol for tool output.
type WorkspaceSymbolEntry struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	Column        int    `json:"column"`
	ContainerName string `json:"containerName,omitempty"`
}

// LSPCodeActionInput is input for code action tool.
type LSPCodeActionInput struct {
	File      string `json:"file"`
	StartLine int    `json:"startLine"`
	StartCol  int    `json:"startCol"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
}

// LSPCodeActionOutput is output for code action tool.
type LSPCodeActionOutput struct {
	File    string        `json:"file"`
	Actions []ActionEntry `json:"actions"`
	Error   string        `json:"error,omitempty"`
}

// ActionEntry is a single code action for tool output.
type ActionEntry struct {
	Title       string `json:"title"`
	Kind        string `json:"kind,omitempty"`
	IsPreferred bool   `json:"isPreferred"`
}

// --- Tool constructors ---

func newLSPDiagnosticsTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-diagnostics",
		`Get LSP diagnostics (errors, warnings) for a file.

Returns compiler errors, type errors, and warnings from the language server.
Use this to check a file for errors without editing it, or to get more detail
than the automatic diagnostics provided after write/edit.`,
		func(ctx agent.Context, input LSPFileInput) (LSPDiagnosticsOutput, error) {
			return lspDiagnosticsHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPDefinitionTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-definition",
		`Go to definition of a symbol at a given position.

Returns the file and line where a function, type, variable, or other symbol
is defined. Line and column are 0-based.`,
		func(ctx agent.Context, input LSPPositionInput) (LSPLocationsOutput, error) {
			return lspDefinitionHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPReferencesTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-references",
		`Find all references to a symbol at a given position.

Returns all locations where the symbol at the given position is referenced,
including the declaration. Line and column are 0-based.`,
		func(ctx agent.Context, input LSPPositionInput) (LSPLocationsOutput, error) {
			return lspReferencesHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPHoverTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-hover",
		`Get type information and documentation for a symbol at a given position.

Returns the type signature and documentation for the symbol under the cursor.
Line and column are 0-based.`,
		func(ctx agent.Context, input LSPPositionInput) (LSPHoverOutput, error) {
			return lspHoverHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPSymbolsTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-symbols",
		`List all symbols (functions, types, variables) in a file.

Returns an overview of the file's structure including function definitions,
type declarations, constants, and variables with their line ranges.`,
		func(ctx agent.Context, input LSPFileInput) (LSPSymbolsOutput, error) {
			return lspSymbolsHandler(ctx, mgr, input)
		}, lspFileAliases)
}

func newLSPWorkspaceSymbolTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-workspace-symbol",
		`Search for symbols across the entire project workspace.

Returns symbols matching a query string from all source files,
including functions, types, constants, and variables across modules.
Useful for finding where a symbol is defined without knowing the file.`,
		func(ctx agent.Context, input LSPWorkspaceSymbolInput) (LSPWorkspaceSymbolOutput, error) {
			return lspWorkspaceSymbolHandler(ctx, mgr, input)
		}, nil)
}

func newLSPCodeActionTool(mgr *lsp.Manager) (tool.Tool, error) {
	return newTool("lsp-code-action",
		`Get available code actions (quick fixes, refactorings) for a selection.

Returns available code actions such as error fixes, imports, refactorings,
and other quick fixes. The selection is specified by start and end positions.
Line and column are 0-based.`,
		func(ctx agent.Context, input LSPCodeActionInput) (LSPCodeActionOutput, error) {
			return lspCodeActionHandler(ctx, mgr, input)
		}, lspFileAliases)
}

// LSPMode selects how much of the LSP surface is advertised to the model.
type LSPMode string

const (
	// LSPOff registers nothing.
	LSPOff LSPMode = "off"
	// LSPMin registers symbols + diagnostics only. This is the default.
	LSPMin LSPMode = "min"
	// LSPFull registers all seven tools.
	LSPFull LSPMode = "full"
)

// ParseLSPMode maps a flag value to a mode. It accepts the three mode names
// plus the truthy/falsey spellings a user is likely to reach for, so
// `--lsp true` and `--lsp full` mean the same thing. An unrecognized value
// returns LSPMin and false, letting the caller report it rather than guess.
func ParseLSPMode(v string) (LSPMode, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "min", "minimal", "default":
		return LSPMin, true
	case "full", "all", "1", "true", "yes", "on":
		return LSPFull, true
	case "off", "none", "0", "false", "no":
		return LSPOff, true
	default:
		return LSPMin, false
	}
}

// minimalLSPBuilders is the pair that carries the traffic. Measured over this
// repo's session history, lsp-symbols and lsp-diagnostics are 592 of 657 LSP
// calls — 90% — while the other five cost 989 tokens on every request for the
// remaining 65. The model reaches for ripgrep instead of lsp-references
// (8171 calls vs 3), so the wide surface is not paying for itself.
//
// Use LSPFull to get the rest back; see ParseLSPMode.
var minimalLSPBuilders = []func(*lsp.Manager) (tool.Tool, error){
	newLSPSymbolsTool,
	newLSPDiagnosticsTool,
}

var fullLSPBuilders = []func(*lsp.Manager) (tool.Tool, error){
	newLSPDiagnosticsTool,
	newLSPDefinitionTool,
	newLSPReferencesTool,
	newLSPHoverTool,
	newLSPSymbolsTool,
	newLSPWorkspaceSymbolTool,
	newLSPCodeActionTool,
}

// LSPTools returns the default (minimal) LSP tool set.
func LSPTools(mgr *lsp.Manager) ([]tool.Tool, error) {
	return LSPToolsFor(mgr, LSPMin)
}

// LSPToolsFor returns the LSP ADK tools for the given mode.
func LSPToolsFor(mgr *lsp.Manager, mode LSPMode) ([]tool.Tool, error) {
	var builders []func(*lsp.Manager) (tool.Tool, error)
	switch mode {
	case LSPOff:
		return nil, nil
	case LSPFull:
		builders = fullLSPBuilders
	default:
		builders = minimalLSPBuilders
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

func getServerOrSkipForLanguage(mgr *lsp.Manager, lang string) (*lsp.Server, string) {
	if !mgr.Available(lang) {
		return nil, fmt.Sprintf("%s not available", lang)
	}
	// Create a dummy file path for the language's file extension.
	exts := mgr.Languages()[lang].FileExtensions
	if len(exts) == 0 {
		return nil, fmt.Sprintf("no file extension for %s", lang)
	}
	// Use a temp file path just to get a server instance.
	tmpFile := "/tmp/dummy" + exts[0]
	srv, err := mgr.ServerFor(tmpFile)
	if err != nil {
		return nil, fmt.Sprintf("language server error: %v", err)
	}
	return srv, ""
}

func lspDiagnosticsHandler(_ agent.Context, mgr *lsp.Manager, input LSPFileInput) (LSPDiagnosticsOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPDiagnosticsOutput{File: input.File, Error: errMsg}, nil
	}

	// Trigger didOpen/didChange to prompt diagnostics push.
	_, _ = srv.Diagnostics(context.Background(), input.File)

	// Read cached diagnostics.
	uri := fileURI(input.File)
	cached := mgr.CachedDiagnostics(uri)

	return LSPDiagnosticsOutput{
		File:           input.File,
		Diagnostics:    convertDiagnostics(cached),
		LSPDiagnostics: formatDiagnosticsForDisplay(input.File, cached),
	}, nil
}

// formatDiagnosticsForDisplay formats diagnostics for human-readable display.
func formatDiagnosticsForDisplay(file string, diags []lsp.Diagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	var lines []string
	for _, d := range diags {
		if d.Severity > lsp.SeverityWarning {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s:%d:%d: %s: %s",
			filepath.Base(file),
			d.Range.Start.Line+1,
			d.Range.Start.Character+1,
			d.SeverityString(),
			d.Message,
		))
	}
	if len(lines) == 0 {
		return ""
	}
	return "⚠ " + strings.Join(lines, "\n⚠ ")
}

func lspDefinitionHandler(_ agent.Context, mgr *lsp.Manager, input LSPPositionInput) (LSPLocationsOutput, error) {
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

func lspReferencesHandler(_ agent.Context, mgr *lsp.Manager, input LSPPositionInput) (LSPLocationsOutput, error) {
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

func lspHoverHandler(_ agent.Context, mgr *lsp.Manager, input LSPPositionInput) (LSPHoverOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPHoverOutput{Error: errMsg}, nil
	}

	result, err := srv.Hover(context.Background(), input.File, input.Line, input.Column)
	if err != nil {
		return LSPHoverOutput{Error: err.Error()}, nil
	}

	return LSPHoverOutput{Content: extractHoverContent(result)}, nil
}

func lspSymbolsHandler(_ agent.Context, mgr *lsp.Manager, input LSPFileInput) (LSPSymbolsOutput, error) {
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

func lspWorkspaceSymbolHandler(_ agent.Context, mgr *lsp.Manager, input LSPWorkspaceSymbolInput) (LSPWorkspaceSymbolOutput, error) {
	// Get the first available server (workspace symbols don't require a specific file).
	var srv *lsp.Server
	var errMsg string
	for lang := range mgr.Languages() {
		srv, errMsg = getServerOrSkipForLanguage(mgr, lang)
		if errMsg == "" && srv != nil {
			break
		}
	}
	if srv == nil {
		return LSPWorkspaceSymbolOutput{Error: "no language server available"}, nil
	}

	symbols, err := srv.WorkspaceSymbols(context.Background(), input.Query)
	if err != nil {
		return LSPWorkspaceSymbolOutput{Error: err.Error()}, nil
	}

	entries := make([]WorkspaceSymbolEntry, 0, len(symbols))
	for _, sym := range symbols {
		file := uriToPath(sym.Location.URI)
		entries = append(entries, WorkspaceSymbolEntry{
			Name:          sym.Name,
			Kind:          symbolKindName(sym.Kind),
			File:          file,
			Line:          sym.Location.Range.Start.Line,
			Column:        sym.Location.Range.Start.Character,
			ContainerName: sym.ContainerName,
		})
	}

	return LSPWorkspaceSymbolOutput{Symbols: entries}, nil
}

func lspCodeActionHandler(_ agent.Context, mgr *lsp.Manager, input LSPCodeActionInput) (LSPCodeActionOutput, error) {
	srv, errMsg := getServerOrSkip(mgr, input.File)
	if errMsg != "" {
		return LSPCodeActionOutput{File: input.File, Error: errMsg}, nil
	}

	actions, err := srv.CodeActions(context.Background(), input.File, input.StartLine, input.StartCol, input.EndLine, input.EndCol)
	if err != nil {
		return LSPCodeActionOutput{File: input.File, Error: err.Error()}, nil
	}

	entries := make([]ActionEntry, 0, len(actions))
	for _, a := range actions {
		entries = append(entries, ActionEntry{
			Title:       a.Title,
			Kind:        a.Kind,
			IsPreferred: a.IsPreferred,
		})
	}

	return LSPCodeActionOutput{
		File:    input.File,
		Actions: entries,
	}, nil
}

// --- Helpers ---

// fileURI converts a file path to a file:// URI (matching lsp package convention).
func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Windows paths start with a drive letter, not a slash. Without the extra
	// leading slash url.URL emits "file://C:/x", where the drive letter is read
	// as the authority instead of part of the path.
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u := &url.URL{Scheme: "file", Path: p}
	return u.String()
}

// convertDiagnostics converts LSP diagnostics to tool output format.
func convertDiagnostics(diags []lsp.Diagnostic) []DiagnosticEntry {
	entries := make([]DiagnosticEntry, 0, len(diags))
	for _, d := range diags {
		entries = append(entries, DiagnosticEntry{
			Line:     d.Range.Start.Line,
			Column:   d.Range.Start.Character,
			Severity: d.SeverityString(),
			Message:  d.Message,
			Source:   d.Source,
		})
	}
	return entries
}

// extractHoverContent returns hover text or a default message if result is nil.
func extractHoverContent(result *lsp.HoverResult) string {
	if result == nil {
		return "no hover information available"
	}
	return result.Contents.Value
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
		p := u.Path
		// Undo the leading slash fileURI puts in front of a drive letter.
		if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
			p = p[1:]
		}
		return filepath.FromSlash(p)
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

// symbolKindNames maps the LSP SymbolKind numbers this package recognizes to
// their display names. The kinds are distinct integers, so the table is the
// same dispatch the switch it replaced performed.
var symbolKindNames = map[int]string{
	lsp.SymbolKindFile:        "file",
	lsp.SymbolKindModule:      "module",
	lsp.SymbolKindNamespace:   "namespace",
	lsp.SymbolKindPackage:     "package",
	lsp.SymbolKindClass:       "class",
	lsp.SymbolKindMethod:      "method",
	lsp.SymbolKindProperty:    "property",
	lsp.SymbolKindField:       "field",
	lsp.SymbolKindConstructor: "constructor",
	lsp.SymbolKindEnum:        "enum",
	lsp.SymbolKindInterface:   "interface",
	lsp.SymbolKindFunction:    "function",
	lsp.SymbolKindVariable:    "variable",
	lsp.SymbolKindConstant:    "constant",
	lsp.SymbolKindStruct:      "struct",
}

func symbolKindName(kind int) string {
	if name, ok := symbolKindNames[kind]; ok {
		return name
	}
	return fmt.Sprintf("kind(%d)", kind)
}
