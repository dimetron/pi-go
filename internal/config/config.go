package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/notice"
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
	BaseURLs        map[string]string     `json:"baseURLs,omitempty"` // provider → base URL; overridden by env
	ExtraHeaders    map[string]string     `json:"extraHeaders,omitempty"`
	InsecureSkipTLS bool                  `json:"insecureSkipTLS,omitempty"`
	// MaxOutputTokens caps a reply on the OpenAI-compatible provider paths,
	// in tokens. Zero uses the provider default (64000), which is the output
	// ceiling of the current Claude and GPT models. Lower it for a backend
	// whose models stop below that and reject the request rather than
	// clamping it. This is an output cap, not a context window.
	MaxOutputTokens int64 `json:"maxOutputTokens,omitempty"`
	// CACertPath is a PEM bundle trusted in addition to the system roots, for
	// TLS-intercepting corporate proxies. Prefer it over insecureSkipTLS,
	// which turns verification off for every endpoint.
	CACertPath string `json:"caCertPath,omitempty"`
	// DisableSystemCAs narrows trust to caCertPath alone.
	DisableSystemCAs bool `json:"disableSystemCAs,omitempty"`
	// TraceHTTP logs every LLM request and response in full — headers and
	// bodies — to the session log and to OTel span events. Equivalent to
	// --trace-http, which can enable it but never turn it off.
	//
	// The resulting log contains the entire conversation, system prompt and
	// tool output in cleartext. Credentials are masked; nothing else is.
	TraceHTTP      bool           `json:"traceHTTP,omitempty"`
	Tools          map[string]any `json:"tools,omitempty"`
	MCP            *MCPConfig     `json:"mcp,omitempty"`
	Hooks          []HookConfig   `json:"hooks,omitempty"`
	MaxDailyTokens int64          `json:"maxDailyTokens,omitempty"` // 0 = unlimited
	// ContextWindow overrides the model's context window in tokens. Needed for
	// models absent from the embedded catalog (notably the opencode ones):
	// auto-compaction measures a percentage of the window, so it stays off
	// rather than guess at an unknown budget.
	ContextWindow int64              `json:"contextWindow,omitempty"`
	Compactor     *CompactorConfig   `json:"compactor,omitempty"`
	AutoCompact   *AutoCompactConfig `json:"autoCompact,omitempty"`
	Memory        *MemoryConfig      `json:"memory,omitempty"`
	Palace        *PalaceConfig      `json:"palace,omitempty"`
	A2A           *A2AConfig         `json:"a2a,omitempty"`
	LLMS          *LLMSConfig        `json:"llms,omitempty"`
	// ReroutedLLMS names the MCP servers whose URL is an llms.txt index and
	// which were therefore given a fetch_docs source during load.
	ReroutedLLMS []string `json:"-"`
	// InferredLLMS holds those generated sources. They are deliberately kept
	// out of LLMS, which is serialized: Save marshals the whole config, so an
	// inference drawn from a project's mcp.json would otherwise be written
	// into the global config file by an unrelated operation such as
	// SaveDefaultRole. Read both together with LLMSSources.
	InferredLLMS []LLMSSource `json:"-"`
}

// PalaceConfig holds settings for the MemPalace memory system.
type PalaceConfig struct {
	Enabled   *bool  `json:"enabled,omitempty"`
	DBPath    string `json:"db_path,omitempty"`
	ModelPath string `json:"model_path,omitempty"`

	// OllamaURL overrides the embedding daemon address. Empty uses the palace
	// default (http://localhost:11434).
	OllamaURL string `json:"ollama_url,omitempty"`
	// OllamaModel overrides the embedding model. Empty uses the palace default.
	// Changing it invalidates every stored vector — embeddings from different
	// models are not comparable — so a change means re-running `pi memory mine`.
	OllamaModel string `json:"ollama_model,omitempty"`
	// LocalEmbedder forces the slower in-process model instead of Ollama.
	LocalEmbedder bool `json:"local_embedder,omitempty"`
}

