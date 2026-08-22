package tui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/dimetron/pi-go/internal/session"
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

// specCategories maps keywords to spec category folders.
// Order matters: first match wins, so more specific keywords come first.
var specCategories = []struct {
	category string
	keywords []string
}{
	{"skills", []string{"skill", "audit skill", "skill-", "SKILL.md"}},
	{"memory", []string{"memory", "palace", "mem-", "memoir", "remember", "recall", "embedding"}},
	{"sessions", []string{"session", "log optim", "conversation", "recovery", "replay", "trajectory", "atif"}},
	{"features/TOO", []string{"tool", "lsp", "completion", "command", "tui", "sidebar", "provider", "ollama", "oauth", "login", "web-serve", "webserver", "subagent", "agent", "test", "bench", "eval"}},
}

// detectCategory inspects the rough idea text and returns the matching
// spec category. Returns "features/TOO" as the default if no keywords match.
func detectCategory(roughIdea string) string {
	lower := strings.ToLower(roughIdea)
	for _, cat := range specCategories {
		for _, kw := range cat.keywords {
			if strings.Contains(lower, kw) {
				return cat.category
			}
		}
	}
	return "features/TOO" // default category
}

// nextSpecNumber scans a category directory under specs/ and returns the
// next zero-padded number prefix (e.g. "004"). If the category directory
// doesn't exist yet, returns "001".
func nextSpecNumber(workDir, category string) string {
	catDir := filepath.Join(workDir, "specs", category)
	entries, err := os.ReadDir(catDir)
	if err != nil {
		return "001"
	}

	maxNum := 0
	numRe := regexp.MustCompile(`^(\d{3})-`)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := numRe.FindStringSubmatch(e.Name())
		if m != nil {
			n := 0
			for _, c := range m[1] {
				n = n*10 + int(c-'0')
			}
			if n > maxNum {
				maxNum = n
			}
		}
	}
	return fmt.Sprintf("%03d", maxNum+1)
}

// findExistingSpec searches a category directory for an existing spec whose
// name (after the NNN- prefix) matches baseName. Returns the full relative
// path from specs/ (e.g. "tools/001-my-feature") or "" if not found.
func findExistingSpec(workDir, category, baseName string) string {
	catDir := filepath.Join(workDir, "specs", category)
	entries, err := os.ReadDir(catDir)
	if err != nil {
		return ""
	}
	numPrefix := regexp.MustCompile(`^\d{3}-`)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		stripped := numPrefix.ReplaceAllString(e.Name(), "")
		if stripped == baseName {
			return filepath.Join(category, e.Name())
		}
	}
	return ""
}

// createSpecSkeleton creates the spec directory skeleton for a /plan task.
// The taskName may include a category prefix (e.g. "tools/003-my-feature").
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

// startPlanWorktree creates the isolated branch used by /plan.
func (m *model) startPlanWorktree(taskName string) (string, error) {
	if m.cfg.Orchestrator == nil || m.cfg.Orchestrator.Worktree() == nil {
		return "", fmt.Errorf("/plan requires a git worktree manager")
	}
	wm := m.cfg.Orchestrator.Worktree()
	agentID := "plan-" + taskName
	wtPath, err := wm.Create(agentID, "pdd-"+taskName)
	if err != nil {
		return "", fmt.Errorf("create PDD worktree: %w", err)
	}
	m.planWorktreeAgentID = agentID
	m.planWorktreePath = wtPath
	m.planTaskName = taskName
	m.planBackupBranch = "specs/" + taskName
	m.planWorktree = wm
	return wtPath, nil
}

// finishPlanWorktree preserves the completed spec under specs/<task-name>,
// merges the temporary planning branch into the invoking branch, and removes
// only the temporary worktree branch.
//
// It runs at the end of every planning turn, not just the last one, so the
// commit and the backup ref below double as an incremental snapshot: a plan
// abandoned before PROMPT.md exists still survives on specs/<task-name> even
// though shutdown force-removes the temporary worktree and its branch.
func (m *model) finishPlanWorktree() error {
	if m.planWorktree == nil {
		return nil
	}

	// Commit first. The planner writes files and never commits — the PDD SOP
	// says nothing about git — so without this the backup ref below points at
	// a commit holding none of the plan, MergeBack merges nothing, and Cleanup
	// deletes the only copy.
	if _, err := m.planWorktree.CommitAll(m.planWorktreeAgentID, "PDD plan: "+m.planTaskName); err != nil {
		return err
	}
	if err := m.planWorktree.CreateBackupBranch(m.planWorktreeAgentID, m.planBackupBranch); err != nil {
		return err
	}

	// The plan is only finished once PROMPT.md exists. Until then keep the
	// worktree so the next turn can carry on in it.
	promptPath := filepath.Join(m.planWorktreePath, "specs", m.planTaskName, "PROMPT.md")
	if _, err := os.Stat(promptPath); err != nil {
		return nil
	}

	// Merge the worktree branch into the invoking branch. This brings the spec
	// files into <workDir>/specs/<task>/ as tracked files.
	if _, err := m.planWorktree.MergeBack(m.planWorktreeAgentID); err != nil {
		// Preserve the finished spec by copying it out even though the merge
		// failed, so /run still finds PROMPT.md.
		_ = copyDir(filepath.Join(m.planWorktreePath, "specs", m.planTaskName),
			filepath.Join(m.cfg.WorkDir, "specs", m.planTaskName))
		return err
	}

	// Belt-and-suspenders: after the merge, copy the finished spec into the
	// invoking checkout's specs/ tree too. /run reads <workDir>/specs/<task>/
	// from disk, so this guarantees the spec is present even if the merge was
	// somehow a no-op. Copying the whole spec dir mirrors everything the
	// planner wrote, so research/ and supporting files survive too. (This runs
	// after the merge, not before: writing untracked copies first would make
	// the merge abort on files it is about to bring in.)
	if err := copyDir(filepath.Join(m.planWorktreePath, "specs", m.planTaskName),
		filepath.Join(m.cfg.WorkDir, "specs", m.planTaskName)); err != nil {
		return fmt.Errorf("copying finished spec into invoking checkout: %w", err)
	}

	if err := m.planWorktree.Cleanup(m.planWorktreeAgentID); err != nil {
		return err
	}
	m.planWorktree = nil
	// The worktree is gone; leaving its path behind would point later turns at
	// a directory that no longer exists.
	m.planWorktreePath = ""
	return nil
}

