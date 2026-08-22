// Tests for the root command: configuration loading, runtime construction,
// resource cleanup and top-level execution.
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
)

func TestLoadRootConfig_ModelOverride(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)

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
	testenv.SetHome(t, tmpDir)

	flagModel = ""
	cfg, err := loadRootConfig()
	if err != nil {
		t.Fatalf("loadRootConfig: %v", err)
	}
	// Just exercise the "no override" branch; config has defaults.
	_ = cfg
}

func TestBuildRootRuntime_InvalidModel(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
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
	testenv.SetHome(t, tmpDir)
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
	testenv.SetHome(t, tmpDir)
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

func TestInitNonInteractiveRuntime_Basic(t *testing.T) {
	resetGlobalFlags(t)
	tmpHome := t.TempDir()
	testenv.SetHome(t, tmpHome)

	cfg := &config.Config{}
	cwd := tmpHome
	sandboxRoot := tmpHome

	rt, err := initNonInteractiveRuntime(context.Background(), cfg, cwd, sandboxRoot, "")
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

func TestRunRoot_WithPprofFlag(t *testing.T) {
	resetGlobalFlags(t)
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
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
	testenv.SetHome(t, tmpDir)
	t.Setenv("OPENAI_API_KEY", "k")

	flagPprof = "trace"
	flagPprofPort = "0"

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "gpt-5.4", "--mode", "print", "--pprof", "trace", "--pprof-port", "0"})
	_ = cmd.Execute()
}

func TestGitHelpersSkippableWithoutGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available; skipping")
	}
	tmp := t.TempDir()
	_ = detectGitRoot(context.Background(), tmp)
	_ = detectBranch(context.Background(), tmp)
	_, _ = computeDiffStats(context.Background(), tmp)
	_ = countUntrackedLines(context.Background(), tmp)
}

func TestPalaceConfigFromCLI_Defaults(t *testing.T) {
	tmp := t.TempDir()
	testenv.SetHome(t, tmp)

	cfg := palaceConfigFromCLI(&config.Config{})
	if cfg.DBPath == "" {
		t.Error("DBPath should be non-empty from HOME fallback")
	}
	if cfg.ModelPath == "" {
		t.Error("ModelPath should be non-empty from HOME fallback")
	}
}

func TestExecute_HelpFlag(t *testing.T) {
	resetGlobalFlags(t)
	origArgs := os.Args
	os.Args = []string{"pi", "--help"}
	defer func() { os.Args = origArgs }()

	_ = Execute() // help prints and returns nil
}

func TestRunRoot_InvalidDotEnv(t *testing.T) {
	// Test with a corrupted .env file in a custom home.
	tmpDir := t.TempDir()
	piDir := filepath.Join(tmpDir, ".pi-go")
	os.MkdirAll(piDir, 0755)
	// Write invalid .env (should not crash loadDotEnv).
	os.WriteFile(filepath.Join(piDir, ".env"), []byte("invalid yaml: ["), 0644)

	testenv.SetHome(t, tmpDir)
	t.Setenv("ANTHROPIC_API_KEY", "test-key")

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "claude-sonnet-4-6", "--mode", "print"})
	// May fail or succeed depending on model availability.
	// Just verify no panic.
	_ = cmd.Execute()
}

func TestRunRoot_NoAPIKeyWithOllamaModel(t *testing.T) {
	tmpDir := t.TempDir()
	testenv.SetHome(t, tmpDir)
	cfgDir := filepath.Join(tmpDir, ".pi-go")
	os.MkdirAll(cfgDir, 0755)
	os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"roles":{"default":{"model":"llama3:8b","provider":"ollama"}}}`), 0644)

	// Clear all real API keys.
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
		os.Unsetenv(k)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"--model", "llama3:8b", "--mode", "print"})
	// Ollama models don't require API keys.
	// The command may fail if Ollama isn't running, but that's expected.
	_ = cmd.Execute()
}

func TestInitResources_CleanupNil(t *testing.T) {
	r := &initResources{}
	r.cleanup()
}

func TestInitResources_CleanupPartial(t *testing.T) {
	r := &initResources{}
	r.cleanup()
}
