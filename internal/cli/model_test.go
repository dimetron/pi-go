package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/testenv"
)

// runModelList executes the cobra "model list" command with the given args,
// capturing stdout. It returns the stdout output and any execution error.
func runModelListCapture(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRootCmd()
	cmd.SetArgs(append([]string{"model", "list"}, args...))
	var execErr error
	stdout := captureStdout(t, func() {
		execErr = cmd.Execute()
	})
	return stdout, execErr
}

// isolateRunModelListEnv clears machine-wide credentials that could leak into
// runModelList via loadDotEnv or config lookups, then chdirs into a clean
// temp dir so findNearestDotEnv can't find any .pi-go/.env file.
// It registers a cleanup to restore the previous working directory.
//
// Callers that need specific credentials should set them AFTER calling this
// helper, so those values are not wiped.
func isolateRunModelListEnv(t *testing.T) {
	t.Helper()
	testenv.SetHome(t, t.TempDir())
	// Clear system-level credentials that may leak through loadDotEnv or
	// config.APIKeys/BaseURLs lookups. Do NOT clear vars the caller may have
	// already set; we clear here only the unprefixed "real machine" sources
	// that we know about.
	//
	// Every entry in allProviders needs its key and base-URL vars listed here.
	// The no-args path queries any provider with either one set, so a var left
	// out sends a live request to that vendor from a unit test — which then
	// fails, or passes, depending on whose machine it runs on. loadDotEnv uses
	// os.Setenv, so a value picked up from .pi-go/.env by an earlier
	// non-isolated test outlives that test and reaches this one.
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
		"GEMINI_API_KEY", "GOOGLE_API_KEY",
		"MISTRAL_API_KEY",
		"XAI_API_KEY",
		"AZURE_OPENAI_API_KEY", "AZUREOPENAI_API_KEY", "AZURE_API_KEY",
		"ANTHROPIC_BASE_URL", "GEMINI_BASE_URL", "MISTRAL_BASE_URL",
		"XAI_BASE_URL",
		"OLLAMA_HOST",
		"OPENAI_API_KEY", "OPENAI_BASE_URL",
		"OPENROUTER_API_KEY", "OPENROUTER_BASE_URL",
	} {
		t.Setenv(k, "")
	}
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cleanDir := t.TempDir()
	if err := os.Chdir(cleanDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevDir) })
	flagURL = ""
	flagInsecure = false
}
func TestRunModelList_OpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "gpt-5.5", "owned_by": "openai"},
				{"id": "gpt-4o", "owned_by": "openai"},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "openai", "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "gpt-5.5") {
		t.Errorf("output missing gpt-5.5: %s", out)
	}
	if !strings.Contains(out, "openai (2 models)") {
		t.Errorf("expected 2 models header: %s", out)
	}
}

func TestRunModelList_Anthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "claude-sonnet-5", "type": "model"},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "anthropic", "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "claude-sonnet-5") {
		t.Errorf("output missing claude-sonnet-5: %s", out)
	}
}

func TestRunModelList_OpenRouter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"id": "google/gemini-3.7-flash", "owned_by": "openrouter"},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "openrouter", "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "google/gemini-3.7-flash") {
		t.Errorf("output missing google/gemini-3.7-flash: %s", out)
	}
}

func TestRunModelList_Mistral(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":                 "mistral-large-latest",
					"owned_by":           "mistral",
					"max_context_length": 128000,
					"capabilities": map[string]any{
						"completion_chat": true,
						"vision":          true,
					},
				},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "mistral", "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "mistral-large-latest") {
		t.Errorf("output missing mistral-large-latest: %s", out)
	}
	if !strings.Contains(out, "128K") {
		t.Errorf("output missing context window 128K: %s", out)
	}
	if !strings.Contains(out, "completion_chat,vision") {
		t.Errorf("output missing capabilities: %s", out)
	}
}

