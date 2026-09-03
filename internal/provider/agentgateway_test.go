package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAgentGateway(t *testing.T) {
	llm, err := NewAgentGateway(context.Background(), "deepseek-v4-flash:0731-cloud", "", "", nil)
	if err != nil {
		t.Fatalf("NewAgentGateway error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewAgentGateway returned nil")
	}
	if llm.Name() != "deepseek-v4-flash:0731-cloud" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "deepseek-v4-flash:0731-cloud")
	}
	// agentgateway delegates to the OpenAI-compatible client.
	if _, ok := llm.(*openaiModel); !ok {
		t.Errorf("expected *openaiModel, got %T", llm)
	}
}

func TestNewAgentGateway_CustomBaseURL(t *testing.T) {
	llm, err := NewAgentGateway(context.Background(), "deepseek-v4-flash:0731-cloud", "", "http://localhost:9999", nil)
	if err != nil {
		t.Fatalf("NewAgentGateway with custom baseURL: %v", err)
	}
	if llm.Name() != "deepseek-v4-flash:0731-cloud" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "deepseek-v4-flash:0731-cloud")
	}
}

func TestResolveAgentGateway(t *testing.T) {
	info, err := Resolve("agentgateway/deepseek-v4-flash:0731-cloud")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "agentgateway" || info.Model != "deepseek-v4-flash:0731-cloud" {
		t.Fatalf("info = %+v, want agentgateway/deepseek-v4-flash:0731-cloud", info)
	}
	if info.Ollama {
		t.Fatalf("info = %+v, expected Ollama to be false (the -cloud tag must not route to Ollama)", info)
	}
}

func TestResolveAgentGatewayCaseInsensitive(t *testing.T) {
	info, err := Resolve("AGENTGATEWAY/deepseek-v4-flash:0731-cloud")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "agentgateway" || info.Model != "deepseek-v4-flash:0731-cloud" {
		t.Fatalf("info = %+v, want agentgateway/deepseek-v4-flash:0731-cloud", info)
	}
}

func TestContextWindowSizeForAgentGateway(t *testing.T) {
	if got := ContextWindowSizeFor("agentgateway", "deepseek-v4-flash:0731-cloud"); got != 1_000_000 {
		t.Errorf("ContextWindowSizeFor(agentgateway, deepseek-v4-flash:0731-cloud) = %d, want 1000000", got)
	}
}

// TestContextWindowSizeForAgentGatewayLayeredRoutes pins that agentgateway/
// prefix and downstream provider prefixes (gemini/, anthropic/, openai/) are
// stripped in sequence so layered names resolve to the underlying vendor's
// nominal window instead of falling back to 0 (which triggers 256k default).
func TestContextWindowSizeForAgentGatewayLayeredRoutes(t *testing.T) {
	tests := []struct {
		model string
		want  int64
	}{
		{"gemini/gemini-2.5-flash", 1_048_576},
		{"gemini/gemini-3.8-flash", 1_048_576},
		{"gemini-3.8-flash", 1_048_576},
		{"agentgateway/gemini/gemini-2.5-flash", 1_048_576},
		{"agentgateway/gemini/gemini-3.8-flash", 1_048_576},
		{"agentgateway/gemini-3.8-flash", 1_048_576},
		{"agentgateway/anthropic/claude-3-5-sonnet", 200_000},
		{"anthropic/claude-3-5-sonnet", 200_000},
		{"agentgateway/openai/gpt-5.2", 1_050_000},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := ContextWindowSizeFor("agentgateway", tt.model); got != tt.want {
				t.Errorf("ContextWindowSizeFor(agentgateway, %q) = %d, want %d", tt.model, got, tt.want)
			}
			if got := ContextWindowSize(tt.model); got != tt.want {
				t.Errorf("ContextWindowSize(%q) = %d, want %d", tt.model, got, tt.want)
			}
		})
	}
}

