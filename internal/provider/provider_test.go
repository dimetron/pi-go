package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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
		{"claude-opus-4-6", "anthropic", false},
		{"gpt-4o", "openai", false},
		{"gpt-5.4", "openai", false},
		{"gemini-2.5-pro", "gemini", false},
		{"", "", true},
		{"llama-3", "ollama", false},
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
		if os.Getenv("GOOGLE_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
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

func TestNewGemini(t *testing.T) {
	if os.Getenv("GOOGLE_API_KEY") == "" && os.Getenv("GEMINI_API_KEY") == "" {
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

func TestResolveLocalSuffix(t *testing.T) {
	info, err := Resolve("qwen2.5:local")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "ollama" {
		t.Errorf("provider = %q, want %q", info.Provider, "ollama")
	}
	if !info.Ollama {
		t.Error("expected Ollama = true")
	}
	if info.Model != "qwen2.5:local" {
		t.Errorf("model = %q, want %q", info.Model, "qwen2.5:local")
	}
}

func TestResolveOllamaModelPrefixes(t *testing.T) {
	tests := []struct {
		model     string
		wantModel string
	}{
		{"qwen2.5", "qwen2.5:latest"},
		{"deepseek-coder", "deepseek-coder:latest"},
		{"mistral-7b", "mistral-7b:latest"},
		{"phi-3", "phi-3:latest"},
		{"codellama", "codellama:latest"},
		{"gemma-2", "gemma-2:latest"},
		{"llama3:8b", "llama3:8b"},
		{"minimax-01", "minimax-01:latest"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			info, err := Resolve(tt.model)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Provider != "ollama" {
				t.Errorf("provider = %q, want ollama", info.Provider)
			}
			if !info.Ollama {
				t.Error("expected Ollama = true")
			}
			if info.Model != tt.wantModel {
				t.Errorf("model = %q, want %q", info.Model, tt.wantModel)
			}
		})
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

func TestResolveKnownProviders(t *testing.T) {
	tests := []struct {
		model    string
		provider string
	}{
		{"claude-3-opus", "anthropic"},
		{"gpt-4o-mini", "openai"},
		{"gemini-2.0-flash", "gemini"},
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
			if info.Model != tt.model {
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
	t.Setenv("GOOGLE_API_KEY", "test-google-key")

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
	t.Setenv("GOOGLE_API_KEY", "test-google-key")

	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "https://custom-gemini.example.com", nil)
	if err != nil {
		t.Fatalf("NewGemini() with baseURL error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
}

func TestNewGeminiWithBaseURLAndHeaders(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-google-key")

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

func TestNewGeminiWithGeminiAPIKeyEnv(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "test-gemini-key")

	llm, err := NewGemini(context.TODO(), "gemini-2.5-flash", "", nil)
	if err != nil {
		t.Fatalf("NewGemini() with GEMINI_API_KEY error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewGemini() returned nil")
	}
}

func TestNewGeminiNoAPIKeyEnvVars(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")

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
	t.Run("nil opts returns nil", func(t *testing.T) {
		tr := BuildTransport(nil)
		if tr != nil {
			t.Fatal("expected nil transport")
		}
	})

	t.Run("no customization returns nil", func(t *testing.T) {
		tr := BuildTransport(&LLMOptions{})
		if tr != nil {
			t.Fatal("expected nil transport")
		}
	})

	t.Run("insecure only", func(t *testing.T) {
		tr := BuildTransport(&LLMOptions{InsecureSkipTLS: true})
		if tr == nil {
			t.Fatal("expected non-nil transport")
		}
		// Should be an *http.Transport with InsecureSkipVerify.
		if _, ok := tr.(*http.Transport); !ok {
			t.Fatalf("expected *http.Transport, got %T", tr)
		}
	})

	t.Run("headers only", func(t *testing.T) {
		tr := BuildTransport(&LLMOptions{ExtraHeaders: map[string]string{"X-Test": "val"}})
		if tr == nil {
			t.Fatal("expected non-nil transport")
		}
		if _, ok := tr.(*headerTransport); !ok {
			t.Fatalf("expected *headerTransport, got %T", tr)
		}
	})

	t.Run("insecure + headers chains transports", func(t *testing.T) {
		tr := BuildTransport(&LLMOptions{
			ExtraHeaders:    map[string]string{"X-Test": "val"},
			InsecureSkipTLS: true,
		})
		if tr == nil {
			t.Fatal("expected non-nil transport")
		}
		ht, ok := tr.(*headerTransport)
		if !ok {
			t.Fatalf("expected outer *headerTransport, got %T", tr)
		}
		// Inner transport should be *http.Transport with InsecureSkipVerify.
		if _, ok := ht.base.(*http.Transport); !ok {
			t.Fatalf("expected inner *http.Transport, got %T", ht.base)
		}
	})
}

func TestBuildHTTPClient(t *testing.T) {
	t.Run("nil opts returns default client", func(t *testing.T) {
		c := BuildHTTPClient(nil, 5*time.Second)
		if c == nil {
			t.Fatal("expected non-nil client")
		}
		if c.Timeout != 5*time.Second {
			t.Errorf("timeout = %v, want 5s", c.Timeout)
		}
		if c.Transport != nil {
			t.Error("expected nil transport for default client")
		}
	})

	t.Run("insecure client has custom transport", func(t *testing.T) {
		c := BuildHTTPClient(&LLMOptions{InsecureSkipTLS: true}, 10*time.Second)
		if c.Transport == nil {
			t.Fatal("expected non-nil transport")
		}
		if c.Timeout != 10*time.Second {
			t.Errorf("timeout = %v, want 10s", c.Timeout)
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
	t.Setenv("GOOGLE_API_KEY", "test-google-key")

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
