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
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

// ErrNoDefaultRole is returned when no default role is configured.
var ErrNoDefaultRole = errors.New("no default model role configured")

// Config holds all pi-go configuration.
type Config struct {
	Roles           map[string]RoleConfig `json:"roles,omitempty"`
	DefaultModel    string                `json:"defaultModel,omitempty"` // deprecated: use roles
	DefaultProvider string                `json:"defaultProvider"`
	ThinkingLevel   string                `json:"thinkingLevel"`
	Theme           string                `json:"theme"`
	Tools           map[string]any        `json:"tools,omitempty"`
	MCP             *MCPConfig            `json:"mcp,omitempty"`
	Hooks           []HookConfig          `json:"hooks,omitempty"`
}

type MCPConfig struct {
	Servers []MCPServer `json:"servers"`
}

type MCPServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// Defaults returns a Config with default values.
func Defaults() Config {
	return Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "claude-sonnet-4-6"},
		},
		DefaultProvider: "anthropic",
		ThinkingLevel:   "medium",
		Theme:           "default",
	}
}

// Known model prefixes for auto-detecting provider.
var modelPrefixes = map[string]string{
	"claude": "anthropic",
	"gpt":    "openai",
	"o1":     "openai",
	"o3":     "openai",
	"o4":     "openai",
	"gemini": "gemini",
}

// ResolveRole returns the model name and provider for a given role.
// Falls back: requested role → "default" role → error.
func (c *Config) ResolveRole(role string) (model string, prov string, err error) {
	if len(c.Roles) == 0 {
		return "", "", ErrNoDefaultRole
	}

	rc, ok := c.Roles[role]
	if !ok {
		rc, ok = c.Roles["default"]
		if !ok {
			return "", "", ErrNoDefaultRole
		}
	}

	if rc.Model == "" {
		return "", "", fmt.Errorf("role %q has no model configured", role)
	}

	prov = rc.Provider
	if prov == "" {
		prov = autoDetectProvider(rc.Model)
		if prov == "" {
			prov = c.DefaultProvider
		}
	}

	return rc.Model, prov, nil
}

// autoDetectProvider detects the provider from model name prefix.
func autoDetectProvider(modelName string) string {
	if strings.HasSuffix(modelName, ":cloud") {
		return "anthropic"
	}
	lower := strings.ToLower(modelName)
	for prefix, provider := range modelPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return provider
		}
	}
	return ""
}

// Load reads config from global (~/.pi-go/config.json) and project (.pi-go/config.json),
// merging project overrides onto global.
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
		"gemini":    {"GOOGLE_API_KEY", "GEMINI_API_KEY"},
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
	}
	for provider, envVar := range envVars {
		if val := os.Getenv(envVar); val != "" {
			urls[provider] = val
		}
	}
	return urls
}
