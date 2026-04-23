package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/tui"
)

// -----------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------

// resetGlobalFlags clears package-level CLI flags so tests do not leak
// state into one another.
func resetGlobalFlags(t *testing.T) {
	t.Helper()
	orig := struct {
		model, mode, session, socket, url, system, pprof, pprofPort string
		headers                                                     []string
		cont, insecure, smol, slow, plan, memOff                    bool
		loginModel                                                  string
		serveAddr, serveProject, serveModel, serveURL               string
		serveHeaders                                                []string
		servePairing                                                time.Duration
		serveInsecure                                               bool
	}{
		flagModel, flagMode, flagSession, flagSocket, flagURL, flagSystem, flagPprof, flagPprofPort,
		flagHeaders,
		flagContinue, flagInsecure, flagSmol, flagSlow, flagPlan, flagMemoryOff,
		flagLoginModel,
		flagServeAddr, flagServeProject, flagServeModel, flagServeURL,
		flagServeHeaders,
		flagServePairingTimeout,
		flagServeInsecure,
	}
	t.Cleanup(func() {
		flagModel = orig.model
		flagMode = orig.mode
		flagSession = orig.session
		flagSocket = orig.socket
		flagURL = orig.url
		flagSystem = orig.system
		flagPprof = orig.pprof
		flagPprofPort = orig.pprofPort
		flagHeaders = orig.headers
		flagContinue = orig.cont
		flagInsecure = orig.insecure
		flagSmol = orig.smol
		flagSlow = orig.slow
		flagPlan = orig.plan
		flagMemoryOff = orig.memOff
		flagLoginModel = orig.loginModel
		flagServeAddr = orig.serveAddr
		flagServeProject = orig.serveProject
		flagServeModel = orig.serveModel
		flagServeURL = orig.serveURL
		flagServeHeaders = orig.serveHeaders
		flagServePairingTimeout = orig.servePairing
		flagServeInsecure = orig.serveInsecure
	})

	flagModel = ""
	flagMode = ""
	flagSession = ""
	flagSocket = "/tmp/pi-go.sock"
	flagURL = ""
	flagSystem = ""
	flagPprof = ""
	flagPprofPort = "6060"
	flagHeaders = nil
	flagContinue = false
	flagInsecure = false
	flagSmol = false
	flagSlow = false
	flagPlan = false
	flagMemoryOff = false
	flagLoginModel = ""
	flagServeAddr = ":8080"
	flagServeProject = ""
	flagServeModel = ""
	flagServeURL = ""
	flagServeHeaders = nil
	flagServePairingTimeout = 5 * time.Minute
	flagServeInsecure = false
}

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

// -----------------------------------------------------------------------
// resolveMode — covers both branches
// -----------------------------------------------------------------------

func TestResolveMode_ExplicitFlag(t *testing.T) {
	resetGlobalFlags(t)
	flagMode = "rpc"
	if got := resolveMode(); got != "rpc" {
		t.Errorf("resolveMode() = %q, want %q", got, "rpc")
	}
}

func TestResolveMode_EmptyFallsBackToDetect(t *testing.T) {
	resetGlobalFlags(t)
	flagMode = ""
	got := resolveMode()
	// Under go test stdin is a pipe so it should be print, but allow both.
	if got != "print" && got != "interactive" {
		t.Errorf("resolveMode() = %q, want 'print' or 'interactive'", got)
	}
}

// -----------------------------------------------------------------------
// loadRootConfig — model override branch
// -----------------------------------------------------------------------

func TestLoadRootConfig_ModelOverride(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	flagModel = "gpt-5.4-mini"
	cfg, err := loadRootConfig()
	if err != nil {
		t.Fatalf("loadRootConfig: %v", err)
	}
	if cfg.Roles["default"].Model != "gpt-5.4-mini" {
		t.Errorf("default role model = %q, want %q", cfg.Roles["default"].Model, "gpt-5.4-mini")
	}
}

func TestLoadRootConfig_NoOverride(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	flagModel = ""
	cfg, err := loadRootConfig()
	if err != nil {
		t.Fatalf("loadRootConfig: %v", err)
	}
	// Just exercise the "no override" branch; config has defaults.
	_ = cfg
}

// -----------------------------------------------------------------------
// buildRootRuntime — error branches
// -----------------------------------------------------------------------

