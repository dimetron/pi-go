package provider

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	tests := []struct {
		model    string
		wantProv string
		wantErr  bool
	}{
		{"claude-sonnet-4-6", "anthropic", false},
		{"claude-opus-4-7", "anthropic", false},
		{"gpt-4o", "openai", false},
		{"gpt-5.5", "openai", false},
		{"gemini-2.5-pro", "gemini", false},
		{"gemini-3.5-flash", "gemini", false},
		{"gemini-3.1-flash-lite", "gemini", false},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for model %q", tt.model)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != tt.wantProv {
				t.Errorf("got provider %q, want %q", info.Provider, tt.wantProv)
			}
			wantModel := tt.model
			// Ollama models without a tag get :latest appended.
			if info.Ollama && !strings.Contains(tt.model, ":") {
				wantModel = tt.model + ":latest"
			}
			if info.Model != wantModel {
				t.Errorf("got model %q, want %q", info.Model, wantModel)
			}
		})
	}
}

func TestNewLLMWithProvider(t *testing.T) {
	t.Run("creates gemini provider", func(t *testing.T) {
		if os.Getenv("GEMINI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
			t.Skip("skipping: no Google/Gemini API key set")
		}
		llm, err := NewLLM(context.TODO(), Info{Provider: "gemini", Model: "gemini-2.5-flash"}, "key", "", "", nil)
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})
	t.Run("creates openai provider", func(t *testing.T) {
		llm, err := NewLLM(context.TODO(), Info{Provider: "openai", Model: "gpt-4o"}, "sk-test", "", "", nil)
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})
	t.Run("creates anthropic provider", func(t *testing.T) {
		llm, err := NewLLM(context.TODO(), Info{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "sk-test", "", "", nil)
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})
}

func TestResolveWithOllamaPrefix(t *testing.T) {
	info, err := Resolve("ollama/llama3:8b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "ollama" {
		t.Errorf("provider = %q, want ollama", info.Provider)
	}
	if info.Ollama != true {
		t.Error("expected Ollama = true")
	}
}

func TestResolveWithAzurePrefix(t *testing.T) {
	info, err := Resolve("azure/gpt-4o-deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "azure" {
		t.Errorf("provider = %q, want azure", info.Provider)
	}
	if info.Model != "gpt-4o-deployment" {
		t.Errorf("model = %q, want %q", info.Model, "gpt-4o-deployment")
	}
}

func TestResolveWithAzurePrefixCaseInsensitive(t *testing.T) {
	info, err := Resolve("AZURE/my-deployment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "azure" {
		t.Errorf("provider = %q, want azure", info.Provider)
	}
	if info.Model != "my-deployment" {
		t.Errorf("model = %q, want %q", info.Model, "my-deployment")
	}
}

func TestResolveWithAnthropicPrefix(t *testing.T) {
	info, err := Resolve("anthropic/claude-fable-5-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", info.Provider)
	}
	if info.Model != "claude-fable-5-1" {
		t.Errorf("model = %q, want %q", info.Model, "claude-fable-5-1")
	}
}

func TestResolveWithAnthropicPrefixCaseInsensitive(t *testing.T) {
	info, err := Resolve("ANTHROPIC/claude-fable-5-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "anthropic" {
		t.Errorf("provider = %q, want anthropic", info.Provider)
	}
	if info.Model != "claude-fable-5-1" {
		t.Errorf("model = %q, want %q", info.Model, "claude-fable-5-1")
	}
}