// copyDir recursively copies the contents of src into dst. dst is created if
// missing. It mirrors the worktree's spec directory into the invoking checkout
// so /run can consume it independently of any git merge.
//
// Existing files in dst are left as-is: when this runs after a successful
// merge the merged copies are already present and identical, and overwriting
// them would be wasted work. So a file already existing in dst is not an
// error — CopyFS reports it as fs.ErrExist and we treat that as success.
func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}

// handlePlanCommand processes "/plan <rough idea>" input.
// Creates the spec skeleton in an isolated worktree, loads the PDD SOP,
// injects it as the system instruction, clears the conversation, and sends
// the rough idea as the first user message so the LLM drives the PDD flow.
func (m *model) handlePlanCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Usage: `/plan <rough idea text>`\n\nExample: `/plan add rate limiting to API`",
		})
		m.inputModel.Clear()
		return m, nil
	}

	roughIdea := strings.Join(parts, " ")
	baseName := toKebabCase(roughIdea)

	category := detectCategory(roughIdea)
	var taskName string
	workDir := m.cfg.WorkDir
	if existing := findExistingSpec(workDir, category, baseName); existing != "" {
		taskName = existing
	} else {
		num := nextSpecNumber(workDir, category)
		taskName = filepath.Join(category, num+"-"+baseName)
	}

	if m.planWorktree == nil {
		wtPath, wtErr := m.startPlanWorktree(taskName)
		if wtErr != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: fmt.Sprintf("Error: %v", wtErr)})
			m.inputModel.Clear()
			return m, nil
		}
		workDir = wtPath
	}

	specDir, err := createSpecSkeleton(workDir, taskName, roughIdea)
	if err != nil {
		// Directory exists — auto-resume the existing plan.
		existingDir := filepath.Join(workDir, "specs", taskName)
		if strings.Contains(err.Error(), "already exists") {
			specDir = existingDir
			return m.startPlanSession(taskName, roughIdea, specDir)
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error: %v", err),
		})
		m.inputModel.Clear()
		return m, nil
	}

	return m.startPlanSession(taskName, roughIdea, specDir)
}

// startPlanSession loads the SOP, rebuilds the agent, and starts streaming.
func (m *model) startPlanSession(taskName, roughIdea, specDir string) (tea.Model, tea.Cmd) {
	workDir := m.cfg.WorkDir
	if m.planWorktreePath != "" {
		workDir = m.planWorktreePath
	}
	// Load PDD SOP (project override → global override → embedded default).
	sopText, err := sop.LoadPDD(workDir)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error loading PDD SOP: %v", err),
		})
		m.inputModel.Clear()
		return m, nil
	}

	instruction := sopText + "\n\n## Current Task\n" +
		"- Task name: " + taskName + "\n" +
		"- Spec directory: " + specDir + "/\n" +
		"- Rough idea: " + roughIdea + "\n\n" +
		"## Instructions\n" +
		"The spec skeleton has been created at `" + specDir + "`. " +
		"Begin the PDD process starting with Step 2 (Initial Process Planning).\n" +
		"Artifacts must be written to `" + specDir + "/` using the write and edit tools. " +
		"The current planning branch is isolated from the invoking branch; do not write PDD artifacts to the invoking checkout.\n"

	// Rebuild the agent with the PDD SOP as system instruction.
	if m.cfg.Agent == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Error: no agent configured for /plan",
		})
		m.inputModel.Clear()
		return m, nil
	}
	if err := m.cfg.Agent.RebuildWithInstruction(instruction); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error configuring agent: %v", err),
		})
		m.inputModel.Clear()
		return m, nil
	}

	// Create a fresh session so the LLM starts with a clean conversation.
	newSessionID, defaultTitle, err := m.cfg.Agent.CreateSession(m.ctx)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error creating session: %v", err),
		})
		m.inputModel.Clear()
		return m, nil
	}
	m.cfg.SessionID = newSessionID
	// Surface the default title in the terminal window/tab title the same way
	// the initial session does — /plan starts a new conversation whose
	// prompt-driven title won't arrive until the first turn completes.
	if defaultTitle != "" {
		m.sessionTitle = defaultTitle
	}

	// Persist plan context for resume (non-fatal).
	if m.cfg.SessionService != nil {
		_ = m.cfg.SessionService.UpdatePlanContext(newSessionID, &session.PlanContext{
			TaskName:  taskName,
			RoughIdea: roughIdea,
			SpecDir:   specDir,
			Phase:     "plan",
		})
	}

	// Clear the TUI conversation (like /clear).
	m.chatModel.Messages = m.chatModel.Messages[:0]
	m.chatModel.Scroll = 0

	// Show a brief confirmation, then start the agent loop with the rough idea.
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role: "assistant",
		content: fmt.Sprintf("Starting PDD session for **%s**\n\nSpec directory: `%s`",
			taskName, specDir),
	})
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: roughIdea})
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""

	m.mode = "plan"
	m.running = true

	return m, m.startAgentLoop(roughIdea)
}
