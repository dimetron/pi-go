package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/extension"
	pisession "github.com/dimetron/pi-go/internal/session"

	tea "charm.land/bubbletea/v2"
)

func (m *model) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])

	// Log all slash commands.
	if m.cfg.Logger != nil {
		m.cfg.Logger.UserMessage(input)
	}

	switch cmd {
	case "/help":
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.formatHelp(),
		})
	case "/clear":
		m.chatModel.Messages = m.chatModel.Messages[:0]
		m.chatModel.Scroll = 0
	case "/model":
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.formatModelInfo(),
		})
	case "/session":
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Session: `%s`", m.cfg.SessionID),
		})
	case "/context":
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.formatContextUsage(),
		})
	case "/branch":
		m.handleBranchCommand(parts[1:])
	case "/compact":
		m.handleCompactCommand()
	case "/agents":
		m.handleAgentsCommand()
	case "/history":
		m.handleHistoryCommand(parts[1:])
	case "/commit":
		return m.handleCommitCommand()
	case "/plan":
		return m.handlePlanCommand(parts[1:])
	case "/run":
		return m.handleRunCommand(parts[1:])
	case "/login":
		return m.handleLoginCommand(parts[1:])
	case "/skills":
		return m.handleSkillsCommand(parts[1:])
	case "/skill-create":
		return m.handleSkillCreateCommand(parts[1:])
	case "/skill-load":
		return m.handleSkillLoadCommand()
	case "/skill-list":
		return m.handleSkillListCommand()
	case "/rtk":
		m.handleRTKCommand(parts[1:])
	case "/ping":
		return m.handlePingCommand(parts[1:])
	case "/restart":
		return m.handleRestartCommand()
	case "/exit", "/quit":
		m.quitting = true
		return m, tea.Quit
	default:
		// Check if it's a dynamic skill command.
		skillName := strings.TrimPrefix(cmd, "/")
		if skill, ok := extension.FindSkill(m.cfg.Skills, skillName); ok {
			return m.handleSkillCommand(skill, parts[1:])
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Unknown command: `%s`. Type `/help` for available commands.", cmd),
		})
	}

	return m, nil
}

// handleBranchCommand handles /branch subcommands: create, switch, list.
func (m *model) handleBranchCommand(args []string) {
	if m.cfg.SessionService == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Branching not available (no session service configured).",
		})
		return
	}

	if len(args) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Usage: `/branch <name>` to create, `/branch switch <name>` to switch, `/branch list` to list.",
		})
		return
	}

	subcmd := strings.ToLower(args[0])

	switch subcmd {
	case "list":
		branches, active, err := m.cfg.SessionService.ListBranches(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID,
		)
		if err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error listing branches: %v", err),
			})
			return
		}
		var b strings.Builder
		b.WriteString("**Branches:**\n")
		for _, br := range branches {
			marker := " "
			if br.Name == active {
				marker = "*"
			}
			fmt.Fprintf(&b, "- %s `%s` (head: %d)\n", marker, br.Name, br.Head)
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: b.String()})

	case "switch":
		if len(args) < 2 {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: "Usage: `/branch switch <name>`",
			})
			return
		}
		branchName := args[1]
		err := m.cfg.SessionService.SwitchBranch(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID, branchName,
		)
		if err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error switching branch: %v", err),
			})
			return
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Switched to branch `%s`.", branchName),
		})

	default:
		// Treat as branch name to create.
		branchName := subcmd
		err := m.cfg.SessionService.CreateBranch(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID, branchName,
		)
		if err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error creating branch: %v", err),
			})
			return
		}
		// Auto-switch to the new branch.
		_ = m.cfg.SessionService.SwitchBranch(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID, branchName,
		)
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Created and switched to branch `%s`.", branchName),
		})
	}
}