// CompactorConfig holds user-overridable compaction settings.
// When nil in config, defaults are applied by the tools package.
type CompactorConfig struct {
	Enabled             *bool  `json:"enabled,omitempty"`
	SourceCodeFiltering string `json:"source_code_filtering,omitempty"` // "none", "minimal", "aggressive"
	MaxChars            int    `json:"max_chars,omitempty"`
	MaxLines            int    `json:"max_lines,omitempty"`
}

// AutoCompactConfig holds user-overridable auto-compaction settings.
// When nil in config, the session package's two-stage defaults apply:
// shed superseded tool results at 60% of the context window, run a full
// summarizing rebuild at 88%.
type AutoCompactConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
	// ShedPercent is the context-window share at which superseded tool results
	// are dropped. Cheap: no LLM call, prompt cache preserved.
	ShedPercent int `json:"shed_percent,omitempty"`
	// SummarizePercent is the share at which a summarizing rebuild runs.
	// Clamped to 95 — beyond that the summarization request itself may not fit.
	SummarizePercent int `json:"summarize_percent,omitempty"`
	// KeepUserMessageTokens caps user messages carried across a rebuild.
	KeepUserMessageTokens int `json:"keep_user_message_tokens,omitempty"`
	// KeepRecentEvents is the conversation tail compaction never touches.
	KeepRecentEvents int `json:"keep_recent_events,omitempty"`
}

type MCPConfig struct {
	Servers []MCPServer `json:"servers"`
}

