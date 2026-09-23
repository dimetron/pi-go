package lsp

import (
	"encoding/json"
	"strings"
)

// --- JSON-RPC 2.0 types ---

// Request is a JSON-RPC 2.0 request message.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Notification is a JSON-RPC 2.0 notification (no ID, no response expected).
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC 2.0 response message.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

// ResponseError represents a JSON-RPC error object.
type ResponseError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ResponseError) Error() string { return e.Message }

// --- LSP protocol types ---

// Position in a text document (0-based line and character).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range inside a particular document.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// TextDocumentIdentifier identifies a text document.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// TextDocumentItem is used in didOpen notifications.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// VersionedTextDocumentIdentifier identifies a specific version of a text document.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// TextDocumentPositionParams identifies a position in a text document.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// DiagnosticSeverity constants.
const (
	SeverityError       = 1
	SeverityWarning     = 2
	SeverityInformation = 3
	SeverityHint        = 4
)

// Diagnostic represents an LSP diagnostic.
type Diagnostic struct {
	Range    Range           `json:"range"`
	Severity int             `json:"severity,omitempty"`
	Source   string          `json:"source,omitempty"`
	Message  string          `json:"message"`
	Code     json.RawMessage `json:"code,omitempty"`
}

// SeverityString returns a human-readable severity name.
func (d *Diagnostic) SeverityString() string {
	switch d.Severity {
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInformation:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "unknown"
	}
}

// SymbolKind constants (subset of commonly used kinds).
const (
	SymbolKindFile        = 1
	SymbolKindModule      = 2
	SymbolKindNamespace   = 3
	SymbolKindPackage     = 4
	SymbolKindClass       = 5
	SymbolKindMethod      = 6
	SymbolKindProperty    = 7
	SymbolKindField       = 8
	SymbolKindConstructor = 9
	SymbolKindEnum        = 10
	SymbolKindInterface   = 11
	SymbolKindFunction    = 12
	SymbolKindVariable    = 13
	SymbolKindConstant    = 14
	SymbolKindStruct      = 23
)

// DocumentSymbol represents a symbol in a document.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// TextEdit represents a change to a text document.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// MarkupContent represents human-readable content.
type MarkupContent struct {
	Kind  string `json:"kind"` // "plaintext" or "markdown"
	Value string `json:"value"`
}

// HoverResult is the result of a textDocument/hover request.
//
// Hover.contents is one of four shapes in the LSP spec: a plain string, a
// MarkedString object ({language, value}), a MarkupContent object
// ({kind, value}), or an array of any of those. Decoding straight into
// MarkupContent fails on three of the four — jdtls returns an array, so
// hover was broken against it — hence the custom unmarshaller below, which
// normalizes every shape into MarkupContent.
type HoverResult struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// UnmarshalJSON accepts any of the spec's hover-contents shapes and
// normalizes them into MarkupContent.
func (h *HoverResult) UnmarshalJSON(data []byte) error {
	var raw struct {
		Contents json.RawMessage `json:"contents"`
		Range    *Range          `json:"range"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	h.Range = raw.Range
	h.Contents = parseHoverContents(raw.Contents)
	return nil
}

// parseHoverContents normalizes a hover contents value into MarkupContent.
// Unrecognized shapes yield an empty value rather than an error: a missing
// hover is not a failure worth aborting the request over.
func parseHoverContents(raw json.RawMessage) MarkupContent {
	if len(raw) == 0 || string(raw) == "null" {
		return MarkupContent{}
	}

	// Array: concatenate each element.
	if raw[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil {
			return MarkupContent{}
		}
		parts := make([]string, 0, len(items))
		kind := ""
		for _, item := range items {
			mc := parseHoverContents(item)
			if mc.Value != "" {
				parts = append(parts, mc.Value)
			}
			if mc.Kind != "" {
				kind = mc.Kind
			}
		}
		return MarkupContent{Kind: kind, Value: strings.Join(parts, "\n\n")}
	}

	// Plain string.
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return MarkupContent{}
		}
		return MarkupContent{Kind: "plaintext", Value: s}
	}

	// Object: either MarkupContent ({kind, value}) or MarkedString
	// ({language, value}). Both carry the text in "value".
	var obj struct {
		Kind     string `json:"kind"`
		Language string `json:"language"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return MarkupContent{}
	}
	kind := obj.Kind
	if kind == "" && obj.Language != "" {
		kind = "plaintext"
	}
	return MarkupContent{Kind: kind, Value: obj.Value}
}

