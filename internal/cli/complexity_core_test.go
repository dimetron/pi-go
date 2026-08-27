// Tests for the helpers extracted while reducing cyclomatic complexity in
// cli.go, interactive.go and serve.go. Each table pins the behavior the
// original branch structure encoded, so a regression in the extraction shows up
// as a failing case rather than a silent change.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/adk/v2/session"
	adktool "google.golang.org/adk/v2/tool"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/lsp"
	"github.com/dimetron/pi-go/internal/memory"
	"github.com/dimetron/pi-go/internal/provider"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/testenv"
	"github.com/dimetron/pi-go/internal/tools"
)

// clearBaseURLEnv unsets every base-URL variable config.BaseURLs consults, so a
// developer's exported endpoint cannot leak into a table's expectations.
func clearBaseURLEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"ANTHROPIC_BASE_URL", "OPENAI_BASE_URL", "GEMINI_BASE_URL",
		"MISTRAL_BASE_URL", "XAI_BASE_URL", "OPENROUTER_BASE_URL",
		"OLLAMA_HOST", "OPENCODE_BASE_URL",
	} {
		t.Setenv(v, "")
	}
}

// isolateModelCatalog makes model validation depend only on the embedded
// modeldata/ snapshots. Two machine-dependent inputs are cut off:
//
//   - The XDG catalog cache (os.UserCacheDir()/pi-go/models). CatalogFor
//     prefers it over the embedded snapshot, so a developer who ran the binary
//     once would validate against their cache, not the tree.
//   - Provider API keys. ValidateModel refreshes a provider's catalog over the
//     network on a validation miss when a key is exported, which turned the
//     "unknown model" cases into live HTTP calls taking tens of seconds.
func isolateModelCatalog(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	for _, v := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY",
		"MISTRAL_API_KEY", "XAI_API_KEY", "OPENROUTER_API_KEY",
	} {
		t.Setenv(v, "")
	}
}

// --- cli.go: buildRootRuntime helpers -------------------------------------

func TestResolveRuntimeModel(t *testing.T) {
	tests := []struct {
		name         string
		flagURL      string
		cfgBaseURLs  map[string]string
		modelName    string
		providerName string
		wantProvider string
		wantModel    string
		wantBaseURL  string
		wantCustom   bool
		wantErr      bool
	}{
		{
			name:         "plain model, no base url anywhere",
			modelName:    "claude-sonnet-4-6",
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4-6",
		},
		{
			name:         "explicit --url marks the model custom",
			flagURL:      "http://gateway.invalid/v1",
			modelName:    "claude-sonnet-4-6",
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4-6",
			wantBaseURL:  "http://gateway.invalid/v1",
			wantCustom:   true,
		},
		{
			// opencode has no model catalog, so the override is observable on
			// its own: info.Provider changes and validation stays out of it.
			name:         "role provider name overrides the resolved provider",
			modelName:    "claude-sonnet-4-6",
			providerName: "opencode",
			wantProvider: "opencode",
			wantModel:    "claude-sonnet-4-6",
		},
		{
			// The override changes the provider but not the model string, so
			// the pair is validated against the overriding provider. OpenRouter
			// names Anthropic models "anthropic/claude-sonnet-4.6"; the bare
			// Anthropic ID is not one of its models and is rejected here rather
			// than at the first request. This case only bites once OpenRouter
			// has an embedded catalog - before modeldata/models-openrouter.json
			// existed, CatalogFor("openrouter") was empty and ValidateModel
			// skipped the provider entirely.
			name:         "role provider override is validated against that provider's catalog",
			modelName:    "claude-sonnet-4-6",
			providerName: "openrouter",
			wantErr:      true,
		},
		{
			name:         "config base url for the role provider is used for resolution",
			cfgBaseURLs:  map[string]string{"openrouter": "http://router.invalid/v1"},
			modelName:    "some-unknown-model",
			providerName: "openrouter",
			wantProvider: "openrouter",
			wantModel:    "some-unknown-model",
			wantBaseURL:  "http://router.invalid/v1",
			wantCustom:   true,
		},
		{
			name:         "config base url for the resolved provider is picked up second",
			cfgBaseURLs:  map[string]string{"anthropic": "http://proxy.invalid/v1"},
			modelName:    "claude-sonnet-4-6",
			wantProvider: "anthropic",
			wantModel:    "claude-sonnet-4-6",
			wantBaseURL:  "http://proxy.invalid/v1",
			wantCustom:   true,
		},
		{
			name:      "unresolvable model is an error",
			modelName: "no-such-model-prefix",
			wantErr:   true,
		},
		{
			name:      "empty model is an error",
			modelName: "",
			wantErr:   true,
		},
		{
			name:      "known provider rejects an unknown model",
			modelName: "gpt-not-a-real-openai-model",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalFlags(t)
			clearBaseURLEnv(t)
			isolateModelCatalog(t)
			flagURL = tt.flagURL

			cfg := config.Config{BaseURLs: tt.cfgBaseURLs}
			info, baseURL, err := resolveRuntimeModel(cfg, tt.modelName, tt.providerName)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveRuntimeModel(%q, %q) = %+v, want error", tt.modelName, tt.providerName, info)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRuntimeModel: %v", err)
			}
			if info.Provider != tt.wantProvider {
				t.Errorf("provider = %q, want %q", info.Provider, tt.wantProvider)
			}
			if info.Model != tt.wantModel {
				t.Errorf("model = %q, want %q", info.Model, tt.wantModel)
			}
			if baseURL != tt.wantBaseURL {
				t.Errorf("baseURL = %q, want %q", baseURL, tt.wantBaseURL)
			}
			if info.Custom != tt.wantCustom {
				t.Errorf("custom = %v, want %v", info.Custom, tt.wantCustom)
			}
		})
	}
}

func TestRequireRuntimeAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		info    provider.Info
		apiKey  string
		baseURL string
		wantErr bool
	}{
		{
			name:    "no key and no base url for a key-requiring provider fails",
			info:    provider.Info{Provider: "anthropic"},
			wantErr: true,
		},
		{
			name:   "a key satisfies the requirement",
			info:   provider.Info{Provider: "anthropic"},
			apiKey: "sk-test",
		},
		{
			name:    "a custom base url stands in for a key",
			info:    provider.Info{Provider: "anthropic"},
			baseURL: "http://gateway.invalid/v1",
		},
		{name: "gemini is exempt", info: provider.Info{Provider: "gemini"}},
		{name: "ollama is exempt", info: provider.Info{Provider: "ollama"}},
		{name: "azure is exempt", info: provider.Info{Provider: "azure"}},
		{
			name: "an ollama-served model is exempt whatever the provider name",
			info: provider.Info{Provider: "openai", Ollama: true},
		},
		{
			name:    "openai without a key fails",
			info:    provider.Info{Provider: "openai"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := requireRuntimeAPIKey(tt.info, tt.apiKey, tt.baseURL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("requireRuntimeAPIKey(%+v, %q, %q) error = %v, wantErr %v",
					tt.info, tt.apiKey, tt.baseURL, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "no API key found") {
				t.Errorf("error = %q, want it to mention the missing key", err)
			}
		})
	}
}

func TestApplyRuntimeOllamaEndpoint(t *testing.T) {
	t.Parallel()

	t.Run("non-ollama model passes through untouched", func(t *testing.T) {
		t.Parallel()
		info := provider.Info{Provider: "anthropic", Model: "claude-sonnet-4-6"}
		got, err := applyRuntimeOllamaEndpoint(&info, "", "http://gateway.invalid/v1")
		if err != nil {
			t.Fatalf("applyRuntimeOllamaEndpoint: %v", err)
		}
		if got != "http://gateway.invalid/v1" {
			t.Errorf("baseURL = %q, want it unchanged", got)
		}
		if info.BaseURL != "" {
			t.Errorf("info.BaseURL = %q, want it left alone for a non-ollama model", info.BaseURL)
		}
	})

	t.Run("ollama model records the resolved endpoint on info", func(t *testing.T) {
		t.Parallel()
		info := provider.Info{Provider: "ollama", Model: "qwen3:8b", Ollama: true}
		// A key is supplied so the local health check is skipped: the branch
		// under test is the endpoint resolution, not the daemon probe.
		got, err := applyRuntimeOllamaEndpoint(&info, "key", "http://ollama.invalid:11434")
		if err != nil {
			t.Fatalf("applyRuntimeOllamaEndpoint: %v", err)
		}
		if got == "" {
			t.Fatal("baseURL = \"\", want a resolved endpoint")
		}
		if info.BaseURL != got {
			t.Errorf("info.BaseURL = %q, want it to match the returned %q", info.BaseURL, got)
		}
	})

	t.Run("cloud endpoint skips the local health check", func(t *testing.T) {
		t.Parallel()
		info := provider.Info{Provider: "ollama", Model: "gpt-oss:120b-cloud", Ollama: true}
		// The key is what sends a :cloud model to api.ollama.com; without one
		// it falls back to the local daemon and the probe does run.
		got, err := applyRuntimeOllamaEndpoint(&info, "sk-ollama-test", "")
		if err != nil {
			t.Fatalf("applyRuntimeOllamaEndpoint: %v", err)
		}
		if !provider.IsOllamaCloudEndpoint(got) {
			t.Fatalf("baseURL = %q, want a cloud endpoint for a :cloud model", got)
		}
	})
}