// handleCompactCommand triggers session compaction.
func (m *model) handleCompactCommand() {
	if m.cfg.SessionService == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Compaction not available (no session service configured).",
		})
		return
	}

	err := m.cfg.SessionService.Compact(
		m.cfg.SessionID, agent.AppName, agent.DefaultUserID,
		pisession.SimpleSummarizer, pisession.CompactConfig{},
	)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Compaction error: %v", err),
		})
		return
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "Session context compacted.",
	})
}

// handleAgentsCommand shows the status of running and recent subagents.
func (m *model) handleAgentsCommand() {
	if m.cfg.Orchestrator == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Subagent system not available.",
		})
		return
	}

	agents := m.cfg.Orchestrator.List()
	if len(agents) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "No subagents have been spawned yet.",
		})
		return
	}

	running, done, failed := 0, 0, 0
	for _, a := range agents {
		switch a.Status {
		case "running":
			running++
		case "completed":
			done++
		case "failed":
			failed++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**Subagents** — %d total, %d running, %d done", len(agents), running, done)
	if failed > 0 {
		fmt.Fprintf(&b, ", %d failed", failed)
	}
	b.WriteString("\n\n")

	for _, a := range agents {
		icon := "  "
		switch a.Status {
		case "running":
			icon = "▶ "
		case "completed":
			icon = "✓ "
		case "failed":
			icon = "✗ "
		case "cancelled":
			icon = "◼ "
		}

		prompt := a.Prompt
		if len(prompt) > 70 {
			prompt = prompt[:67] + "..."
		}

		dur := a.Duration
		if dur == "" {
			dur = time.Since(a.StartedAt).Truncate(time.Second).String()
		}

		fmt.Fprintf(&b, "%s `%s` **%s** [%s] %s (%s)\n", icon, a.AgentID[:8], a.Type, a.Status, prompt, dur)
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: b.String(),
	})
}

// formatModelInfo returns a formatted string showing the current model and all configured roles.
func (m *model) formatModelInfo() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Current model: **%s**", m.cfg.ModelName)
	if m.cfg.ActiveRole != "" && m.cfg.ActiveRole != "default" {
		fmt.Fprintf(&b, " (role: %s)", m.cfg.ActiveRole)
	}

	if len(m.cfg.Roles) > 0 {
		b.WriteString("\n\n**Configured roles:**\n")
		// Sort role names for stable output.
		names := make([]string, 0, len(m.cfg.Roles))
		for name := range m.cfg.Roles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			rc := m.cfg.Roles[name]
			marker := " "
			if name == m.cfg.ActiveRole || (m.cfg.ActiveRole == "" && name == "default") {
				marker = "*"
			}
			provInfo := ""
			if rc.Provider != "" {
				provInfo = fmt.Sprintf(" [%s]", rc.Provider)
			}
			fmt.Fprintf(&b, "- %s **%s**: `%s`%s\n", marker, name, rc.Model, provInfo)
		}
	}
	return b.String()
}

