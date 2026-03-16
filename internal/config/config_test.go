package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()
	if len(cfg.Roles) == 0 {
		t.Fatal("expected default roles to be set")
	}
	rc, ok := cfg.Roles["default"]
	if !ok {
		t.Fatal("expected 'default' role")
	}
	if rc.Model != "claude-sonnet-4-20250514" {
		t.Errorf("unexpected default model: %s", rc.Model)
	}
	if cfg.DefaultProvider != "anthropic" {
		t.Errorf("unexpected default provider: %s", cfg.DefaultProvider)
	}
}

func TestResolveRole_ExactMatch(t *testing.T) {
	cfg := Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "claude-sonnet-4-20250514"},
			"smol":    {Model: "gemini-2.0-flash"},
			"slow":    {Model: "claude-opus-4-20250514", Provider: "anthropic"},
		},
		DefaultProvider: "anthropic",
	}

	model, prov, err := cfg.ResolveRole("smol")
	if err != nil {
		t.Fatal(err)
	}
	if model != "gemini-2.0-flash" {
		t.Errorf("expected gemini-2.0-flash, got %s", model)
	}
	if prov != "gemini" {
		t.Errorf("expected gemini provider, got %s", prov)
	}
}

func TestResolveRole_FallbackToDefault(t *testing.T) {
	cfg := Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "claude-sonnet-4-20250514"},
		},
		DefaultProvider: "anthropic",
	}

	model, prov, err := cfg.ResolveRole("plan")
	if err != nil {
		t.Fatal(err)
	}
	if model != "claude-sonnet-4-20250514" {
		t.Errorf("expected fallback to default model, got %s", model)
	}
	if prov != "anthropic" {
		t.Errorf("expected anthropic provider, got %s", prov)
	}
}

func TestResolveRole_NoDefault(t *testing.T) {
	cfg := Config{
		Roles: map[string]RoleConfig{},
	}

	_, _, err := cfg.ResolveRole("default")
	if err != ErrNoDefaultRole {
		t.Errorf("expected ErrNoDefaultRole, got %v", err)
	}
}

func TestResolveRole_NilRoles(t *testing.T) {
	cfg := Config{}

	_, _, err := cfg.ResolveRole("default")
	if err != ErrNoDefaultRole {
		t.Errorf("expected ErrNoDefaultRole, got %v", err)
	}
}

func TestResolveRole_AutoDetectProvider(t *testing.T) {
	tests := []struct {
		model    string
		wantProv string
	}{
		{"claude-sonnet-4-20250514", "anthropic"},
		{"gpt-4o", "openai"},
		{"gemini-2.5-pro", "gemini"},
		{"o3-mini", "openai"},
		{"minimax-m2.5:cloud", "anthropic"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			cfg := Config{
				Roles: map[string]RoleConfig{
					"default": {Model: tt.model},
				},
				DefaultProvider: "anthropic",
			}
			_, prov, err := cfg.ResolveRole("default")
			if err != nil {
				t.Fatal(err)
			}
			if prov != tt.wantProv {
				t.Errorf("expected provider %s, got %s", tt.wantProv, prov)
			}
		})
	}
}

func TestResolveRole_ExplicitProvider(t *testing.T) {
	cfg := Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "my-custom-model", Provider: "openai"},
		},
	}

	_, prov, err := cfg.ResolveRole("default")
	if err != nil {
		t.Fatal(err)
	}
	if prov != "openai" {
		t.Errorf("expected explicit provider openai, got %s", prov)
	}
}

func TestResolveRole_UnknownModelFallsToDefaultProvider(t *testing.T) {
	cfg := Config{
		Roles: map[string]RoleConfig{
			"default": {Model: "unknown-model-xyz"},
		},
		DefaultProvider: "anthropic",
	}

	_, prov, err := cfg.ResolveRole("default")
	if err != nil {
		t.Fatal(err)
	}
	if prov != "anthropic" {
		t.Errorf("expected fallback to defaultProvider, got %s", prov)
	}
}

func TestConfigMerge_RolesOverride(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	err := os.WriteFile(cfgPath, []byte(`{
		"roles": {
			"default": {"model": "gpt-4o"},
			"smol": {"model": "gemini-2.0-flash"}
		},
		"theme": "dark"
	}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Defaults()
	if err := loadFile(cfgPath, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Roles["default"].Model != "gpt-4o" {
		t.Errorf("expected default role override, got %s", cfg.Roles["default"].Model)
	}
	if cfg.Roles["smol"].Model != "gemini-2.0-flash" {
		t.Errorf("expected smol role, got %s", cfg.Roles["smol"].Model)
	}
	if cfg.Theme != "dark" {
		t.Errorf("expected theme override, got %s", cfg.Theme)
	}
}

func TestLoadFile_LegacyDefaultModel(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Simulate legacy config with only defaultModel, no roles
	err := os.WriteFile(cfgPath, []byte(`{"defaultModel":"gpt-4o","theme":"dark"}`), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg := Config{} // empty — no defaults
	if err := loadFile(cfgPath, &cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.DefaultModel != "gpt-4o" {
		t.Errorf("expected defaultModel override, got %s", cfg.DefaultModel)
	}
	if cfg.Theme != "dark" {
		t.Errorf("expected theme override, got %s", cfg.Theme)
	}
}

func TestAPIKeys(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("OPENAI_API_KEY", "")

	keys := APIKeys()
	if keys["anthropic"] != "test-key" {
		t.Errorf("expected anthropic key, got %q", keys["anthropic"])
	}
	if _, ok := keys["openai"]; ok {
		t.Error("expected no openai key for empty env var")
	}
}
