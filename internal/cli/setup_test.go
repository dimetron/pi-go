package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
	"github.com/dimetron/pi-go/internal/tui"
)

// TestSetupCmdIsRegistered proves `pi setup` is reachable from the root command
// and is documented — a command that exists but is unreachable is the failure
// this catches.
func TestSetupCmdIsRegistered(t *testing.T) {
	root := newRootCmd()

	var found bool
	for _, c := range root.Commands() {
		if c.Name() == "setup" {
			found = true
			if c.Short == "" {
				t.Error("setup command has no Short description")
			}
			if c.RunE == nil {
				t.Error("setup command has no RunE")
			}
		}
	}
	if !found {
		t.Fatal("`setup` is not registered as a subcommand of the root command")
	}
}

// TestSaveSetupResultWritesKeyAndRole proves the wizard's result lands in the
// two files pi actually reads: the key in ~/.pi-go/.env, the provider and model
// in the default role of ~/.pi-go/config.json.
//
// This is the test that would catch a wiring mistake the wizard's own tests
// cannot see, because the wizard returns data and this is the only place that
// persists it.
func TestSaveSetupResultWritesKeyAndRole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	entry, ok := lookupSetupProvider("anthropic")
	if !ok {
		t.Fatal("anthropic missing from the setup provider table")
	}

	err := saveSetupResult(entry, tui.SetupResult{
		Provider: "anthropic",
		APIKey:   "sk-ant-test-123456",
		Model:    "claude-sonnet-5",
	})
	if err != nil {
		t.Fatalf("saveSetupResult: %v", err)
	}

	envData, err := os.ReadFile(filepath.Join(home, ".pi-go", ".env"))
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	if !strings.Contains(string(envData), "ANTHROPIC_API_KEY") {
		t.Errorf(".env does not contain ANTHROPIC_API_KEY:\n%s", envData)
	}
	if !strings.Contains(string(envData), "sk-ant-test-123456") {
		t.Errorf(".env does not contain the key value:\n%s", envData)
	}

	cfgData, err := os.ReadFile(filepath.Join(home, ".pi-go", "config.json"))
	if err != nil {
		t.Fatalf("reading config.json: %v", err)
	}
	if !strings.Contains(string(cfgData), "claude-sonnet-5") {
		t.Errorf("config.json does not contain the model:\n%s", cfgData)
	}
	if !strings.Contains(string(cfgData), "anthropic") {
		t.Errorf("config.json does not contain the provider:\n%s", cfgData)
	}
}

// TestSaveSetupResultRoundTripsThroughResolveRole proves the saved config is
// usable: config.Load + ResolveRole must return the model and provider the
// wizard collected. Checking the file bytes alone would pass even if the role
// were written in a shape ResolveRole cannot read.
func TestSaveSetupResultRoundTripsThroughResolveRole(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	entry, _ := lookupSetupProvider("xai")
	if err := saveSetupResult(entry, tui.SetupResult{
		Provider: "xai",
		APIKey:   "xai-test-key",
		Model:    "grok-4.6",
	}); err != nil {
		t.Fatalf("saveSetupResult: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load after setup: %v", err)
	}

	model, prov, _, _, _, err := cfg.ResolveRole("default")
	if err != nil {
		t.Fatalf("ResolveRole(default): %v", err)
	}
	if model != "grok-4.6" {
		t.Errorf("resolved model = %q, want grok-4.6", model)
	}
	if prov != "xai" {
		t.Errorf("resolved provider = %q, want xai", prov)
	}
}