func TestStripKnownProviderPrefixes(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gpt-5.4", "gpt-5.4"},
		{"openai/gpt-5.6-luna", "gpt-5.6-luna"},
		{"OPENAI/gpt-5.6-luna", "gpt-5.6-luna"},
		{"agentgateway/openai/gpt-5.6-luna", "gpt-5.6-luna"},
		{"agentgateway/openrouter/anthropic/claude-3-5-sonnet", "claude-3-5-sonnet"},
		{"agentgateway/gemini/Gemini-3.8-Flash", "Gemini-3.8-Flash"},
		{"azure/gpt-4o", "gpt-4o"},
		{"ollama/llama3.3", "llama3.3"},
		{"ollama-cloud/deepseek-v3", "deepseek-v3"},
		{"mycorp/custom-model", "mycorp/custom-model"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := StripKnownProviderPrefixes(tt.input); got != tt.want {
				t.Errorf("StripKnownProviderPrefixes(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestResolveMistralPrefixStripped(t *testing.T) {
	tests := []struct {
		model     string
		wantProv  string
		wantModel string
	}{
		{"mistral/codestral-2508", "mistral", "codestral-2508"},
		{"mistral/mistral-small-latest", "mistral", "mistral-small-latest"},
		{"MISTRAL/large", "mistral", "large"},
		// Bare mistral-* names auto-route via the modelPrefixes map with the
		// full model name preserved.
		{"mistral-large-latest", "mistral", "mistral-large-latest"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != tt.wantProv {
				t.Errorf("provider = %q, want %q", info.Provider, tt.wantProv)
			}
			if info.Model != tt.wantModel {
				t.Errorf("model = %q, want %q", info.Model, tt.wantModel)
			}
		})
	}
}

func TestCheckOllamaUnreachable(t *testing.T) {
	// Port 19 (chargen) is almost certainly not running Ollama.
	err := CheckOllama("http://localhost:19")
	if err == nil {
		t.Fatal("expected error for unreachable Ollama")
	}
}

func TestCheckOllamaInvalidURL(t *testing.T) {
	err := CheckOllama("://bad")
	if err == nil {
		t.Fatal("expected error for invalid URL")
	}
}

func TestCheckOllamaWrongStatus(t *testing.T) {
	// Start a local server that returns 500.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := CheckOllama(srv.URL)
	if err == nil {
		t.Fatal("expected error for non-200 status")
	}
}

func TestCheckOllamaOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ollama is running"))
	}))
	defer srv.Close()

	err := CheckOllama(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckOllamaOKNoScheme(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ollama is running"))
	}))
	defer srv.Close()

	// Extract host:port from the server URL (e.g. "http://127.0.0.1:PORT" -> "127.0.0.1:PORT")
	u, _ := url.Parse(srv.URL)
	noScheme := u.Host // "127.0.0.1:PORT" — no scheme

	err := CheckOllama(noScheme)
	if err != nil {
		t.Fatalf("unexpected error for URL %q: %v", noScheme, err)
	}
}

func TestNewGemini(t *testing.T) {
	if os.Getenv("GEMINI_API_KEY") == "" && os.Getenv("GOOGLE_API_KEY") == "" {
		t.Skip("skipping: no Google/Gemini API key set")
	}
	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "", nil)
	if err != nil {
		t.Fatalf("NewGemini() error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
	if llm.Name() != "gemini-2.5-flash" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "gemini-2.5-flash")
	}
}

func TestResolveOllamaCloudSuffix(t *testing.T) {
	info, err := Resolve("qwen2.5:cloud")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "ollama" {
		t.Errorf("provider = %q, want %q", info.Provider, "ollama")
	}
	if !info.Ollama {
		t.Error("expected Ollama = true")
	}
	if info.Model != "qwen2.5:cloud" {
		t.Errorf("model = %q, want %q", info.Model, "qwen2.5:cloud")
	}
}

func TestResolveOllamaRequiresExplicitPrefix(t *testing.T) {
	for _, model := range []string{"qwen2.5", "deepseek-coder", "phi-3", "codellama", "gemma-2", "llama3:8b", "minimax-01", "qwen2.5:local"} {
		t.Run(model, func(t *testing.T) {
			_, err := Resolve(model)
			if err == nil {
				t.Fatalf("expected error for bare Ollama model %q", model)
			}
		})
	}
}

