package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// HookConfig defines a shell command hook for tool call events.
type HookConfig struct {
	Event   string   `json:"event"`
	Command string   `json:"command"`
	Tools   []string `json:"tools,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
}

// RoleConfig maps a role to a specific model and optional provider override.
type RoleConfig struct {
	Model          string `json:"model"`
	Provider       string `json:"provider,omitempty"`
	AdvisorModel   string `json:"advisorModel,omitempty"`   // Advisor model for advisor tool (e.g., "claude-opus-4-7")
	AdvisorMaxUses int    `json:"advisorMaxUses,omitempty"` // Max advisor calls per request (0 = unlimited)
	AdvisorCaching bool   `json:"advisorCaching,omitempty"` // Enable advisor prompt caching
}

// ErrNoDefaultRole is returned when no default role is configured.
var ErrNoDefaultRole = errors.New("no default model role configured")

// MemoryConfig holds settings for the persistent memory system.
type MemoryConfig struct {
	Enabled          *bool    `json:"enabled,omitempty"`
	DBPath           string   `json:"db_path,omitempty"`
	TokenBudget      int      `json:"token_budget,omitempty"`
	CompressionRole  string   `json:"compression_model_role,omitempty"`
	MaxPending       int      `json:"max_pending_observations,omitempty"`
	LookbackHours    int      `json:"context_lookback_hours,omitempty"`
	ExcludedTools    []string `json:"excluded_tools,omitempty"`
	ExcludedProjects []string `json:"excluded_projects,omitempty"`
}

// MemoryDefaults returns a MemoryConfig with default values.
func MemoryDefaults() MemoryConfig {
	return MemoryConfig{
		TokenBudget:     8000,
		CompressionRole: "smol",
		MaxPending:      100,
		LookbackHours:   72,
	}
}

// Config holds all pi-go configuration.
type Config struct {
	Roles           map[string]RoleConfig `json:"roles,omitempty"`
	DefaultModel    string                `json:"defaultModel,omitempty"` // deprecated: use roles
	DefaultProvider string                `json:"defaultProvider"`
	ThinkingLevel   string                `json:"thinkingLevel"`
	Theme           string                `json:"theme"`
	ExtraHeaders    map[string]string     `json:"extraHeaders,omitempty"`
	InsecureSkipTLS bool                  `json:"insecureSkipTLS,omitempty"`
	Tools           map[string]any        `json:"tools,omitempty"`
	MCP             *MCPConfig            `json:"mcp,omitempty"`
	Hooks           []HookConfig          `json:"hooks,omitempty"`
	MaxDailyTokens  int64                 `json:"maxDailyTokens,omitempty"` // 0 = unlimited
	Compactor       *CompactorConfig      `json:"compactor,omitempty"`
	Memory          *MemoryConfig         `json:"memory,omitempty"`
	Palace          *PalaceConfig         `json:"palace,omitempty"`
	A2A             *A2AConfig            `json:"a2a,omitempty"`
}

// PalaceConfig holds settings for the MemPalace memory system.
type PalaceConfig struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	DBPath    string `json:"db_path,omitempty"`
	ModelPath string `json:"model_path,omitempty"`
}

// CompactorConfig holds user-overridable compaction settings.
// When nil in config, defaults are applied by the tools package.
type CompactorConfig struct {
	Enabled             *bool  `json:"enabled,omitempty"`
	SourceCodeFiltering string `json:"source_code_filtering,omitempty"` // "none", "minimal", "aggressive"
	MaxChars            int    `json:"max_chars,omitempty"`
	MaxLines            int    `json:"max_lines,omitempty"`
}

type MCPConfig struct {
	Servers []MCPServer `json:"servers"`
}

type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"` // HTTP transport (e.g., cloudflare-api)
}