// TestContextWindowSizeForAgentGatewayVirtualModels pins that the gateway's
// virtual model names resolve to the deepseek window they route to. A session
// that names the virtual model (e.g. `ollama-deepseek`) must not fall back to a
// 0 window, which disables auto-compaction and lets the session grow unchecked
// until the model hits its output cap.
func TestContextWindowSizeForAgentGatewayVirtualModels(t *testing.T) {
	for _, name := range []string{"ollama-deepseek", "ollama-deepseek-balanced", "pi-fast"} {
		if got := ContextWindowSizeFor("agentgateway", name); got != 1_000_000 {
			t.Errorf("ContextWindowSizeFor(agentgateway, %q) = %d, want 1000000", name, got)
		}
	}
}

// TestListAgentGatewayModels covers the listing path against a stub gateway:
// the OpenAI {"data":[...]} envelope, and the bearer header a gateway that
// requires the optional AGENTGATEWAY_API_KEY would check.
func TestListAgentGatewayModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer agw-key" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer agw-key")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "agentgateway", "owned_by": "openai"},
				{"id": "ollama1/*", "owned_by": "openai"},
			},
		})
	}))
	defer srv.Close()

	models, err := listAgentGatewayModels(context.Background(), ListModelsOptions{
		APIKey:  "agw-key",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("listAgentGatewayModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "agentgateway" {
		t.Errorf("models[0].ID = %q, want agentgateway", models[0].ID)
	}
	// A wildcard route is a real gateway entry and must survive listing.
	if models[1].ID != "ollama1/*" {
		t.Errorf("models[1].ID = %q, want ollama1/*", models[1].ID)
	}
}

// TestListAgentGatewayModelsEnrichesContextWindow pins that the listing path
// fills in the context window from the embedded table, since the gateway's
// /v1/models only carries id/owned_by. This is what makes `pi model list
// agentgateway -o json` show a real context_window for the virtual models.
func TestListAgentGatewayModelsEnrichesContextWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "ollama-deepseek", "owned_by": "openai"},
				{"id": "ollama-gemma4", "owned_by": "openai"},
			},
		})
	}))
	defer srv.Close()

	models, err := listAgentGatewayModels(context.Background(), ListModelsOptions{
		APIKey:  "agw-key",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("listAgentGatewayModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	// ollama-deepseek routes to deepseek-v4-flash:0731-cloud (1M).
	if got := models[0].ContextWindow; got != 1_000_000 {
		t.Errorf("ollama-deepseek ContextWindow = %d, want 1000000", got)
	}
	// ollama-gemma4 routes to gemma4:cloud, which is not in the table → 0.
	if got := models[1].ContextWindow; got != 0 {
		t.Errorf("ollama-gemma4 ContextWindow = %d, want 0 (uncataloged)", got)
	}
}

// TestListModelsRoutesAgentGateway pins that the exported entry point reaches
// the agentgateway branch, not the default "unsupported provider" error.
func TestListModelsRoutesAgentGateway(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{{"id": "agentgateway", "owned_by": "openai"}},
		})
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), "agentgateway", ListModelsOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels(agentgateway): %v", err)
	}
	if len(models) != 1 || models[0].ID != "agentgateway" {
		t.Fatalf("models = %+v, want one entry with ID agentgateway", models)
	}
}

// TestNewAgentGatewayWrapsConstructionError pins that a construction failure
// names agentgateway rather than the OpenAI client it delegates to: a missing
// CA bundle is the realistic trigger, via --ca-cert.
func TestNewAgentGatewayWrapsConstructionError(t *testing.T) {
	_, err := NewAgentGateway(context.Background(), "agentgateway", "", "", &LLMOptions{
		CACertPath: filepath.Join(t.TempDir(), "absent.pem"),
	})
	if err == nil {
		t.Fatal("NewAgentGateway: want an error for an unreadable CA bundle")
	}
	if !strings.Contains(err.Error(), "creating agentgateway client") {
		t.Errorf("error = %q, want it to name agentgateway", err)
	}
}
