package ctxwindow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	info := provider.Info{Provider: "anthropic", Model: "claude-sonnet-4-6"}
	catalog := provider.ContextWindowSizeFor(info.Provider, info.Model)

	tests := []struct {
		name string
		cfg  config.Config
		want int64
	}{
		{name: "catalog size when config says nothing", cfg: config.Config{}, want: catalog},
		{name: "explicit config value wins", cfg: config.Config{ContextWindow: 4242}, want: 4242},
		{name: "zero config value does not override", cfg: config.Config{ContextWindow: 0}, want: catalog},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Resolve(context.Background(), tt.cfg, info, "")
			if got != tt.want {
				t.Errorf("Resolve = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestResolveRuntimeOverride covers the runtime-ask branches of Resolve: an
// Ollama or OpenRouter answer corrects the embedded catalog, a failed or
// skipped lookup falls back to the catalog rather than to zero, and an
// explicit config window still beats every live answer.
func TestResolveRuntimeOverride(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// qwen3:8b has a catalog window (the flattened table's "local" entry), so
	// a fallback to the catalog is distinguishable from both a live answer
	// and zero.
	ollamaModel := "qwen3:8b"
	ollamaCatalog := provider.ContextWindowSizeFor("ollama", ollamaModel)
	if ollamaCatalog <= 0 {
		t.Fatalf("catalog window for %q = %d, want > 0", ollamaModel, ollamaCatalog)
	}

	// ollamaShowServer answers every request with an /api/show-shaped reply
	// carrying the given parameters block, counting the requests it receives.
	ollamaShowServer := func(t *testing.T, parameters string, status int) (*httptest.Server, *atomic.Int64) {
		t.Helper()
		var hits atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"parameters": parameters})
		}))
		t.Cleanup(srv.Close)
		return srv, &hits
	}

	// openrouterModelsServer answers GET /models with the given JSON body,
	// counting the requests it receives. Every subtest gets its own server so
	// the per-base-URL cache behind the OpenRouter lookup cannot leak between
	// cases.
	openrouterModelsServer := func(t *testing.T, body string, status int) (*httptest.Server, *atomic.Int64) {
		t.Helper()
		var hits atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.URL.Path != "/models" {
				t.Errorf("request path = %q, want %q", r.URL.Path, "/models")
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv, &hits
	}

	tests := []struct {
		name      string
		info      provider.Info
		cfg       config.Config
		server    func(t *testing.T) (baseURL string, hits *atomic.Int64)
		wantNoHit bool
		want      int64
	}{
		{
			name: "ollama runtime num_ctx corrects the catalog",
			info: provider.Info{Provider: "ollama", Model: ollamaModel, Ollama: true},
			server: func(t *testing.T) (string, *atomic.Int64) {
				srv, hits := ollamaShowServer(t, "num_ctx 8192\nother x", http.StatusOK)
				return srv.URL, hits
			},
			want: 8192,
		},
		{
			name: "ollama failure falls back to the catalog",
			info: provider.Info{Provider: "ollama", Model: ollamaModel, Ollama: true},
			server: func(t *testing.T) (string, *atomic.Int64) {
				srv, hits := ollamaShowServer(t, "{}", http.StatusNotFound)
				return srv.URL, hits
			},
			want: ollamaCatalog,
		},
		{
			name: "unreachable ollama falls back to the catalog",
			info: provider.Info{Provider: "ollama", Model: ollamaModel, Ollama: true},
			server: func(t *testing.T) (string, *atomic.Int64) {
				// A server that has already stopped listening: connecting
				// fails outright, which must degrade to the catalog window,
				// not to zero.
				var hits atomic.Int64
				srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
					hits.Add(1)
				}))
				u := srv.URL
				srv.Close()
				return u, &hits
			},
			want: ollamaCatalog,
		},
		{
			name: "openrouter listing answers for the model",
			info: provider.Info{Provider: "openrouter", Model: "testvendor/testmodel"},
			server: func(t *testing.T) (string, *atomic.Int64) {
				srv, hits := openrouterModelsServer(t,
					`{"data":[{"id":"TestVendor/TestModel","top_provider":{"context_length":77777}}]}`,
					http.StatusOK)
				return srv.URL, hits
			},
			want: 77777,
		},
		{
			name:      "openrouter model auto never asks the server",
			info:      provider.Info{Provider: "openrouter", Model: "auto"},
			wantNoHit: true,
			server: func(t *testing.T) (string, *atomic.Int64) {
				srv, hits := openrouterModelsServer(t, `{"data":[]}`, http.StatusOK)
				return srv.URL, hits
			},
			want: provider.ContextWindowSizeFor("openrouter", "auto"),
		},
		{
			name:      "openrouter empty model never asks the server",
			info:      provider.Info{Provider: "openrouter", Model: ""},
			wantNoHit: true,
			server: func(t *testing.T) (string, *atomic.Int64) {
				srv, hits := openrouterModelsServer(t, `{"data":[]}`, http.StatusOK)
				return srv.URL, hits
			},
			want: provider.ContextWindowSizeFor("openrouter", ""),
		},
		{
			name: "config window beats a live ollama answer",
			info: provider.Info{Provider: "ollama", Model: ollamaModel, Ollama: true},
			cfg:  config.Config{ContextWindow: 4242},
			server: func(t *testing.T) (string, *atomic.Int64) {
				srv, hits := ollamaShowServer(t, "num_ctx 8192", http.StatusOK)
				return srv.URL, hits
			},
			want: 4242,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			baseURL, hits := tt.server(t)
			got := Resolve(ctx, tt.cfg, tt.info, baseURL)
			if got != tt.want {
				t.Errorf("Resolve = %d, want %d", got, tt.want)
			}
			if tt.wantNoHit && hits.Load() != 0 {
				t.Errorf("requests to the fake server = %d, want 0", hits.Load())
			}
		})
	}
}