// TestSaveSetupResultSkipsKeyForLocalProvider proves a provider that needs no
// credential writes no key, so a local Ollama setup does not leave a bogus
// empty entry in .env.
func TestSaveSetupResultSkipsKeyForLocalProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())

	entry, ok := lookupSetupProvider("ollama")
	if !ok {
		t.Fatal("ollama missing from the setup provider table")
	}
	if entry.needsKey {
		t.Fatal("ollama should not require a key")
	}

	if err := saveSetupResult(entry, tui.SetupResult{
		Provider: "ollama",
		Model:    "gemma4:e4b",
	}); err != nil {
		t.Fatalf("saveSetupResult: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, ".pi-go", ".env")); !os.IsNotExist(err) {
		t.Errorf("a key-less provider wrote a .env file (stat err = %v)", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	model, prov, _, _, _, err := cfg.ResolveRole("default")
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if model != "gemma4:e4b" || prov != "ollama" {
		t.Errorf("resolved (%q, %q), want (gemma4:e4b, ollama)", model, prov)
	}
}

// TestSetupModelCandidatesCoverCataloguedProviders proves the catalog wiring is
// real: every provider pi-go ships an offline catalog for offers models in the
// wizard.
//
// The list is explicit rather than "every provider that needs a key", because
// three of them have no catalog at all: Ollama and agentgateway are enumerated
// from a running daemon, and opencode and Azure serve account- or
// subscription-specific names. For those the wizard's free-text entry is the
// correct answer, so a test demanding candidates there would be asserting a
// false invariant.
func TestSetupModelCandidatesCoverCataloguedProviders(t *testing.T) {
	candidates := setupModelCandidates()

	for _, name := range []string{"anthropic", "openai", "gemini", "mistral", "xai", "openrouter"} {
		if len(candidates[name]) == 0 {
			t.Errorf("provider %q ships an offline catalog but offers no candidates", name)
		}
	}
}

// TestSetupModelCandidatesAbsentForDaemonProviders proves the wizard does not
// invent a list for a provider pi-go cannot enumerate offline. A fabricated
// list would offer model names that may not exist on the user's daemon.
func TestSetupModelCandidatesAbsentForDaemonProviders(t *testing.T) {
	candidates := setupModelCandidates()

	for _, name := range []string{"ollama", "agentgateway", "azure", "opencode"} {
		if _, ok := candidates[name]; ok {
			t.Errorf("provider %q has no offline catalog but the wizard offers %d models",
				name, len(candidates[name]))
		}
	}
}

// TestSetupModelCandidatesMatchTheValidator proves every model the wizard can
// offer is one pi will actually accept. The wizard and ValidateModel both read
// provider.CatalogFor; this asserts that stays true, because a divergence would
// mean setup writes a config that fails on the next run.
func TestSetupModelCandidatesMatchTheValidator(t *testing.T) {
	for prov, ids := range setupModelCandidates() {
		for _, id := range ids {
			info := provider.Info{Provider: prov, Model: id, Custom: true}
			if err := provider.ValidateModel(info); err != nil {
				t.Errorf("offered %s/%s but ValidateModel rejects it: %v", prov, id, err)
			}
		}
	}
}

// TestSetupRankModelsPutsNewestFirst proves the ordering a user relies on:
// the current model tier appears before older ones.
func TestSetupRankModelsPutsNewestFirst(t *testing.T) {
	ids := []string{"claude-opus-4-5-20251101", "claude-sonnet-5", "claude-haiku-4-5-20251001"}

	got := setupRankModels("anthropic", ids)
	if len(got) != len(ids) {
		t.Fatalf("setupRankModels returned %d entries, want %d", len(got), len(ids))
	}
	if got[0] != "claude-sonnet-5" {
		t.Errorf("newest model is %q, want claude-sonnet-5 first; full order: %v", got[0], got)
	}

	// Every input must survive: a ranking that drops models would silently
	// remove valid choices.
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	for _, id := range ids {
		if !seen[id] {
			t.Errorf("setupRankModels dropped %q", id)
		}
	}
}

// TestSetupProviderTableIsWellFormed proves each entry carries what it claims:
// a key-requiring provider has a URL to send the user to, and names are unique.
func TestSetupProviderTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range setupProviders {
		if p.name == "" || p.label == "" || p.envVar == "" {
			t.Errorf("incomplete entry: %+v", p)
		}
		if seen[p.name] {
			t.Errorf("duplicate provider %q", p.name)
		}
		seen[p.name] = true
		if p.needsKey && p.keyURL == "" {
			t.Errorf("provider %q needs a key but has no keyURL to show the user", p.name)
		}
	}
}

// TestCurrentProviderReflectsConfig proves re-running setup starts from the
// existing configuration rather than resetting it.
func TestCurrentProviderReflectsConfig(t *testing.T) {
	writeGlobalConfig(t, map[string]any{
		"defaultProvider": "gemini",
		"roles": map[string]any{
			"default": map[string]any{"model": "gemini-3.7-flash", "provider": "gemini"},
		},
	})

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	if got := currentProvider(cfg); got != "gemini" {
		t.Errorf("currentProvider = %q, want gemini", got)
	}
}

// TestRunSetupRefusesWithoutTerminal proves the command fails with a usable
// message instead of hanging when there is no TTY to draw on.
func TestRunSetupRefusesWithoutTerminal(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	cmd := newSetupCmd()
	// Under `go test`, stdout is a pipe rather than a character device, which
	// is exactly the headless case this guard is for.
	err := runSetup(cmd, nil)
	if err == nil {
		t.Fatal("expected runSetup to refuse a non-terminal stdout")
	}
	if !strings.Contains(err.Error(), "terminal") {
		t.Errorf("error should mention the terminal requirement, got: %v", err)
	}
}

// TestSetupHasTTYRejectsDevNull guards the specific hole an os.ModeCharDevice
// check leaves: /dev/null IS a character device but is NOT a terminal, so
// `pi setup < /dev/null` passed a char-device guard and then hung forever on a
// read that could never return.
//
// Both streams are pointed at /dev/null, which is the case that actually
// distinguishes the two implementations: with stdin as a pipe or stdout as a
// pipe, a char-device check rejects it too and the test would pass against the
// buggy version. Only when both are char devices is isatty the sole thing that
// says no.
func TestSetupHasTTYRejectsDevNull(t *testing.T) {
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Skipf("cannot open %s: %v", os.DevNull, err)
	}
	defer devNull.Close()

	// Sanity-check the premise: /dev/null must look like a char device, or the
	// buggy implementation would reject it for the wrong reason and this test
	// would prove nothing.
	fi, err := devNull.Stat()
	if err != nil {
		t.Fatalf("stat %s: %v", os.DevNull, err)
	}
	if fi.Mode()&os.ModeCharDevice == 0 {
		t.Skipf("%s is not a char device on this platform; the test cannot distinguish the two checks", os.DevNull)
	}

	origStdin, origStdout := os.Stdin, os.Stdout
	t.Cleanup(func() { os.Stdin, os.Stdout = origStdin, origStdout })
	os.Stdin, os.Stdout = devNull, devNull

	if setupHasTTY() {
		t.Error("setupHasTTY accepted /dev/null as a terminal; `pi setup < /dev/null` would hang")
	}
}