// --- Initialize request/response types ---

// InitializeParams for the initialize request.
type InitializeParams struct {
	ProcessID    int                `json:"processId"`
	RootURI      string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities declares client features.
type ClientCapabilities struct {
	TextDocument *TextDocumentClientCapabilities `json:"textDocument,omitempty"`
}

// TextDocumentClientCapabilities for text document features.
type TextDocumentClientCapabilities struct {
	Synchronization    *TextDocumentSyncClientCapabilities   `json:"synchronization,omitempty"`
	PublishDiagnostics *PublishDiagnosticsClientCapabilities `json:"publishDiagnostics,omitempty"`
}

// TextDocumentSyncClientCapabilities for synchronization.
type TextDocumentSyncClientCapabilities struct {
	DidSave bool `json:"didSave,omitempty"`
}

// PublishDiagnosticsClientCapabilities for diagnostics.
type PublishDiagnosticsClientCapabilities struct {
	RelatedInformation bool `json:"relatedInformation,omitempty"`
}

// InitializeResult from the server.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities advertises server features.
type ServerCapabilities struct {
	TextDocumentSync           json.RawMessage `json:"textDocumentSync,omitempty"`
	CompletionProvider         json.RawMessage `json:"completionProvider,omitempty"`
	HoverProvider              json.RawMessage `json:"hoverProvider,omitempty"`
	DefinitionProvider         json.RawMessage `json:"definitionProvider,omitempty"`
	ReferencesProvider         json.RawMessage `json:"referencesProvider,omitempty"`
	DocumentSymbolProvider     json.RawMessage `json:"documentSymbolProvider,omitempty"`
	DocumentFormattingProvider json.RawMessage `json:"documentFormattingProvider,omitempty"`
}

// --- Notification params ---

// DidOpenTextDocumentParams for textDocument/didOpen.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams for textDocument/didChange.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent is a full document sync change.
type TextDocumentContentChangeEvent struct {
	Text string `json:"text"`
}

// DidCloseTextDocumentParams for textDocument/didClose.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// PublishDiagnosticsParams from the server.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// DocumentFormattingParams for textDocument/formatting.
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

// FormattingOptions for formatting requests.
type FormattingOptions struct {
	TabSize      int  `json:"tabSize"`
	InsertSpaces bool `json:"insertSpaces"`
}

// DocumentSymbolParams for textDocument/documentSymbol.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ReferenceParams for textDocument/references.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// ReferenceContext controls reference search behavior.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// --- Workspace Symbol types ---

// WorkspaceSymbolParams for workspace/symbol.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// WorkspaceSymbol represents a symbol found via workspace/symbol.
type WorkspaceSymbol struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      Location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

// --- Code Action types ---

// CodeActionParams for textDocument/codeAction.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// CodeActionContext contains information about the code action request.
type CodeActionContext struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// CodeAction represents a code action that can be executed.
type CodeAction struct {
	Title       string         `json:"title"`
	Kind        string         `json:"kind,omitempty"`
	Edit        *WorkspaceEdit `json:"edit,omitempty"`
	Command     *Command       `json:"command,omitempty"`
	IsPreferred bool           `json:"isPreferred,omitempty"`
}

// WorkspaceEdit represents changes to be applied to a workspace.
type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes,omitempty"`
}

// Command represents a command to execute.
type Command struct {
	Command   string `json:"command"`
	Arguments []any  `json:"arguments,omitempty"`
}
