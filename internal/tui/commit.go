package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	llmmodel "google.golang.org/adk/model"
	"google.golang.org/genai"
)

// commitState tracks the /commit interactive flow.
type commitState struct {
	phase   string // "generating", "confirming"
	message string // proposed commit message
}

// commitGeneratedMsg is sent when the LLM finishes generating a commit message.
type commitGeneratedMsg struct {
	message string
	err     error
}

// commitDoneMsg is sent after git commit completes.
type commitDoneMsg struct {
	output string
	err    error
}

const commitSystemPrompt = `You are a commit message generator. Given a unified diff of staged changes, generate a single conventional commit message.

Format: type(scope): description

Types: feat, fix, refactor, docs, test, chore, style, perf, ci, build
Scope: optional, the main area affected (e.g., tools, config, tui, agent)

Rules:
- One line only, no body
- Lowercase, no period at end
- Under 72 characters
- Be specific about what changed
- Output ONLY the commit message, nothing else`

// handleCommitCommand initiates the /commit flow.
func (m *model) handleCommitCommand() (tea.Model, tea.Cmd) {
	if m.cfg.GenerateCommitMsg == nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Commit generation not available (no LLM configured for commit role).",
		})
		return m, nil
	}

	cwd := m.cwd()

	// Check if we're in a git repo.
	if _, err := gitCmd(cwd, "rev-parse", "--git-dir"); err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Not a git repository.",
		})
		return m, nil
	}

	// Get porcelain status.
	status, err := gitCmd(cwd, "status", "--porcelain")
	if err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error getting git status: %v", err),
		})
		return m, nil
	}

	staged, _, _ := commitParsePorcelain(status)
	if len(staged) == 0 {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "No staged changes. Use `git add <files>` to stage changes first.",
		})
		return m, nil
	}

	// Collect diffs for staged files.
	var diffs strings.Builder
	for _, entry := range staged {
		// entry is like "M file.go" — extract filename
		parts := strings.SplitN(entry, " ", 2)
		fname := parts[len(parts)-1]
		diff, _ := gitCmd(cwd, "diff", "--cached", "--", fname)
		if diff != "" {
			fmt.Fprintf(&diffs, "=== %s ===\n%s\n", fname, diff)
		}
	}

	if diffs.Len() == 0 {
		// Could be new files — show what's staged
		diffs.WriteString("Staged files (new/renamed):\n")
		for _, entry := range staged {
			diffs.WriteString("  " + entry + "\n")
		}
	}

	// Show progress.
	m.messages = append(m.messages, message{
		role:    "assistant",
		content: "Generating commit message...",
	})
	m.commit = &commitState{phase: "generating"}

	// Start async LLM call.
	diffText := diffs.String()
	return m, func() tea.Msg {
		msg, err := m.cfg.GenerateCommitMsg(m.ctx, diffText)
		return commitGeneratedMsg{message: msg, err: err}
	}
}

// handleCommitGenerated processes the LLM-generated commit message.
func (m *model) handleCommitGenerated(msg commitGeneratedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.commit = nil
		// Update the "generating" message to show error.
		if len(m.messages) > 0 {
			m.messages[len(m.messages)-1].content = fmt.Sprintf("Error generating commit message: %v", msg.err)
		}
		return m, nil
	}

	// Clean up the message (trim whitespace, remove quotes).
	commitMsg := strings.TrimSpace(msg.message)
	commitMsg = strings.Trim(commitMsg, "\"'`")
	commitMsg = strings.TrimSpace(commitMsg)

	m.commit = &commitState{phase: "confirming", message: commitMsg}

	// Update the "generating" message to show the proposed commit.
	if len(m.messages) > 0 {
		m.messages[len(m.messages)-1].content = fmt.Sprintf(
			"**Proposed commit message:**\n```\n%s\n```\n\nPress **Enter** to commit, **Esc** to cancel.",
			commitMsg,
		)
	}

	return m, nil
}

// handleCommitConfirm executes the commit.
func (m *model) handleCommitConfirm() (tea.Model, tea.Cmd) {
	if m.commit == nil || m.commit.phase != "confirming" {
		return m, nil
	}

	commitMsg := m.commit.message
	m.commit = nil

	cwd := m.cwd()
	return m, func() tea.Msg {
		out, err := gitCmd(cwd, "commit", "-m", commitMsg)
		return commitDoneMsg{output: out, err: err}
	}
}

// handleCommitDone processes the git commit result.
func (m *model) handleCommitDone(msg commitDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Commit failed: %v\n%s", msg.err, msg.output),
		})
	} else {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Committed successfully.",
		})
	}
	return m, nil
}

// handleCommitCancel cancels the commit flow.
func (m *model) handleCommitCancel() (tea.Model, tea.Cmd) {
	m.commit = nil
	m.messages = append(m.messages, message{
		role:    "assistant",
		content: "Commit cancelled.",
	})
	return m, nil
}

// cwd returns the current working directory.
func (m *model) cwd() string {
	if m.cfg.WorkDir != "" {
		return m.cfg.WorkDir
	}
	return "."
}

// gitCmd runs a git command and returns stdout.
func gitCmd(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(stderr.String()), err)
	}
	return stdout.String(), nil
}

// commitParsePorcelain parses `git status --porcelain` output.
func commitParsePorcelain(output string) (staged, unstaged, untracked []string) {
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 3 {
			continue
		}
		x := line[0]
		y := line[1]
		file := strings.TrimSpace(line[2:])
		if idx := strings.Index(file, " -> "); idx >= 0 {
			file = file[idx+4:]
		}

		if x == '?' && y == '?' {
			untracked = append(untracked, file)
			continue
		}
		if x != ' ' && x != '?' {
			staged = append(staged, string(x)+" "+file)
		}
		if y != ' ' && y != '?' {
			unstaged = append(unstaged, string(y)+" "+file)
		}
	}
	return
}

// GenerateCommitMsgFunc creates a function that generates commit messages using the given LLM.
func GenerateCommitMsgFunc(llm llmmodel.LLM) func(ctx context.Context, diffs string) (string, error) {
	return func(ctx context.Context, diffs string) (string, error) {
		req := &llmmodel.LLMRequest{
			Contents: []*genai.Content{
				genai.NewContentFromText(diffs, genai.RoleUser),
			},
			Config: &genai.GenerateContentConfig{
				SystemInstruction: genai.NewContentFromText(commitSystemPrompt, genai.RoleUser),
			},
		}

		var result strings.Builder
		for resp, err := range llm.GenerateContent(ctx, req, false) {
			if err != nil {
				return "", fmt.Errorf("LLM error: %w", err)
			}
			if resp.Content != nil {
				for _, part := range resp.Content.Parts {
					if part.Text != "" {
						result.WriteString(part.Text)
					}
				}
			}
		}

		msg := strings.TrimSpace(result.String())
		if msg == "" {
			return "", fmt.Errorf("LLM returned empty commit message")
		}
		return msg, nil
	}
}
