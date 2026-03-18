package provider

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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
		llm, err := NewLLM(nil, Info{Provider: "gemini", Model: "gemini-2.5-flash"}, "key", "", "")
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})
	t.Run("creates openai provider", func(t *testing.T) {
		llm, err := NewLLM(nil, Info{Provider: "openai", Model: "gpt-4o"}, "sk-test", "", "")
		if err != nil {
			t.Fatalf("NewLLM() error: %v", err)
		}
		if llm == nil {
			t.Fatal("NewLLM() returned nil")
		}
	})
	t.Run("creates anthropic provider", func(t *testing.T) {
		llm, err := NewLLM(nil, Info{Provider: "anthropic", Model: "claude-sonnet-4-6"}, "sk-test", "", "")
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
	llm, err := NewGemini(nil, "gemini-2.5-flash", "")
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
