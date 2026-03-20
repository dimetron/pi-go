package tui

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	historyFile    = "history"
	maxHistorySize = 1000
)

// historyPath returns the path to the persistent history file (~/.pi-go/history).
func historyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".pi-go", historyFile)
}

// loadHistory reads command history from disk.
func loadHistory() []string {
	path := historyPath()
	if path == "" {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
	}

	// Keep only the last maxHistorySize entries.
	if len(lines) > maxHistorySize {
		lines = lines[len(lines)-maxHistorySize:]
	}
	return lines
}

// appendHistory adds a single entry to the persistent history file.
func appendHistory(entry string) {
	path := historyPath()
	if path == "" {
		return
	}

	// Ensure directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()

	// Replace newlines with spaces to keep one-entry-per-line format.
	entry = strings.ReplaceAll(entry, "\n", " ")
	f.WriteString(entry + "\n")
}

// handleHistoryCommand shows command history, optionally filtered by a query.
func (m *model) handleHistoryCommand(args []string) {
	query := strings.ToLower(strings.Join(args, " "))

	var filtered []string
	for _, h := range m.inputModel.History {
		if query == "" || strings.Contains(strings.ToLower(h), query) {
			filtered = append(filtered, h)
		}
	}

	if len(filtered) == 0 {
		msg := "No command history."
		if query != "" {
			msg = fmt.Sprintf("No history matching `%s`.", query)
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: msg})
		return
	}

	// Show last 20 entries.
	start := 0
	if len(filtered) > 20 {
		start = len(filtered) - 20
	}
	var sb strings.Builder
	if query != "" {
		sb.WriteString(fmt.Sprintf("**History matching `%s`** (%d total):\n", query, len(filtered)))
	} else {
		sb.WriteString(fmt.Sprintf("**Command history** (%d total, showing last %d):\n", len(filtered), len(filtered)-start))
	}
	for i := start; i < len(filtered); i++ {
		sb.WriteString(fmt.Sprintf("- `%s`\n", filtered[i]))
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: sb.String()})
}
