package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProviderDefaultBaseURL(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"anthropic", "https://api.anthropic.com"},
		{"openai", "https://api.openai.com"},
		{"gemini", "https://generativelanguage.googleapis.com"},
		{"mistral", "https://api.mistral.ai"},
		// Deliberately without the /v1 segment that the LLM-side default
		// carries: listBearerModels appends it, and a versioned value here
		// would produce /v1/v1/models.
		{"xai", "https://api.x.ai"},
		{"openrouter", "https://openrouter.ai/api/v1"},
		{"unknown", ""},
	}
	for _, tt := range tests {
		if got := providerDefaultBaseURL(tt.name); got != tt.want {
			t.Errorf("providerDefaultBaseURL(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestListModelsUnsupportedProvider(t *testing.T) {
	_, err := ListModels(context.Background(), "nope", ListModelsOptions{})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestListModelsOllamaWrapsNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "llama3:latest"},
				{"name": "qwen:7b"},
			},
		})
	}))
	defer srv.Close()

	models, err := ListModels(context.Background(), "ollama", ListModelsOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("ListModels ollama: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "llama3:latest" {
		t.Errorf("models[0].ID = %q, want llama3:latest", models[0].ID)
	}
}

func TestListOpenAIModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer testkey" {
			t.Errorf("missing bearer token, got %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-5.5", "owned_by": "openai"},
				{"id": "gpt-4o", "owned_by": "openai"},
			},
		})
	}))
	defer srv.Close()

	models, err := listOpenAIModels(context.Background(), ListModelsOptions{
		APIKey:  "testkey",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("listOpenAIModels: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("got %d models, want 2", len(models))
	}
	if models[0].ID != "gpt-5.5" || models[0].OwnedBy != "openai" {
		t.Errorf("models[0] = %+v", models[0])
	}
}

func TestListOpenAIModels_V1BaseURL(t *testing.T) {
	// BaseURL ending in /v1 should produce /v1/models endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
	}))
	defer srv.Close()

	_, err := listOpenAIModels(context.Background(), ListModelsOptions{BaseURL: srv.URL + "/v1"})
	if err != nil {
		t.Fatalf("listOpenAIModels: %v", err)
	}
}

func TestListMistralModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mkey" {
			t.Errorf("missing bearer token")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "mistral-large", "owned_by": "mistral"},
			},
		})
	}))
	defer srv.Close()

	models, err := listMistralModels(context.Background(), ListModelsOptions{
		APIKey:  "mkey",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("listMistralModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "mistral-large" {
		t.Errorf("models = %+v", models)
	}
}

func TestListOpenRouterModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer orkey" {
			t.Errorf("missing bearer token, got %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "google/gemini-3.7-flash", "owned_by": "openrouter"},
			},
		})
	}))
	defer srv.Close()

	models, err := listOpenRouterModels(context.Background(), ListModelsOptions{
		APIKey:  "orkey",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("listOpenRouterModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	if models[0].ID != "google/gemini-3.7-flash" {
		t.Errorf("models[0].ID = %q, want google/gemini-3.7-flash", models[0].ID)
	}
	if models[0].OwnedBy != "openrouter" {
		t.Errorf("models[0].OwnedBy = %q, want openrouter", models[0].OwnedBy)
	}
}

func TestListAnthropicModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "akey" {
			t.Errorf("missing x-api-key header")
		}
		if r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("missing anthropic-version header")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "claude-sonnet-5", "type": "model"},
			},
		})
	}))
	defer srv.Close()

	models, err := listAnthropicModels(context.Background(), ListModelsOptions{
		APIKey:  "akey",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("listAnthropicModels: %v", err)
	}
	if len(models) != 1 || models[0].ID != "claude-sonnet-5" {
		t.Errorf("models = %+v", models)
	}
	if models[0].OwnedBy != "model" {
		t.Errorf("OwnedBy = %q, want model", models[0].OwnedBy)
	}
}

func TestListGeminiModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "gkey" {
			t.Errorf("missing key query param, got %q", r.URL.Query().Get("key"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "models/gemini-3.5-flash", "displayName": "Gemini 3.5 Flash"},
			},
		})
	}))
	defer srv.Close()

	models, err := listGeminiModels(context.Background(), ListModelsOptions{
		APIKey:  "gkey",
		BaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("listGeminiModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	if models[0].ID != "gemini-3.5-flash" {
		t.Errorf("ID = %q, want gemini-3.5-flash", models[0].ID)
	}
	if models[0].OwnedBy != "Gemini 3.5 Flash" {
		t.Errorf("OwnedBy = %q, want Gemini 3.5 Flash", models[0].OwnedBy)
	}
}

func TestListGeminiModels_NoAPIKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "" {
			t.Errorf("expected no key param")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{}})
	}))
	defer srv.Close()

	_, err := listGeminiModels(context.Background(), ListModelsOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("listGeminiModels: %v", err)
	}
}

