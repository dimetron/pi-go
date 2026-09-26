package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/testenv"
	"github.com/dimetron/pi-go/internal/tui"
)

// wizardKeys is one scripted interaction: named keys (enter, esc) or text.
type wizardKeys []string

// stubSetupTerminal replaces the two terminal-bound calls in runSetup: the TTY
// guard reports a terminal, and the wizard is driven through its Update method
// with the scripted keys instead of a tea.Program. wizardErr, when set, is
// returned in place of running the wizard at all.
func stubSetupTerminal(t *testing.T, keys wizardKeys, wizardErr error) {
	t.Helper()
	origTTY, origRun := setupTerminal, runSetupWizard
	t.Cleanup(func() { setupTerminal, runSetupWizard = origTTY, origRun })

	setupTerminal = func() bool { return true }
	runSetupWizard = func(_ *cobra.Command, w *tui.SetupWizard) error {
		if wizardErr != nil {
			return wizardErr
		}
		w.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		for _, k := range keys {
			switch k {
			case "enter":
				w.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			case "esc":
				w.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
			default:
				for _, r := range k {
					w.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
				}
			}
		}
		return nil
	}
}

// setupRunEnv isolates HOME and the working directory, and preconfigures the
// default role so the wizard opens on that provider and Enter selects it.
func setupRunEnv(t *testing.T, provider, model string) string {
	t.Helper()
	home := t.TempDir()
	testenv.SetHome(t, home)
	t.Chdir(t.TempDir())
	t.Setenv("AGENTGATEWAY_API_KEY", "")
	if provider != "" {
		if err := config.SaveDefaultRole(model, provider); err != nil {
			t.Fatalf("SaveDefaultRole: %v", err)
		}
	}
	return home
}

func runSetupCaptured(t *testing.T) (stdout, stderr string, err error) {
	t.Helper()
	cmd := newSetupCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err = runSetup(cmd, nil)
	return out.String(), errOut.String(), err
}

func assertDefaultRole(t *testing.T, wantModel, wantProvider string) {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	model, prov, _, _, _, err := cfg.ResolveRole("default")
	if err != nil {
		t.Fatalf("ResolveRole: %v", err)
	}
	if model != wantModel || prov != wantProvider {
		t.Errorf("default role = (%q, %q), want (%q, %q)", model, prov, wantModel, wantProvider)
	}
}

// TestRunSetupSavesGatewayKey drives the whole command for a gateway behind an
// apiKey policy: the key reaches .env, the role reaches config.json, and the
// summary reports the key masked.
func TestRunSetupSavesGatewayKey(t *testing.T) {
	home := setupRunEnv(t, "agentgateway", "old-model")
	stubSetupTerminal(t, wizardKeys{"enter", "gw-secret-123456", "enter", "anthropic/claude-sonnet-5", "enter"}, nil)

	out, _, err := runSetupCaptured(t)
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	for _, want := range []string{"Configured agentgateway", "key    ~/.pi-go/.env", "model  anthropic/claude-sonnet-5"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gw-secret-123456") {
		t.Errorf("summary printed the key unmasked:\n%s", out)
	}

	data, err := os.ReadFile(filepath.Join(home, ".pi-go", ".env"))
	if err != nil {
		t.Fatalf("reading .env: %v", err)
	}
	if !strings.Contains(string(data), "AGENTGATEWAY_API_KEY") || !strings.Contains(string(data), "gw-secret-123456") {
		t.Errorf(".env does not hold the gateway key:\n%s", data)
	}
	assertDefaultRole(t, "anthropic/claude-sonnet-5", "agentgateway")
}

// TestRunSetupOpenGatewaySkipsKey proves a blank gateway key configures the
// model without writing .env or claiming a key in the summary.
func TestRunSetupOpenGatewaySkipsKey(t *testing.T) {
	home := setupRunEnv(t, "agentgateway", "old-model")
	stubSetupTerminal(t, wizardKeys{"enter", "enter", "my-route", "enter"}, nil)

	out, _, err := runSetupCaptured(t)
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if strings.Contains(out, "key    ") {
		t.Errorf("summary reports a key that was never entered:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi-go", ".env")); !os.IsNotExist(err) {
		t.Errorf("a blank gateway key wrote .env (stat err = %v)", err)
	}
	assertDefaultRole(t, "my-route", "agentgateway")
}

