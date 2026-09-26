package tui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func testWizardConfig() SetupConfig {
	return SetupConfig{
		Providers: []SetupProvider{
			{Name: "anthropic", Label: "Anthropic (Claude)", EnvVar: "ANTHROPIC_API_KEY", NeedsKey: true, KeyURL: "https://console.anthropic.com/settings/keys"},
			{Name: "ollama", Label: "Ollama (local daemon)", EnvVar: "OLLAMA_API_KEY", NeedsKey: false},
		},
		Candidates: map[string][]string{
			"anthropic": {"claude-sonnet-5", "claude-opus-5", "claude-haiku-4-5"},
		},
	}
}

func newTestWizard(t *testing.T) *SetupWizard {
	t.Helper()
	w := NewSetupWizard(testWizardConfig(), Palette{})
	w.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return w
}

func wizPress(t *testing.T, w *SetupWizard, code rune) {
	t.Helper()
	w.Update(tea.KeyPressMsg(tea.Key{Code: code}))
}

func wizPressText(t *testing.T, w *SetupWizard, text string) {
	t.Helper()
	for _, r := range text {
		w.Update(tea.KeyPressMsg(tea.Key{Code: r, Text: string(r)}))
	}
}

// TestSetupWizard_HappyPath walks the full flow: pick a provider, enter a key,
// choose a model, confirm. This is the one test that proves the wizard can
// actually produce a result rather than merely render.
func TestSetupWizard_HappyPath(t *testing.T) {
	w := newTestWizard(t)

	// Step 1: anthropic is first, so Enter selects it.
	wizPress(t, w, tea.KeyEnter)
	if w.step != setupStepKey {
		t.Fatalf("after selecting a provider that needs a key, step = %v, want setupStepKey", w.step)
	}

	// Step 2: type a key and continue.
	wizPressText(t, w, "sk-test-123")
	wizPress(t, w, tea.KeyEnter)
	if w.step != setupStepModel {
		t.Fatalf("after entering a key, step = %v, want setupStepModel", w.step)
	}

	// Step 3: the newest model is first; Enter confirms it.
	wizPress(t, w, tea.KeyEnter)
	if !w.quitting {
		t.Fatal("confirming a model should quit the program")
	}

	got := w.Result()
	if got.Canceled {
		t.Fatal("Result reported Canceled after a completed flow")
	}
	if got.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", got.Provider)
	}
	if got.APIKey != "sk-test-123" {
		t.Errorf("APIKey = %q, want sk-test-123", got.APIKey)
	}
	if got.Model != "claude-sonnet-5" {
		t.Errorf("Model = %q, want claude-sonnet-5", got.Model)
	}
}

// TestSetupWizard_SkipsKeyStepForLocalProvider proves the NeedsKey=false path
// goes straight from provider to model — the reason that field exists.
func TestSetupWizard_SkipsKeyStepForLocalProvider(t *testing.T) {
	w := newTestWizard(t)

	wizPress(t, w, tea.KeyDown) // move to ollama
	wizPress(t, w, tea.KeyEnter)

	if w.step != setupStepModel {
		t.Fatalf("selecting a provider needing no key gave step = %v, want setupStepModel", w.step)
	}
}

// TestSetupWizard_RejectsEmptyKey proves Enter on a blank key does not advance,
// so the user cannot confirm an empty credential.
func TestSetupWizard_RejectsEmptyKey(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter) // choose anthropic

	wizPress(t, w, tea.KeyEnter) // blank key
	if w.step != setupStepKey {
		t.Fatalf("blank key advanced to %v, want to stay on setupStepKey", w.step)
	}
	if w.errMsg == "" {
		t.Error("expected an error message explaining the key is required")
	}
}

// TestSetupWizard_ModelFilterNarrowsAndSelects proves typing filters the list
// and the highlighted entry is what gets confirmed.
func TestSetupWizard_ModelFilterNarrowsAndSelects(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter) // anthropic
	wizPressText(t, w, "sk-1")
	wizPress(t, w, tea.KeyEnter) // to model step

	wizPressText(t, w, "haiku")

	list := w.filtered()
	if len(list) != 1 || list[0] != "claude-haiku-4-5" {
		t.Fatalf("filtered(%q) = %v, want just claude-haiku-4-5", "haiku", list)
	}

	wizPress(t, w, tea.KeyEnter)
	if got := w.Result().Model; got != "claude-haiku-4-5" {
		t.Errorf("Model = %q, want claude-haiku-4-5", got)
	}
}