func TestResolveRuntimeContextWindow(t *testing.T) {
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
			got := resolveRuntimeContextWindow(context.Background(), tt.cfg, info, "")
			if got != tt.want {
				t.Errorf("resolveRuntimeContextWindow = %d, want %d", got, tt.want)
			}
		})
	}
}

// --- cli.go: runNonInteractive helpers ------------------------------------

func TestAppendNonInteractiveMemoryTools(t *testing.T) {
	t.Parallel()

	base := make([]adktool.Tool, 3)
	got := appendNonInteractiveMemoryTools(base, nil)
	if len(got) != len(base) {
		t.Errorf("nil store changed the tool count: %d, want %d", len(got), len(base))
	}
}

func TestBuildNonInteractiveInstruction(t *testing.T) {
	tests := []struct {
		name          string
		flagSystem    string
		palaceContext string
		want          string // "" means "compare against the built-in instruction"
	}{
		{
			name:       "--system replaces the built-in instruction",
			flagSystem: "be terse",
			want:       "be terse",
		},
		{
			name:          "palace context is appended under its own heading",
			flagSystem:    "be terse",
			palaceContext: "remembered things",
			want:          "be terse\n\n## Palace Memory Context\n\nremembered things",
		},
		{
			name:          "empty palace context adds no heading",
			flagSystem:    "be terse",
			palaceContext: "",
			want:          "be terse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalFlags(t)
			flagSystem = tt.flagSystem
			if got := buildNonInteractiveInstruction(tt.palaceContext); got != tt.want {
				t.Errorf("buildNonInteractiveInstruction() = %q, want %q", got, tt.want)
			}
		})
	}

	// The built-in instruction interpolates live environment and project state,
	// so it is pinned structurally rather than by an exact snapshot.
	t.Run("built-in instruction is used when --system is unset", func(t *testing.T) {
		resetGlobalFlags(t)
		flagSystem = ""
		got := buildNonInteractiveInstruction("")
		if got == "" {
			t.Fatal("instruction is empty, want the built-in one")
		}
		if !strings.Contains(got, "You are pi-go") {
			t.Errorf("instruction does not look like the built-in one:\n%q", got[:min(200, len(got))])
		}
		if strings.Contains(got, "## Palace Memory Context") {
			t.Error("instruction has a palace heading with no palace context")
		}
	})

	t.Run("palace context is appended to the built-in instruction too", func(t *testing.T) {
		resetGlobalFlags(t)
		flagSystem = ""
		got := buildNonInteractiveInstruction("remembered things")
		if !strings.HasSuffix(got, "\n\n## Palace Memory Context\n\nremembered things") {
			t.Errorf("instruction does not end with the palace block:\n%q", got[max(0, len(got)-120):])
		}
	})
}

func TestAppendNonInteractiveLSPTools(t *testing.T) {
	t.Parallel()

	mgr := lsp.NewManager(nil)
	t.Cleanup(mgr.Shutdown)

	base := make([]adktool.Tool, 2)
	got, err := appendNonInteractiveLSPTools(base, mgr)
	if err != nil {
		t.Fatalf("appendNonInteractiveLSPTools: %v", err)
	}
	if len(got) < len(base) {
		t.Errorf("tool count shrank: %d, want at least %d", len(got), len(base))
	}
	// With no language server installed the slice must come back untouched;
	// with one installed it only ever grows.
	if !mgr.AnyAvailable() && len(got) != len(base) {
		t.Errorf("no server available but tool count changed: %d, want %d", len(got), len(base))
	}
}

func TestOpenSessionService(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	path, svc, err := openSessionService()
	if err != nil {
		t.Fatalf("openSessionService: %v", err)
	}
	if svc == nil {
		t.Fatal("session service is nil")
	}
	want := filepath.Join(home, ".pi-go", "sessions")
	if path != want {
		t.Errorf("sessions path = %q, want %q", path, want)
	}
}