// TestRunSetupCanceledChangesNothing proves Esc leaves the config as it was.
func TestRunSetupCanceledChangesNothing(t *testing.T) {
	setupRunEnv(t, "agentgateway", "old-model")
	stubSetupTerminal(t, wizardKeys{"esc"}, nil)

	out, _, err := runSetupCaptured(t)
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if !strings.Contains(out, "Setup canceled") {
		t.Errorf("cancel not reported:\n%s", out)
	}
	assertDefaultRole(t, "old-model", "agentgateway")
}

// TestRunSetupReportsWizardFailure proves a program that fails to run is
// returned as an error rather than read as a cancel.
func TestRunSetupReportsWizardFailure(t *testing.T) {
	setupRunEnv(t, "", "")
	stubSetupTerminal(t, nil, errors.New("tty gone"))

	_, _, err := runSetupCaptured(t)
	if err == nil || !strings.Contains(err.Error(), "running setup wizard") || !strings.Contains(err.Error(), "tty gone") {
		t.Fatalf("err = %v, want a wrapped wizard failure", err)
	}
}

// TestRunSetupStartsFromDefaultsOnBrokenConfig proves an unreadable config does
// not block the command that exists to repair it: setup warns and continues.
func TestRunSetupStartsFromDefaultsOnBrokenConfig(t *testing.T) {
	home := setupRunEnv(t, "", "")
	dir := filepath.Join(home, ".pi-go")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubSetupTerminal(t, wizardKeys{"esc"}, nil)

	out, errOut, err := runSetupCaptured(t)
	if err != nil {
		t.Fatalf("runSetup: %v", err)
	}
	if !strings.Contains(errOut, "starting from defaults") {
		t.Errorf("no warning for the broken config; stderr:\n%s", errOut)
	}
	if !strings.Contains(out, "Setup canceled") {
		t.Errorf("setup did not run past the broken config:\n%s", out)
	}
}

// TestRunSetupReportsKeySaveFailure proves a key that cannot be written fails
// the command instead of printing a summary for a credential that was lost.
func TestRunSetupReportsKeySaveFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("AGENTGATEWAY_API_KEY", "")
	testenv.SetUnwritableHome(t)
	// With HOME unusable no role is configured, so the list opens on its
	// first entry, Anthropic, which requires a key.
	stubSetupTerminal(t, wizardKeys{"enter", "sk-ant-test", "enter", "enter"}, nil)

	out, _, err := runSetupCaptured(t)
	if err == nil || !strings.Contains(err.Error(), "saving API key") {
		t.Fatalf("err = %v, want a key-save failure", err)
	}
	if strings.Contains(out, "Configured") {
		t.Errorf("printed a success summary after a failed save:\n%s", out)
	}
}

// TestSetupWizardProvidersMirrorsTable proves every CLI entry reaches the
// wizard with its key requirement intact, including agentgateway's optional key.
func TestSetupWizardProvidersMirrorsTable(t *testing.T) {
	got := setupWizardProviders()
	if len(got) != len(setupProviders) {
		t.Fatalf("wizard has %d providers, table has %d", len(got), len(setupProviders))
	}
	for i, p := range setupProviders {
		w := got[i]
		if w.Name != p.name || w.EnvVar != p.envVar || w.NeedsKey != p.needsKey || w.OptionalKey != p.optionalKey || w.KeyURL != p.keyURL {
			t.Errorf("entry %d: wizard %+v does not mirror table %+v", i, w, p)
		}
	}
}

// TestLookupSetupProviderRejectsUnknown covers the miss path, which runSetup
// turns into an error rather than saving a role for a provider it cannot name.
func TestLookupSetupProviderRejectsUnknown(t *testing.T) {
	if _, ok := lookupSetupProvider("no-such-provider"); ok {
		t.Error("lookup accepted an unknown provider")
	}
	if _, ok := lookupSetupProvider("AgentGateway"); !ok {
		t.Error("lookup should be case-insensitive")
	}
}
