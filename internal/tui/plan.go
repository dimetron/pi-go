package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/dimetron/pi-go/internal/sop"

	tea "charm.land/bubbletea/v2"
)

// toKebabCase converts a rough idea string to a kebab-case task name.
// Lowercases, replaces non-alphanumeric chars with hyphens, collapses
// consecutive hyphens, trims leading/trailing hyphens, and truncates
// to 50 characters at a word boundary.
func toKebabCase(idea string) string {
	// Lowercase.
	s := strings.ToLower(strings.TrimSpace(idea))

	// Replace non-alphanumeric characters with hyphens.
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	s = b.String()

	// Collapse consecutive hyphens.
	re := regexp.MustCompile(`-{2,}`)
	s = re.ReplaceAllString(s, "-")

	// Trim leading/trailing hyphens.
	s = strings.Trim(s, "-")

	// Truncate to 50 chars at a word (hyphen) boundary.
	if len(s) > 50 {
		s = s[:50]
		// Cut at last hyphen to avoid splitting a word.
		if idx := strings.LastIndex(s, "-"); idx > 0 {
			s = s[:idx]
		}
	}

	return s
}

// createSpecSkeleton creates the spec directory skeleton for a /plan task.
// Returns the spec directory path or an error if the directory already exists.
func createSpecSkeleton(workDir, taskName, roughIdea string) (string, error) {
	specDir := filepath.Join(workDir, "specs", taskName)

	// Check if directory already exists.
	if _, err := os.Stat(specDir); err == nil {
		return "", fmt.Errorf("spec directory already exists: %s", specDir)
	}

	// Create directory structure.
	researchDir := filepath.Join(specDir, "research")
	if err := os.MkdirAll(researchDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create spec directory: %w", err)
	}

	// Write rough-idea.md.
	roughIdeaPath := filepath.Join(specDir, "rough-idea.md")
	roughIdeaContent := fmt.Sprintf("# Rough Idea\n\n%s\n", roughIdea)
	if err := os.WriteFile(roughIdeaPath, []byte(roughIdeaContent), 0o644); err != nil {
		return "", fmt.Errorf("failed to write rough-idea.md: %w", err)
	}

	// Write empty requirements.md with Q&A header.
	reqPath := filepath.Join(specDir, "requirements.md")
	reqContent := "# Requirements\n\n## Questions & Answers\n\n"
	if err := os.WriteFile(reqPath, []byte(reqContent), 0o644); err != nil {
		return "", fmt.Errorf("failed to write requirements.md: %w", err)
	}

	return specDir, nil
}

// handlePlanCommand processes "/plan <rough idea>" input.
// Creates the spec skeleton, loads the PDD SOP, injects it as the system
// instruction, clears the conversation, and sends the rough idea as the
// first user message so the LLM drives the PDD flow.
func (m *model) handlePlanCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) == 0 {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Usage: `/plan <rough idea text>`\n\nExample: `/plan add rate limiting to API`",
		})
		m.input = ""
		m.cursorPos = 0
		return m, nil
	}

	roughIdea := strings.Join(parts, " ")
	taskName := toKebabCase(roughIdea)

	specDir, err := createSpecSkeleton(m.cfg.WorkDir, taskName, roughIdea)
	if err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error: %v", err),
		})
		m.input = ""
		m.cursorPos = 0
		return m, nil
	}

	// Load PDD SOP (project override → global override → embedded default).
	sopText, err := sop.LoadPDD(m.cfg.WorkDir)
	if err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error loading PDD SOP: %v", err),
		})
		m.input = ""
		m.cursorPos = 0
		return m, nil
	}

	// Construct augmented system instruction with SOP + task context.
	instruction := sopText + "\n\n## Current Task\n" +
		"- Task name: " + taskName + "\n" +
		"- Spec directory: specs/" + taskName + "/\n" +
		"- Rough idea: " + roughIdea + "\n\n" +
		"## Instructions\n" +
		"The spec skeleton has been created at `" + specDir + "`. " +
		"Begin the PDD process starting with Step 2 (Initial Process Planning).\n" +
		"Artifacts should be written to `specs/" + taskName + "/` using the write and edit tools.\n"

	// Rebuild the agent with the PDD SOP as system instruction.
	if err := m.cfg.Agent.RebuildWithInstruction(instruction); err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error configuring agent: %v", err),
		})
		m.input = ""
		m.cursorPos = 0
		return m, nil
	}

	// Create a fresh session so the LLM starts with a clean conversation.
	newSessionID, err := m.cfg.Agent.CreateSession(m.ctx)
	if err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error creating session: %v", err),
		})
		m.input = ""
		m.cursorPos = 0
		return m, nil
	}
	m.cfg.SessionID = newSessionID

	// Clear the TUI conversation (like /clear).
	m.messages = m.messages[:0]
	m.scroll = 0

	// Show a brief confirmation, then start the agent loop with the rough idea.
	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf("Starting PDD session for **%s**\n\nSpec directory: `%s`",
			taskName, specDir),
	})
	m.messages = append(m.messages, message{role: "user", content: roughIdea})
	m.messages = append(m.messages, message{role: "assistant", content: ""})
	m.streaming = ""
	m.thinking = ""

	// Clear input and start the agent.
	m.input = ""
	m.cursorPos = 0
	m.running = true

	m.agentCh = make(chan agentMsg, 64)
	go m.runAgentLoop(roughIdea)

	return m, waitForAgent(m.agentCh)
}