// recordingStore is a memory.Store that only remembers the sessions created on
// it; every other method is an unused stub.
type recordingStore struct {
	memory.Store
	created []*memory.Session
}

func (s *recordingStore) CreateSession(_ context.Context, sess *memory.Session) error {
	s.created = append(s.created, sess)
	return nil
}

func TestArmMemoryObservationSession(t *testing.T) {
	t.Parallel()

	t.Run("nil store leaves the callback disarmed", func(t *testing.T) {
		t.Parallel()
		memSessionID := ""
		armMemoryObservationSession(context.Background(), nil, "sess-1", "/proj", &memSessionID)
		if memSessionID != "" {
			t.Errorf("memSessionID = %q, want it left empty", memSessionID)
		}
	})

	t.Run("store arms the callback and opens the session", func(t *testing.T) {
		t.Parallel()
		store := &recordingStore{}
		memSessionID := ""
		armMemoryObservationSession(context.Background(), store, "sess-1", "/proj", &memSessionID)
		if memSessionID != "sess-1" {
			t.Errorf("memSessionID = %q, want %q", memSessionID, "sess-1")
		}
		if len(store.created) != 1 {
			t.Fatalf("created %d sessions, want 1", len(store.created))
		}
		got := store.created[0]
		if got.SessionID != "sess-1" || got.Project != "/proj" || got.Status != "active" {
			t.Errorf("created session = %+v, want sess-1 / /proj / active", got)
		}
	})
}

// --- cli.go: lastLoggedError helpers --------------------------------------

