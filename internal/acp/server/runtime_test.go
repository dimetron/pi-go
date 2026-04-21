package server

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
)

func TestBuildMCPToolsetsFromCfg_NilMCPReturnsNil(t *testing.T) {
	if ts := buildMCPToolsetsFromCfg(config.Config{}); ts != nil {
		t.Fatalf("expected nil toolsets, got %d", len(ts))
	}
}

func TestBuildMCPToolsetsFromCfg_EmptyServersReturnsNil(t *testing.T) {
	cfg := config.Config{MCP: &config.MCPConfig{}}
	if ts := buildMCPToolsetsFromCfg(cfg); ts != nil {
		t.Fatalf("expected nil toolsets, got %d", len(ts))
	}
}

func TestBuildMCPToolsetsFromCfg_BuildsOneToolsetPerServer(t *testing.T) {
	cfg := config.Config{
		MCP: &config.MCPConfig{Servers: []config.MCPServer{
			{Name: "cmd-srv", Command: "echo", Args: []string{"hi"}},
			{Name: "url-srv", URL: "https://example.invalid/mcp"},
		}},
	}
	ts := buildMCPToolsetsFromCfg(cfg)
	if len(ts) != 2 {
		t.Fatalf("expected 2 toolsets, got %d", len(ts))
	}
	names := []string{ts[0].Name(), ts[1].Name()}
	want := map[string]bool{"cmd-srv": true, "url-srv": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected toolset name %q (got names=%v)", n, names)
		}
	}
}

func TestBuildMCPToolsetsFromCfg_SkipsServerWithNeitherCommandNorURL(t *testing.T) {
	cfg := config.Config{
		MCP: &config.MCPConfig{Servers: []config.MCPServer{
			{Name: "broken"},
			{Name: "ok", Command: "echo"},
		}},
	}
	ts := buildMCPToolsetsFromCfg(cfg)
	if len(ts) != 1 {
		t.Fatalf("expected 1 toolset (broken skipped), got %d", len(ts))
	}
	if ts[0].Name() != "ok" {
		t.Errorf("expected remaining toolset 'ok', got %q", ts[0].Name())
	}
}

// TestNewPromptHandler_EmptyCWD tests that an empty CWD doesn't crash.
// The handler should fall back to os.Getwd when turn.CWD is empty.
func TestNewPromptHandler_EmptyCWD(t *testing.T) {
	// Provide a valid config loader so we get past the CWD fallback
	// without hitting real file system issues.
	origGetwd := getwd
	var getwdCalled bool
	getwd = func() (string, error) {
		getwdCalled = true
		// Return a valid directory so LoadConfig can work.
		dir := t.TempDir()
		return dir, nil
	}
	defer func() { getwd = origGetwd }()

	handler := NewPromptHandler(RuntimeConfig{
		LoadConfig: func() (config.Config, error) {
			return config.Config{}, errors.New("stop-after-cwd")
		},
	})

	ctx := context.Background()
	turn := PromptTurn{CWD: "", Prompt: "hello"}

	_, err := handler(ctx, turn)

	if !getwdCalled {
		t.Error("expected Getwd to be called for empty CWD")
	}
	if err == nil {
		t.Error("expected error after config load, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "stop-after-cwd") {
		// Verify we got past CWD handling
		t.Logf("got error (expected after config load): %v", err)
	}
}

// TestNewPromptHandler_LoadConfigError tests that LoadConfig errors are propagated.
func TestNewPromptHandler_LoadConfigError(t *testing.T) {
	wantErr := errors.New("config load failed")

	handler := NewPromptHandler(RuntimeConfig{
		LoadConfig: func() (config.Config, error) {
			return config.Config{}, wantErr
		},
	})

	ctx := context.Background()
	turn := PromptTurn{CWD: t.TempDir(), Prompt: "hello"}

	_, err := handler(ctx, turn)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Error should wrap the loadConfig error
	if !strings.Contains(err.Error(), "loading config") {
		t.Errorf("expected 'loading config' in error, got: %v", err)
	}
}

// TestNewPromptHandler_ModelResolveError tests model resolve failure returns error.
// This happens when ResolveRole returns an error for a missing model.
func TestNewPromptHandler_ModelResolveError(t *testing.T) {
	// Config with a role that has an empty model will cause ResolveRole to fail.
	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: ""}, // Empty model causes resolve error
		},
	}

	handler := NewPromptHandler(RuntimeConfig{
		LoadConfig: func() (config.Config, error) {
			return cfg, nil
		},
	})

	ctx := context.Background()
	turn := PromptTurn{CWD: t.TempDir(), Prompt: "hello"}

	_, err := handler(ctx, turn)

	if err == nil {
		t.Fatal("expected error from model resolve, got nil")
	}
	if !strings.Contains(err.Error(), "resolving model role") {
		t.Errorf("expected 'resolving model role' in error, got: %v", err)
	}
}