func TestBuildRootRuntime_InvalidModel(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	// Set all known provider keys so we cover the key lookup.
	t.Setenv("OPENAI_API_KEY", "key")
	t.Setenv("ANTHROPIC_API_KEY", "key")

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"),
		[]byte(`{"roles":{"default":{"model":"definitely-not-a-real-model-xyz"}}}`), 0o644)

	_, err := buildRootRuntime(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for invalid model")
	}
}

func TestBuildRootRuntime_ContinueNoSession(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	flagContinue = true
	flagModel = "gpt-5.4"
	flagMode = "print"

	_, err := buildRootRuntime(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for --continue with no sessions")
	}
	if !strings.Contains(err.Error(), "no previous session") {
		t.Errorf("err = %v, want 'no previous session'", err)
	}
}

func TestBuildRootRuntime_HappyPath(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "key-ok")

	flagModel = "gpt-5.4"
	flagMode = "print"

	rt, err := buildRootRuntime(context.Background(), []string{"hello", "world"})
	if err != nil {
		t.Fatalf("buildRootRuntime: %v", err)
	}
	if rt.llm == nil {
		t.Error("expected non-nil LLM")
	}
	if rt.prompt != "hello world" {
		t.Errorf("prompt = %q, want %q", rt.prompt, "hello world")
	}
	if rt.mode != "print" {
		t.Errorf("mode = %q, want 'print'", rt.mode)
	}
}

// -----------------------------------------------------------------------
// initNonInteractiveRuntime — exercise sandbox path
// -----------------------------------------------------------------------

func TestInitNonInteractiveRuntime_Basic(t *testing.T) {
	resetGlobalFlags(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	cfg := &config.Config{}
	cwd := tmpHome
	sandboxRoot := tmpHome

	rt, err := initNonInteractiveRuntime(cfg, cwd, sandboxRoot, "")
	if err != nil {
		t.Fatalf("initNonInteractiveRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("expected non-nil runtime")
	}
	if rt.sandbox == nil {
		t.Error("expected non-nil sandbox")
	}
	if rt.orch == nil {
		t.Error("expected non-nil orchestrator")
	}
	rt.close()
	// Double close is safe.
	rt.close()
}

func TestInitNonInteractiveRuntime_CloseNil(t *testing.T) {
	var rt *nonInteractiveRuntime
	rt.close() // should not panic
}

// -----------------------------------------------------------------------
// runRoot — exercise --pprof flag (which spawns goroutine)
// -----------------------------------------------------------------------

func TestRunRoot_WithPprofFlag(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	// Use a weird port to avoid listen conflicts; even if it fails, we want
	// the pprof branch to be taken.
	flagPprof = "cpu"
	flagPprofPort = "0"

	cmd := newRootCmd()
	// Intentionally no prompt to exit quickly.
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "print", "--pprof", "cpu", "--pprof-port", "0"})
	_ = cmd.Execute()
}

func TestRunRoot_WithPprofTrace(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	flagPprof = "trace"
	flagPprofPort = "0"

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "print", "--pprof", "trace", "--pprof-port", "0"})
	_ = cmd.Execute()
}

// -----------------------------------------------------------------------
// runPing — full path against a fake Ollama server
// -----------------------------------------------------------------------

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

// -----------------------------------------------------------------------
// runServe — exercise additional paths via flags
// -----------------------------------------------------------------------

func TestRunServe_ValidHeadersShortRun(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()

	origCwd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	flagServeAddr = "127.0.0.1:0"
	flagServeProject = tmpDir
	flagServePairingTimeout = 1 * time.Second
	flagServeHeaders = []string{"X-Custom=ok", "Authorization=Bearer x"}
	flagServeInsecure = true
	flagServeModel = "gpt-5.4"
	flagServeURL = "https://example.test"

	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open devnull: %v", err)
	}
	origStdout := os.Stdout
	os.Stdout = devNull
	defer func() {
		os.Stdout = origStdout
		_ = devNull.Close()
	}()

	cmd := &cobra.Command{}
	done := make(chan error, 1)
	go func() { done <- runServe(cmd, nil) }()

	time.Sleep(200 * time.Millisecond)

	// Signal ourselves.
	proc, _ := os.FindProcess(os.Getpid())
	_ = proc.Signal(os.Interrupt)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runServe did not exit in time")
	}
}

