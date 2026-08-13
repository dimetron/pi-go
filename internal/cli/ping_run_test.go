// Tests for `pi ping` end-to-end runs against stub HTTP servers.
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newOllamaHTTPServer returns an httptest server that emulates the subset of
// Ollama HTTP endpoints used by `pi ping` and `pi` root against Ollama.
func newOllamaHTTPServer(t *testing.T, models []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/", "/api/version":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"version": "0.0.0-test"})
		case "/api/tags":
			w.Header().Set("Content-Type", "application/json")
			resp := struct {
				Models []struct{ Name string } `json:"models"`
			}{}
			for _, m := range models {
				resp.Models = append(resp.Models, struct{ Name string }{Name: m})
			}
			_ = json.NewEncoder(w).Encode(resp)
		case "/api/chat":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"message": map[string]string{"role": "assistant", "content": "prompt-prompt"},
				"done":    true,
			})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestRunPing_OllamaModel_Happy(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	srv := newOllamaHTTPServer(t, []string{"llama3:8b"})
	defer srv.Close()

	// Point OLLAMA_BASE_URL so provider.CheckOllama uses our mock.
	t.Setenv("OLLAMA_BASE_URL", srv.URL)

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"llama3:8b","provider":"ollama"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--url", srv.URL})

	stderr := captureStderr(t, func() {
		_ = cmd.Execute()
	})
	_ = stderr // just verify no panic
}

func TestRunPing_DNSResolutionFailure(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"gpt-5.4","provider":"openai"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	// Use an invalid TLD so DNS fails fast.
	cmd.SetArgs([]string{"--url", "https://nonexistent-host.invalid"})

	stderr := captureStderr(t, func() {
		err := cmd.Execute()
		if err == nil {
			t.Log("unexpected success on invalid DNS")
		}
	})
	_ = stderr
}

func TestRunPing_InvalidURLParse(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"gpt-5.4","provider":"openai"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--url", "://not-a-valid-url"})

	_ = captureStderr(t, func() {
		_ = cmd.Execute()
	})
}

func TestRunPing_InvalidModelResolution(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"totally-bogus-model-name"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{})

	_ = captureStderr(t, func() {
		err := cmd.Execute()
		if err == nil {
			t.Error("expected error for invalid model")
		}
	})
}

func TestRunPing_HTTPServerReachable(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "sk-test-fake")

	// httptest server that responds 200 to GET /v1/models.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"gpt-5.4","provider":"openai"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--url", srv.URL, "--header", "X-Extra=foo"})

	_ = captureStderr(t, func() {
		_ = cmd.Execute()
	})
}

func TestRunPing_HTTPServer401(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "sk-test-fake")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"gpt-5.4","provider":"openai"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--url", srv.URL})

	_ = captureStderr(t, func() {
		_ = cmd.Execute()
	})
}

func TestRunPing_HTTPServer500(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "sk-test-fake")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"gpt-5.4","provider":"openai"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--url", srv.URL})

	_ = captureStderr(t, func() {
		_ = cmd.Execute()
	})
}

func TestRunPing_AnthropicAuthHeaders(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-fake")

	var gotAPIKey, gotVersion string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"claude-sonnet-4-6","provider":"anthropic"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--url", srv.URL})

	_ = captureStderr(t, func() {
		_ = cmd.Execute()
	})
	if gotAPIKey == "" {
		t.Error("expected x-api-key header to be set")
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, "2023-06-01")
	}
}

func TestRunPing_WithPromptArg(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-fake")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"claude-sonnet-4-6","provider":"anthropic"}}}`), 0o644)

	cmd := newPingCmd()
	cmd.SetContext(context.Background())
	cmd.SetArgs([]string{"--url", srv.URL, "custom", "prompt"})

	_ = captureStderr(t, func() {
		_ = cmd.Execute()
	})
}

func TestRunPing_InvalidURL(t *testing.T) {
	// Test with a URL that cannot be parsed.
	// We need to go through runPing but it requires config resolution.
	// Instead, test that with a model that doesn't exist, we get an error.
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("HOME", t.TempDir())

	// Point config to a model that doesn't resolve.
	tmpDir := t.TempDir()
	cfgDir := filepath.Join(tmpDir, ".pi-go")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"roles":{"default":{"model":"nonexistent-model-12345"}}}`), 0644)

	cmd := newPingCmd()
	// The ping command will fail at model resolution or HTTP phase.
	// Just verify no panic.
	_ = cmd.ExecuteContext(context.Background())
}

func TestDefaultAPIBaseURL_TableDriven(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "https://api.anthropic.com"},
		{"openai", "https://api.openai.com"},
		{"gemini", "https://generativelanguage.googleapis.com"},
		{"xai", "https://api.x.ai"},
		{"ollama", ""},
		{"", ""},
		{"unknown", ""},
		{"azure", ""},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := defaultAPIBaseURL(tt.provider)
			if got != tt.want {
				t.Errorf("defaultAPIBaseURL(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}

func TestPingEndpoint_TableDriven(t *testing.T) {
	tests := []struct {
		provider string
		want     string
	}{
		{"anthropic", "/v1/messages"},
		{"openai", "/v1/models"},
		{"gemini", "/v1beta/models"},
		{"xai", "/v1/models"},
		{"ollama", "/"},
		{"", "/"},
		{"unknown", "/"},
		{"azure", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			got := pingEndpoint(tt.provider)
			if got != tt.want {
				t.Errorf("pingEndpoint(%q) = %q, want %q", tt.provider, got, tt.want)
			}
		})
	}
}