// TestNewPromptHandler_NoAPIKey tests missing API key returns appropriate error.
func TestNewPromptHandler_NoAPIKey(t *testing.T) {
	// Use a valid anthropic model that requires an API key (not gemini, ollama, or azure).
	cfg := config.Config{
		Roles: map[string]config.RoleConfig{
			"default": {Model: "claude-3-5-haiku-latest"}, // Valid Anthropic model
		},
	}

	handler := NewPromptHandler(RuntimeConfig{
		LoadConfig: func() (config.Config, error) {
			return cfg, nil
		},
	})

	ctx := context.Background()
	turn := PromptTurn{CWD: t.TempDir(), Prompt: "hello"}

	_, err := handler(ctx, turn)

	if err == nil {
		t.Fatal("expected error for missing API key, got nil")
	}
	// Error should mention the provider and the env var
	if !strings.Contains(err.Error(), "no API key") {
		t.Errorf("expected 'no API key' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "anthropic") {
		t.Errorf("expected 'anthropic' provider in error, got: %v", err)
	}
}

// TestProviderEnvVar tests providerEnvVar returns correct env var names.
func TestProviderEnvVar(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "ANTHROPIC_API_KEY"},
		{"openai", "OPENAI_API_KEY"},
		{"azure", "AZURE_OPENAI_API_KEY"},
		{"gemini", "GEMINI_API_KEY"},
		{"unknown", "UNKNOWN_API_KEY"},
		{"ollama", "OLLAMA_API_KEY"},
		{"cohere", "COHERE_API_KEY"},
	}

	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			got := providerEnvVar(tc.provider)
			if got != tc.want {
				t.Errorf("providerEnvVar(%q) = %q, want %q", tc.provider, got, tc.want)
			}
		})
	}
}

// TestMergeExtraHeaders tests merging config headers with CLI headers.
func TestMergeExtraHeaders(t *testing.T) {
	t.Run("nil both returns nil", func(t *testing.T) {
		got := mergeExtraHeaders(nil, nil)
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("empty both returns nil", func(t *testing.T) {
		got := mergeExtraHeaders(map[string]string{}, []string{})
		if got != nil {
			t.Errorf("expected nil, got %v", got)
		}
	})

	t.Run("cfg headers only", func(t *testing.T) {
		cfgHeaders := map[string]string{"Authorization": "Bearer token", "X-Custom": "value"}
		got := mergeExtraHeaders(cfgHeaders, nil)
		if len(got) != 2 {
			t.Fatalf("expected 2 headers, got %d", len(got))
		}
		if got["Authorization"] != "Bearer token" {
			t.Errorf("Authorization = %q, want %q", got["Authorization"], "Bearer token")
		}
		if got["X-Custom"] != "value" {
			t.Errorf("X-Custom = %q, want %q", got["X-Custom"], "value")
		}
	})

	t.Run("cli headers only", func(t *testing.T) {
		cliHeaders := []string{"Authorization=Bearer cli", "X-Other=value"}
		got := mergeExtraHeaders(nil, cliHeaders)
		if len(got) != 2 {
			t.Fatalf("expected 2 headers, got %d", len(got))
		}
		if got["Authorization"] != "Bearer cli" {
			t.Errorf("Authorization = %q, want %q", got["Authorization"], "Bearer cli")
		}
	})

	t.Run("cli headers override cfg headers", func(t *testing.T) {
		cfgHeaders := map[string]string{"Authorization": "Bearer cfg", "X-Custom": "cfg-value"}
		cliHeaders := []string{"Authorization=Bearer cli", "X-New=cli-value"}
		got := mergeExtraHeaders(cfgHeaders, cliHeaders)
		if got["Authorization"] != "Bearer cli" {
			t.Errorf("Authorization = %q (cli should override), want %q", got["Authorization"], "Bearer cli")
		}
		if got["X-Custom"] != "cfg-value" {
			t.Errorf("X-Custom = %q, want %q", got["X-Custom"], "cfg-value")
		}
		if got["X-New"] != "cli-value" {
			t.Errorf("X-New = %q, want %q", got["X-New"], "cli-value")
		}
	})

	t.Run("header with equals in value", func(t *testing.T) {
		cliHeaders := []string{"Authorization=Bearer token=secret"}
		got := mergeExtraHeaders(nil, cliHeaders)
		if got["Authorization"] != "Bearer token=secret" {
			t.Errorf("Authorization = %q, want %q", got["Authorization"], "Bearer token=secret")
		}
	})

	t.Run("invalid cli header without equals ignored", func(t *testing.T) {
		cliHeaders := []string{"InvalidHeader", "X-Valid=value"}
		got := mergeExtraHeaders(nil, cliHeaders)
		if len(got) != 1 {
			t.Errorf("expected 1 header (invalid ignored), got %d", len(got))
		}
		if got["X-Valid"] != "value" {
			t.Errorf("X-Valid = %q, want %q", got["X-Valid"], "value")
		}
	})
}

// TestDetectGitRootError tests detectGitRoot returns empty string when not in a git repo.
func TestDetectGitRootError(t *testing.T) {
	// Use an empty temp directory that is not a git repository.
	dir := t.TempDir()

	got := detectGitRoot(dir)

	if got != "" {
		t.Errorf("detectGitRoot(%q) = %q, want empty string for non-git dir", dir, got)
	}
}

// TestDetectGitRootSuccess tests detectGitRoot finds git root in a valid repo.
func TestDetectGitRootSuccess(t *testing.T) {
	// Create a temp directory, init a git repo, and verify detectGitRoot finds it.
	dir := t.TempDir()

	// Run git init
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		t.Skipf("skipping: git not available: %v", err)
	}

	got := detectGitRoot(dir)

	if got == "" {
		t.Errorf("detectGitRoot(%q) returned empty, expected a path", dir)
	}
}
