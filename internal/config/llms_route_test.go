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

func TestRouteLLMSDocsServersMovesDocsIndexToSources(t *testing.T) {
	cfg := Config{MCP: &MCPConfig{Servers: []MCPServer{
		{Name: "adk-docs-mcp", URL: "https://adk.dev/llms.txt"},
		{Name: "openrouter", URL: "https://mcp.openrouter.ai/mcp"},
		{Name: "local", Command: "some-server"},
	}}}

	moved := routeLLMSDocsServers(&cfg)

	if len(moved) != 1 || moved[0] != "adk-docs-mcp" {
		t.Fatalf("moved = %v, want [adk-docs-mcp]", moved)
	}
	if len(cfg.MCP.Servers) != 2 {
		t.Fatalf("MCP servers = %d, want 2 (docs index removed)", len(cfg.MCP.Servers))
	}
	for _, srv := range cfg.MCP.Servers {
		if srv.Name == "adk-docs-mcp" {
			t.Error("docs index left in the MCP server list; it would 405 on every connect")
		}
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
func TestRouteLLMSDocsServersSkipsDuplicateSource(t *testing.T) {
	cfg := Config{
		MCP:  &MCPConfig{Servers: []MCPServer{{Name: "adk-docs-mcp", URL: "https://adk.dev/llms.txt"}}},
		LLMS: &LLMSConfig{Sources: []LLMSSource{{Name: "AgentDevelopmentKit", URL: "https://adk.dev/llms.txt"}}},
	}

	moved := routeLLMSDocsServers(&cfg)

	if len(moved) != 1 {
		t.Fatalf("moved = %v, want the server reported as rerouted", moved)
	}
	if len(cfg.MCP.Servers) != 0 {
		t.Errorf("MCP servers = %d, want 0", len(cfg.MCP.Servers))
	}
	if len(cfg.LLMS.Sources) != 1 {
		t.Errorf("LLMS sources = %d, want 1 (no duplicate)", len(cfg.LLMS.Sources))
	}
	if cfg.LLMS.Sources[0].Name != "AgentDevelopmentKit" {
		t.Errorf("existing source renamed to %q", cfg.LLMS.Sources[0].Name)
	}
}

func TestRouteLLMSDocsServersLeavesRealServersAlone(t *testing.T) {
	cfg := Config{MCP: &MCPConfig{Servers: []MCPServer{
		{Name: "openrouter", URL: "https://mcp.openrouter.ai/mcp"},
	}}}

	if moved := routeLLMSDocsServers(&cfg); moved != nil {
		t.Errorf("moved = %v, want nil", moved)
	}
	if len(cfg.MCP.Servers) != 1 {
		t.Errorf("MCP servers = %d, want 1", len(cfg.MCP.Servers))
	}
	if cfg.LLMS != nil {
		t.Errorf("LLMS config created for a plain MCP server: %+v", cfg.LLMS)
	}
}

func TestRouteLLMSDocsServersHandlesEmptyConfig(t *testing.T) {
	var cfg Config
	if moved := routeLLMSDocsServers(&cfg); moved != nil {
		t.Errorf("moved = %v, want nil for a config with no MCP section", moved)
	}
	cfg.MCP = &MCPConfig{}
	if moved := routeLLMSDocsServers(&cfg); moved != nil {
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