func TestLastLoggedErrorInLogFile(t *testing.T) {
	t.Parallel()

	entry := func(typ, content string) string {
		blob, err := json.Marshal(map[string]string{"type": typ, "content": content})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return string(blob)
	}

	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{name: "empty file has no error", lines: nil, want: ""},
		{name: "only non-error entries", lines: []string{entry("user", "hi"), entry("llm", "yo")}, want: ""},
		{name: "single error is found", lines: []string{entry("error", "boom")}, want: "boom"},
		{
			name:  "last error wins over an earlier one",
			lines: []string{entry("error", "first"), entry("user", "hi"), entry("error", "second")},
			want:  "second",
		},
		{
			name:  "an error with empty content is skipped",
			lines: []string{entry("error", "real"), entry("error", "")},
			want:  "real",
		},
		{
			name:  "blank lines and malformed json are skipped",
			lines: []string{entry("error", "real"), "", "   ", "{not json"},
			want:  "real",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := filepath.Join(t.TempDir(), "session-x.log")
			if err := os.WriteFile(p, []byte(strings.Join(tt.lines, "\n")), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			if got := lastLoggedErrorInLogFile(p); got != tt.want {
				t.Errorf("lastLoggedErrorInLogFile = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("unreadable file yields no error message", func(t *testing.T) {
		t.Parallel()
		if got := lastLoggedErrorInLogFile(filepath.Join(t.TempDir(), "missing.log")); got != "" {
			t.Errorf("lastLoggedErrorInLogFile = %q, want \"\"", got)
		}
	})
}

func TestLastLoggedErrorInDateDir(t *testing.T) {
	t.Parallel()

	t.Run("missing directory yields nothing", func(t *testing.T) {
		t.Parallel()
		p, m := lastLoggedErrorInDateDir(filepath.Join(t.TempDir(), "nope"))
		if p != "" || m != "" {
			t.Errorf("= (%q, %q), want empty", p, m)
		}
	})

	t.Run("only session-*.log files are read, newest first", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		write := func(name, content string) {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		// Names sort ascending, so session-02 is the newer of the two.
		write("session-01.log", errorLogLine("old"))
		write("session-02.log", errorLogLine("new"))
		// Neither of these matches the session-*.log shape.
		write("other.log", errorLogLine("ignored-prefix"))
		write("session-03.txt", errorLogLine("ignored-suffix"))
		if err := os.Mkdir(filepath.Join(dir, "session-dir.log"), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}

		p, m := lastLoggedErrorInDateDir(dir)
		if m != "new" {
			t.Errorf("message = %q, want %q", m, "new")
		}
		if filepath.Base(p) != "session-02.log" {
			t.Errorf("path = %q, want it to be session-02.log", p)
		}
	})

	t.Run("falls back to an older file when the newest holds no error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "session-01.log"), []byte(errorLogLine("old")), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "session-02.log"), []byte(`{"type":"user","content":"hi"}`), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		p, m := lastLoggedErrorInDateDir(dir)
		if m != "old" || filepath.Base(p) != "session-01.log" {
			t.Errorf("= (%q, %q), want session-01.log / old", p, m)
		}
	})
}

// errorLogLine renders one JSONL session-log line carrying an error entry.
func errorLogLine(content string) string {
	blob, err := json.Marshal(map[string]string{"type": "error", "content": content})
	if err != nil {
		panic(err)
	}
	return string(blob) + "\n"
}

func TestLastLoggedError(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)

	t.Run("absent log root is not an error", func(t *testing.T) {
		path, msg, err := lastLoggedError()
		if err != nil {
			t.Fatalf("lastLoggedError: %v", err)
		}
		if path != "" || msg != "" {
			t.Errorf("= (%q, %q), want empty", path, msg)
		}
	})

	t.Run("newest date directory wins", func(t *testing.T) {
		logRoot := filepath.Join(home, ".pi-go", "log")
		for _, d := range []struct{ dir, msg string }{
			{"2026-08-20", "older"},
			{"2026-08-21", "newer"},
		} {
			full := filepath.Join(logRoot, d.dir)
			if err := os.MkdirAll(full, 0o750); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(filepath.Join(full, "session-1.log"), []byte(errorLogLine(d.msg)), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
		// A stray file at the log root must not be mistaken for a date dir.
		if err := os.WriteFile(filepath.Join(logRoot, "stray.log"), []byte("junk"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}

		path, msg, err := lastLoggedError()
		if err != nil {
			t.Fatalf("lastLoggedError: %v", err)
		}
		if msg != "newer" {
			t.Errorf("message = %q, want %q", msg, "newer")
		}
		if !strings.Contains(path, "2026-08-21") {
			t.Errorf("path = %q, want it under 2026-08-21", path)
		}
	})
}

// --- cli.go: runPrint / runJSON part rendering ----------------------------

func modelEvent(parts ...*genai.Part) *session.Event {
	ev := &session.Event{Author: "assistant"}
	ev.Content = &genai.Content{Role: genai.RoleModel, Parts: parts}
	return ev
}

func TestPrintEventParts(t *testing.T) {
	ev := modelEvent(
		&genai.Part{Text: "hello"},
		&genai.Part{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"cmd": "ls"}}},
		&genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "bash", Response: map[string]any{"out": "ok"}}},
	)

	var dedup agent.StreamDedup
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			dedup.BeginEvent(ev)
			printEventParts(ev, &dedup, nil)
		})
	})

	if stdout != "hello" {
		t.Errorf("stdout = %q, want %q", stdout, "hello")
	}
	if !strings.Contains(stderr, "tool: bash") {
		t.Errorf("stderr = %q, want it to name the tool call", stderr)
	}
	if !strings.Contains(stderr, "done") {
		t.Errorf("stderr = %q, want it to report the tool result", stderr)
	}
}

func TestPrintEventPartsThinkingGoesToStderr(t *testing.T) {
	ev := &session.Event{Author: "assistant"}
	ev.Content = &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: "pondering"}}}

	var dedup agent.StreamDedup
	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			dedup.BeginEvent(ev)
			printEventParts(ev, &dedup, nil)
		})
	})

	if stdout != "" {
		t.Errorf("stdout = %q, want thinking kept off stdout", stdout)
	}
	if !strings.Contains(stderr, "pondering") {
		t.Errorf("stderr = %q, want the thinking text", stderr)
	}
}

func TestPrintEventPartsSkipsAggregateResend(t *testing.T) {
	delta := modelEvent(&genai.Part{Text: "hi"})
	delta.Partial = true
	aggregate := modelEvent(&genai.Part{Text: "hi"})

	var dedup agent.StreamDedup
	stdout := captureStdout(t, func() {
		_ = captureStderr(t, func() {
			for _, ev := range []*session.Event{delta, aggregate} {
				dedup.BeginEvent(ev)
				printEventParts(ev, &dedup, nil)
			}
		})
	})

	if stdout != "hi" {
		t.Errorf("stdout = %q, want the aggregate re-send suppressed", stdout)
	}
}