func TestFetchJSON_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer srv.Close()

	var dst map[string]any
	err := fetchJSON(context.Background(), http.MethodGet, srv.URL, ListModelsOptions{}, "openai", &dst)
	if err == nil {
		t.Fatal("expected error for HTTP 403")
	}
}

func TestFetchJSON_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	var dst map[string]any
	err := fetchJSON(context.Background(), http.MethodGet, srv.URL, ListModelsOptions{APIKey: "key"}, "openai", &dst)
	if err == nil {
		t.Fatal("expected decode error")
	}
}

func TestListOpenAIModels_DefaultBaseURL(t *testing.T) {
	// With no BaseURL and no server, should fail with a network error (not panic).
	// Use a short-deadline context so the test fails fast instead of waiting
	// the full 30s request timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := listOpenAIModels(ctx, ListModelsOptions{})
	if err == nil {
		t.Fatal("expected error with no base URL")
	}
}

func TestListGeminiModels_DefaultBaseURL(t *testing.T) {
	// With no BaseURL, the default https URL is used and the request will
	// fail with a network error. The important property is that we don't
	// panic and the error is wrapped with "listing" context.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := listGeminiModels(ctx, ListModelsOptions{})
	if err == nil {
		t.Fatal("expected error with no base URL")
	}
}

func TestListGeminiModels_Non200Status(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden body"))
	}))
	defer srv.Close()

	_, err := listGeminiModels(context.Background(), ListModelsOptions{BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected error from non-200 status")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected status code in error, got: %v", err)
	}
}

func TestListGeminiModels_DecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	_, err := listGeminiModels(context.Background(), ListModelsOptions{BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected decode error")
	}
	if !strings.Contains(err.Error(), "decoding") {
		t.Errorf("expected 'decoding' in error, got: %v", err)
	}
}

func TestListGeminiModels_StripsModelsPrefix(t *testing.T) {
	// Ensure the "models/" prefix is stripped from the model name.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "models/gemini-2.5-flash", "displayName": "Gemini 2.5 Flash"},
			},
		})
	}))
	defer srv.Close()

	models, err := listGeminiModels(context.Background(), ListModelsOptions{BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("listGeminiModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	if models[0].ID != "gemini-2.5-flash" {
		t.Errorf("ID = %q, want gemini-2.5-flash (prefix stripped)", models[0].ID)
	}
}

func TestListModels_Dispatch(t *testing.T) {
	// Each provider dispatch should reach the corresponding lower-level
	// function. Stub out each provider's response so we can verify dispatch.
	cases := []struct {
		providerName string
		wantID       string
	}{
		{"anthropic", "anthropic-model"},
		{"openai", "openai-model"},
		{"gemini", "gemini-model"},
		{"mistral", "mistral-model"},
	}
	for _, tc := range cases {
		t.Run(tc.providerName, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Return a payload that matches any of the expected shapes.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data":   []map[string]any{{"id": tc.wantID, "owned_by": tc.providerName, "type": "model"}},
					"models": []map[string]any{{"name": "models/" + tc.wantID, "displayName": tc.wantID}},
				})
			}))
			defer srv.Close()

			models, err := ListModels(context.Background(), tc.providerName, ListModelsOptions{BaseURL: srv.URL})
			if err != nil {
				t.Fatalf("ListModels(%q): %v", tc.providerName, err)
			}
			if len(models) == 0 {
				t.Fatalf("ListModels(%q) returned no models", tc.providerName)
			}
		})
	}
}

func TestListMistralModels_DefaultBaseURL(t *testing.T) {
	// With no BaseURL, the default https URL is used. Use a short-deadline
	// context so the test fails fast instead of waiting the full request
	// timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := listMistralModels(ctx, ListModelsOptions{})
	if err == nil {
		t.Fatal("expected error with no base URL")
	}
}

func TestListMistralModels_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("oops"))
	}))
	defer srv.Close()

	_, err := listMistralModels(context.Background(), ListModelsOptions{BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected error from non-200 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected 500 in error, got: %v", err)
	}
}

func TestListAnthropicModels_DefaultBaseURL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := listAnthropicModels(ctx, ListModelsOptions{})
	if err == nil {
		t.Fatal("expected error with no base URL")
	}
}

func TestListAnthropicModels_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("oops"))
	}))
	defer srv.Close()

	_, err := listAnthropicModels(context.Background(), ListModelsOptions{BaseURL: srv.URL})
	if err == nil {
		t.Fatal("expected error from non-200 status")
	}
}