func TestValidateModel(t *testing.T) {
	// Isolate from the real user cache so CatalogFor falls back to the
	// embedded snapshot.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home+"/.cache")
	t.Setenv("MISTRAL_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("XAI_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "")
	tests := []struct {
		info    Info
		wantErr bool
	}{
		// Valid cloud models.
		{Info{Provider: "anthropic", Model: "claude-sonnet-4-6"}, false},
		{Info{Provider: "anthropic", Model: "claude-opus-4-7"}, false},
		{Info{Provider: "anthropic", Model: "claude-haiku-4-5"}, false},
		{Info{Provider: "openai", Model: "gpt-5.5"}, false},
		{Info{Provider: "openai", Model: "gpt-5.4"}, false},
		{Info{Provider: "gemini", Model: "gemini-2.5-pro"}, false},
		{Info{Provider: "gemini", Model: "gemini-2.5-flash"}, false},
		{Info{Provider: "gemini", Model: "gemini-3.5-flash"}, false},
		{Info{Provider: "gemini", Model: "gemini-3.1-flash-lite"}, false},
		{Info{Provider: "mistral", Model: "mistral-large-latest"}, false},
		{Info{Provider: "mistral", Model: "codestral"}, false},
		{Info{Provider: "mistral", Model: "pixtral"}, false},
		{Info{Provider: "mistral", Model: "ministral"}, false},
		// Ollama models are always valid.
		{Info{Provider: "ollama", Model: "whatever:latest", Ollama: true}, false},
		// Unknown provider is always valid (no known list).
		{Info{Provider: "custom", Model: "some-model"}, false},
		// Invalid models.
		{Info{Provider: "anthropic", Model: "bogus-model"}, true},
		{Info{Provider: "openai", Model: "davinci-002"}, true},
		{Info{Provider: "gemini", Model: "palm-2"}, true},
		{Info{Provider: "mistral", Model: "llama-3"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.info.Provider+"/"+tt.info.Model, func(t *testing.T) {
			err := ValidateModel(tt.info)
			if tt.wantErr && err == nil {
				t.Errorf("expected error for %s model %q", tt.info.Provider, tt.info.Model)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateModel_CacheHit(t *testing.T) {
	// A model not in the embedded snapshot passes when the XDG cache contains it.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home+"/.cache")
	t.Setenv("MISTRAL_API_KEY", "")
	dir := modelsCacheDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cf := catalogFile{
		Provider:  "mistral",
		FetchedAt: "2026-08-27T00:00:00Z",
		Models:    []ModelInfo{{ID: "codestral-2508"}},
	}
	b, _ := json.Marshal(cf)
	if err := os.WriteFile(cachePath("mistral"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModel(Info{Provider: "mistral", Model: "codestral-2508"}); err != nil {
		t.Errorf("ValidateModel with cached model: %v", err)
	}
}

func TestValidateModel_NoKeyNoNetwork(t *testing.T) {
	// A model in neither list and no key → error; no network call (no server hit).
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home+"/.cache")
	t.Setenv("MISTRAL_API_KEY", "")
	err := ValidateModel(Info{Provider: "mistral", Model: "llama-3"})
	if err == nil {
		t.Fatal("expected error for unknown model with no key")
	}
	if !strings.Contains(err.Error(), "mistral") || !strings.Contains(err.Error(), "llama-3") {
		t.Errorf("error should mention provider and model: %v", err)
	}
}

// refreshOnMissServer serves a single Mistral model and reports whether it was
// ever asked. The ID must be absent from both the embedded snapshot and the
// hard-coded KnownModels list, or ValidateModel matches before the refresh and
// the test proves nothing.
func refreshOnMissServer(t *testing.T, id string) (*httptest.Server, *bool) {
	t.Helper()
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":                 id,
					"owned_by":           "mistral",
					"max_context_length": 256000,
					"capabilities":       map[string]any{"completion_chat": true},
				},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &called
}

const refreshOnMissModel = "zz-not-in-any-catalog-2599"

func TestValidateModel_RefreshOnMiss(t *testing.T) {
	withTempCacheDir(t)
	// Guard the premise: if the ID ever lands in a catalog, this test would
	// pass without the refresh running at all.
	if matchPrefix(CatalogFor("mistral"), refreshOnMissModel) {
		t.Fatalf("%q is now a known mistral model; pick another sentinel", refreshOnMissModel)
	}
	srv, called := refreshOnMissServer(t, refreshOnMissModel)
	t.Setenv("MISTRAL_API_KEY", "testkey")
	t.Setenv("MISTRAL_BASE_URL", srv.URL)

	if err := ValidateModel(Info{Provider: "mistral", Model: refreshOnMissModel}); err != nil {
		t.Errorf("ValidateModel after refresh: %v", err)
	}
	if !*called {
		t.Error("the provider was never contacted; the refresh path did not run")
	}
}

// TestValidateModel_RefreshOnMissWithoutCache pins that validation depends on
// what the provider returned, not on whether it could be cached. RefreshCatalog
// hands back the fetched models even when there is nowhere to persist them, and
// re-reading CatalogFor at that point would see only the embedded snapshot.
func TestValidateModel_RefreshOnMissWithoutCache(t *testing.T) {
	srv, called := refreshOnMissServer(t, refreshOnMissModel)
	withUnresolvableCacheDir(t)
	t.Setenv("MISTRAL_API_KEY", "testkey")
	t.Setenv("MISTRAL_BASE_URL", srv.URL)

	if err := ValidateModel(Info{Provider: "mistral", Model: refreshOnMissModel}); err != nil {
		t.Errorf("ValidateModel with no usable cache: %v", err)
	}
	if !*called {
		t.Error("the provider was never contacted; the refresh path did not run")
	}
}

// TestValidateModel_RefreshOnMissStillRejectsUnknown keeps the refresh from
// becoming a blanket pass: a model the provider does not list stays rejected.
func TestValidateModel_RefreshOnMissStillRejectsUnknown(t *testing.T) {
	withTempCacheDir(t)
	srv, called := refreshOnMissServer(t, refreshOnMissModel)
	t.Setenv("MISTRAL_API_KEY", "testkey")
	t.Setenv("MISTRAL_BASE_URL", srv.URL)

	if err := ValidateModel(Info{Provider: "mistral", Model: "zz-something-else-entirely"}); err == nil {
		t.Error("ValidateModel: want an error for a model the refresh did not return")
	}
	if !*called {
		t.Error("the provider was never contacted; the refresh path did not run")
	}
}

func TestValidateModel_UnknownProvider(t *testing.T) {
	if err := ValidateModel(Info{Provider: "nope", Model: "whatever"}); err != nil {
		t.Errorf("unknown provider should skip validation, got: %v", err)
	}
}

func TestResolveUnknownModel(t *testing.T) {
	_, err := Resolve("totally-unknown-model")
	if err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestResolveOllamaPrefixStripsPrefix(t *testing.T) {
	info, err := Resolve("ollama/my-custom-model:v2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "ollama" {
		t.Errorf("provider = %q, want ollama", info.Provider)
	}
	if info.Model != "my-custom-model:v2" {
		t.Errorf("model = %q, want my-custom-model:v2", info.Model)
	}
	if !info.Ollama {
		t.Error("expected Ollama = true")
	}
}

func TestResolveOllamaPrefixCaseInsensitive(t *testing.T) {
	info, err := Resolve("Ollama/MyModel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "ollama" {
		t.Errorf("provider = %q, want ollama", info.Provider)
	}
	if info.Model != "MyModel" {
		t.Errorf("model = %q, want MyModel", info.Model)
	}
}

func TestResolveWithBaseURLPrefersCustomOpenAIForAmbiguousModel(t *testing.T) {
	info, err := ResolveWithBaseURL("qwen-3.6", "http://127.0.0.1:2276/v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "openai" {
		t.Errorf("provider = %q, want openai", info.Provider)
	}
	if info.Model != "qwen-3.6" {
		t.Errorf("model = %q, want qwen-3.6", info.Model)
	}
	if info.Ollama {
		t.Error("expected Ollama = false for custom OpenAI-compatible endpoint")
	}
	if !info.Custom {
		t.Error("expected Custom = true")
	}
}

func TestResolveWithBaseURLKeepsExplicitOllamaPrefix(t *testing.T) {
	info, err := ResolveWithBaseURL("ollama/qwen-3.6", "http://127.0.0.1:11434")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "ollama" || info.Model != "qwen-3.6" || !info.Ollama {
		t.Fatalf("info = %+v, want explicit Ollama model", info)
	}
}

func TestResolveOpenRouter(t *testing.T) {
	info, err := Resolve("openrouter/google/gemini-3.7-flash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "openrouter" || info.Model != "google/gemini-3.7-flash" {
		t.Fatalf("info = %+v, want openrouter/google/gemini-3.7-flash", info)
	}
	if info.Ollama || info.Custom {
		t.Fatalf("info = %+v, expected Ollama and Custom to be false", info)
	}

	info, err = Resolve("openrouter/auto")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "openrouter" || info.Model != "auto" {
		t.Fatalf("info = %+v, want openrouter/auto", info)
	}

	info, err = ResolveWithBaseURL("openrouter/auto", "https://custom.example")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "openrouter" || info.Model != "auto" {
		t.Fatalf("info = %+v, want provider openrouter with model auto", info)
	}
	if info.Custom {
		t.Fatalf("info = %+v, expected Custom to be false for openrouter", info)
	}
}

func TestResolveKnownProviders(t *testing.T) {
	tests := []struct {
		model    string
		provider string
	}{
		{"claude-opus-4-7", "anthropic"},
		{"gpt-5.5", "openai"},
		{"gemini-2.5-flash", "gemini"},
		{"gemini-3.5-flash", "gemini"},
		{"gemini-3.1-flash-lite", "gemini"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != tt.provider {
				t.Errorf("provider = %q, want %q", info.Provider, tt.provider)
			}
			if info.Model != tt.model && (tt.model != "openai/qwen-3.6" || info.Model != "qwen-3.6") {
				t.Errorf("model = %q, want %q", info.Model, tt.model)
			}
			if info.Ollama {
				t.Error("expected Ollama = false for cloud provider")
			}
		})
	}
}

func TestNewLLMUnsupportedProvider(t *testing.T) {
	_, err := NewLLM(context.Background(), Info{Provider: "unsupported", Model: "test"}, "key", "", "", nil)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestNewLLMWithExtraHeaders(t *testing.T) {
	opts := &LLMOptions{ExtraHeaders: map[string]string{
		"X-Custom":      "value1",
		"X-Application": "test-app",
	}}

	t.Run("openai with extra headers", func(t *testing.T) {
		llm, err := NewLLM(context.Background(), Info{Provider: "openai", Model: "gpt-4o"}, "sk-test", "", "", opts)
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})

	t.Run("anthropic with extra headers", func(t *testing.T) {
		llm, err := NewLLM(context.Background(), Info{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "sk-test", "", "", opts)
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})

	t.Run("ollama with extra headers", func(t *testing.T) {
		llm, err := NewLLM(context.Background(), Info{Provider: "ollama", Model: "test-model", Ollama: true}, "", "http://localhost:11434", "", opts)
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})

	t.Run("nil opts", func(t *testing.T) {
		llm, err := NewLLM(context.Background(), Info{Provider: "openai", Model: "gpt-4o"}, "sk-test", "", "", nil)
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})

	t.Run("empty opts", func(t *testing.T) {
		llm, err := NewLLM(context.Background(), Info{Provider: "openai", Model: "gpt-4o"}, "sk-test", "", "", &LLMOptions{})
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})

	t.Run("insecure TLS", func(t *testing.T) {
		llm, err := NewLLM(context.Background(), Info{Provider: "openai", Model: "gpt-4o"}, "sk-test", "", "", &LLMOptions{InsecureSkipTLS: true})
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})

	t.Run("insecure TLS with headers", func(t *testing.T) {
		llm, err := NewLLM(context.Background(), Info{Provider: "openai", Model: "gpt-4o"}, "sk-test", "", "", &LLMOptions{
			ExtraHeaders:    map[string]string{"X-Test": "val"},
			InsecureSkipTLS: true,
		})
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})
}

func TestNewGeminiWithExtraHeaders(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-google-key")

	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "", &LLMOptions{
		ExtraHeaders: map[string]string{
			"X-Custom-Header": "value1",
			"X-Another":       "value2",
		},
	})
	if err != nil {
		t.Fatalf("NewGemini() with headers error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
}

func TestNewGeminiWithBaseURL(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-google-key")

	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "https://custom-gemini.example.com", nil)
	if err != nil {
		t.Fatalf("NewGemini() with baseURL error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
}

func TestNewGeminiWithBaseURLAndHeaders(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "test-google-key")

	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "https://custom.example.com", &LLMOptions{
		ExtraHeaders: map[string]string{"X-Custom": "val"},
	})
	if err != nil {
		t.Fatalf("NewGemini() with baseURL+headers error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
}

func TestNewGeminiWithGoogleAPIKeyFallback(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "test-google-key")

	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "", nil)
	if err != nil {
		t.Fatalf("NewGemini() with GOOGLE_API_KEY fallback error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
}

func TestNewGeminiNoAPIKeyEnvVars(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")

	// Without API keys, NewGemini may still succeed (using ADC) or fail depending on environment.
	// We just verify it doesn't panic.
	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "", nil)
	_ = llm
	_ = err
}

func TestHeaderTransport(t *testing.T) {
	headers := map[string]string{
		"X-Username":    "dimetron",
		"X-Application": "kagent",
	}

	// Create a test server that echoes back the received headers.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			got := r.Header.Get(k)
			if got != v {
				http.Error(w, "missing header "+k, http.StatusBadRequest)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	transport := &headerTransport{
		base:    http.DefaultTransport,
		headers: headers,
	}
	client := &http.Client{Transport: transport}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestBuildTransport(t *testing.T) {
	mustBuild := func(t *testing.T, opts *LLMOptions) http.RoundTripper {
		t.Helper()
		tr, err := BuildTransport(opts)
		if err != nil {
			t.Fatalf("BuildTransport: %v", err)
		}
		return tr
	}

	t.Run("nil opts returns nil", func(t *testing.T) {
		if tr := mustBuild(t, nil); tr != nil {
			t.Fatal("expected nil transport")
		}
	})

	t.Run("no customization returns nil", func(t *testing.T) {
		if tr := mustBuild(t, &LLMOptions{}); tr != nil {
			t.Fatal("expected nil transport")
		}
	})

	t.Run("insecure only", func(t *testing.T) {
		tr := mustBuild(t, &LLMOptions{InsecureSkipTLS: true})
		ht, ok := tr.(*http.Transport)
		if !ok {
			t.Fatalf("expected *http.Transport, got %T", tr)
		}
		if ht.TLSClientConfig == nil || !ht.TLSClientConfig.InsecureSkipVerify {
			t.Error("expected InsecureSkipVerify")
		}
		// The clone must keep the settings of http.DefaultTransport — losing
		// Proxy here would silently break HTTPS_PROXY users.
		if ht.Proxy == nil {
			t.Error("cloned transport lost Proxy (HTTPS_PROXY would stop working)")
		}
	})

	t.Run("headers only", func(t *testing.T) {
		tr := mustBuild(t, &LLMOptions{ExtraHeaders: map[string]string{"X-Test": "val"}})
		if _, ok := tr.(*headerTransport); !ok {
			t.Fatalf("expected *headerTransport, got %T", tr)
		}
	})

	t.Run("insecure + headers chains transports", func(t *testing.T) {
		tr := mustBuild(t, &LLMOptions{
			ExtraHeaders:    map[string]string{"X-Test": "val"},
			InsecureSkipTLS: true,
		})
		ht, ok := tr.(*headerTransport)
		if !ok {
			t.Fatalf("expected outer *headerTransport, got %T", tr)
		}
		if _, ok := ht.base.(*http.Transport); !ok {
			t.Fatalf("expected inner *http.Transport, got %T", ht.base)
		}
	})

	t.Run("connect timeout alone customizes the dialer", func(t *testing.T) {
		tr := mustBuild(t, &LLMOptions{ConnectTimeout: 2 * time.Second})
		ht, ok := tr.(*http.Transport)
		if !ok {
			t.Fatalf("expected *http.Transport, got %T", tr)
		}
		if ht.DialContext == nil {
			t.Error("expected a custom DialContext")
		}
	})

	t.Run("missing CA file reports the path", func(t *testing.T) {
		_, err := BuildTransport(&LLMOptions{CACertPath: filepath.Join(t.TempDir(), "absent.pem")})
		if err == nil {
			t.Fatal("expected an error for a missing CA bundle")
		}
		if !strings.Contains(err.Error(), "absent.pem") {
			t.Errorf("error should name the file, got %v", err)
		}
	})

	t.Run("non-PEM CA file is rejected", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "junk.pem")
		if err := os.WriteFile(path, []byte("not a certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := BuildTransport(&LLMOptions{CACertPath: path}); err == nil {
			t.Fatal("expected an error for a file with no PEM block")
		}
	})

	t.Run("insecure wins over CA path", func(t *testing.T) {
		tr := mustBuild(t, &LLMOptions{
			InsecureSkipTLS: true,
			CACertPath:      filepath.Join(t.TempDir(), "absent.pem"), // never read
		})
		ht := tr.(*http.Transport)
		if !ht.TLSClientConfig.InsecureSkipVerify {
			t.Error("expected InsecureSkipVerify to win")
		}
	})
}

// TestBuildTransportCustomCA drives a real TLS handshake against a server whose
// certificate is signed by a CA the system roots do not know.
func TestBuildTransportCustomCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: srv.Certificate().Raw,
	})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// Without the CA the handshake must fail...
	plain := &http.Client{Timeout: 5 * time.Second}
	if _, err := plain.Get(srv.URL); err == nil { //nolint:bodyclose // request is expected to fail
		t.Fatal("expected the untrusted certificate to be rejected")
	}

	for _, disableSystemCAs := range []bool{false, true} {
		client, err := BuildHTTPClient(&LLMOptions{
			CACertPath:       caPath,
			DisableSystemCAs: disableSystemCAs,
		}, 5*time.Second)
		if err != nil {
			t.Fatalf("BuildHTTPClient(disableSystemCAs=%v): %v", disableSystemCAs, err)
		}
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("GET with custom CA (disableSystemCAs=%v): %v", disableSystemCAs, err)
		}
		resp.Body.Close() //nolint:errcheck
		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	}
}

// TestHeaderTransportDoesNotMutateRequest pins the RoundTripper contract: the
// request handed in belongs to the caller, and the SDKs replay it on retry.
func TestHeaderTransportDoesNotMutateRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Test"); got != "val" {
			t.Errorf("X-Test header = %q, want val", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := BuildHTTPClient(&LLMOptions{ExtraHeaders: map[string]string{"X-Test": "val"}}, 5*time.Second)
	if err != nil {
		t.Fatalf("BuildHTTPClient: %v", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close() //nolint:errcheck

	if got := req.Header.Get("X-Test"); got != "" {
		t.Errorf("caller's request was mutated: X-Test = %q", got)
	}
}

func TestBuildHTTPClient(t *testing.T) {
	t.Run("nil opts returns default client", func(t *testing.T) {
		c, err := BuildHTTPClient(nil, 5*time.Second)
		if err != nil {
			t.Fatalf("BuildHTTPClient: %v", err)
		}
		if c.Timeout != 5*time.Second {
			t.Errorf("timeout = %v, want 5s", c.Timeout)
		}
		if c.Transport != nil {
			t.Error("expected nil transport for default client")
		}
	})

	t.Run("insecure client has custom transport", func(t *testing.T) {
		c, err := BuildHTTPClient(&LLMOptions{InsecureSkipTLS: true}, 10*time.Second)
		if err != nil {
			t.Fatalf("BuildHTTPClient: %v", err)
		}
		if c.Transport == nil {
			t.Fatal("expected non-nil transport")
		}
		if c.Timeout != 10*time.Second {
			t.Errorf("timeout = %v, want 10s", c.Timeout)
		}
	})

	t.Run("bad CA path is surfaced, not swallowed", func(t *testing.T) {
		if _, err := BuildHTTPClient(&LLMOptions{CACertPath: "/nonexistent/ca.pem"}, time.Second); err == nil {
			t.Fatal("expected an error")
		}
	})
}

func TestCheckOllamaHTTPSPortInference(t *testing.T) {
	// Start a TLS test server to exercise the https host+":443" port-inference branch.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Ollama is running")) //nolint:errcheck
	}))
	defer srv.Close()

	// srv.URL is already "https://127.0.0.1:<port>", so use just the host without port
	// to hit the "no colon → append :443" branch in CheckOllama.
	// We can't actually test the real :443 without network, but we can test that
	// a URL like "https://127.0.0.1" (no port) triggers the port-inference path
	// by passing an HTTPS URL without a port that will fail at TCP dial - which
	// is fine because we just need to exercise that code branch.
	err := CheckOllama("https://192.0.2.1") // TEST-NET-1, guaranteed unreachable
	// We expect an error (TCP dial failure), but the https port-inference path was exercised.
	if err == nil {
		t.Fatal("expected error for unreachable HTTPS host")
	}
	if !strings.Contains(err.Error(), ":443") {
		t.Errorf("expected error to mention :443 port, got: %v", err)
	}
}

func TestNewGeminiInsecureTLSOnly(t *testing.T) {
	// Exercise the InsecureSkipTLS path in NewGemini without extra headers.
	t.Setenv("GEMINI_API_KEY", "test-google-key")

	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "", &LLMOptions{
		InsecureSkipTLS: true,
	})
	if err != nil {
		t.Fatalf("NewGemini() with InsecureSkipTLS error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
}