func TestPrintGroundingEvent(t *testing.T) {
	newEvent := func(queries []string) *session.Event {
		ev := &session.Event{Author: "assistant"}
		if queries != nil {
			ev.GroundingMetadata = &genai.GroundingMetadata{WebSearchQueries: queries}
		}
		return ev
	}

	t.Run("no grounding metadata prints nothing", func(t *testing.T) {
		got := captureStderr(t, func() {
			printGroundingEvent(newEvent(nil), map[string]bool{}, nil)
		})
		if got != "" {
			t.Errorf("stderr = %q, want nothing", got)
		}
	})

	t.Run("empty query list prints nothing", func(t *testing.T) {
		got := captureStderr(t, func() {
			printGroundingEvent(newEvent([]string{}), map[string]bool{}, nil)
		})
		if got != "" {
			t.Errorf("stderr = %q, want nothing", got)
		}
	})

	t.Run("a search is reported once and then suppressed", func(t *testing.T) {
		seen := map[string]bool{}
		ev := newEvent([]string{"go generics"})

		first := captureStderr(t, func() { printGroundingEvent(ev, seen, nil) })
		if !strings.Contains(first, agent.GroundingToolName) {
			t.Errorf("stderr = %q, want it to name %q", first, agent.GroundingToolName)
		}

		second := captureStderr(t, func() { printGroundingEvent(ev, seen, nil) })
		if second != "" {
			t.Errorf("repeat stderr = %q, want the second report suppressed", second)
		}
	})
}

func TestEncodeEventParts(t *testing.T) {
	ev := modelEvent(
		&genai.Part{Text: "hello"},
		&genai.Part{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"cmd": "ls"}}},
		&genai.Part{FunctionResponse: &genai.FunctionResponse{Name: "bash", Response: map[string]any{"out": "ok"}}},
	)

	var buf bytes.Buffer
	var dedup agent.StreamDedup
	dedup.BeginEvent(ev)
	encodeEventParts(json.NewEncoder(&buf), ev, &dedup, nil)

	var gotTypes []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var e jsonEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		gotTypes = append(gotTypes, e.Type)
	}

	want := []string{"text_delta", "tool_call", "tool_result"}
	if strings.Join(gotTypes, ",") != strings.Join(want, ",") {
		t.Errorf("event types = %v, want %v", gotTypes, want)
	}
}

func TestEncodeEventPartsThinkingAndDedup(t *testing.T) {
	thinking := &session.Event{Author: "assistant"}
	thinking.Content = &genai.Content{Role: "thinking", Parts: []*genai.Part{{Text: "pondering"}}}

	var buf bytes.Buffer
	var dedup agent.StreamDedup
	dedup.BeginEvent(thinking)
	encodeEventParts(json.NewEncoder(&buf), thinking, &dedup, nil)
	if !strings.Contains(buf.String(), `"thinking_delta"`) {
		t.Errorf("output = %q, want a thinking_delta event", buf.String())
	}

	delta := modelEvent(&genai.Part{Text: "hi"})
	delta.Partial = true
	aggregate := modelEvent(&genai.Part{Text: "hi"})

	buf.Reset()
	enc := json.NewEncoder(&buf)
	var d2 agent.StreamDedup
	for _, ev := range []*session.Event{delta, aggregate} {
		d2.BeginEvent(ev)
		encodeEventParts(enc, ev, &d2, nil)
	}
	if n := strings.Count(buf.String(), `"text_delta"`); n != 1 {
		t.Errorf("text_delta count = %d, want 1 (aggregate re-send suppressed)", n)
	}
}

// --- interactive.go: deferredInit helpers ---------------------------------

func TestDeferredInitCoreTools(t *testing.T) {
	root := t.TempDir()
	var res initResources

	coreTools, err := deferredInitCoreTools(root, "", &res)
	if err != nil {
		t.Fatalf("deferredInitCoreTools: %v", err)
	}
	t.Cleanup(func() {
		if res.bashSup != nil {
			res.bashSup.KillAll()
		}
		if res.sandbox != nil {
			_ = res.sandbox.Close()
		}
	})

	if len(coreTools) == 0 {
		t.Error("no core tools returned")
	}
	// Both must be recorded on res, otherwise a later init failure leaks them.
	if res.sandbox == nil {
		t.Error("res.sandbox not recorded")
	}
	if res.bashSup == nil {
		t.Error("res.bashSup not recorded")
	}
}

