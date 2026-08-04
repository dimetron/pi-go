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

// handleSkillListCommand reports the skill set: what is loaded, and what else
// was found on disk but is not in play.
func (m *model) handleSkillListCommand() (tea.Model, tea.Cmd) {
	m.inputModel.Clear()
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: formatSkillsReport(m.cfg.Skills, m.cfg.SkillDirs),
	})
	return m, nil
}

// skillDescLimit caps a description in the table. Frontmatter descriptions run
// to several hundred characters — they are written to be matched by a model,
// not read in a cell — and one of them would otherwise set the column width for
// the whole table.
const skillDescLimit = 72

// formatSkillsReport renders the skill set as two tables: the skills the model
// can actually see, and everything else discovered under the skill directories.
//
// The second table is the point. A skill silently losing to a same-named one in
// a higher-priority directory, or failing to load at all, is indistinguishable
// from "never written" in a list that only shows winners — and the usual first
// symptom is a skill that simply never triggers.
func formatSkillsReport(skills []extension.Skill, dirs []string) string {
	if len(skills) == 0 {
		return "No skills loaded. Place `SKILL.md` files in `~/.pi-go/skills/<name>/` or `.pi-go/skills/<name>/`."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**Loaded skills (%d)**\n\n", len(skills))
	b.WriteString("| Skill | Source | Body | Description |\n|---|---|---:|---|\n")

	var bodyTotal int
	for _, s := range skills {
		body := "—"
		if txt, err := extension.LoadSkillBody(skills, s.Name); err == nil && txt != "" {
			bodyTotal += len(txt)
			body = fmt.Sprintf("%.1f KB", float64(len(txt))/1024)
		}
		fmt.Fprintf(&b, "| `/%s` | %s | %s | %s |\n",
			s.Name, s.Source, body, tableCell(s.Description, skillDescLimit))
	}

	// Only the menu is resident; bodies are injected per activation. Printing
	// both makes the difference legible instead of implying the whole corpus is
	// in the window.
	fmt.Fprintf(&b, "\nMenu in context: **~%d tokens**. Bodies on demand: **%.0f KB** total, injected only when a skill activates.\n",
		skillMenuTokens(skills), float64(bodyTotal)/1024)

	if rows := discoverInactiveSkills(skills, dirs); len(rows) > 0 {
		fmt.Fprintf(&b, "\n**Discovered but not active (%d)**\n\n", len(rows))
		b.WriteString("| Skill | Found in | Why |\n|---|---|---|\n")
		for _, r := range rows {
			fmt.Fprintf(&b, "| `%s` | `%s` | %s |\n", r.name, r.dir, r.why)
		}
	}

	if len(dirs) > 0 {
		b.WriteString("\n**Skill directories** (later overrides earlier)\n\n")
		for _, d := range dirs {
			fmt.Fprintf(&b, "- `%s`\n", shortSkillDir(d))
		}
	}
	return b.String()
}

// inactiveSkill is a SKILL.md on disk that the model is not seeing.
type inactiveSkill struct{ name, dir, why string }

// discoverInactiveSkills walks the skill directories and reports every SKILL.md
// that did not become the loaded skill of that name.
//
// Two causes, distinguished because they need different fixes: a name that
// loaded from somewhere else is *shadowed* (rename it or edit the winner), and
// a name that loaded from nowhere failed to load (bad frontmatter, or the audit
// blocked it — `pi audit` says which).
//
// The "is this file the winner?" check keys on the absolute path, not on the
// directory name, so a SKILL.md whose frontmatter `name:` differs from its
// directory still classifies correctly.
func discoverInactiveSkills(skills []extension.Skill, dirs []string) []inactiveSkill {
	winnerByPath := make(map[string]struct{}, len(skills))
	for _, s := range skills {
		if s.BodyPath != "" {
			winnerByPath[s.BodyPath] = struct{}{}
		}
	}
	var out []inactiveSkill
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name(), "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				continue
			}
			if _, isWinner := winnerByPath[path]; isWinner {
				continue // already in the first table
			}
			inactive := inactiveSkill{name: e.Name(), dir: shortSkillDir(dir)}
			if s, ok := extension.FindSkill(skills, e.Name()); ok {
				if s.BodyPath != "" {
					inactive.why = "shadowed by `" + shortSkillDir(filepath.Dir(filepath.Dir(s.BodyPath))) + "`"
				} else {
					inactive.why = "shadowed by `" + s.Source + "`"
				}
			} else {
				inactive.why = "not loaded — bad frontmatter or blocked by audit"
			}
			out = append(out, inactive)
		}
	}
	return out
}

// shortSkillDir renders a skill directory the way a person refers to it:
// relative to the project when it is inside it, `~`-prefixed when it is under
// home, absolute only when it is neither.
//
// Absolute paths are what broke the table. Every skill directory here shares a
// long prefix, so the one distinguishing segment — `.pi-go` versus `.claude` —
// arrives past the column's width and gets truncated away, leaving two rows
// that read identically and say nothing.
func shortSkillDir(dir string) string {
	if cwd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(cwd, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, dir); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return dir
}

// skillMenuTokens estimates what the skills menu costs in the system prompt, in
// the same "- /name: description" shape appendSkillsMenu writes, at the usual
// ~4 characters per token.
func skillMenuTokens(skills []extension.Skill) int {
	chars := 0
	for _, s := range skills {
		chars += len(s.Name) + len(s.Description) + 5
	}
	return chars / 4
}

// tableCell flattens text to one line and truncates it, so no cell can break
// the table's row structure or blow out a column.
func tableCell(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return strings.TrimSpace(string(r[:limit-1])) + "…"
}

// handleSkillLoadCommand reloads skills from disk and reports what was found.
func (m *model) handleSkillLoadCommand() (tea.Model, tea.Cmd) {
	m.inputModel.Clear()

	m.inputModel.ReloadSkills()
	m.cfg.Skills = m.inputModel.Skills

	if len(m.cfg.Skills) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Reloaded: no skills found. Place `SKILL.md` files in `~/.pi-go/skills/<name>/` or `.pi-go/skills/<name>/`.",
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