// TestSetupWizard_AcceptsCustomModel proves a typed name that matches no
// candidate can still be confirmed. Without this, a provider with no offline
// catalog (Ollama, Azure) would be impossible to configure.
func TestSetupWizard_AcceptsCustomModel(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyDown)  // ollama
	wizPress(t, w, tea.KeyEnter) // straight to model
	wizPressText(t, w, "gemma4:e4b")

	if got := w.model(); got != "gemma4:e4b" {
		t.Fatalf("model() = %q, want gemma4:e4b", got)
	}

	wizPress(t, w, tea.KeyEnter)
	res := w.Result()
	if res.Canceled {
		t.Fatal("a custom model should be confirmable")
	}
	if res.Model != "gemma4:e4b" {
		t.Errorf("Model = %q, want gemma4:e4b", res.Model)
	}
	if res.Provider != "ollama" {
		t.Errorf("Provider = %q, want ollama", res.Provider)
	}
}

// TestSetupWizard_EscBacksUpAndCancels proves Esc steps back within the flow
// and cancels from the first step, rather than always aborting.
func TestSetupWizard_EscBacksUpAndCancels(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter) // anthropic -> key
	if w.step != setupStepKey {
		t.Fatalf("setup: step = %v, want setupStepKey", w.step)
	}

	wizPress(t, w, tea.KeyEsc)
	if w.quitting {
		t.Fatal("Esc from the key step should step back, not quit")
	}
	if w.step != setupStepProvider {
		t.Fatalf("after Esc, step = %v, want setupStepProvider", w.step)
	}

	wizPress(t, w, tea.KeyEsc)
	if !w.quitting {
		t.Fatal("Esc from the first step should quit")
	}
	if !w.Result().Canceled {
		t.Error("a canceled run must report Canceled")
	}
}

// TestSetupWizard_CtrlCAlwaysCancels proves Ctrl+C aborts from any step.
func TestSetupWizard_CtrlCAlwaysCancels(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter) // to key step

	w.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if !w.quitting {
		t.Fatal("Ctrl+C should quit")
	}
	if !w.Result().Canceled {
		t.Error("Ctrl+C must report Canceled even after entering a partial key")
	}
}

// TestSetupWizard_SelectionWraps proves Up at the top wraps to the bottom,
// which is what makes the radio list behave like a list rather than a clamp.
func TestSetupWizard_SelectionWraps(t *testing.T) {
	w := newTestWizard(t)
	if w.provIdx != 0 {
		t.Fatalf("initial provIdx = %d, want 0", w.provIdx)
	}
	wizPress(t, w, tea.KeyUp)
	if w.provIdx != 1 {
		t.Errorf("Up from the first entry gave provIdx = %d, want %d (wrapped)", w.provIdx, 1)
	}
	wizPress(t, w, tea.KeyDown)
	if w.provIdx != 0 {
		t.Errorf("Down from the last entry gave provIdx = %d, want 0 (wrapped)", w.provIdx)
	}
}

// TestSetupWizard_PreselectsCurrentConfig proves re-running setup starts on the
// existing choice rather than resetting it.
func TestSetupWizard_PreselectsCurrentConfig(t *testing.T) {
	cfg := testWizardConfig()
	cfg.InitialProvider = "ollama"
	w := NewSetupWizard(cfg, Palette{})

	if got := w.provider().Name; got != "ollama" {
		t.Errorf("initial provider = %q, want ollama", got)
	}
}

// TestSetupWizard_ViewRendersEachStep asserts every step draws its own prompt,
// which catches a View that panics or silently renders nothing.
func TestSetupWizard_ViewRendersEachStep(t *testing.T) {
	w := newTestWizard(t)
	w.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	if got := w.View().Content; !strings.Contains(got, "Which provider") {
		t.Errorf("provider step view missing its prompt:\n%s", got)
	}

	wizPress(t, w, tea.KeyEnter) // key step
	if got := w.View().Content; !strings.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("key step view missing the env var hint:\n%s", got)
	}

	wizPressText(t, w, "sk-1")
	wizPress(t, w, tea.KeyEnter) // model step
	if got := w.View().Content; !strings.Contains(got, "claude-sonnet-5") {
		t.Errorf("model step view missing the candidate list:\n%s", got)
	}
}