func TestRunDeferredInitPhase2(t *testing.T) {
	resetGlobalFlags(t)

	var mu struct {
		items []string
	}
	done := make(chan struct{})
	events := make(chan string, 64)
	go func() {
		defer close(done)
		for item := range events {
			mu.items = append(mu.items, item)
		}
	}()

	// cfg.MCP is nil, so the MCP goroutine returns before sending anything —
	// which is exactly the branch the "mcp" absence below pins.
	ps := runDeferredInitPhase2(context.Background(), config.Config{}, t.TempDir(),
		func(item string, done bool) {
			if done {
				events <- item
			}
		})
	close(events)
	<-done
	t.Cleanup(func() {
		if ps.lspMgr != nil {
			ps.lspMgr.Shutdown()
		}
	})

	if ps == nil {
		t.Fatal("runDeferredInitPhase2 returned nil")
	}
	if ps.lspMgr == nil {
		t.Error("lsp manager not set")
	}
	if ps.skillDirs == nil {
		t.Error("skill dirs not set")
	}
	joined := strings.Join(mu.items, ",")
	for _, want := range []string{"git", "lsp", "skills"} {
		if !strings.Contains(joined, want) {
			t.Errorf("progress items = %q, want %q reported done", joined, want)
		}
	}
	if strings.Contains(joined, "mcp") {
		t.Errorf("progress items = %q, want no mcp step when no servers are configured", joined)
	}
}

func TestAppendDeferredMemoryTools(t *testing.T) {
	t.Run("memory off returns nil store and recorder", func(t *testing.T) {
		resetGlobalFlags(t)
		flagMemoryOff = true

		base := make([]adktool.Tool, 2)
		got, store, rec := appendDeferredMemoryTools(config.Config{}, t.TempDir(), base)
		if len(got) != len(base) {
			t.Errorf("tool count = %d, want %d", len(got), len(base))
		}
		if store != nil || rec != nil {
			t.Errorf("store = %v, recorder = %v, want both nil", store, rec)
		}
	})

	t.Run("memory on returns a store and recorder", func(t *testing.T) {
		resetGlobalFlags(t)
		flagMemoryOff = false

		base := make([]adktool.Tool, 2)
		got, store, rec := appendDeferredMemoryTools(config.Config{}, t.TempDir(), base)
		if store == nil {
			t.Fatal("store is nil, want a lazy store")
		}
		if rec == nil {
			t.Fatal("recorder is nil")
		}
		if len(got) <= len(base) {
			t.Errorf("tool count = %d, want the memory tools appended to %d", len(got), len(base))
		}
	})
}

func TestBuildDeferredInstructionParts(t *testing.T) {
	t.Run("built-in parts by default", func(t *testing.T) {
		resetGlobalFlags(t)
		flagSystem = ""
		got := buildDeferredInstructionParts()
		if got.String() == "" {
			t.Error("instruction is empty, want the built-in set")
		}
	})

	t.Run("--system replaces the base outright", func(t *testing.T) {
		resetGlobalFlags(t)
		flagSystem = "be terse"
		got := buildDeferredInstructionParts()
		if got.Base != "be terse" {
			t.Errorf("Base = %q, want %q", got.Base, "be terse")
		}
		if got.String() != "be terse" {
			t.Errorf("String() = %q, want %q", got.String(), "be terse")
		}
	})
}

func TestBuildDeferredCallbacks(t *testing.T) {
	resetGlobalFlags(t)

	sandbox, err := tools.NewSandbox(t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewSandbox: %v", err)
	}
	t.Cleanup(func() { _ = sandbox.Close() })

	mgr := lsp.NewManager(nil)
	t.Cleanup(mgr.Shutdown)

	base := buildDeferredCallbacks(config.Config{}, "anthropic", sandbox, nil, nil)
	if base.deduper == nil {
		t.Error("deduper is nil")
	}
	if base.compactMetrics == nil {
		t.Error("compactMetrics is nil")
	}
	if len(base.beforeModel) == 0 {
		t.Error("no before-model callbacks; the read-image callback is always added")
	}
	// The compactor and dedup callbacks are unconditional.
	if len(base.afterTool) < 2 {
		t.Errorf("after-tool callbacks = %d, want at least the compactor and deduper", len(base.afterTool))
	}

	withLSP := buildDeferredCallbacks(config.Config{}, "anthropic", sandbox, mgr, nil)
	if len(withLSP.afterTool) != len(base.afterTool)+1 {
		t.Errorf("after-tool callbacks with an LSP manager = %d, want %d",
			len(withLSP.afterTool), len(base.afterTool)+1)
	}

	rec := newDeferredMemoryRecorder(config.Config{}, t.TempDir())
	withMem := buildDeferredCallbacks(config.Config{}, "anthropic", sandbox, nil, rec)
	if len(withMem.afterTool) != len(base.afterTool)+1 {
		t.Errorf("after-tool callbacks with a memory recorder = %d, want %d",
			len(withMem.afterTool), len(base.afterTool)+1)
	}
}

