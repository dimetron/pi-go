package tui

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

// JediTerm ignores CBT, so the renderer's predicted column diverges from the
// real cursor. Subsequent sidebar writes then wrap into the chat panel.
func TestJetBrainsCursorMovement(t *testing.T) {
	env := []string{"TERM=xterm-256color", "TERMINAL_EMULATOR=JetBrains-JediTerm", "COLORTERM=truecolor"}
	var output bytes.Buffer
	renderer := uv.NewTerminalRenderer(&output, terminalRendererEnvironment(env))
	renderer.SetRelativeCursor(true)
	renderer.Resize(80, 24)
	renderer.SetTabStops(80)
	renderer.SetBackspace(true)
	renderer.MoveTo(24, 0)
	renderer.MoveTo(16, 0)
	if err := renderer.Flush(); err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`\x1b\[[0-9]*Z`).Match(output.Bytes()) {
		t.Fatalf("renderer emitted unsupported cursor-backward-tab: %q", output.String())
	}
}

func TestTerminalRendererEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name     string
		env      []string
		wantTERM string
	}{
		{"jediterm", []string{"TERM=xterm-256color", "TERMINAL_EMULATOR=JetBrains-JediTerm", "COLORTERM=truecolor"}, "TERM=linux"},
		{"ordinary xterm", []string{"TERM=xterm-256color", "COLORTERM=truecolor"}, "TERM=xterm-256color"},
		{"tmux in IDE", []string{"TERM=tmux-256color", "TERMINAL_EMULATOR=JetBrains-JediTerm"}, "TERM=tmux-256color"},
		{"screen in IDE", []string{"TERM=screen-256color", "TERMINAL_EMULATOR=JetBrains-JediTerm"}, "TERM=screen-256color"},
		{"missing TERM", []string{"TERMINAL_EMULATOR=JetBrains-JediTerm"}, "TERM=linux"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			original := slices.Clone(tt.env)
			got := terminalRendererEnvironment(tt.env)
			if !slices.Equal(tt.env, original) {
				t.Fatal("modified caller environment")
			}
			var terms []string
			for _, entry := range got {
				if strings.HasPrefix(entry, "TERM=") {
					terms = append(terms, entry)
				}
			}
			if !slices.Equal(terms, []string{tt.wantTERM}) {
				t.Fatalf("TERM entries = %v, want %s", terms, tt.wantTERM)
			}
			for _, entry := range original {
				if !strings.HasPrefix(entry, "TERM=") && !slices.Contains(got, entry) {
					t.Errorf("lost environment entry %q", entry)
				}
			}
		})
	}
}
