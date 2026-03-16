package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

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
// Creates the spec skeleton and shows confirmation.
// Agent kickoff with SOP injection is deferred to Step 3.
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

	m.messages = append(m.messages, message{
		role: "assistant",
		content: fmt.Sprintf("Created spec skeleton: `%s`\n\n"+
			"- `rough-idea.md` — your idea\n"+
			"- `requirements.md` — Q&A template\n"+
			"- `research/` — research artifacts\n\n"+
			"_PDD SOP injection will be added in the next step._",
			specDir),
	})

	m.input = ""
	m.cursorPos = 0
	return m, nil
}
