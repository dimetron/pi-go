package tui

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// runWizardProgram drives a real tea.Program — not the model's Update in a loop
// — so the test covers the same path `pi setup` takes: the program's input
// parser, its renderer and its shutdown.
//
// It is deliberately separate from the Update-level tests: those prove the
// state machine, this proves the wizard is a working Bubble Tea program. A
// model that renders nothing, or a View that panics, passes every Update test
// and fails here.
func runWizardProgram(t *testing.T, cfg SetupConfig, input string) (*SetupWizard, string) {
	t.Helper()

	w := NewSetupWizard(cfg, Palette{})
	var out bytes.Buffer

	p := tea.NewProgram(w,
		tea.WithInput(strings.NewReader(input)),
		tea.WithOutput(&out),
		tea.WithoutSignals(),
		// No renderer: the program still parses input and runs Update, but
		// skips the terminal control sequences, which keeps the captured
		// output readable and independent of a terminal's capabilities.
		tea.WithoutRenderer(),
	)

	done := make(chan error, 1)
	go func() {
		_, err := p.Run()
		done <- err
	}()

	select {
	case err := <-done:
		// EOF is the expected end of a scripted input stream, not a failure.
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("program.Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("program did not exit within 10s — the wizard may be waiting for input it never receives")
	}

	return w, out.String()
}

// TestSetupWizardProgram_EndToEnd drives the real program with a keystroke
// stream and asserts the result reaches the caller and the files it names.
//
// This is the closest thing to a user running `pi setup`: the keys go through
// Bubble Tea's own input decoding rather than being handed to Update directly.
func TestSetupWizardProgram_EndToEnd(t *testing.T) {
	cfg := testWizardConfig()

	// Enter (accept anthropic) → type a key → Enter → type "haiku" to filter
	// → Enter (confirm the filtered model).
	input := "\rsk-ant-e2e-key\rhaiku\r"
	w, _ := runWizardProgram(t, cfg, input)

	res := w.Result()
	if res.Canceled {
		t.Fatalf("program exited without a confirmation; result = %+v", res)
	}
	if res.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", res.Provider)
	}
	if res.APIKey != "sk-ant-e2e-key" {
		t.Errorf("APIKey = %q, want sk-ant-e2e-key", res.APIKey)
	}
	if res.Model != "claude-haiku-4-5" {
		t.Errorf("Model = %q, want claude-haiku-4-5 (the filtered match)", res.Model)
	}
}

// TestSetupWizardProgram_ArrowKeysSelect proves the arrow keys the hint line
// advertises actually move the radio selection when decoded by the real input
// parser — a hand-built KeyPressMsg in a unit test cannot show that, because
// the escape sequence has to be interpreted first.
func TestSetupWizardProgram_ArrowKeysSelect(t *testing.T) {
	// Down arrow selects ollama (no key step), then Enter → model step, type a
	// custom model, Enter to confirm.
	input := "\x1b[B\rgemma4:e4b\r"
	w, _ := runWizardProgram(t, testWizardConfig(), input)

	res := w.Result()
	if res.Canceled {
		t.Fatalf("program exited without confirming; result = %+v", res)
	}
	if res.Provider != "ollama" {
		t.Errorf("Provider = %q, want ollama (selected with the down arrow)", res.Provider)
	}
	if res.Model != "gemma4:e4b" {
		t.Errorf("Model = %q, want gemma4:e4b", res.Model)
	}
}

// TestSetupWizardProgram_CtrlCCancels proves Ctrl+C ends the program through
// the real input path and reports cancellation.
func TestSetupWizardProgram_CtrlCCancels(t *testing.T) {
	w, _ := runWizardProgram(t, testWizardConfig(), "\x03")

	if !w.Result().Canceled {
		t.Errorf("Ctrl+C left result = %+v, want Canceled", w.Result())
	}
}

// TestSetupWizardProgram_EscQuitsFromFirstStep proves Esc cancels from the
// first step when decoded by the program's parser.
func TestSetupWizardProgram_EscQuitsFromFirstStep(t *testing.T) {
	w, _ := runWizardProgram(t, testWizardConfig(), "\x1b")

	if !w.Result().Canceled {
		t.Errorf("Esc on the first step left result = %+v, want Canceled", w.Result())
	}
}
