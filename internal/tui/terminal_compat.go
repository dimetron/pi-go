package tui

import (
	"io"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// terminalProgramOptions keeps renderer capabilities separate from color
// detection. JediTerm advertises xterm-256color but ignores CBT (CSI Z), so
// incremental updates drift horizontally and spill sidebar cells into chat.
func terminalProgramOptions(output io.Writer, env []string) []tea.ProgramOption {
	return []tea.ProgramOption{
		tea.WithEnvironment(terminalRendererEnvironment(env)),
		tea.WithColorProfile(colorprofile.Detect(output, env)),
	}
}

func terminalRendererEnvironment(env []string) []string {
	if !slices.Contains(env, "TERMINAL_EMULATOR=JetBrains-JediTerm") {
		return env
	}
	// A multiplexer interprets the renderer's commands itself; its advertised
	// capabilities take precedence over the inherited outer-terminal marker.
	for _, entry := range env {
		if strings.HasPrefix(entry, "TERM=screen") || strings.HasPrefix(entry, "TERM=tmux") {
			return env
		}
	}

	// Bubble Tea 2.0.9 has no per-capability override. Its linux renderer
	// profile avoids tab movement (including CBT) while retaining ordinary
	// cursor/erase commands. This environment belongs only to Bubble Tea:
	// subprocesses keep the real TERM, and colors use the original env above.
	compatible := slices.DeleteFunc(slices.Clone(env), func(entry string) bool {
		return strings.HasPrefix(entry, "TERM=")
	})
	return append(compatible, "TERM=linux")
}
