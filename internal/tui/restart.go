package tui

import (
	"os"
	"syscall"

	tea "charm.land/bubbletea/v2"
)

// restartMsg signals the TUI to exec-restart after clean shutdown.
type restartMsg struct{}

// handleRestartCommand triggers a clean restart of the current process.
func (m *model) handleRestartCommand() (tea.Model, tea.Cmd) {
	m.input = ""
	m.cursorPos = 0
	m.quitting = true
	return m, func() tea.Msg { return restartMsg{} }
}

// execRestart replaces the current process with a fresh copy of itself.
func execRestart() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(1)
	}
	syscall.Exec(exe, os.Args, os.Environ()) //nolint:errcheck
	os.Exit(1)
}