// A2AAgentConfig defines a single A2A-capable agent endpoint.
type A2AAgentConfig struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// A2AConfig holds configuration for A2A agent connections.
type A2AConfig struct {
	Agents []A2AAgentConfig `json:"agents,omitempty"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "gpt-5.4"},
		},
		DefaultProvider: "openai",
		ThinkingLevel:   "medium",
		Theme:           "default",
	}
}

// Known model prefixes for auto-detecting provider.
var modelPrefixes = map[string]string{
	"claude": "anthropic",
	"gpt":    "openai",
	"gpt-5":  "openai",
	"gemini": "gemini",
}

// ResolveRole returns the model name, provider, and advisor settings for a given role.
// Falls back: requested role → "default" role → error.
func (c *Config) ResolveRole(role string) (model string, prov string, advisorModel string, advisorMaxUses int, advisorCaching bool, err error) {
	if len(c.Roles) == 0 {
		return "", "", "", 0, false, ErrNoDefaultRole
	}

	rc, ok := c.Roles[role]
	if !ok {
		rc, ok = c.Roles["default"]
		if !ok {
			return "", "", "", 0, false, ErrNoDefaultRole
		}
	}

	if rc.Model == "" {
		return "", "", "", 0, false, fmt.Errorf("role %q has no model configured", role)
	}

	prov = rc.Provider
	if prov == "" {
		prov = autoDetectProvider(rc.Model)
		if prov == "" {
			prov = c.DefaultProvider
		}
	}

	return rc.Model, prov, rc.AdvisorModel, rc.AdvisorMaxUses, rc.AdvisorCaching, nil
}

// autoDetectProvider detects the provider from model name prefix.
func autoDetectProvider(modelName string) string {
	// azure/ prefix → Azure OpenAI provider.
	if strings.HasPrefix(strings.ToLower(modelName), "azure/") {
		return "azure"
	}
	// Ollama suffixes → native Ollama provider.
	if strings.HasSuffix(modelName, ":cloud") || strings.HasSuffix(modelName, ":local") {
		return "ollama"
	}
	lower := strings.ToLower(modelName)
	for prefix, provider := range modelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return provider
		}
	}
	// Common Ollama model prefixes → native Ollama provider.
	ollamaPrefixes := []string{"qwen", "minimax", "deepseek", "llama", "mistral", "phi", "codellama", "gemma"}
	for _, prefix := range ollamaPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return "ollama"
		}
	}
	// ollama/ prefix → native Ollama provider.
	if strings.HasPrefix(lower, "ollama/") {
		return "ollama"
	}
	return ""
}

// Load reads config from global (~/.pi-go/config.json) and project (.pi-go/config.json),
// merging project overrides onto global. MCP servers are also loaded from
// separate .pi-go/mcp.json files if present.
func Load() (Config, error) {
	cfg := Defaults()

	home, err := os.UserHomeDir()
	if err == nil {
		globalPath := filepath.Join(home, ".pi-go", "config.json")
		if err := loadFile(globalPath, &cfg); err != nil && !os.IsNotExist(err) {
			return cfg, err
		}
	}

	projectPath := filepath.Join(".pi-go", "config.json")
	if err := loadFile(projectPath, &cfg); err != nil && !os.IsNotExist(err) {
		return cfg, err
	}

	// Load MCP config from separate mcp.json files if present.
	mcpServers := LoadMCPServers()
	if len(mcpServers) > 0 {
		// Merge: mcp.json servers take precedence if none in config.json.
		if cfg.MCP == nil || len(cfg.MCP.Servers) == 0 {
			cfg.MCP = &MCPConfig{Servers: mcpServers}
		}
	}

	// Migrate deprecated DefaultModel to roles if roles not set.
	if cfg.DefaultModel != "" && len(cfg.Roles) == 0 {
		cfg.Roles = map[string]RoleConfig{
			"default": {Model: cfg.DefaultModel},
		}
	} else if cfg.DefaultModel != "" && cfg.Roles != nil {
		// If DefaultModel is set alongside roles, update the default role if not already set.
		if _, ok := cfg.Roles["default"]; !ok {
			cfg.Roles["default"] = RoleConfig{Model: cfg.DefaultModel}
		}
	}

	return cfg, nil
}

// mcpServerFile represents the JSON structure of a standalone mcp.json file.
// MCP JSON Schema v1 (Claude Desktop / NPM compatible) — mcpServers as object.
// Also supports the legacy array format used in config.json.
type mcpServerFile struct {
	MCPServers any `json:"mcpServers"` // object (Claude Desktop) or []MCPServer (legacy)
}

// LoadMCPServers reads MCP server configurations from standalone mcp.json files.
// Resolution order: project .pi-go/mcp.json overrides global ~/.pi-go/mcp.json.
// Supports both the Claude Desktop object format (servers keyed by name) and
// the legacy array format.
func LoadMCPServers() []MCPServer {
	var servers []MCPServer

	// Try global path first.
	if home, err := os.UserHomeDir(); err == nil {
		globalPath := filepath.Join(home, ".pi-go", "mcp.json")
		servers = loadMCPServersFromFile(globalPath)
	}

	// Project path overrides global (only if project file exists).
	projectPath := filepath.Join(".pi-go", "mcp.json")
	if projectServers := loadMCPServersFromFile(projectPath); projectServers != nil {
		servers = projectServers
	}

	return servers
}

// loadMCPServersFromFile reads MCP servers from a single mcp.json file.
// Returns nil if the file does not exist. Supports both object format
// (Claude Desktop) and array format.
func loadMCPServersFromFile(path string) []MCPServer {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nil
	}
	var f mcpServerFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil
	}
	servers := parseMCPServers(f.MCPServers)
	return substituteEnvVars(servers)
}

// SubstituteEnvVars replaces ${VAR} patterns in server URLs with values from
// ~/.pi-go/.env (or project .pi-go/.env). Secrets stay in the file and are
// never exposed in logs or TUI.
func substituteEnvVars(servers []MCPServer) []MCPServer {
	// Load env from ~/.pi-go/.env
	env := loadEnvFile()
	for i := range servers {
		if servers[i].URL != "" {
			servers[i].URL = substituteEnv(env, servers[i].URL)
		}
	}
	return servers
}

// loadEnvFile reads key=value pairs from ~/.pi-go/.env and project
// .pi-go/.env, with project values overriding global ones.
func loadEnvFile() map[string]string {
	result := make(map[string]string)
	if home, err := os.UserHomeDir(); err == nil {
		mergeEnvFile(result, filepath.Join(home, ".pi-go", ".env"))
	}
	mergeEnvFile(result, filepath.Join(".pi-go", ".env"))
	return result
}

// mergeEnvFile parses a single .env file and writes entries into dst.
// Missing files are silently ignored.
func mergeEnvFile(dst map[string]string, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		dst[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
}

// substituteEnv replaces ${VAR} patterns in s with values from env map.
func substituteEnv(env map[string]string, s string) string {
	return os.Expand(s, func(key string) string {
		if v, ok := env[key]; ok {
			return v
		}
		return os.Getenv(key) // fallback to real env
	})
}

// parseMCPServers handles both Claude Desktop object format and legacy array format.
func parseMCPServers(v any) []MCPServer {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		// Claude Desktop object format: mcpServers is a map keyed by server name.
		// Each value is { "command": "...", "args": [...] } or { "url": "..." }.
		servers := make([]MCPServer, 0, len(x))
		for name, val := range x {
			srv := MCPServer{Name: name}
			if m, ok := val.(map[string]any); ok {
				if cmd, ok := m["command"].(string); ok {
					srv.Command = cmd
				}
				if args, ok := m["args"].([]any); ok {
					srv.Args = toStringSlice(args)
				}
				if url, ok := m["url"].(string); ok {
					srv.URL = url
				}
			}
			// Skip servers that have neither command nor URL (e.g., disabled servers).
			if srv.Command == "" && srv.URL == "" {
				continue
			}
			servers = append(servers, srv)
		}
		return servers
	case []any:
		// Legacy array format.
		servers := make([]MCPServer, 0, len(x))
		for _, item := range x {
			if m, ok := item.(map[string]any); ok {
				srv := MCPServer{}
				if n, ok := m["name"].(string); ok {
					srv.Name = n
				}
				if cmd, ok := m["command"].(string); ok {
					srv.Command = cmd
				}
				if args, ok := m["args"].([]any); ok {
					srv.Args = toStringSlice(args)
				}
				if url, ok := m["url"].(string); ok {
					srv.URL = url
				}
				// Skip servers that have neither command nor URL.
				if srv.Command == "" && srv.URL == "" {
					continue
				}
				servers = append(servers, srv)
			}
		}
		return servers
	default:
		return nil
	}
}

// toStringSlice converts []any to []string.
func toStringSlice(v []any) []string {
	if v == nil {
		return nil
	}
	s := make([]string, len(v))
	for i, x := range v {
		if str, ok := x.(string); ok {
			s[i] = str
		}
	}
	return s
}

func loadFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, cfg)
}

// APIKeys returns detected API keys from environment variables.
// For Anthropic, checks ANTHROPIC_API_KEY and ANTHROPIC_AUTH_TOKEN (Ollama compatibility).
func APIKeys() map[string]string {
	keys := make(map[string]string)
	envVars := map[string][]string{
		"anthropic": {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
		"openai":    {"OPENAI_API_KEY"},
		"azure":     {"AZURE_OPENAI_API_KEY", "AZUREOPENAI_API_KEY", "AZURE_API_KEY"},
		"gemini":    {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		"mistral":   {"MISTRAL_API_KEY"},
	}
	for provider, vars := range envVars {
		for _, envVar := range vars {
			if val := os.Getenv(envVar); val != "" {
				keys[provider] = val
				break
			}
		}
	}
	return keys
}

// BaseURLs returns provider base URLs from environment variables.
// Supports ANTHROPIC_BASE_URL (Ollama compatibility), OPENAI_BASE_URL, and GEMINI_BASE_URL.
func BaseURLs() map[string]string {
	urls := make(map[string]string)
	envVars := map[string]string{
		"anthropic": "ANTHROPIC_BASE_URL",
		"openai":    "OPENAI_BASE_URL",
		"gemini":    "GEMINI_BASE_URL",
		"mistral":   "MISTRAL_BASE_URL",
	}
	for provider, envVar := range envVars {
		if val := os.Getenv(envVar); val != "" {
			urls[provider] = val
		}
	}
	return urls
}
