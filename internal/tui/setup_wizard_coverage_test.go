package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestSetupWizard_ModelArrowsMoveAndWrap proves ↑/↓ on the model step move the
// highlight, and that the highlighted model is what gets confirmed.
func TestSetupWizard_ModelArrowsMoveAndWrap(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter) // anthropic
	wizPressText(t, w, "sk-1")
	wizPress(t, w, tea.KeyEnter) // model step

	wizPress(t, w, tea.KeyDown)
	if got := w.model(); got != "claude-opus-5" {
		t.Errorf("after Down, model = %q, want claude-opus-5", got)
	}
	wizPress(t, w, tea.KeyUp)
	wizPress(t, w, tea.KeyUp) // wraps to the last candidate
	if got := w.model(); got != "claude-haiku-4-5" {
		t.Errorf("after wrapping Up, model = %q, want claude-haiku-4-5", got)
	}
}

// TestSetupWizard_EmptyModelIsRejected proves Enter with no candidates and no
// typed name stays on the step and says why.
func TestSetupWizard_EmptyModelIsRejected(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyDown)  // ollama: no candidates
	wizPress(t, w, tea.KeyEnter) // model step

	if got := w.View().Content; !strings.Contains(got, "no models listed for ollama") {
		t.Errorf("empty model step does not say what to do:\n%s", got)
	}
	wizPress(t, w, tea.KeyEnter)
	if w.confirmed || w.step != setupStepModel {
		t.Fatal("an empty model was confirmed")
	}
	if got := w.View().Content; !strings.Contains(got, "choose a model or type one") {
		t.Errorf("rejection not shown:\n%s", got)
	}
}

// TestSetupWizard_FilterClampsHighlight proves narrowing the list under a
// highlight that is past its end moves it onto the last remaining match.
func TestSetupWizard_FilterClampsHighlight(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter)
	wizPressText(t, w, "sk-1")
	wizPress(t, w, tea.KeyEnter)

	wizPress(t, w, tea.KeyUp) // last of three
	wizPressText(t, w, "o")   // sonnet, opus: two left
	if w.candIdx != 1 {
		t.Errorf("candIdx = %d after filtering, want 1 (clamped)", w.candIdx)
	}
}

// TestSetupWizard_ViewShowsOverflowCount proves a list longer than the window
// says how many rows are hidden rather than silently cutting them off.
func TestSetupWizard_ViewShowsOverflowCount(t *testing.T) {
	cfg := testWizardConfig()
	many := make([]string, 30)
	for i := range many {
		many[i] = "model-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	cfg.Candidates["anthropic"] = many
	w := NewSetupWizard(cfg, Palette{})
	w.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	wizPress(t, w, tea.KeyEnter)
	wizPressText(t, w, "sk-1")
	wizPress(t, w, tea.KeyEnter)

	if got := w.View().Content; !strings.Contains(got, "more") {
		t.Errorf("overflowing list has no hidden-row count:\n%s", got)
	}
}

// TestSetupWizard_DefensiveBranches covers the states the key handlers never
// reach but the accessors guard against: an out-of-range provider index, an
// unknown step, a stale highlight and an empty list.
func TestSetupWizard_DefensiveBranches(t *testing.T) {
	w := newTestWizard(t)

	w.provIdx = 99
	if got := w.provider(); got.Name != "" {
		t.Errorf("out-of-range provider = %+v, want zero value", got)
	}
	w.provIdx = 0

	w.step = setupStep(42)
	w.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if got := w.View().Content; !strings.Contains(got, "unexpected step") {
		t.Errorf("unknown step renders:\n%s", got)
	}

	w.candIdx = 99 // stale: past the end of the candidate list
	if got := w.model(); got != "claude-sonnet-5" {
		t.Errorf("stale highlight model = %q, want the first candidate", got)
	}

	if got := wrapIndex(3, 0); got != 0 {
		t.Errorf("wrapIndex on an empty list = %d, want 0", got)
	}
}

// TestPaletteForName proves a theme name resolves to a usable palette, and an
// unknown or empty name falls back to the default rather than a zero palette.
func TestPaletteForName(t *testing.T) {
	def := PaletteForName("")
	if def.Primary == nil || def.Text == nil {
		t.Fatalf("default palette has nil colors: %+v", def)
	}
	if got := PaletteForName("no-such-theme"); paletteKey(got) != paletteKey(def) {
		t.Error("an unknown theme did not fall back to the default palette")
	}
	if got := PaletteForName("pi-classic"); got.Primary == nil {
		t.Error("a named theme resolved to a palette without colors")
	}
}

// TestSetupProgramOptions proves the wizard gets the terminal options the main
// TUI negotiates, rather than none.
func TestSetupProgramOptions(t *testing.T) {
	if len(SetupProgramOptions()) == 0 {
		t.Error("SetupProgramOptions returned no options")
	}
}
