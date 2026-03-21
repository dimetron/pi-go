package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/extension"
)

// handleSkillCommand executes a dynamic skill by sending its instruction to the agent.
func (m *model) handleSkillCommand(skill extension.Skill, args []string) (tea.Model, tea.Cmd) {
	// Build the prompt: skill instruction + user arguments
	userArgs := strings.Join(args, " ")
	prompt := skill.Instruction
	if userArgs != "" {
		prompt = skill.Instruction + "\n\nUser request: " + userArgs
	}

	// Show as user message
	display := "/" + skill.Name
	if userArgs != "" {
		display += " " + userArgs
	}
	if m.cfg.Logger != nil {
		m.cfg.Logger.Info(fmt.Sprintf("skill:%s instruction=%d bytes", skill.Name, len(prompt)))
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: display})
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})

	// Clear input and start agent
	m.inputModel.Clear()
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0

	m.agentCh = make(chan agentMsg, 64)
	go m.runAgentLoop(prompt)

	return m, waitForAgent(m.agentCh)
}

// pendingSkillCreate holds state for skill-create overwrite confirmation.
type pendingSkillCreate struct {
	name string
	desc string
	path string
}

// handleSkillCreateCommand creates a new skill file directly (internal command).
func (m *model) handleSkillCreateCommand(args []string) (tea.Model, tea.Cmd) {
	m.inputModel.Clear()

	if len(args) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Usage: `/skill-create <name> [description]`\nCreates `.pi-go/skills/<name>/SKILL.md`",
		})
		return m, nil
	}

	skillName := strings.TrimSpace(args[0])

	// Validate skill name
	for _, c := range skillName {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '-' && c != '_' {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: "Invalid skill name. Use only alphanumeric characters, dashes, and underscores.",
			})
			return m, nil
		}
	}

	desc := ""
	if len(args) > 1 {
		desc = strings.Join(args[1:], " ")
	}

	skillDir := filepath.Join(".pi-go", "skills", skillName)
	skillPath := filepath.Join(skillDir, "SKILL.md")

	// Check if already exists — ask to overwrite
	if _, err := os.Stat(skillPath); err == nil {
		m.pendingSkillCreate = &pendingSkillCreate{
			name: skillName,
			desc: desc,
			path: skillPath,
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Skill already exists: `%s`\n\nPress **Enter** to overwrite, **Esc** to cancel.", skillPath),
		})
		return m, nil
	}

	return m.writeSkillFile(skillName, desc, skillPath)
}

// handleSkillCreateConfirm handles Enter during skill-create overwrite confirmation.
func (m *model) handleSkillCreateConfirm() (tea.Model, tea.Cmd) {
	p := m.pendingSkillCreate
	m.pendingSkillCreate = nil
	return m.writeSkillFile(p.name, p.desc, p.path)
}

// handleSkillCreateCancel cancels skill-create overwrite.
func (m *model) handleSkillCreateCancel() (tea.Model, tea.Cmd) {
	m.pendingSkillCreate = nil
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "Skill creation canceled.",
	})
	return m, nil
}

// writeSkillFile creates the skill directory and writes the SKILL.md template.
func (m *model) writeSkillFile(name, desc, path string) (tea.Model, tea.Cmd) {
	skillDir := filepath.Dir(path)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error creating directory: %v", err),
		})
		return m, nil
	}

	content := fmt.Sprintf(`---
name: %s
description: %s
---

# %s

[Instructions for this skill]

## Examples

- Example usage 1
- Example usage 2

## Guidelines

- Guideline 1
- Guideline 2
`, name, desc, name)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error creating skill: %v", err),
		})
		return m, nil
	}

	m.inputModel.ReloadSkills()
	m.cfg.Skills = m.inputModel.Skills

	// Start agent to interview the user and refine the skill
	prompt := fmt.Sprintf(`A new skill file was just created at %s with a basic template.

Configure skill /%s in two phases:

**Phase 1 — Research:** Do quick research first:
- Search the codebase for related patterns, commands, or workflows
- Check existing skills in .pi-go/skills/ for reference
- Identify what tools and steps are typically needed for this kind of task

**Phase 2 — Interview:** Based on your research, ask the user 1-3 focused questions:
A. What should this skill do and when? (one sentence)
B. What are the key steps? (or confirm the steps you found)
C. Anything specific to add or change?

After the user answers, update %s with:
- frontmatter: name + description from answer A
- Instructions expanded from answers
- ## Steps from answer B or research
- ## Examples with concrete usage
- ## Guidelines from answer C + research`, path, name, path)

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("Created skill `/%s` at `%s`\n\nLet me help you configure it.", name, path),
	})
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})

	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0

	m.agentCh = make(chan agentMsg, 64)
	go m.runAgentLoop(prompt)

	return m, waitForAgent(m.agentCh)
}

// handleSkillListCommand lists all currently loaded skills.
func (m *model) handleSkillListCommand() (tea.Model, tea.Cmd) {
	m.inputModel.Clear()

	if len(m.cfg.Skills) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "No skills loaded. Place `*.SKILL.md` files in `~/.pi-go/skills/` or `.pi-go/skills/`.",
		})
		return m, nil
	}

	var b strings.Builder
	b.WriteString("**Loaded skills:**\n")
	for _, s := range m.cfg.Skills {
		fmt.Fprintf(&b, "  `/%s`", s.Name)
		if s.Description != "" {
			b.WriteString(" — " + s.Description)
		}
		b.WriteString("\n")
	}
	if len(m.cfg.SkillDirs) > 0 {
		b.WriteString("\n**Skill directories:**\n")
		for _, d := range m.cfg.SkillDirs {
			fmt.Fprintf(&b, "  `%s`\n", d)
		}
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: b.String(),
	})
	return m, nil
}

// handleSkillLoadCommand reloads skills from disk and reports what was found.
func (m *model) handleSkillLoadCommand() (tea.Model, tea.Cmd) {
	m.inputModel.Clear()

	m.inputModel.ReloadSkills()
	m.cfg.Skills = m.inputModel.Skills

	if len(m.cfg.Skills) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Reloaded: no skills found. Place `*.SKILL.md` files in `~/.pi-go/skills/` or `.pi-go/skills/`.",
		})
		return m, nil
	}

	var names []string
	for _, s := range m.cfg.Skills {
		names = append(names, "/"+s.Name)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("Reloaded %d skill(s): %s", len(m.cfg.Skills), strings.Join(names, ", ")),
	})
	return m, nil
}
