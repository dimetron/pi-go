package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/extension"
)

// handleSkillCommand activates a dynamic skill by injecting its body into the
// system prompt for the next turn (Level-2 / slash-activation). The user
// message itself stays clean — only the user-provided args are sent.
func (m *model) handleSkillCommand(skill extension.Skill, args []string) (tea.Model, tea.Cmd) {
	body, display, ok := m.prepareSkillActivation(skill, args)
	if !ok {
		return m, nil
	}

	if m.cfg.Logger != nil {
		m.cfg.Logger.Info(fmt.Sprintf("skill:%s instruction=%d bytes", skill.Name, len(body)))
	}
	// Show a visible confirmation that the skill was loaded and activated. The
	// card renders as "⏺ Skill(disk-check) Successfully loaded skill" so a
	// slash-activated skill is not invisible in the transcript.
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "tool",
		tool:    "skill",
		toolIn:  skill.Name,
		content: "Successfully loaded skill",
	})
	// Send the user's args (without the body) as the user prompt.
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: display})
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})

	// Clear input and start agent
	m.inputModel.Clear()
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0

	return m, m.startAgentLoop(display)
}

// prepareSkillActivation loads the skill body, rebuilds the agent's system
// instruction with the body appended, and returns the body and the display
// string the caller should send as the user prompt. Returns ok=false if the
// body could not be loaded or the agent could not be rebuilt; in that case an
// error message has been appended to chatModel.Messages.
//
// Split out from handleSkillCommand so tests can assert the rebuild side
// effect without starting the agent loop goroutine.
func (m *model) prepareSkillActivation(skill extension.Skill, args []string) (body string, display string, ok bool) {
	body, err := extension.LoadSkillBody(m.cfg.Skills, skill.Name)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error loading skill `%s`: %v", skill.Name, err),
		})
		return "", "", false
	}

	userArgs := strings.Join(args, " ")
	display = "/" + skill.Name
	if userArgs != "" {
		display += " " + userArgs
	}

	// Rebuild the agent with the skill body injected into the system prompt.
	if m.cfg.Agent != nil {
		base := agent.LoadInstruction(agent.SystemInstruction)
		newInstruction := agent.AppendActiveSkill(base, skill, body)
		if err := m.cfg.Agent.RebuildWithInstruction(newInstruction); err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error configuring agent for skill `%s`: %v", skill.Name, err),
			})
			return "", "", false
		}
	}
	return body, display, true
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

	return m, m.startAgentLoop(prompt)
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