// formatContextUsage builds a context usage display similar to Claude Code's /context.
func (m *model) formatContextUsage() string {
	var b strings.Builder

	// Count chars per role (rough token estimate: ~4 chars per token).
	userChars, assistantChars, toolChars := 0, 0, 0
	for _, msg := range m.chatModel.Messages {
		size := len(msg.content) + len(msg.tool) + len(msg.toolIn)
		switch msg.role {
		case "user":
			userChars += size
		case "assistant":
			assistantChars += size
		default: // tool
			toolChars += size
		}
	}
	totalChars := userChars + assistantChars + toolChars
	totalTokens := int64(totalChars / 4)
	userTokens := int64(userChars / 4)
	assistantTokens := int64(assistantChars / 4)
	toolTokens := int64(toolChars / 4)

	// Header.
	b.WriteString("**Context Usage**\n\n")

	// Progress bar (20 blocks).
	const barLen = 20
	var usedBlocks int
	var limitTokens int64
	if tt := m.cfg.TokenTracker; tt != nil && tt.Limit() > 0 {
		limitTokens = tt.Limit()
		pct := float64(tt.TotalUsed()) / float64(limitTokens)
		usedBlocks = int(pct * barLen)
		if usedBlocks > barLen {
			usedBlocks = barLen
		}
	} else {
		// No limit — show context proportion only.
		if totalTokens > 0 {
			usedBlocks = 1
			if totalTokens > 10000 {
				usedBlocks = int(float64(totalTokens) / 100000 * barLen)
				if usedBlocks < 1 {
					usedBlocks = 1
				}
				if usedBlocks > barLen {
					usedBlocks = barLen
				}
			}
		}
	}
	bar := strings.Repeat("█", usedBlocks) + strings.Repeat("░", barLen-usedBlocks)

	// Model line with bar.
	modelLabel := m.cfg.ModelName
	if m.cfg.ProviderName != "" {
		modelLabel = m.cfg.ProviderName + " | " + modelLabel
	}
	if limitTokens > 0 {
		tt := m.cfg.TokenTracker
		b.WriteString(fmt.Sprintf("`%s`  %s · %s/%s tokens (%.0f%%)\n\n",
			bar, modelLabel,
			formatTokenCount(tt.TotalUsed()), formatTokenCount(limitTokens), tt.PercentUsed()))
	} else {
		b.WriteString(fmt.Sprintf("`%s`  %s · ctx ~%s tokens\n\n",
			bar, modelLabel, formatTokenCount(totalTokens)))
	}

	// Category breakdown.
	b.WriteString("*Estimated usage by category*\n")
	b.WriteString(fmt.Sprintf("- **User messages**: ~%s tokens (%d msgs)\n",
		formatTokenCount(userTokens), countByRole(m.chatModel.Messages, "user")))
	b.WriteString(fmt.Sprintf("- **Assistant messages**: ~%s tokens (%d msgs)\n",
		formatTokenCount(assistantTokens), countByRole(m.chatModel.Messages, "assistant")))
	b.WriteString(fmt.Sprintf("- **Tool calls**: ~%s tokens (%d calls)\n",
		formatTokenCount(toolTokens), countByRole(m.chatModel.Messages, "tool")))
	b.WriteString(fmt.Sprintf("- **Total context**: ~%s tokens (%d messages)\n",
		formatTokenCount(totalTokens), len(m.chatModel.Messages)))

	// Daily token usage (actual, not estimated).
	if tt := m.cfg.TokenTracker; tt != nil {
		total := tt.TotalUsed()
		if total > 0 {
			b.WriteString(fmt.Sprintf("\n*Daily token usage*\n"))
			b.WriteString(fmt.Sprintf("- **Consumed today**: %s tokens\n", formatTokenCount(total)))
			if tt.Limit() > 0 {
				b.WriteString(fmt.Sprintf("- **Remaining**: %s tokens\n", formatTokenCount(tt.Remaining())))
			}
		}
	}

	// Subagents.
	if m.cfg.Orchestrator != nil {
		agents := m.cfg.Orchestrator.List()
		if len(agents) > 0 {
			running, done, failed := 0, 0, 0
			for _, a := range agents {
				switch a.Status {
				case "running":
					running++
				case "failed":
					failed++
				default:
					done++
				}
			}
			b.WriteString(fmt.Sprintf("\n*Subagents*\n"))
			b.WriteString(fmt.Sprintf("- **Total**: %d (running: %d, done: %d, failed: %d)\n",
				len(agents), running, done, failed))
		}
	}

	// Compaction stats.
	if cm := m.cfg.CompactMetrics; cm != nil {
		stats := cm.FormatStats()
		if stats != "" {
			b.WriteString(fmt.Sprintf("\n*Output compaction*\n"))
			b.WriteString(stats)
		}
	}

	return b.String()
}

