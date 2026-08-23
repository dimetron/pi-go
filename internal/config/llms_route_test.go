package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/notice"
)

func TestIsLLMSDocsURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://adk.dev/llms.txt", true},
		{"https://adk.dev/llms-full.txt", true},
		{"https://example.com/docs/LLMS.TXT", true},
		{"  https://adk.dev/llms.txt  ", true},
		{"https://mcp.openrouter.ai/mcp", false},
		{"https://example.com/llms.txt.json", false},
		{"https://example.com/allms.txt", false},
		{"", false},
		{"://nonsense", false},
	}
	for _, tc := range tests {
		if got := IsLLMSDocsURL(tc.url); got != tc.want {
			t.Errorf("IsLLMSDocsURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestRegisterLLMSDocsSourcesRegistersDocsIndex(t *testing.T) {
	cfg := Config{MCP: &MCPConfig{Servers: []MCPServer{
		{Name: "adk-docs-mcp", URL: "https://adk.dev/llms.txt"},
		{Name: "openrouter", URL: "https://mcp.openrouter.ai/mcp"},
		{Name: "local", Command: "some-server"},
	}}}

	moved := registerLLMSDocsSources(&cfg)

	if len(moved) != 1 || moved[0] != "adk-docs-mcp" {
		t.Fatalf("moved = %v, want [adk-docs-mcp]", moved)
	}
	// The entry stays: a base name cannot prove the URL is not a real MCP
	// endpoint, and removing one on a guess would silently strip its tools.
	if len(cfg.MCP.Servers) != 3 {
		t.Fatalf("MCP servers = %d, want all 3 kept", len(cfg.MCP.Servers))
	}
	if cfg.LLMS == nil || len(cfg.LLMS.Sources) != 1 {
		t.Fatalf("LLMS sources = %+v, want one entry", cfg.LLMS)
	}
	src := cfg.LLMS.Sources[0]
	if src.Name != "adk-docs-mcp" || src.URL != "https://adk.dev/llms.txt" {
		t.Errorf("source = %+v, want the rerouted server name and URL", src)
	}
}

// The same index configured both ways must not register twice: fetch_docs
// would then list one source under two names.
func TestRegisterLLMSDocsSourcesSkipsDuplicateSource(t *testing.T) {
	cfg := Config{
		MCP:  &MCPConfig{Servers: []MCPServer{{Name: "adk-docs-mcp", URL: "https://adk.dev/llms.txt"}}},
		LLMS: &LLMSConfig{Sources: []LLMSSource{{Name: "AgentDevelopmentKit", URL: "https://adk.dev/llms.txt"}}},
	}

	moved := registerLLMSDocsSources(&cfg)

	if len(moved) != 1 {
		t.Fatalf("moved = %v, want the server reported as rerouted", moved)
	}
	if len(cfg.MCP.Servers) != 1 {
		t.Errorf("MCP servers = %d, want the entry kept", len(cfg.MCP.Servers))
	}
	if len(cfg.LLMS.Sources) != 1 {
		t.Errorf("LLMS sources = %d, want 1 (no duplicate)", len(cfg.LLMS.Sources))
	}
	if cfg.LLMS.Sources[0].Name != "AgentDevelopmentKit" {
		t.Errorf("existing source renamed to %q", cfg.LLMS.Sources[0].Name)
	}
}

func TestRegisterLLMSDocsSourcesLeavesRealServersAlone(t *testing.T) {
	cfg := Config{MCP: &MCPConfig{Servers: []MCPServer{
		{Name: "openrouter", URL: "https://mcp.openrouter.ai/mcp"},
	}}}

	if moved := registerLLMSDocsSources(&cfg); moved != nil {
		t.Errorf("moved = %v, want nil", moved)
	}
	if len(cfg.MCP.Servers) != 1 {
		t.Errorf("MCP servers = %d, want 1", len(cfg.MCP.Servers))
	}
	if cfg.LLMS != nil {
		t.Errorf("LLMS config created for a plain MCP server: %+v", cfg.LLMS)
	}
}

func TestRegisterLLMSDocsSourcesHandlesEmptyConfig(t *testing.T) {
	var cfg Config
	if moved := registerLLMSDocsSources(&cfg); moved != nil {
		t.Errorf("moved = %v, want nil for a config with no MCP section", moved)
	}
	cfg.MCP = &MCPConfig{}
	if moved := registerLLMSDocsSources(&cfg); moved != nil {
		t.Errorf("moved = %v, want nil for an empty server list", moved)
	}
}

// The notice must not be raised during load. In the TUI, config is read before
// any front end exists and the first frame is preceded by a full terminal
// reset (ESC c) that clears screen and scrollback, so a message written at
// load time is erased before it can be read.
func TestLoadDefersReroutedLLMSNotice(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	writeProjectMCPFile(t, dir, `{"mcpServers":{"adk-docs-mcp":{"url":"https://adk.dev/llms.txt"}}}`)

	var raised []string
	prev := notice.SetSink(func(msg string) { raised = append(raised, msg) })
	defer notice.SetSink(prev)

	cfg, err := LoadFrom(dir)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if len(raised) != 0 {
		t.Errorf("LoadFrom raised %v; the notice must wait for a front end", raised)
	}
	if len(cfg.ReroutedLLMS) != 1 || cfg.ReroutedLLMS[0] != "adk-docs-mcp" {
		t.Fatalf("ReroutedLLMS = %v, want [adk-docs-mcp]", cfg.ReroutedLLMS)
	}

	NotifyReroutedLLMS(cfg)
	if len(raised) != 1 {
		t.Fatalf("NotifyReroutedLLMS raised %v, want one notice", raised)
	}
	for _, want := range []string{`"adk-docs-mcp"`, "fetch_docs", "llms", "sources"} {
		if !strings.Contains(raised[0], want) {
			t.Errorf("notice %q does not mention %q", raised[0], want)
		}
	}
}

func TestNotifyReroutedLLMSSilentWhenNothingMoved(t *testing.T) {
	var raised []string
	prev := notice.SetSink(func(msg string) { raised = append(raised, msg) })
	defer notice.SetSink(prev)

	NotifyReroutedLLMS(Config{})
	if len(raised) != 0 {
		t.Errorf("raised %v for a config with nothing rerouted", raised)
	}
}

func writeProjectMCPFile(t *testing.T, dir, body string) {
	t.Helper()
	piDir := filepath.Join(dir, ".pi-go")
	if err := os.MkdirAll(piDir, 0o755); err != nil {
		t.Fatalf("creating %s: %v", piDir, err)
	}
	if err := os.WriteFile(filepath.Join(piDir, "mcp.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("writing mcp.json: %v", err)
	}
}

// The base name is a heuristic and rerouting is destructive, so an entry that
// declares configuration only a real MCP endpoint needs must be left alone.
func TestRegisterLLMSDocsSourcesKeepsConfiguredEndpoints(t *testing.T) {
	tests := []struct {
		name string
		srv  MCPServer
	}{
		{"custom headers", MCPServer{Name: "a", URL: "https://x.example/llms.txt", Headers: map[string]string{"Authorization": "Bearer k"}}},
		{"oauth", MCPServer{Name: "b", URL: "https://x.example/llms.txt", OAuth: true}},
		{"command wins over url", MCPServer{Name: "c", URL: "https://x.example/llms.txt", Command: "srv"}},
	}
	for _, tc := range tests {
		cfg := Config{MCP: &MCPConfig{Servers: []MCPServer{tc.srv}}}
		if moved := registerLLMSDocsSources(&cfg); moved != nil {
			t.Errorf("%s: rerouted %v, want the server kept", tc.name, moved)
		}
		if len(cfg.MCP.Servers) != 1 {
			t.Errorf("%s: server removed from the MCP list", tc.name)
		}
		if cfg.LLMS != nil {
			t.Errorf("%s: registered a docs source for a configured MCP endpoint", tc.name)
		}
	}
}

// IsLLMSDocsURL tolerates surrounding whitespace, so the registered source
// must be trimmed too: fetch_docs parses the stored value to check the host,
// and an untrimmed URL fails that parse and yields an unusable source.
func TestRegisterLLMSDocsSourcesTrimsURL(t *testing.T) {
	cfg := Config{MCP: &MCPConfig{Servers: []MCPServer{
		{Name: "adk-docs-mcp", URL: "  https://adk.dev/llms.txt\n"},
	}}}

	if moved := registerLLMSDocsSources(&cfg); len(moved) != 1 {
		t.Fatalf("moved = %v, want the whitespace-padded entry recognized", moved)
	}
	if cfg.LLMS == nil || len(cfg.LLMS.Sources) != 1 {
		t.Fatalf("LLMS = %+v, want one source", cfg.LLMS)
	}
	if got := cfg.LLMS.Sources[0].URL; got != "https://adk.dev/llms.txt" {
		t.Errorf("stored URL = %q, want it trimmed", got)
	}
}

// The same URL padded differently must not register twice.
func TestRegisterLLMSDocsSourcesDedupesAcrossWhitespace(t *testing.T) {
	cfg := Config{MCP: &MCPConfig{Servers: []MCPServer{
		{Name: "a", URL: "https://adk.dev/llms.txt"},
		{Name: "b", URL: " https://adk.dev/llms.txt "},
	}}}

	registerLLMSDocsSources(&cfg)
	if cfg.LLMS == nil || len(cfg.LLMS.Sources) != 1 {
		t.Errorf("LLMS sources = %+v, want one entry", cfg.LLMS)
	}
}
