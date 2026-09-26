package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// newOptionalKeyWizard builds a wizard whose only provider is agentgateway-like:
// it asks for a key but does not require one.
func newOptionalKeyWizard(t *testing.T) *SetupWizard {
	t.Helper()
	w := NewSetupWizard(SetupConfig{
		Providers: []SetupProvider{
			{Name: "agentgateway", Label: "agentgateway (local gateway)", EnvVar: "AGENTGATEWAY_API_KEY", OptionalKey: true},
		},
	}, Palette{})
	w.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return w
}

// TestSetupWizard_OptionalKeyShowsKeyStep proves a gateway behind an apiKey
// policy can be configured: the key step appears, and the typed key is returned.
func TestSetupWizard_OptionalKeyShowsKeyStep(t *testing.T) {
	w := newOptionalKeyWizard(t)
	wizPress(t, w, tea.KeyEnter)
	if w.step != setupStepKey {
		t.Fatalf("optional-key provider gave step = %v, want setupStepKey", w.step)
	}
	if got := w.stepIndicator(); got != "step 2/3" {
		t.Errorf("key step indicator = %q, want step 2/3", got)
	}
	view := w.View().Content
	for _, want := range []string{"AGENTGATEWAY_API_KEY", "optional"} {
		if !strings.Contains(view, want) {
			t.Errorf("key step view missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Get a key at") {
		t.Errorf("key step shows a key URL for a provider that has none:\n%s", view)
	}

	wizPressText(t, w, "gw-secret")
	wizPress(t, w, tea.KeyEnter)
	wizPressText(t, w, "anthropic/claude-sonnet-5")
	wizPress(t, w, tea.KeyEnter)

	got := w.Result()
	if got.Canceled || got.APIKey != "gw-secret" || got.Model != "anthropic/claude-sonnet-5" {
		t.Errorf("Result = %+v, want key gw-secret and model anthropic/claude-sonnet-5", got)
	}
}

// TestSetupWizard_OptionalKeyAcceptsBlank proves an open gateway is not blocked
// by the key step: Enter on a blank key advances with no error.
func TestSetupWizard_OptionalKeyAcceptsBlank(t *testing.T) {
	w := newOptionalKeyWizard(t)
	wizPress(t, w, tea.KeyEnter) // provider -> key
	wizPress(t, w, tea.KeyEnter) // blank key

	if w.step != setupStepModel {
		t.Fatalf("blank optional key gave step = %v, want setupStepModel", w.step)
	}
	if w.errMsg != "" {
		t.Errorf("blank optional key set an error: %q", w.errMsg)
	}
}

// TestSetupWizard_OptionalKeyListLabel proves the provider list says the key is
// optional rather than claiming none is needed.
func TestSetupWizard_OptionalKeyListLabel(t *testing.T) {
	w := newOptionalKeyWizard(t)
	view := w.View().Content
	if !strings.Contains(view, "key optional") || strings.Contains(view, "no key needed") {
		t.Errorf("provider list mislabels an optional key:\n%s", view)
	}
}

// TestSetupWizard_EscFromModelSkipsHiddenKeyStep proves Esc on the model step
// of a no-key provider returns to the provider list, not to a key box the
// provider never showed.
func TestSetupWizard_EscFromModelSkipsHiddenKeyStep(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyDown)  // ollama, no key
	wizPress(t, w, tea.KeyEnter) // straight to model

	wizPress(t, w, tea.KeyEsc)
	if w.step != setupStepProvider {
		t.Fatalf("Esc from a no-key model step gave step = %v, want setupStepProvider", w.step)
	}
}
