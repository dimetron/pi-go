package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Server wraps a Client with higher-level LSP operations.
type Server struct {
	client   *Client
	language string
	rootURI  string
	opened   map[string]int // uri → version
	mu       sync.Mutex
}

// Diagnostics requests diagnostics for a file.
// Note: most servers push diagnostics via notifications; this triggers
// didOpen/didChange to prompt a refresh and returns empty.
// Callers should use the Manager's diagnostic cache instead.
func (s *Server) Diagnostics(ctx context.Context, file string) ([]Diagnostic, error) {
	if err := s.ensureOpen(file); err != nil {
		return nil, err
	}
	// Diagnostics are pushed asynchronously by servers via publishDiagnostics.
	// Return empty here; the Manager caches them.
	return nil, nil
}

// Definition sends textDocument/definition and returns locations.
func (s *Server) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	if err := s.ensureOpen(file); err != nil {
		return nil, err
	}
	uri := fileURI(file)
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: col},
	}
	result, err := s.client.Request(ctx, "textDocument/definition", params)
	if err != nil {
		return nil, err
	}
	return parseLocations(result)
}

// References sends textDocument/references and returns locations.
func (s *Server) References(ctx context.Context, file string, line, col int) ([]Location, error) {
	if err := s.ensureOpen(file); err != nil {
		return nil, err
	}
	uri := fileURI(file)
	params := ReferenceParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: col},
		Context:      ReferenceContext{IncludeDeclaration: true},
	}
	result, err := s.client.Request(ctx, "textDocument/references", params)
	if err != nil {
		return nil, err
	}
	return parseLocations(result)
}

// Hover sends textDocument/hover and returns the result.
func (s *Server) Hover(ctx context.Context, file string, line, col int) (*HoverResult, error) {
	if err := s.ensureOpen(file); err != nil {
		return nil, err
	}
	uri := fileURI(file)
	params := TextDocumentPositionParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: col},
	}
	result, err := s.client.Request(ctx, "textDocument/hover", params)
	if err != nil {
		return nil, err
	}
	if string(result) == "null" || len(result) == 0 {
		return nil, nil
	}
	var hover HoverResult
	if err := json.Unmarshal(result, &hover); err != nil {
		return nil, fmt.Errorf("parsing hover result: %w", err)
	}
	return &hover, nil
}

// Symbols sends textDocument/documentSymbol and returns symbols.
func (s *Server) Symbols(ctx context.Context, file string) ([]DocumentSymbol, error) {
	if err := s.ensureOpen(file); err != nil {
		return nil, err
	}
	uri := fileURI(file)
	params := DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
	}
	result, err := s.client.Request(ctx, "textDocument/documentSymbol", params)
	if err != nil {
		return nil, err
	}
	var symbols []DocumentSymbol
	if err := json.Unmarshal(result, &symbols); err != nil {
		return nil, fmt.Errorf("parsing symbols: %w", err)
	}
	return symbols, nil
}

// Format sends textDocument/formatting and returns text edits.
func (s *Server) Format(ctx context.Context, file string) ([]TextEdit, error) {
	if err := s.ensureOpen(file); err != nil {
		return nil, err
	}
	uri := fileURI(file)
	params := DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Options:      FormattingOptions{TabSize: 4, InsertSpaces: false},
	}
	result, err := s.client.Request(ctx, "textDocument/formatting", params)
	if err != nil {
		return nil, err
	}
	if string(result) == "null" || len(result) == 0 {
		return nil, nil
	}
	var edits []TextEdit
	if err := json.Unmarshal(result, &edits); err != nil {
		return nil, fmt.Errorf("parsing formatting edits: %w", err)
	}
	return edits, nil
}

// NotifyChange sends a didChange notification for an already-open file.
func (s *Server) NotifyChange(file string, content string) error {
	uri := fileURI(file)
	s.mu.Lock()
	ver := s.opened[uri] + 1
	s.opened[uri] = ver
	s.mu.Unlock()

	return s.client.Notify("textDocument/didChange", DidChangeTextDocumentParams{
		TextDocument:   VersionedTextDocumentIdentifier{URI: uri, Version: ver},
		ContentChanges: []TextDocumentContentChangeEvent{{Text: content}},
	})
}

// Close sends didClose for all open files.
func (s *Server) Close() error {
	s.mu.Lock()
	uris := make([]string, 0, len(s.opened))
	for uri := range s.opened {
		uris = append(uris, uri)
	}
	s.mu.Unlock()

	for _, uri := range uris {
		_ = s.client.Notify("textDocument/didClose", DidCloseTextDocumentParams{
			TextDocument: TextDocumentIdentifier{URI: uri},
		})
	}
	return s.client.Close()
}

// ensureOpen sends didOpen for a file if not already opened.
func (s *Server) ensureOpen(file string) error {
	uri := fileURI(file)
	s.mu.Lock()
	if _, ok := s.opened[uri]; ok {
		s.mu.Unlock()
		return nil
	}
	s.opened[uri] = 1
	s.mu.Unlock()

	content, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("reading file for didOpen: %w", err)
	}

	return s.client.Notify("textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        uri,
			LanguageID: s.language,
			Version:    1,
			Text:       string(content),
		},
	})
}

// --- Manager ---

// ManagerConfig holds optional overrides for language server configurations.
type ManagerConfig struct {
	Languages map[string]*LanguageConfig `json:"languages,omitempty"`
	Disabled  []string                   `json:"disabled,omitempty"`
}