// TestSaveSetupResultOptionalGatewayKey proves agentgateway's optional key is
// saved when given and leaves .env untouched when blank, so re-running setup
// on an open gateway does not erase a key set earlier.
func TestSaveSetupResultOptionalGatewayKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(t.TempDir())
	envPath := filepath.Join(home, ".pi-go", ".env")

	entry, ok := lookupSetupProvider("agentgateway")
	if !ok {
		t.Fatal("agentgateway missing from the setup provider table")
	}
	if entry.needsKey || !entry.optionalKey {
		t.Fatalf("agentgateway should ask for an optional key, got %+v", entry)
	}

	if err := saveSetupResult(entry, tui.SetupResult{Provider: "agentgateway", Model: "m"}); err != nil {
		t.Fatalf("saveSetupResult (blank key): %v", err)
	}
	if _, err := os.Stat(envPath); !os.IsNotExist(err) {
		t.Errorf("a blank optional key wrote a .env file (stat err = %v)", err)
	}

	if err := saveSetupResult(entry, tui.SetupResult{Provider: "agentgateway", APIKey: "gw-secret", Model: "m"}); err != nil {
		t.Fatalf("saveSetupResult (with key): %v", err)
	}
	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	if !strings.Contains(string(data), "AGENTGATEWAY_API_KEY") || !strings.Contains(string(data), "gw-secret") {
		t.Errorf(".env does not hold the gateway key:\n%s", data)
	}

	if err := saveSetupResult(entry, tui.SetupResult{Provider: "agentgateway", Model: "m"}); err != nil {
		t.Fatalf("saveSetupResult (blank re-run): %v", err)
	}
	after, _ := os.ReadFile(envPath)
	if !strings.Contains(string(after), "gw-secret") {
		t.Errorf("a blank re-run erased the existing gateway key:\n%s", after)
	}
}