// TestSetupWizard_VisibleWindowKeepsSelectionOnScreen covers the scrolling
// window: a 448-model catalog cannot be drawn whole, and the highlighted row
// must never scroll out of the frame.
func TestSetupWizard_VisibleWindowKeepsSelectionOnScreen(t *testing.T) {
	ids := make([]string, 448)
	for i := range ids {
		ids[i] = "model-" + string(rune('a'+i%26)) + "-" + itoa(i)
	}
	cfg := testWizardConfig()
	cfg.Candidates["anthropic"] = ids
	w := NewSetupWizard(cfg, Palette{})
	w.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	wizPress(t, w, tea.KeyEnter)
	wizPressText(t, w, "k")
	wizPress(t, w, tea.KeyEnter)

	for _, target := range []int{0, 50, len(w.filtered()) - 1} {
		w.candIdx = target
		rows, selected := w.visibleWindow()
		if selected < 0 || selected >= len(rows) {
			t.Fatalf("candIdx %d: selected %d outside %d rows", target, selected, len(rows))
		}
		if len(rows) > setupMaxVisible {
			t.Fatalf("candIdx %d: rendered %d rows, cap is %d", target, len(rows), setupMaxVisible)
		}
		if got := rows[selected]; got != w.filtered()[target] {
			t.Fatalf("candIdx %d: window shows %q, want %q", target, got, w.filtered()[target])
		}
	}
}

// itoa is strconv.Itoa, kept local so the table-driven tests below read
// without an extra call-site import.
func itoa(n int) string { return strconv.Itoa(n) }

// TestSetupWizard_WindowAroundHandlesEdges pins the windowing arithmetic,
// including the two ends where an off-by-one would show a blank frame.
func TestSetupWizard_WindowAroundHandlesEdges(t *testing.T) {
	cases := []struct {
		idx, total, height int
		wantStart, wantEnd int
	}{
		{0, 100, 10, 0, 10},
		{99, 100, 10, 90, 100},
		{50, 100, 10, 45, 55},
		{0, 5, 10, 0, 5},
		{0, 0, 10, 0, 0},
	}
	for _, c := range cases {
		start, end := windowAround(c.idx, c.total, c.height)
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("windowAround(%d,%d,%d) = (%d,%d), want (%d,%d)",
				c.idx, c.total, c.height, start, end, c.wantStart, c.wantEnd)
		}
		if c.total > 0 && (c.idx < start || c.idx >= end) {
			t.Errorf("windowAround(%d,%d,%d) = (%d,%d) excludes the selection",
				c.idx, c.total, c.height, start, end)
		}
	}
}

// TestSetupWizard_CtrlCOnFinalStepIsNotAConfirmation guards a bug the step-based
// result check had: Ctrl+C on the model step also stops the program with the
// cursor on that step, so inferring acceptance from the step alone reported a
// canceled run as confirmed and wrote the config anyway.
func TestSetupWizard_CtrlCOnFinalStepIsNotAConfirmation(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter) // anthropic
	wizPressText(t, w, "sk-1")
	wizPress(t, w, tea.KeyEnter) // model step

	// A model is available, so a step-based check would look like success.
	if w.model() == "" {
		t.Fatal("test setup: expected a model candidate to be available")
	}

	w.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if !w.quitting {
		t.Fatal("Ctrl+C should quit")
	}
	if !w.Result().Canceled {
		t.Errorf("Ctrl+C on the final step was reported as a confirmation: %+v", w.Result())
	}
}

// TestSetupWizard_ResultIsCanceledBeforeConfirmation proves an exit that never
// confirmed writes nothing, even when the wizard holds a complete-looking
// selection.
func TestSetupWizard_ResultIsCanceledBeforeConfirmation(t *testing.T) {
	w := newTestWizard(t)
	wizPress(t, w, tea.KeyEnter)
	wizPressText(t, w, "sk-1")
	wizPress(t, w, tea.KeyEnter)

	if !w.Result().Canceled {
		t.Error("Result before an explicit confirmation must be Canceled")
	}
}

// TestSetupWizard_StepIndicatorCountsRealSteps proves a no-key provider is not
// promised a key step it will never see.
func TestSetupWizard_StepIndicatorCountsRealSteps(t *testing.T) {
	w := newTestWizard(t)
	if got := w.stepIndicator(); got != "step 1/3" {
		t.Errorf("first step indicator = %q, want step 1/3", got)
	}

	wizPress(t, w, tea.KeyDown)  // ollama, no key
	wizPress(t, w, tea.KeyEnter) // straight to model
	if got := w.stepIndicator(); got != "step 2/2" {
		t.Errorf("no-key provider model step indicator = %q, want step 2/2", got)
	}
}