// Manager manages LSP server lifecycle — starting servers on demand,
// caching running servers, and shutting them down.
type Manager struct {
	languages   map[string]*LanguageConfig
	servers     map[string]*Server      // language+root → server
	diagnostics map[string][]Diagnostic // uri → latest diagnostics
	available   map[string]bool         // language → binary found
	mu          sync.Mutex
}

// NewManager creates a Manager with the given config merged over defaults.
// It checks which language servers are installed and logs warnings for missing ones.
func NewManager(cfg *ManagerConfig) *Manager {
	langs := DefaultLanguages()

	// Apply user overrides.
	if cfg != nil {
		maps.Copy(langs, cfg.Languages)
		for _, name := range cfg.Disabled {
			delete(langs, name)
		}
	}

	// Check which servers are installed.
	available := make(map[string]bool, len(langs))
	for name, lcfg := range langs {
		_, err := exec.LookPath(lcfg.Command)
		available[name] = err == nil
		// silently skip unavailable servers
	}

	return &Manager{
		languages:   langs,
		servers:     make(map[string]*Server),
		diagnostics: make(map[string][]Diagnostic),
		available:   available,
	}
}

// ServerFor returns a Server for the given file, starting one if needed.
// Returns (nil, nil) if no language server is configured or installed for the file type.
func (m *Manager) ServerFor(filePath string) (*Server, error) {
	lang := DetectLanguage(filePath, m.languages)
	if lang == "" {
		return nil, nil
	}

	if !m.available[lang] {
		return nil, nil
	}

	lcfg := m.languages[lang]

	// Find project root.
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath
	}
	root := FindRoot(absPath, lcfg.RootMarkers)

	key := lang + ":" + root

	m.mu.Lock()
	if srv, ok := m.servers[key]; ok {
		m.mu.Unlock()
		return srv, nil
	}
	m.mu.Unlock()

	// Start new server outside lock (starting a process can be slow).
	srv, err := m.startServer(lang, lcfg, root)
	if err != nil {
		return nil, fmt.Errorf("starting %s server: %w", lang, err)
	}

	m.mu.Lock()
	// Double-check in case another goroutine started one concurrently.
	if existing, ok := m.servers[key]; ok {
		m.mu.Unlock()
		_ = srv.Close()
		return existing, nil
	}
	m.servers[key] = srv
	m.mu.Unlock()

	return srv, nil
}

// CachedDiagnostics returns the latest diagnostics for a file URI.
func (m *Manager) CachedDiagnostics(fileURI string) []Diagnostic {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.diagnostics[fileURI]
}

// Languages returns the configured language map (for inspection/testing).
func (m *Manager) Languages() map[string]*LanguageConfig {
	return m.languages
}

// Available returns whether a language server binary was found.
func (m *Manager) Available(lang string) bool {
	return m.available[lang]
}

// Shutdown gracefully shuts down all running servers.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	servers := make(map[string]*Server, len(m.servers))
	maps.Copy(servers, m.servers)
	m.servers = make(map[string]*Server)
	m.mu.Unlock()

	for _, srv := range servers {
		_ = srv.Close()
	}
}

// startServer launches an LSP server process and completes the initialize handshake.
func (m *Manager) startServer(_ string, lcfg *LanguageConfig, root string) (*Server, error) {
	client, err := NewClient(lcfg.Command, lcfg.Args...)
	if err != nil {
		return nil, err
	}

	rootURI := pathToURI(root)

	// Send initialize request.
	initParams := InitializeParams{
		ProcessID: os.Getpid(),
		RootURI:   rootURI,
		Capabilities: ClientCapabilities{
			TextDocument: &TextDocumentClientCapabilities{
				Synchronization: &TextDocumentSyncClientCapabilities{DidSave: true},
				PublishDiagnostics: &PublishDiagnosticsClientCapabilities{
					RelatedInformation: true,
				},
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), DefaultRequestTimeout)
	defer cancel()

	result, err := client.Request(ctx, "initialize", initParams)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	// Parse server capabilities (for potential future use).
	var initResult InitializeResult
	_ = json.Unmarshal(result, &initResult)

	// Send initialized notification.
	if err := client.Notify("initialized", struct{}{}); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialized notification: %w", err)
	}

	srv := &Server{
		client:   client,
		language: lcfg.LanguageID,
		rootURI:  rootURI,
		opened:   make(map[string]int),
	}

	// Set up diagnostic caching from server notifications.
	client.NotificationHandler = func(method string, params json.RawMessage) {
		if method == "textDocument/publishDiagnostics" {
			var dp PublishDiagnosticsParams
			if err := json.Unmarshal(params, &dp); err == nil {
				m.mu.Lock()
				m.diagnostics[dp.URI] = dp.Diagnostics
				m.mu.Unlock()
			}
		}
	}

	return srv, nil
}

// --- Helpers ---

// fileURI converts a file path to a file:// URI.
func fileURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return pathToURI(abs)
}

// pathToURI converts an absolute path to a file:// URI.
func pathToURI(path string) string {
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	return u.String()
}

// parseLocations parses a JSON result that may be a single Location,
// an array of Location, or null.
func parseLocations(result json.RawMessage) ([]Location, error) {
	if string(result) == "null" || len(result) == 0 {
		return nil, nil
	}

	// Try as array first.
	var locs []Location
	if err := json.Unmarshal(result, &locs); err == nil {
		return locs, nil
	}

	// Try as single location.
	var loc Location
	if err := json.Unmarshal(result, &loc); err != nil {
		return nil, fmt.Errorf("parsing locations: %w", err)
	}
	return []Location{loc}, nil
}