type MCPServer struct {
	Name    string            `json:"name"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	URL     string            `json:"url,omitempty"`     // HTTP transport (e.g., cloudflare-api)
	Headers map[string]string `json:"headers,omitempty"` // Custom HTTP headers for the URL (Streamable HTTP) transport
	OAuth   bool              `json:"oauth,omitempty"`   // run the OAuth authorization-code flow on first connect

	// fromStandaloneFile marks a server that was loaded from a .pi-go/mcp.json
	// file rather than declared in config.json. Such servers live in memory
	// only: Save serializes the whole Config, so without this marker a merged
	// project server would be copied into ~/.pi-go/config.json on the next
	// save and leak into every other project. Unexported, so json.Marshal
	// skips it.
	fromStandaloneFile bool
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

// LLMSSource defines a single llms.txt documentation source.
type LLMSSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	// ExactURLOnly restricts fetch_docs to this exact URL rather than any
	// page on the same host. It is set on sources pi-go inferred rather than
	// ones the user wrote, and is not serialized: configuring a source is a
	// deliberate grant of the whole host, whereas an inference drawn from a
	// file name is a guess, and a guess must not quietly widen what the model
	// can reach.
	ExactURLOnly bool `json:"-"`
}

// LLMSConfig holds configuration for llms.txt documentation sources.
type LLMSConfig struct {
	Sources []LLMSSource `json:"sources,omitempty"`
}

// Defaults returns a Config with default values.
// The default model is the latest top-tier entry in the embedded OpenAI catalog
// (modeldata/llm-prices-openai.json): gpt-5.6-sol is the current frontier at
// the time of writing. Bump this in lockstep with the catalog.
func Defaults() Config {
	return Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "gpt-5.6-sol"},
		},
		DefaultProvider: "openai",
		ThinkingLevel:   "high",
		Theme:           "default",
	}
}

// Known model prefixes for auto-detecting provider.
var modelPrefixes = map[string]string{
	"claude":    "anthropic",
	"gpt":       "openai",
	"gpt-5":     "openai",
	"gemini":    "gemini",
	"mistral":   "mistral",
	"magistral": "mistral",
	"grok":      "xai",
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
	lower := strings.ToLower(modelName)
	// ollama/ prefix → native Ollama provider.
	if strings.HasPrefix(lower, "ollama/") {
		return "ollama"
	}
	// opencode/ prefix → OpenCode Go provider.
	if strings.HasPrefix(lower, "opencode/") {
		return "opencode"
	}
	// openrouter/ prefix → OpenRouter provider.
	if strings.HasPrefix(lower, "openrouter/") {
		return "openrouter"
	}
	// agentgateway/ prefix → agentgateway provider. Checked before the
	// :cloud/-cloud suffix check below: agentgateway model IDs carry a
	// "-cloud" tag (e.g. deepseek-v4-flash:0731-cloud) that would otherwise
	// route them to Ollama.
	if strings.HasPrefix(lower, "agentgateway/") {
		return "agentgateway"
	}
	// :cloud suffix → native Ollama provider.
	if strings.HasSuffix(modelName, ":cloud") || strings.HasSuffix(modelName, "-cloud") {
		return "ollama"
	}
	for prefix, provider := range modelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return provider
		}
	}
	return ""
}

// Load reads config from global (~/.pi-go/config.json) and project
// (.pi-go/config.json) relative to the current process working directory.
func Load() (Config, error) {
	return LoadFrom(".")
}

// LoadFrom reads config from global (~/.pi-go/config.json) and the nearest
// project .pi-go/config.json relative to cwd, merging project overrides onto
// global. MCP servers are also loaded from separate .pi-go/mcp.json files if
// present.
func LoadFrom(cwd string) (Config, error) {
	cfg := Defaults()

	if err := loadConfigFiles(&cfg, cwd); err != nil {
		return cfg, err
	}

	// Load MCP config from separate mcp.json files if present and merge with
	// servers declared in config.json. A server name already present wins from
	// config.json; mcp.json-only servers are appended so a project can add
	// servers without redefining the global set. Appended entries keep the
	// standalone marker so Save never copies them into config.json.
	mergeStandaloneMCPServers(&cfg, LoadMCPServersFrom(cwd))

	// An llms.txt index configured as an MCP server is also registered as a
	// fetch_docs source, so the documentation is readable straight away.
	cfg.ReroutedLLMS = registerLLMSDocsSources(&cfg)

	migrateDefaultModelRoles(&cfg)

	return cfg, nil
}

// loadConfigFiles overlays the nearest project .pi-go/config.json onto the
// global ~/.pi-go/config.json on top of cfg. A missing file is not an error;
// any other read or parse failure is.
func loadConfigFiles(cfg *Config, cwd string) error {
	if home, err := os.UserHomeDir(); err == nil {
		globalPath := filepath.Join(home, ".pi-go", "config.json")
		if err := loadFile(globalPath, cfg); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if projectPath := findNearestProjectFile(cwd, filepath.Join(".pi-go", "config.json")); projectPath != "" {
		if err := loadFile(projectPath, cfg); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// mergeStandaloneMCPServers appends standalone mcp.json servers that config.json
// does not already declare. Existing names win; appended entries keep the
// standalone marker so Save never copies them into config.json.
func mergeStandaloneMCPServers(cfg *Config, mcpServers []MCPServer) {
	if len(mcpServers) == 0 {
		return
	}
	if cfg.MCP == nil {
		cfg.MCP = &MCPConfig{}
	}
	known := make(map[string]bool, len(cfg.MCP.Servers))
	for _, s := range cfg.MCP.Servers {
		known[s.Name] = true
	}
	for _, s := range mcpServers {
		if !known[s.Name] {
			s.fromStandaloneFile = true
			cfg.MCP.Servers = append(cfg.MCP.Servers, s)
		}
	}
}

// migrateDefaultModelRoles migrates the deprecated DefaultModel field into
// Roles: as the default role when none exist, otherwise filling in just the
// default role if roles are set but leave it unset.
func migrateDefaultModelRoles(cfg *Config) {
	if cfg.DefaultModel == "" {
		return
	}
	if len(cfg.Roles) == 0 {
		cfg.Roles = map[string]RoleConfig{
			"default": {Model: cfg.DefaultModel},
		}
	} else if cfg.Roles != nil {
		if _, ok := cfg.Roles["default"]; !ok {
			cfg.Roles["default"] = RoleConfig{Model: cfg.DefaultModel}
		}
	}
}

// mcpServerFile represents the JSON structure of a standalone mcp.json file.
// MCP JSON Schema v1 (Claude Desktop / NPM compatible) — mcpServers as object.
// Also supports the legacy array format used in config.json.
type mcpServerFile struct {
	MCPServers any `json:"mcpServers"` // object (Claude Desktop) or []MCPServer (legacy)
}

// LoadMCPServers reads MCP server configurations from standalone mcp.json files
// relative to the current process working directory.
func LoadMCPServers() []MCPServer {
	return LoadMCPServersFrom(".")
}

// LoadMCPServersFrom reads MCP server configurations from standalone mcp.json
// files relative to cwd. Resolution order: project .pi-go/mcp.json overrides
// global ~/.pi-go/mcp.json. Supports both the Claude Desktop object format
// (servers keyed by name) and the legacy array format.
func LoadMCPServersFrom(cwd string) []MCPServer {
	var servers []MCPServer

	// Try global path first.
	if home, err := os.UserHomeDir(); err == nil {
		globalPath := filepath.Join(home, ".pi-go", "mcp.json")
		servers = loadMCPServersFromFile(globalPath)
	}

	// Project path overrides global (only if project file exists).
	if projectPath := findNearestProjectFile(cwd, filepath.Join(".pi-go", "mcp.json")); projectPath != "" {
		if projectServers := loadMCPServersFromFile(projectPath); projectServers != nil {
			servers = projectServers
		}
	}

	return substituteEnvVarsFrom(cwd, servers)
}

func findNearestProjectFile(cwd, rel string) string {
	start := strings.TrimSpace(cwd)
	if start == "" {
		start = "."
	}
	dir := filepath.Clean(start)
	for {
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
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
	return parseMCPServers(f.MCPServers)
}

// SubstituteEnvVars replaces ${VAR} patterns in server URLs with values from
// ~/.pi-go/.env (or project .pi-go/.env). Secrets stay in the file and are
// never exposed in logs or TUI.
func substituteEnvVars(servers []MCPServer) []MCPServer {
	return substituteEnvVarsFrom(".", servers)
}

func substituteEnvVarsFrom(cwd string, servers []MCPServer) []MCPServer {
	env := loadEnvFileFrom(cwd)
	for i := range servers {
		if servers[i].URL != "" {
			servers[i].URL = substituteEnv(env, servers[i].URL)
		}
		for k, v := range servers[i].Headers {
			servers[i].Headers[k] = substituteEnv(env, v)
		}
	}
	return servers
}

func loadEnvFileFrom(cwd string) map[string]string {
	result := make(map[string]string)
	if home, err := os.UserHomeDir(); err == nil {
		mergeEnvFile(result, filepath.Join(home, ".pi-go", ".env"))
	}
	if projectEnv := findNearestProjectFile(cwd, filepath.Join(".pi-go", ".env")); projectEnv != "" {
		mergeEnvFile(result, projectEnv)
	}
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
		return parseMCPServerObject(x)
	case []any:
		return parseMCPServerArray(x)
	default:
		return nil
	}
}

// parseMCPServerObject reads the Claude Desktop object format: mcpServers is a
// map keyed by server name, and each value is { "command": "...", "args": [...] }
// or { "url": "..." }.
func parseMCPServerObject(x map[string]any) []MCPServer {
	servers := make([]MCPServer, 0, len(x))
	for name, val := range x {
		srv := MCPServer{Name: name}
		if m, ok := val.(map[string]any); ok {
			applyMCPTransport(&srv, m)
		}
		// Skip servers that have neither command nor URL (e.g., disabled servers).
		if srv.Command == "" && srv.URL == "" {
			continue
		}
		servers = append(servers, srv)
	}
	return servers
}

// parseMCPServerArray reads the legacy array format, where the server name is a
// field of each element rather than the key it is stored under.
func parseMCPServerArray(x []any) []MCPServer {
	servers := make([]MCPServer, 0, len(x))
	for _, item := range x {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		srv := MCPServer{}
		if n, ok := m["name"].(string); ok {
			srv.Name = n
		}
		applyMCPTransport(&srv, m)
		// Skip servers that have neither command nor URL.
		if srv.Command == "" && srv.URL == "" {
			continue
		}
		servers = append(servers, srv)
	}
	return servers
}

// applyMCPTransport fills the transport fields of srv — command, args, url and
// headers — from one server object. Entries that are absent or hold the wrong
// JSON type are left at their zero value, which is what makes a malformed entry
// fall out as "neither command nor URL" and be skipped by the callers.
func applyMCPTransport(srv *MCPServer, m map[string]any) {
	if cmd, ok := m["command"].(string); ok {
		srv.Command = cmd
	}
	if args, ok := m["args"].([]any); ok {
		srv.Args = toStringSlice(args)
	}
	if url, ok := m["url"].(string); ok {
		srv.URL = url
	}
	if headers, ok := m["headers"].(map[string]any); ok {
		srv.Headers = toStringMap(headers)
	}
	if oauth, ok := m["oauth"].(bool); ok {
		srv.OAuth = oauth
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

// toStringMap converts map[string]any to map[string]string, keeping only
// string values. Non-string header values are skipped.
func toStringMap(v map[string]any) map[string]string {
	if len(v) == 0 {
		return nil
	}
	m := make(map[string]string, len(v))
	for k, x := range v {
		if str, ok := x.(string); ok {
			m[k] = str
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
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
		"anthropic":    {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
		"openai":       {"OPENAI_API_KEY"},
		"azure":        {"AZURE_OPENAI_API_KEY", "AZUREOPENAI_API_KEY", "AZURE_API_KEY"},
		"gemini":       {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		"mistral":      {"MISTRAL_API_KEY"},
		"xai":          {"XAI_API_KEY"},
		"openrouter":   {"OPENROUTER_API_KEY"},
		"ollama":       {"OLLAMA_API_KEY"},
		"opencode":     {"OPENCODE_API_KEY"},
		"agentgateway": {"AGENTGATEWAY_API_KEY"},
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
// Supports ANTHROPIC_BASE_URL (Ollama compatibility), OPENAI_BASE_URL,
// GEMINI_BASE_URL, and OLLAMA_HOST (Ollama server address override).
func BaseURLs() map[string]string {
	urls := make(map[string]string)
	envVars := map[string]string{
		"anthropic":    "ANTHROPIC_BASE_URL",
		"openai":       "OPENAI_BASE_URL",
		"gemini":       "GEMINI_BASE_URL",
		"mistral":      "MISTRAL_BASE_URL",
		"xai":          "XAI_BASE_URL",
		"openrouter":   "OPENROUTER_BASE_URL",
		"ollama":       "OLLAMA_HOST",
		"opencode":     "OPENCODE_BASE_URL",
		"agentgateway": "AGENTGATEWAY_BASE_URL",
	}
	for provider, envVar := range envVars {
		if val := os.Getenv(envVar); val != "" {
			urls[provider] = val
		}
	}
	return urls
}

// ResolveBaseURLs returns provider base URLs from the config's baseURLs map
// merged with the environment, environment winning. This lets a self-hosted
// endpoint (e.g. an Ollama server on the LAN) live in config.json instead of
// requiring a shell export, while keeping the env vars usable as a per-shell
// or CI override. An empty env var does not mask a configured value.
func (c *Config) ResolveBaseURLs() map[string]string {
	urls := make(map[string]string, len(c.BaseURLs))
	for provider, url := range c.BaseURLs {
		if url != "" {
			urls[provider] = url
		}
	}
	for provider, url := range BaseURLs() {
		urls[provider] = url
	}
	return urls
}

// Save writes the config to the global ~/.pi-go/config.json file.
// It does not overwrite project-level config.
func (c *Config) Save() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("user home dir: %w", err)
	}
	path := filepath.Join(home, ".pi-go", "config.json")

	// Servers merged in from .pi-go/mcp.json are project-scoped and live in
	// memory only; strip them before serializing so a save (e.g. from
	// SaveDefaultRole) does not copy them into the global config.
	snapshot := *c
	if snapshot.MCP != nil && len(snapshot.MCP.Servers) > 0 {
		kept := make([]MCPServer, 0, len(snapshot.MCP.Servers))
		for _, s := range snapshot.MCP.Servers {
			if !s.fromStandaloneFile {
				kept = append(kept, s)
			}
		}
		snapshot.MCP = &MCPConfig{Servers: kept}
	}

	data, err := json.MarshalIndent(&snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

// SaveDefaultRole updates the "default" role in the global config and saves it.
// If the default role doesn't exist, it creates it. If a model is already set,
// it updates only the model field (preserving provider, advisor, etc.).
func SaveDefaultRole(model, provider string) error {
	cfg, err := Load()
	if err != nil {
		// If no config exists yet, start with defaults.
		cfg = Defaults()
	}

	if cfg.Roles == nil {
		cfg.Roles = make(map[string]RoleConfig)
	}

	role := cfg.Roles["default"]
	role.Model = model
	if provider != "" {
		role.Provider = provider
	}
	cfg.Roles["default"] = role

	return cfg.Save()
}

// IsLLMSDocsURL reports whether u points at an llms.txt documentation index
// rather than an MCP endpoint. Such a URL serves plain text over GET and
// answers the MCP "initialize" POST with 405 Method Not Allowed, so treating
// it as an MCP server can only ever fail.
//
// The convention (llmstxt.org) fixes the file name, not the host or path, so
// the base name is the deciding test: "llms.txt" and the expanded
// "llms-full.txt". The URL must also be one fetch_docs could actually read —
// https with a host — since that is the only tool that would serve it.
func IsLLMSDocsURL(u string) bool {
	parsed, err := url.Parse(strings.TrimSpace(u))
	if err != nil {
		return false
	}
	// fetch_docs serves only https URLs and matches sources by host, so
	// anything else could never be read through it. Classifying such a URL as
	// a docs source would advertise a source that rejects every fetch.
	if parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	if isPrivateHostLiteral(parsed.Hostname()) {
		return false
	}
	// Never infer from a URL that carries credentials. A source's full URL is
	// written into the fetch_docs tool description and sent to the model
	// provider, so inferring one from userinfo or a query string would turn
	// transport configuration the user kept in mcp.json into something the
	// model — and its provider — can read. A docs index needs neither; anyone
	// who genuinely has one can configure it under "llms" deliberately.
	if parsed.User != nil || parsed.RawQuery != "" {
		return false
	}
	switch strings.ToLower(path.Base(parsed.Path)) {
	case "llms.txt", "llms-full.txt":
		return true
	}
	return false
}

// isPrivateHostLiteral reports whether a host is a literal IP that fetch_docs
// refuses as private, so a source it could never fetch is not advertised.
//
// Only literal addresses are tested. fetch_docs also resolves host names and
// rejects those landing on a private address, but doing that here would put a
// DNS lookup — and its latency and failure modes — inside config loading. The
// cost of missing that case is one source entry whose fetches are refused with
// a clear message; the cost of the lookup is paid on every start.
func isPrivateHostLiteral(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// isLLMSDocsServer reports whether an MCP entry looks like a documentation
// index filed under the wrong key.
//
// The URL's base name is the signal: llms.txt is a reserved file name in an
// established convention (llmstxt.org) for a plain-text index fetched with
// GET. It is only a signal, never proof — MCP endpoint paths are arbitrary —
// so nothing destructive hangs off it. An entry that carries configuration a
// public docs index would never need (a command, OAuth, custom headers) is not
// a docs index at all and is excluded outright.
func isLLMSDocsServer(srv MCPServer) bool {
	if srv.URL == "" || srv.Command != "" || srv.OAuth || len(srv.Headers) > 0 {
		return false
	}
	return IsLLMSDocsURL(srv.URL)
}

// registerLLMSDocsSources makes an MCP entry that points at an llms.txt index
// readable by the fetch_docs tool. Configuring a docs index under "mcpServers"
// is a common mix-up — both are "a URL a model reads from" — and left alone it
// yields a 405 on every startup and no documentation tool.
//
// The entry is left in the MCP list. Endpoint paths are arbitrary in MCP, so
// the base name cannot prove the URL is not a real server, and removing one on
// a guess would silently strip its tools. Adding a source is additive and
// reversible: a genuine MCP server keeps every tool it had and merely gains an
// inert docs source, while a genuine docs index becomes readable immediately
// and reports its own failure through resilientToolset, which explains what
// the entry really is once the connection has actually failed.
//
// An entry whose URL is already configured as a source is not added twice.
func registerLLMSDocsSources(cfg *Config) []string {
	if cfg.MCP == nil || len(cfg.MCP.Servers) == 0 {
		return nil
	}
	existing := make(map[string]bool)
	if cfg.LLMS != nil {
		for _, s := range cfg.LLMS.Sources {
			existing[s.URL] = true
		}
	}

	var registered []string
	var added []LLMSSource
	for _, srv := range cfg.MCP.Servers {
		if !isLLMSDocsServer(srv) {
			continue
		}
		registered = append(registered, srv.Name)
		// Store the trimmed URL. IsLLMSDocsURL deliberately tolerates
		// surrounding whitespace, but fetch_docs parses the stored value to
		// check the host against its sources — an untrimmed URL fails that
		// parse, so the source it advertises would be unusable.
		url := strings.TrimSpace(srv.URL)
		if existing[url] {
			continue
		}
		existing[url] = true
		added = append(added, LLMSSource{Name: srv.Name, URL: url, ExactURLOnly: true})
	}
	cfg.InferredLLMS = append(cfg.InferredLLMS, added...)
	return registered
}

// LLMSSources returns the llms.txt sources to serve, both those the user
// configured and those inferred from MCP entries during load. It returns nil
// when there are none, so callers can test it directly to decide whether to
// register the fetch_docs tool at all.
//
// The two are kept apart in the struct and joined only here, so nothing that
// marshals a Config can persist an inference.
func (c *Config) LLMSSources() *LLMSConfig {
	var sources []LLMSSource
	if c.LLMS != nil {
		sources = append(sources, c.LLMS.Sources...)
	}
	sources = append(sources, c.InferredLLMS...)
	if len(sources) == 0 {
		return nil
	}
	return &LLMSConfig{Sources: sources}
}

// quoteAll returns the names quoted, for embedding a list in a notice without
// leaving a name like "adk docs" ambiguous against the separator.
func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strconv.Quote(n)
	}
	return out
}

// NotifyReroutedLLMS announces the MCP servers that load-time rerouting moved
// to the llms.txt sources. It is separate from LoadFrom so the message is
// raised only once a front end has claimed the notice sink — in the TUI that
// means after runInteractive installs it, since anything written before the
// first frame is erased by the terminal reset.
//
// It is safe to call more than once only if the caller intends to repeat the
// message; each call emits.
func NotifyReroutedLLMS(cfg Config) {
	if len(cfg.ReroutedLLMS) == 0 {
		return
	}
	notice.Notifyf("MCP server(s) %s point at an llms.txt index and are now readable with the "+
		"fetch_docs tool. If they are not MCP endpoints, move them to \"llms\": {\"sources\": [...]} "+
		"so they are not dialed on every startup.",
		strings.Join(quoteAll(cfg.ReroutedLLMS), ", "))
}
