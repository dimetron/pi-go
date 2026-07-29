package server

import (
	"errors"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
)

func TestLoadSessionConfig(t *testing.T) {
	t.Parallel()

	t.Run("propagates loader errors", func(t *testing.T) {
		t.Parallel()
		want := errors.New("boom")
		rt := RuntimeConfig{LoadConfig: func() (config.Config, error) { return config.Config{}, want }}

		if _, err := loadSessionConfig(rt, t.TempDir()); !errors.Is(err, want) {
			t.Errorf("loadSessionConfig() error = %v, want it to wrap %v", err, want)
		}
	})

	t.Run("model override replaces the default role", func(t *testing.T) {
		t.Parallel()
		rt := RuntimeConfig{
			Model:      "claude-test-model",
			LoadConfig: func() (config.Config, error) { return config.Config{}, nil },
		}

		cfg, err := loadSessionConfig(rt, t.TempDir())
		if err != nil {
			t.Fatalf("loadSessionConfig() error = %v", err)
		}
		if got := cfg.Roles["default"].Model; got != "claude-test-model" {
			t.Errorf("default role model = %q, want %q", got, "claude-test-model")
		}
	})

	t.Run("model override initializes a nil role map", func(t *testing.T) {
		t.Parallel()
		// A config with no Roles map at all must not panic on override.
		rt := RuntimeConfig{
			Model:      "m",
			LoadConfig: func() (config.Config, error) { return config.Config{Roles: nil}, nil },
		}

		cfg, err := loadSessionConfig(rt, t.TempDir())
		if err != nil {
			t.Fatalf("loadSessionConfig() error = %v", err)
		}
		if len(cfg.Roles) != 1 {
			t.Errorf("expected exactly one role, got %d", len(cfg.Roles))
		}
	})

	t.Run("without an override the config is untouched", func(t *testing.T) {
		t.Parallel()
		rt := RuntimeConfig{
			LoadConfig: func() (config.Config, error) {
				return config.Config{Roles: map[string]config.RoleConfig{"default": {Model: "original"}}}, nil
			},
		}

		cfg, err := loadSessionConfig(rt, t.TempDir())
		if err != nil {
			t.Fatalf("loadSessionConfig() error = %v", err)
		}
		if got := cfg.Roles["default"].Model; got != "original" {
			t.Errorf("default role model = %q, want it left as %q", got, "original")
		}
	})
}

func TestResolveSessionProvider(t *testing.T) {
	t.Parallel()

	t.Run("explicit base URL marks the provider custom", func(t *testing.T) {
		t.Parallel()
		info, baseURL, err := resolveSessionProvider(config.Config{}, "https://proxy.example", "gpt-4o", "openai")
		if err != nil {
			t.Fatalf("resolveSessionProvider() error = %v", err)
		}
		if baseURL != "https://proxy.example" {
			t.Errorf("baseURL = %q, want the flag value", baseURL)
		}
		if !info.Custom {
			t.Error("an explicit base URL should mark the provider as custom")
		}
		if info.Provider != "openai" {
			t.Errorf("provider = %q, want %q", info.Provider, "openai")
		}
	})

	t.Run("no base URL leaves the provider standard", func(t *testing.T) {
		t.Parallel()
		info, baseURL, err := resolveSessionProvider(config.Config{}, "", "gpt-4o", "openai")
		if err != nil {
			t.Fatalf("resolveSessionProvider() error = %v", err)
		}
		if baseURL != "" {
			t.Errorf("baseURL = %q, want empty", baseURL)
		}
		if info.Custom {
			t.Error("no base URL should leave Custom false")
		}
	})

	t.Run("unresolvable model is an error", func(t *testing.T) {
		t.Parallel()
		if _, _, err := resolveSessionProvider(config.Config{}, "", "", ""); err == nil {
			t.Error("expected an error for an empty model name")
		}
	})
}

func TestStreamProxyIsSafeWhenUnset(t *testing.T) {
	t.Parallel()
	// Between turns the proxy has no stream; tool events must be dropped
	// rather than panic.
	p := &streamProxy{}

	id, err := p.OnToolStart(t.Context(), "grep", map[string]any{"q": "x"})
	if err != nil || id != "" {
		t.Errorf("OnToolStart with no stream = (%q, %v), want (\"\", nil)", id, err)
	}
	if err := p.OnToolEnd(t.Context(), "call-1", nil, nil, nil); err != nil {
		t.Errorf("OnToolEnd with no stream = %v, want nil", err)
	}

	// Swapping to nil explicitly must stay safe too.
	p.swap(nil)
	if _, err := p.OnToolStart(t.Context(), "grep", nil); err != nil {
		t.Errorf("OnToolStart after swap(nil) = %v, want nil", err)
	}
}