func TestRunModelList_MistralJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":                 "mistral-large-latest",
					"owned_by":           "mistral",
					"max_context_length": 128000,
					"capabilities": map[string]any{
						"completion_chat": true,
						"vision":          true,
					},
				},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "mistral", "--url", srv.URL, "-o", "json")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var doc struct {
		Provider  string `json:"provider"`
		FetchedAt string `json:"fetched_at"`
		Models    []struct {
			ID            string   `json:"id"`
			ContextWindow int64    `json:"context_window"`
			Capabilities  []string `json:"capabilities"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output is not a JSON document: %v\n%s", err, out)
	}
	if doc.Provider != "mistral" {
		t.Errorf("provider = %q, want mistral", doc.Provider)
	}
	if doc.FetchedAt == "" {
		t.Error("fetched_at is empty")
	}
	if len(doc.Models) != 1 || doc.Models[0].ID != "mistral-large-latest" {
		t.Fatalf("models = %+v", doc.Models)
	}
	if doc.Models[0].ContextWindow != 128000 {
		t.Errorf("context_window = %d, want 128000", doc.Models[0].ContextWindow)
	}
	if len(doc.Models[0].Capabilities) != 2 {
		t.Errorf("capabilities = %v, want 2 entries", doc.Models[0].Capabilities)
	}
}

func TestRunModelList_Gemini(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "models/gemini-3.5-flash", "displayName": "Gemini 3.5 Flash"},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "gemini", "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "gemini-3.5-flash") {
		t.Errorf("output missing gemini-3.5-flash: %s", out)
	}
}

// Azure lists from the embedded catalog and issues no request at all: there is
// no key-authenticated route that enumerates deployments. The stub server is
// here to prove it stays untouched even when --url points at one.
func TestRunModelList_Azure(t *testing.T) {
	isolateRunModelListEnv(t)
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{}})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "azure", "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if hits != 0 {
		t.Errorf("made %d requests, want 0 — the azure catalog is embedded", hits)
	}
	for _, want := range []string{
		"deployments, from the embedded catalog",
		"gpt-5.6-luna",
		"1.05M context",
		"--model azure/<deployment>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// A user with only Azure configured previously got "no providers configured",
// because azure is absent from allProviders.
func TestRunModelList_NoArgs_AzureOnly(t *testing.T) {
	// The no-args path always queries ollama, so it needs a stub to reach —
	// otherwise the run fails on ollama and never says anything about azure.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
	}))
	defer srv.Close()
	isolateRunModelListEnv(t)
	t.Setenv("AZURE_OPENAI_API_KEY", "testkey")
	t.Setenv("OLLAMA_HOST", srv.URL)

	out, err := runModelListCapture(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "gpt-5.6-luna") {
		t.Errorf("no-args listing omitted the azure catalog:\n%s", out)
	}
}

func TestHumanTokens(t *testing.T) {
	cases := map[int64]string{
		1_050_000: "1.05M",
		1_000_000: "1.00M",
		272_000:   "272K",
		8_000:     "8K",
		32_768:    "32K",
		512:       "512",
	}
	for n, want := range cases {
		if got := humanTokens(n); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPrintAzureDeployments(t *testing.T) {
	var sb strings.Builder
	printAzureDeployments(&sb)
	out := sb.String()

	want := len(provider.AzureDeployments())
	if !strings.Contains(out, fmt.Sprintf("azure (%d deployments", want)) {
		t.Errorf("header does not report %d deployments:\n%s", want, out)
	}
	// The window is the whole point of showing this table — a deployment name
	// alone says nothing that --model does not already.
	if !strings.Contains(out, "gpt-4") || !strings.Contains(out, "context") {
		t.Errorf("output missing deployment rows:\n%s", out)
	}
}

func TestRunModelList_UnknownProvider(t *testing.T) {
	_, err := runModelListCapture(t, "nope")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRunModelList_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "server error")
	}))
	defer srv.Close()

	_, err := runModelListCapture(t, "openai", "--url", srv.URL)
	if err == nil {
		t.Fatal("expected error from failed API")
	}
}

func TestRunModelList_Ollama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "llama3:latest"},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "ollama", "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "llama3:latest") {
		t.Errorf("output missing llama3:latest: %s", out)
	}
}

// TestRunModelList_NoArgs_OllamaOnly verifies that without args, ollama is
// always queried (the short-circuit branch in the allProviders loop) and
// the localhost default URL is used (the baseURL == "" && p == "ollama" branch).
func TestRunModelList_NoArgs_OllamaOnly(t *testing.T) {
	isolateRunModelListEnv(t)
	// The shared --url flag is applied to ollama too, so a stub server is
	// sufficient to exercise the ollama-only no-args path.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "stub-model"},
			},
		})
	}))
	defer srv.Close()

	out, err := runModelListCapture(t, "--url", srv.URL)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "stub-model") {
		t.Errorf("output missing stub-model from ollama: %s", out)
	}
	if !strings.Contains(out, "ollama (1 models)") {
		t.Errorf("expected ollama header in output: %s", out)
	}
}

// TestRunModelList_NoArgs_EnvBaseURL covers the env-derived base URL branch
// for the no-args case where --url is not set, and ensures non-ollama
// providers are added to the query list when their base URL is configured.
func TestRunModelList_NoArgs_EnvBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Distinguish openai from ollama by request path so ollama's
		// localhost query doesn't accidentally satisfy the openai assertion.
		if strings.HasSuffix(r.URL.Path, "/v1/models") {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{{"id": "gpt-5.5"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
	}))
	defer srv.Close()
	isolateRunModelListEnv(t)
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	t.Setenv("OPENAI_API_KEY", "testkey")
	// Point ollama at the mock too so the no-args branch (which always
	// queries ollama) does not depend on a real ollama server.
	t.Setenv("OLLAMA_HOST", srv.URL)

	out, err := runModelListCapture(t)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "gpt-5.5") {
		t.Errorf("output missing gpt-5.5: %s", out)
	}
	if !strings.Contains(out, "openai (1 models)") {
		t.Errorf("expected openai header in output: %s", out)
	}
}