func TestResolveDeferredSessionResumed(t *testing.T) {
	resetGlobalFlags(t)
	flagSession = "existing-session"

	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("session service: %v", err)
	}

	// ag is never touched on the resume path: the session ID is already known,
	// so CreateSession is not reached. Passing nil pins that.
	sessionID, title, resumed, err := resolveDeferredSession(
		context.Background(), nil, svc, &cliMockLLM{name: "test-model"}, "anthropic", "http://x.invalid")
	if err != nil {
		t.Fatalf("resolveDeferredSession: %v", err)
	}
	if sessionID != "existing-session" {
		t.Errorf("sessionID = %q, want %q", sessionID, "existing-session")
	}
	if !resumed {
		t.Error("resumed = false, want true when --session names one")
	}
	if title != "" {
		t.Errorf("defaultTitle = %q, want empty for a resumed session", title)
	}
}

// --- serve.go -------------------------------------------------------------

func TestValidateServeHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers []string
		wantErr bool
	}{
		{name: "no headers", headers: nil},
		{name: "one valid header", headers: []string{"X-Key=value"}},
		{name: "several valid headers", headers: []string{"a=1", "b=2"}},
		{name: "empty value still has the separator", headers: []string{"a="}},
		{name: "missing separator", headers: []string{"nope"}, wantErr: true},
		{name: "one bad header among good ones", headers: []string{"a=1", "nope"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalFlags(t)
			flagServeHeaders = tt.headers
			err := validateServeHeaders()
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateServeHeaders(%v) error = %v, wantErr %v", tt.headers, err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), "expected key=value") {
				t.Errorf("error = %q, want it to explain the expected form", err)
			}
		})
	}
}

func TestResolveServeProject(t *testing.T) {
	t.Run("--project wins", func(t *testing.T) {
		resetGlobalFlags(t)
		flagServeProject = "/some/project"
		got, err := resolveServeProject()
		if err != nil {
			t.Fatalf("resolveServeProject: %v", err)
		}
		if got != "/some/project" {
			t.Errorf("project = %q, want %q", got, "/some/project")
		}
	})

	t.Run("falls back to the working directory", func(t *testing.T) {
		resetGlobalFlags(t)
		flagServeProject = ""
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd: %v", err)
		}
		got, err := resolveServeProject()
		if err != nil {
			t.Fatalf("resolveServeProject: %v", err)
		}
		if got != cwd {
			t.Errorf("project = %q, want %q", got, cwd)
		}
	})
}

func TestPrintServeBanner(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		url        string
		headers    []string
		insecure   bool
		wantLines  []string
		absentText []string
	}{
		{
			name:       "minimal run prints only the required lines",
			wantLines:  []string{"http://localhost:8765", "Project: /proj", "Pair code: ABC123", "Pairing timeout:"},
			absentText: []string{"Model:", "URL:", "Header:", "TLS verification", "Voice:"},
		},
		{
			name:      "model is printed when set",
			model:     "claude-sonnet-4-6",
			wantLines: []string{"Model: claude-sonnet-4-6"},
		},
		{
			name:      "url is printed when set",
			url:       "http://gateway.invalid/v1",
			wantLines: []string{"URL: http://gateway.invalid/v1"},
		},
		{
			name:      "every header gets its own line",
			headers:   []string{"a=1", "b=2"},
			wantLines: []string{"Header: a=1", "Header: b=2"},
		},
		{
			name:      "insecure is called out",
			insecure:  true,
			wantLines: []string{"TLS verification: disabled (--insecure)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetGlobalFlags(t)
			flagServeModel = tt.model
			flagServeURL = tt.url
			flagServeHeaders = tt.headers
			flagServeInsecure = tt.insecure

			var buf bytes.Buffer
			printServeBanner(&buf, ":8765", "/proj", "ABC123")
			got := buf.String()

			for _, want := range tt.wantLines {
				if !strings.Contains(got, want) {
					t.Errorf("banner missing %q:\n%s", want, got)
				}
			}
			for _, absent := range tt.absentText {
				if strings.Contains(got, absent) {
					t.Errorf("banner unexpectedly contains %q:\n%s", absent, got)
				}
			}
		})
	}
}