// showCommandList displays available slash commands as an assistant message.
func (m *model) showCommandList() {
	var b strings.Builder
	b.WriteString("**Commands:**\n")
	for _, cmd := range slashCommands {
		desc := slashCommandDesc(cmd)
		b.WriteString("  `" + cmd + "`")
		if desc != "" {
			b.WriteString(" — " + desc)
		}
		b.WriteString("\n")
	}
	if len(m.cfg.Skills) > 0 {
		b.WriteString("\n**Skills:**\n")
		for _, skill := range m.cfg.Skills {
			fmt.Fprintf(&b, "  `/%s`", skill.Name)
			if skill.Description != "" {
				b.WriteString(" — " + skill.Description)
			}
			b.WriteString("\n")
		}
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: b.String(),
	})
}

// formatHelp builds the grouped help text for /help.
func (m *model) formatHelp() string {
	var b strings.Builder

	b.WriteString("**Commands:**\n")
	b.WriteString("  `/help`                — Show this help\n")
	b.WriteString("  `/clear`               — Clear conversation\n")
	b.WriteString("  `/model`               — Show current model and roles\n")
	b.WriteString("  `/session`             — Show session info\n")
	b.WriteString("  `/context`             — Show context usage\n")
	b.WriteString("  `/compact`             — Compact session context\n")
	b.WriteString("  `/history [query]`     — Command history\n")
	b.WriteString("  `/exit`, `/quit`       — Exit\n")

	b.WriteString("\n**Git & Planning:**\n")
	b.WriteString("  `/commit`              — Generate commit from staged changes\n")
	b.WriteString("  `/branch <name>`       — Create/switch/list branches\n")
	b.WriteString("  `/plan <idea>`         — Start PDD planning session\n")
	b.WriteString("  `/run <spec>`          — Execute a spec with task agent\n")

	b.WriteString("\n**System:**\n")
	b.WriteString("  `/agents`              — Show running subagents\n")
	b.WriteString("  `/rtk`                 — Output compaction stats\n")
	b.WriteString("  `/login <provider>`    — Configure API keys\n")
	b.WriteString("  `/restart`             — Restart pi process\n")

	b.WriteString("\n**Skills:**\n")
	b.WriteString("  `/skills`              — List available skills\n")
	b.WriteString("  `/skills create <n>`   — Create a new skill\n")
	b.WriteString("  `/skills load`         — Reload skills from disk\n")

	if len(m.cfg.Skills) > 0 {
		b.WriteString("\n**Available skills:**\n")
		for _, s := range m.cfg.Skills {
			fmt.Fprintf(&b, "  `/%s`", s.Name)
			if s.Description != "" {
				b.WriteString(" — " + s.Description)
			}
			b.WriteString("\n")
		}
	}

	b.WriteString("\n**Keyboard shortcuts:**\n")
	b.WriteString("  `Enter` — Submit  `Ctrl+C`/`Esc` — Cancel  `Up/Down` — History  `PgUp/PgDn` — Scroll\n")

	return b.String()
}

// handleSkillsCommand handles /skills and its subcommands: create, load, list (default).
func (m *model) handleSkillsCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		return m.handleSkillListCommand()
	}
	sub := strings.ToLower(args[0])
	switch sub {
	case "create":
		return m.handleSkillCreateCommand(args[1:])
	case "load", "reload":
		return m.handleSkillLoadCommand()
	case "list":
		return m.handleSkillListCommand()
	default:
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Usage: `/skills` — list  |  `/skills create <name>` — create  |  `/skills load` — reload",
		})
		return m, nil
	}
}

// handleRTKCommand handles the /rtk command and subcommands.
func (m *model) handleRTKCommand(args []string) {
	sub := "stats"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}
	switch sub {
	case "stats":
		if m.cfg.CompactMetrics == nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: "Output compactor is not active.",
			})
			return
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.cfg.CompactMetrics.FormatStats(),
		})
	default:
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Usage: `/rtk` or `/rtk stats` — Show output compaction statistics",
		})
	}
}