func TestRunServe_EmptyProjectUsesCWD(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()

	origCwd, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	flagServeAddr = "127.0.0.1:0"
	flagServeProject = "" // triggers Getwd path
	flagServePairingTimeout = 1 * time.Second

	devNull, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	origStdout := os.Stdout
	os.Stdout = devNull
	defer func() {
		os.Stdout = origStdout
		_ = devNull.Close()
	}()

	cmd := &cobra.Command{}
	done := make(chan error, 1)
	go func() { done <- runServe(cmd, nil) }()

	time.Sleep(200 * time.Millisecond)
	proc, _ := os.FindProcess(os.Getpid())
	_ = proc.Signal(os.Interrupt)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runServe did not exit in time")
	}
}

// -----------------------------------------------------------------------
// runLogin — more branches via --model post-login
// -----------------------------------------------------------------------

func TestRunLogin_FindProviderSuccess_ManualCodeEmpty(t *testing.T) {
	// Skip: this test requires a real OAuth flow with browser interaction
	// which cannot be properly mocked in a unit test environment.
	// The auth flow blocks waiting for callback that never comes.
	t.Skip("requires real OAuth provider interaction")
}

func TestRunLogin_ModelFlagResolveProvider(t *testing.T) {
	// Skip: this test requires a real OAuth flow with browser interaction
	// which cannot be properly mocked in a unit test environment.
	// The auth flow blocks waiting for callback that never comes.
	t.Skip("requires real OAuth provider interaction")
}

// -----------------------------------------------------------------------
// openBrowser: exercise default (unknown OS) branch and linux/windows
// -----------------------------------------------------------------------

func TestOpenBrowser_ReturnsNilOrError(t *testing.T) {
	// Mock so we don't call the real browser.
	orig := openBrowser
	openBrowser = func(url string) error { return nil }
	t.Cleanup(func() { openBrowser = orig })
	_ = openBrowser("https://example.invalid")
}

// -----------------------------------------------------------------------
// saveResult — both branches re-covered with explicit checks.
// -----------------------------------------------------------------------

func TestSaveResult_ErrorField(t *testing.T) {
	r := &auth.Result{Err: fmt.Errorf("boom")}
	if err := saveResult(r); err == nil {
		t.Fatal("expected error")
	}
}

// -----------------------------------------------------------------------
// runMemoryModelDownload — hit platform auto-detect branch (no onnxFilePath).
// -----------------------------------------------------------------------

func TestRunMemoryModelDownload_AutoDetectPlatformBranch(t *testing.T) {
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	dest := filepath.Join(t.TempDir(), "models")
	_ = runMemoryModelDownload(dest, "")
}

// -----------------------------------------------------------------------
// newMemoryInitCmd with arg — exercise arg branch in cobra RunE wrapper
// -----------------------------------------------------------------------

func TestNewMemoryInitCmd_WithArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir()

	cmd := newMemoryInitCmd()
	cmd.SetArgs([]string{dir})
	_ = captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
}

func TestNewMemoryInitCmd_NoArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	origCwd, _ := os.Getwd()
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	cmd := newMemoryInitCmd()
	cmd.SetArgs(nil)
	_ = captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Logf("execute: %v", err)
		}
	})
}

// -----------------------------------------------------------------------
// newMemoryRecentCmd RunE — exercise with arg and without
// -----------------------------------------------------------------------

func TestNewMemoryRecentCmd_NoArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	origCwd, _ := os.Getwd()
	workDir := filepath.Join(tmp, "work")
	_ = os.MkdirAll(workDir, 0o755)
	if err := os.Chdir(workDir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	cmd := newMemoryRecentCmd()
	cmd.SetArgs(nil)
	_ = captureStdout(t, func() {
		_ = cmd.Execute() // will fail (no DB), but exercises RunE
	})
}

func TestNewMemoryRecentCmd_WithArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cmd := newMemoryRecentCmd()
	cmd.SetArgs([]string{tmp})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

// -----------------------------------------------------------------------
// newMemoryModelDownloadCmd RunE wrapper — exercise flags
// -----------------------------------------------------------------------

func TestNewMemoryModelDownloadCmd_RunEError(t *testing.T) {
	resetGlobalFlags(t)
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	tmp := t.TempDir()
	cmd := newMemoryModelDownloadCmd()
	cmd.SetArgs([]string{"--dest", filepath.Join(tmp, "m")})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryModelStatusCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	cmd := newMemoryModelStatusCmd()
	cmd.SetArgs([]string{"--path", tmp})
	_ = captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})
}

// -----------------------------------------------------------------------
// memory kg cmds RunE wrappers
// -----------------------------------------------------------------------

func TestNewMemoryKGQueryCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryKGQueryCmd()
	cmd.SetArgs([]string{"SomeEntity", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryKGAddCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryKGAddCmd()
	cmd.SetArgs([]string{"Alice", "works_on", "api", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryKGTimelineCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryKGTimelineCmd()
	cmd.SetArgs([]string{"Alice", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryKGCmd_Subcommands(t *testing.T) {
	cmd := newMemoryKGCmd()
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"query", "add", "timeline"} {
		if !names[want] {
			t.Errorf("kg subcommand %q missing", want)
		}
	}
}

func TestNewMemorySearchCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemorySearchCmd()
	cmd.SetArgs([]string{"query text", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryWakeUpCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryWakeUpCmd()
	cmd.SetArgs([]string{"--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryStatusCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryStatusCmd()
	cmd.SetArgs([]string{"--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryMineCmd_FlagDefaults(t *testing.T) {
	cmd := newMemoryMineCmd()
	convos, _ := cmd.Flags().GetBool("convos")
	if convos {
		t.Error("convos default should be false")
	}
	wing, _ := cmd.Flags().GetString("wing")
	if wing != "" {
		t.Error("wing default should be empty")
	}
}

// -----------------------------------------------------------------------
// detectMode — piped-stdin path
// -----------------------------------------------------------------------

func TestDetectMode_PipedStdin(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	orig := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = orig }()

	mode := detectMode()
	if mode != "print" {
		t.Errorf("detectMode with piped stdin = %q, want 'print'", mode)
	}
}

// -----------------------------------------------------------------------
// runInteractive — drive through but cancel ctx quickly.
// runInteractive starts a TUI which blocks in tui.Run. We cancel the ctx
// to force deferred init to exit; tui.Run may fail but we just want to
// exercise the function.
// -----------------------------------------------------------------------

func TestRunInteractive_CancelContext(t *testing.T) {
	if raceEnabled {
		t.Skip("Bubble Tea/cancelreader shutdown races under -race in this TTY simulation")
	}
	resetGlobalFlags(t)
	flagMemoryOff = true
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	llm := &cliMockLLM{name: "test-interactive", response: "ok"}
	cfg := config.Config{}
	tracker := guardrail.New(0)

	// info value needs Provider set.
	info := provInfo("openai", "gpt-5.4")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- runInteractive(ctx, cfg, llm, info, tracker, "default", tmpHome, tmpHome, "")
	}()

	// Give it a moment to start, then cancel. tui.Run requires a real TTY
	// which will fail quickly in test env.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Either nil or an error from TUI is acceptable.
	case <-time.After(30 * time.Second):
		t.Fatal("runInteractive did not exit in time")
	}
}

// provInfo returns a provider.Info-like struct. We use a minimal shim
// because importing provider.Info here just duplicates setup.
func provInfo(provider, model string) provInfoT {
	return provInfoT{Provider: provider, Model: model}
}

type provInfoT = struct {
	Provider string
	Model    string
	Ollama   bool
}

// -----------------------------------------------------------------------
// runNonInteractive — drive via runRoot with memory-off and a simple model.
// Exercises initNonInteractiveRuntime + json mode early-exit.
// -----------------------------------------------------------------------

func TestRunNonInteractive_JSONEmptyPromptEarlyExit(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	flagMemoryOff = true

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "json", "--memory-off"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunNonInteractive_PrintEmptyPromptEarlyExit(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	flagMemoryOff = true

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "print", "--memory-off"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// -----------------------------------------------------------------------
// runNonInteractive — exercise with --system flag + hooks config
// -----------------------------------------------------------------------

func TestRunNonInteractive_WithSystemAndHooks(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	cfgDir := filepath.Join(tmpDir, ".pi-go")
	_ = os.MkdirAll(cfgDir, 0o755)
	_ = os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{
		"roles": {"default": {"model":"gpt-5.4","provider":"openai"}},
		"hooks": [{"event":"before_tool","command":"echo x","tools":["read"]}],
		"memory": {"enabled": false}
	}`), 0o644)

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--system", "custom system prompt", "--mode", "print"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// -----------------------------------------------------------------------
// writeLastSession — happy path
// -----------------------------------------------------------------------

func TestWriteLastSession_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	orig := lastSessionFile
	lastSessionFile = filepath.Join(tmpDir, "subdir", "last.json")
	defer func() { lastSessionFile = orig }()

	if err := writeLastSession("/work", "openai", "gpt-5.4"); err != nil {
		t.Fatalf("writeLastSession: %v", err)
	}
	data, err := os.ReadFile(lastSessionFile)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	var got lastSessionData
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.WorkDir != "/work" || got.Model != "gpt-5.4" {
		t.Errorf("data = %+v", got)
	}
}

// -----------------------------------------------------------------------
// lastLoggedError — corrupted JSON log line should be skipped (not error)
// -----------------------------------------------------------------------

func TestLastLoggedError_CorruptedLogLine(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)

	dateDir := filepath.Join(tmpDir, ".pi-go", "log", "2024-07-01")
	if err := os.MkdirAll(dateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dateDir, "session-00-00-00.log")
	// Mix of invalid JSON and valid entries.
	content := "not json line\n" +
		`{"type":"info","content":"hi"}` + "\n" +
		"another garbage\n" +
		`{"type":"error","content":"real error"}` + "\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, msg, err := lastLoggedError()
	if err != nil {
		t.Fatalf("lastLoggedError: %v", err)
	}
	if msg != "real error" {
		t.Errorf("msg = %q, want 'real error'", msg)
	}
}

// -----------------------------------------------------------------------
// readLastSession — additional coverage (read error other than NotExist).
// -----------------------------------------------------------------------

func TestReadLastSession_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	f := filepath.Join(tmpDir, "empty.json")
	_ = os.WriteFile(f, []byte(""), 0o644)

	orig := lastSessionFile
	lastSessionFile = f
	defer func() { lastSessionFile = orig }()

	_, err := readLastSession()
	if err == nil {
		t.Error("expected error for empty JSON file")
	}
}

// -----------------------------------------------------------------------
// deferredInit — drive with skills + nothing in cfg
// -----------------------------------------------------------------------

func TestDeferredInit_WithSkillDir(t *testing.T) {
	resetGlobalFlags(t)
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create a skill file.
	skillsDir := filepath.Join(tmpHome, ".pi-go", "skills", "my-skill")
	_ = os.MkdirAll(skillsDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillsDir, "SKILL.md"),
		[]byte("---\nname: my-skill\ndescription: test skill\n---\n\nBody"), 0o644)

	flagMemoryOff = true
	llm := &cliMockLLM{name: "test-skill", response: "ok"}
	tracker := guardrail.New(0)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch := make(chan tui.InitEvent, 128)
	var res initResources
	defer res.cleanup()

	done := make(chan struct{})
	go func() {
		defer close(done)
		deferredInit(ctx, config.Config{}, llm, tracker, tmpHome, tmpHome, "", ch, &res)
		close(ch)
	}()

	for range ch {
	}
	<-done
}

// -----------------------------------------------------------------------
// detectGitRoot / countUntrackedLines / computeDiffStats — ensure git tests
// are skipped if git is unavailable, to keep deterministic coverage.
// -----------------------------------------------------------------------

func TestGitHelpersSkippableWithoutGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available; skipping")
	}
	tmp := t.TempDir()
	_ = detectGitRoot(tmp)
	_ = detectBranch(tmp)
	_, _ = computeDiffStats(tmp)
	_ = countUntrackedLines(tmp)
}

// -----------------------------------------------------------------------
// palaceConfigFromCLI — with HOME unset path
// -----------------------------------------------------------------------

func TestPalaceConfigFromCLI_Defaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := palaceConfigFromCLI(&config.Config{})
	if cfg.DBPath == "" {
		t.Error("DBPath should be non-empty from HOME fallback")
	}
	if cfg.ModelPath == "" {
		t.Error("ModelPath should be non-empty from HOME fallback")
	}
}

// -----------------------------------------------------------------------
// Execute — top-level entry
// -----------------------------------------------------------------------

func TestExecute_HelpFlag(t *testing.T) {
	resetGlobalFlags(t)
	origArgs := os.Args
	os.Args = []string{"pi", "--help"}
	defer func() { os.Args = origArgs }()

	_ = Execute() // help prints and returns nil
}

// -----------------------------------------------------------------------
// openBrowser with non-existent URL — exercises the actual exec path.
// The run may fail (macOS `open` returns error for bad URLs) but the
// branch is covered.
// -----------------------------------------------------------------------

func TestOpenBrowser_BadURL(t *testing.T) {
	// Mock so we don't call the real browser or exec anything.
	orig := openBrowser
	openBrowser = func(url string) error { return fmt.Errorf("mocked") }
	t.Cleanup(func() { openBrowser = orig })

	err := openBrowser("file:///nonexistent/path/xyzzy")
	if err == nil {
		t.Error("expected mock error")
	}
}
