package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *model) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])

	// Log all slash commands.
	if m.cfg.Logger != nil {
		m.cfg.Logger.UserMessage(input)
	}

	// Sync the terminal window/tab title and the persisted session metadata
	// with the slash command the user just typed. The next View() will emit
	// OSC 0 carrying the new title (formatTerminalTitle scrubs C0 controls,
	// so the envelope stays well-formed). Skipped for the three commands
	// whose semantics aren't "make this the title":
	//   - /clear  resets the title to the app default
	//   - /exit, /quit  tear the program down before any frame can render
	switch cmd {
	case "/clear", "/exit", "/quit":
	default:
		m.setSessionTitle(input)
	}

	switch cmd {
	case "/help":
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.formatHelp(),
		})
	case "/clear":
		m.clearConversation()
	case "/copy":
		return m.handleCopyCommand()
	case "/model":
		return m.handleModelCommand(parts[1:])
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
	case "/subagents":
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
	case "/theme":
		return m.handleThemeCommand(parts[1:])
	case "/rtk":
		m.handleRTKCommand(parts[1:])
	case "/ping":
		return m.handlePingCommand(parts[1:])
	case "/restart":
		return m.handleRestartCommand()
	case "/mcp":
		m.handleMCPCommand()
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

func (m *model) handleCopyCommand() (tea.Model, tea.Cmd) {
	transcript := m.chatModel.PlainTranscript()
	if transcript == "" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Nothing to copy yet.",
		})
		return m, nil
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "Copied conversation to clipboard.",
	})
	return m, tea.SetClipboard(transcript)
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

// clearConversation resets the transcript and the session context behind it.
//
// This is the body of /clear, split out so the context gauge's [x] button runs
// exactly this path. A second copy of these fifteen assignments would drift the
// first time one of them changed, and the failure mode is silent: a transcript
// that looks cleared while the model still receives every prior turn.
func (m *model) clearConversation() {
	m.chatModel.Messages = m.chatModel.Messages[:0]
	m.chatModel.Scroll = 0
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.chatModel.TraceLog = nil
	m.running = false
	m.run = nil
	m.statusModel.ActiveTool = ""
	m.statusModel.ToolStart = time.Time{}
	m.loadingItems = nil
	m.matrix.clear()
	m.matrix.feed("pi-go", m.mainWidth())
	// Drop the session's events and zero the context gauge. Without this the
	// transcript looks empty while the model still receives — and still pays
	// for — every prior turn.
	m.clearSessionContext()
	// Reset the terminal window/tab title and the persisted session metadata
	// title. Funnel through setSessionTitle("") so the agent's SetSessionTitle
	// is also called and meta.json stays in sync with the OSC 0 frame that the
	// next View() will emit.
	m.setSessionTitle("")
}

// clearSessionContext empties the session's event history and zeroes the
// context gauge, so /clear resets what the model sees rather than only what
// the user sees.
//
// Order matters: the gauge is only zeroed once the events are actually gone.
// If ClearEvents fails the history survives, and a zeroed gauge would then
// under-report a still-full window — a worse lie than the stale reading it
// replaced. So on error the failure is surfaced in the chat and the tracker is
// left alone.
//
// Both dependencies are optional: a model can be built without a session
// service (nothing to clear) or without a token tracker (no gauge to zero).
func (m *model) clearSessionContext() {
	if m.cfg.SessionService != nil {
		if err := m.cfg.SessionService.ClearEvents(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID,
		); err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Context not cleared: %v", err),
			})
			return
		}
	}

	if tt := m.cfg.TokenTracker; tt != nil {
		tt.ResetContextWindow()
	}
}

// handleCompactCommand triggers session compaction.
//
// After a successful compact, the gauge is updated immediately with the
// post-compaction token count so the user sees the window shrink on the same
// keystroke, rather than waiting for the next LLM response. Without this
// push the gauge keeps reading the pre-compaction number — visually
// indistinguishable from no compaction at all.
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

	// Refresh the gauge. EstimateTokens is the same chars/4 heuristic the
	// session uses internally, so it matches what the next LLM response will
	// report up to that fuzz. The token-tracker's cache prefix is also reset
	// by SetLastPromptTokens, since the new window starts a fresh prefix.
	if tt := m.cfg.TokenTracker; tt != nil {
		if est, estErr := m.cfg.SessionService.EstimateTokens(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID,
		); estErr == nil {
			tt.SetLastPromptTokens(int64(est))
		}
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: "Session context compacted.",
	})
}

// countAgentsByStatus counts agents in each status category.
func countAgentsByStatus(agents []subagent.AgentStatus) (running, done, failed int) {
	for _, a := range agents {
		switch a.Status {
		case "running":
			running++
		case "completed":
			done++
		case "failed":
			failed++
			// "killed" and "canceled" are not counted as failed
		}
	}
	return
}

// agentStatusIcon returns a display icon for an agent status.
func agentStatusIcon(status string) string {
	switch status {
	case "running":
		return "▶ "
	case "completed":
		return "✓ "
	case "failed":
		return "✗ "
	case "canceled":
		return "◼ "
	case "killed":
		return "⚠ "
	default:
		return "  "
	}
}

// formatAgentsList formats a list of agents for display.
func formatAgentsList(agents []subagent.AgentStatus) string {
	if len(agents) == 0 {
		return "No subagents have been spawned yet."
	}

	running, done, failed := countAgentsByStatus(agents)

	var b strings.Builder
	fmt.Fprintf(&b, "**Subagents** — %d total, %d running, %d done", len(agents), running, done)
	if failed > 0 {
		fmt.Fprintf(&b, ", %d failed", failed)
	}
	b.WriteString("\n\n")

	for _, a := range agents {
		icon := agentStatusIcon(a.Status)

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
	return b.String()
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

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: formatAgentsList(m.cfg.Orchestrator.List()),
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

// handleModelCommand handles /model: show current model, switch model, or switch role.
//   - /model           — show current model and configured roles
//   - /model <name>    — switch to the named model (or role if name matches a role)
func (m *model) handleModelCommand(args []string) (tea.Model, tea.Cmd) {
	if len(args) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.formatModelInfo(),
		})
		return m, nil
	}

	target := strings.TrimSpace(args[0])

	// Resolve: if target is a configured role name, use that role's model.
	var modelName, roleName string
	if rc, ok := m.cfg.Roles[target]; ok {
		modelName = rc.Model
		roleName = target
	} else {
		modelName = target
	}

	if modelName == "" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Role `%s` has no model configured.", target),
		})
		return m, nil
	}

	if m.cfg.ModelSwitcher == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Model switching is not available in this context.",
		})
		return m, nil
	}

	if m.running {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Cannot switch model while a response is running. Wait for it to finish or cancel it first.",
		})
		return m, nil
	}

	newLLM, newName, newProvider, err := m.cfg.ModelSwitcher(m.ctx, modelName)
	if err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Failed to switch model: %s", err),
		})
		return m, nil
	}

	// Rebuild the agent with the new LLM.
	if m.cfg.Agent != nil {
		if err := m.cfg.Agent.RebuildWithModel(newLLM); err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Failed to rebuild agent: %s", err),
			})
			return m, nil
		}
	}

	// Update TUI state.
	m.cfg.LLM = newLLM
	m.cfg.ModelName = newName
	m.cfg.ProviderName = newProvider
	if roleName != "" {
		m.cfg.ActiveRole = roleName
	} else {
		m.cfg.ActiveRole = "default"
		// Persist the new model as the default role so it survives restart.
		saveModelToConfig(newName, newProvider)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Switched model to **%s** (provider: %s).", newName, newProvider)
	if roleName != "" && roleName != "default" {
		fmt.Fprintf(&sb, " Role: `%s`.", roleName)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: sb.String(),
	})
	return m, nil
}

// saveModelToConfig persists the model and provider as the default role in
// ~/.pi-go/config.json. Silently ignores errors (best-effort persistence).
func saveModelToConfig(modelName, provider string) {
	_ = config.SaveDefaultRole(modelName, provider)
}

// formatContextUsage builds a context usage display similar to Claude Code's /context.
func (m *model) formatContextUsage() string {
	var b strings.Builder

	// The breakdown answers "what is filling the window", which is the question
	// a user asks before deciding what to trim. Lead with it.
	if bd := m.cfg.ContextBreakdown; bd != nil {
		used := int64(0)
		window := int64(0)
		if tt := m.cfg.TokenTracker; tt != nil {
			used = tt.LastPromptTokens()
			window = tt.ContextWindowSize()
		}
		if used == 0 {
			used = estimateContextTokenCount(m.chatModel.Messages) + bd.FixedTotal()
		}
		if window <= 0 {
			window = autoRangeWindow(used)
		}
		b.WriteString("*Context usage*\n\n")
		b.WriteString(RenderContextBreakdown(
			bd.withConversationFrom(used), window, min(m.chatWidth()-4, 64), m.palette))
		b.WriteString("\n\n")
	}

	// Same estimate as the status bar's, split by role.
	userChars, assistantChars, toolChars := 0, 0, 0
	for _, msg := range m.chatModel.Messages {
		size := messageChars(msg)
		switch msg.role {
		case "user":
			userChars += size
		case "assistant":
			assistantChars += size
		default: // tool
			toolChars += size
		}
	}
	totalTokens := estimateTokens(userChars + assistantChars + toolChars)
	userTokens := estimateTokens(userChars)
	assistantTokens := estimateTokens(assistantChars)
	toolTokens := estimateTokens(toolChars)

	// Header.
	b.WriteString("**Context Usage**\n\n")

	// Progress bar (20 blocks).
	const barLen = 20
	var usedBlocks int
	var limitTokens int64
	if tt := m.cfg.TokenTracker; tt != nil && tt.Limit() > 0 {
		limitTokens = tt.Limit()
		usedBlocks = barFill(float64(tt.TotalUsed())/float64(limitTokens), barLen)
	} else if totalTokens > 0 {
		// No limit to measure against — show the context against a nominal 100k
		// window instead, and never less than one block, so a short conversation
		// still reads as "something is in here".
		usedBlocks = 1
		if totalTokens > 10000 {
			usedBlocks = max(barFill(float64(totalTokens)/100000, barLen), 1)
		}
	}
	bar := barGlyphs(usedBlocks, barLen)

	// Model line with bar.
	modelLabel := m.cfg.ModelName
	if m.cfg.ProviderName != "" {
		modelLabel = m.cfg.ProviderName + " | " + modelLabel
	}
	if limitTokens > 0 {
		tt := m.cfg.TokenTracker
		fmt.Fprintf(&b, "`%s`  %s · %s/%s tokens (%.0f%%)\n\n",
			bar, modelLabel,
			formatTokenCount(tt.TotalUsed()), formatTokenCount(limitTokens), tt.PercentUsed())
	} else {
		fmt.Fprintf(&b, "`%s`  %s · ctx ~%s tokens\n\n",
			bar, modelLabel, formatTokenCount(totalTokens))
	}

	// Category breakdown.
	b.WriteString("*Estimated usage by category*\n")
	fmt.Fprintf(&b, "- **User messages**: ~%s tokens (%d msgs)\n",
		formatTokenCount(userTokens), countByRole(m.chatModel.Messages, "user"))
	fmt.Fprintf(&b, "- **Assistant messages**: ~%s tokens (%d msgs)\n",
		formatTokenCount(assistantTokens), countByRole(m.chatModel.Messages, "assistant"))
	fmt.Fprintf(&b, "- **Tool calls**: ~%s tokens (%d calls)\n",
		formatTokenCount(toolTokens), countByRole(m.chatModel.Messages, "tool"))
	fmt.Fprintf(&b, "- **Total context**: ~%s tokens (%d messages)\n",
		formatTokenCount(totalTokens), len(m.chatModel.Messages))

	// Daily token usage (actual, not estimated).
	if tt := m.cfg.TokenTracker; tt != nil {
		total := tt.TotalUsed()
		if total > 0 {
			b.WriteString("\n*Daily token usage*\n")
			fmt.Fprintf(&b, "- **Consumed today**: %s tokens\n", formatTokenCount(total))
			if tt.Limit() > 0 {
				fmt.Fprintf(&b, "- **Remaining**: %s tokens\n", formatTokenCount(tt.Remaining()))
			}
		}
	}

	// Context window usage (from last LLM response).
	if tt := m.cfg.TokenTracker; tt != nil && tt.LastPromptTokens() > 0 {
		promptTokens := tt.LastPromptTokens()
		ctxWindow := tt.ContextWindowSize()

		b.WriteString("\n*Context window*\n")
		if ctxWindow > 0 {
			pct := tt.ContextPercentUsed()
			freeTokens := ctxWindow - promptTokens
			if freeTokens < 0 {
				freeTokens = 0
			}

			const ctxBarLen = 20
			ctxBar := barGlyphs(barFill(pct/100, ctxBarLen), ctxBarLen)

			fmt.Fprintf(&b, "`%s`  %s / %s (%.0f%%)\n",
				ctxBar,
				formatTokenCount(promptTokens), formatTokenCount(ctxWindow), pct)
			fmt.Fprintf(&b, "- **Used**: %s tokens\n", formatTokenCount(promptTokens))
			fmt.Fprintf(&b, "- **Free**: %s tokens (%.0f%%)\n",
				formatTokenCount(freeTokens), 100-pct)
		} else {
			fmt.Fprintf(&b, "- **Last prompt**: %s tokens (window size unknown)\n",
				formatTokenCount(promptTokens))
		}

		// Prompt cache. Reported explicitly even at zero hits — a silent
		// section reads the same whether caching works or is entirely absent.
		b.WriteString("\n*Prompt cache*\n")
		cached := tt.LastCachedTokens()
		if cached > 0 {
			fmt.Fprintf(&b, "- **Last request**: %s of %s prompt tokens cached (%.0f%%)\n",
				formatTokenCount(cached), formatTokenCount(promptTokens),
				float64(cached)/float64(promptTokens)*100)
		} else {
			b.WriteString("- **Last request**: no cache hit\n")
		}
		if today := tt.CachedTokensToday(); today > 0 {
			fmt.Fprintf(&b, "- **Today**: %s tokens read from cache (%.0f%% of input)\n",
				formatTokenCount(today), tt.CacheHitRateToday())
		}
		if prefix := tt.CachePrefixTokens(); prefix > 0 {
			fmt.Fprintf(&b, "- **Stable prefix**: %s tokens · **body since**: %s tokens\n",
				formatTokenCount(prefix), formatTokenCount(tt.BodyTokens()))
		}
	}

	// Skills.
	if len(m.cfg.Skills) > 0 {
		b.WriteString("\n*Skills* ")
		fmt.Fprintf(&b, "(%d loaded)\n", len(m.cfg.Skills))
		// Stable, alphabetical listing for predictable /context output.
		names := make([]string, 0, len(m.cfg.Skills))
		byName := make(map[string]extension.Skill, len(m.cfg.Skills))
		for _, s := range m.cfg.Skills {
			names = append(names, s.Name)
			byName[s.Name] = s
		}
		sort.Strings(names)
		for _, name := range names {
			s := byName[name]
			source := s.Source
			if source == "" {
				source = "user"
			}
			var bodyDesc string
			if size, ok := extension.SkillBodySize(m.cfg.Skills, s.Name); ok {
				bodyDesc = fmt.Sprintf("body: %s", formatTokenCount(int64(size)))
			} else {
				bodyDesc = "body: not loaded"
			}
			desc := s.Description
			if desc == "" {
				desc = "(no description)"
			}
			fmt.Fprintf(&b, "- /%s — %s [%s]  %s\n", s.Name, desc, source, bodyDesc)
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
			b.WriteString("\n*Subagents*\n")
			fmt.Fprintf(&b, "- **Total**: %d (running: %d, done: %d, failed: %d)\n",
				len(agents), running, done, failed)
		}
	}

	// Compaction stats.
	if cm := m.cfg.CompactMetrics; cm != nil {
		stats := cm.FormatStats()
		if stats != "" {
			b.WriteString("\n*Output compaction*\n")
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

	b.WriteString("**Commands:**\n\n")
	b.WriteString("| Command | Description |\n")
	b.WriteString("|---------|-------------|\n")
	b.WriteString("| `/help` | Show this help |\n")
	b.WriteString("| `/clear` | Clear conversation |\n")
	b.WriteString("| `/copy` | Copy conversation to clipboard |\n")
	b.WriteString("| `/model [name]` | Show or switch current model |\n")
	b.WriteString("| `/session` | Show session info |\n")
	b.WriteString("| `/context` | Show context usage |\n")
	b.WriteString("| `/compact` | Compact session context |\n")
	b.WriteString("| `/history [query]` | Command history |\n")
	b.WriteString("| `/exit`, `/quit` | Exit |\n")

	b.WriteString("\n**Git & Planning:**\n\n")
	b.WriteString("| Command | Description |\n")
	b.WriteString("|---------|-------------|\n")
	b.WriteString("| `/commit` | Generate commit from staged changes |\n")
	b.WriteString("| `/branch <name>` | Create/switch/list branches |\n")
	b.WriteString("| `/plan <idea>` | Start PDD planning session |\n")
	b.WriteString("| `/plan resume` | Resume interrupted plan session |\n")
	b.WriteString("| `/run <spec>` | Execute a spec with task agent |\n")

	b.WriteString("\n**Display:**\n\n")
	b.WriteString("| Command | Description |\n")
	b.WriteString("|---------|-------------|\n")
	b.WriteString("| `/theme [name]` | List or switch themes |\n")

	b.WriteString("\n**System:**\n\n")
	b.WriteString("| Command | Description |\n")
	b.WriteString("|---------|-------------|\n")
	b.WriteString("| `/subagents` | Show running subagents |\n")
	b.WriteString("| `/rtk` | Output compaction stats |\n")
	b.WriteString("| `/mcp` | List MCP servers and tool status |\n")
	b.WriteString("| `/login <provider>` | Configure API keys |\n")
	b.WriteString("| `/restart` | Restart pi process |\n")

	b.WriteString("\n**Skills:**\n\n")
	b.WriteString("| Command | Description |\n")
	b.WriteString("|---------|-------------|\n")
	b.WriteString("| `/skills` | List available skills |\n")
	b.WriteString("| `/skills create <n>` | Create a new skill |\n")
	b.WriteString("| `/skills load` | Reload skills from disk |\n")

	if len(m.cfg.Skills) > 0 {
		b.WriteString("\n**Available skills:**\n\n")
		b.WriteString("| Skill | Description |\n")
		b.WriteString("|-------|-------------|\n")
		for _, s := range m.cfg.Skills {
			desc := s.Description
			if len(desc) > 80 {
				desc = desc[:77] + "..."
			}
			fmt.Fprintf(&b, "| `/%s` | %s |\n", s.Name, desc)
		}
	}

	b.WriteString("\n**Keyboard shortcuts:**\n\n")
	b.WriteString("| Key | Action |\n")
	b.WriteString("|-----|--------|\n")
	b.WriteString("| `Enter` | Submit |\n")
	b.WriteString("| `Ctrl+C` / `Esc` | Cancel |\n")
	b.WriteString("| `Up/Down` | Prompt history |\n")
	b.WriteString("| `Ctrl+R` | History search |\n")
	b.WriteString("| `PgUp/PgDn` | Scroll chat |\n")

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

// formatThemeList builds the theme list: one row per theme, each carrying its
// own colors, so the list is the preview — no separate command to run and no
// switching themes one at a time to see them.
//
// The rows carry ANSI, so the message must be marked preRendered — glamour
// prints escape bytes as text.
func formatThemeList(themes []Theme, currentName string, p Palette) string {
	p = paletteOrDark(p)
	name := lipgloss.NewStyle().Foreground(p.Text)
	active := lipgloss.NewStyle().Foreground(p.Success).Bold(true)

	nameWidth := 0
	for _, t := range themes {
		if len(t.Name) > nameWidth {
			nameWidth = len(t.Name)
		}
	}

	var b strings.Builder
	for i, t := range themes {
		if i > 0 {
			b.WriteString("\n")
		}
		icon := "🌙"
		if t.ThemeType == "light" {
			icon = "☀️"
		}
		nameStyle := name
		if t.Name == currentName {
			nameStyle = active
		}
		fmt.Fprintf(&b, "  %s  %s  %s",
			icon,
			nameStyle.Render(fmt.Sprintf("%-*s", nameWidth, t.Name)),
			swatchStrip(t.Colors))
	}
	return b.String()
}

// formatThemeError builds an error message with optional close-match suggestions.
func formatThemeError(name string, matches []string) string {
	msg := fmt.Sprintf("Unknown theme `%s`.", name)
	if len(matches) > 0 {
		msg += " Did you mean: " + strings.Join(matches, ", ") + "?"
	}
	return msg
}

// handleThemeCommand handles /theme: list themes, switch theme, or show current.
func (m *model) handleThemeCommand(args []string) (tea.Model, tea.Cmd) {
	if m.themeManager == nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "Theme system not available.",
		})
		return m, nil
	}

	if len(args) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:        "assistant",
			preRendered: true,
			content:     formatThemeList(m.themeManager.List(), m.themeManager.CurrentName(), m.palette),
		})
		return m, nil
	}

	if strings.ToLower(args[0]) == "palette" {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:        "assistant",
			preRendered: true,
			content:     formatPalettePreview(m.palette),
		})
		return m, nil
	}

	name := strings.ToLower(args[0])
	if err := m.themeManager.SetTheme(name); err != nil {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: formatThemeError(name, m.themeManager.ClosestMatches(name, 5)),
		})
		return m, nil
	}

	saveThemeToConfig(name)

	// An explicit choice wins over terminal background detection for the rest
	// of the session.
	m.bgDetected = true
	m.applyTheme()

	cur := m.themeManager.Current()
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: fmt.Sprintf("Theme switched to `%s` (%s).", cur.Name, cur.DisplayName),
	})
	return m, nil
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

// handleMCPCommand shows the status of configured MCP servers and their tools.
func (m *model) handleMCPCommand() {
	servers := m.cfg.MCPServers
	toolsets := m.cfg.MCPToolsets

	if len(servers) == 0 {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: "No MCP servers configured. Add servers under `mcp.servers` in `~/.pi-go/config.json`.",
		})
		return
	}

	statuses := extension.ToolsetStatuses(toolsets)
	// Index statuses by name for lookup.
	statusByName := make(map[string]extension.MCPServerStatus, len(statuses))
	for _, s := range statuses {
		statusByName[s.Name] = s
	}

	var b strings.Builder
	fmt.Fprintf(&b, "**MCP Servers** — %d configured\n\n", len(servers))
	b.WriteString("| Server | URL | Status | Tools |\n")
	b.WriteString("|--------|-----|--------|-------|\n")
	for _, srv := range servers {
		st, ok := statusByName[srv.Name]
		statusIcon := "⏳ pending"
		toolCount := "—"
		if ok {
			switch st.Status {
			case "connected":
				statusIcon = "✓ connected"
				toolCount = fmt.Sprintf("%d", st.ToolCount)
			case "failed":
				statusIcon = "✗ failed"
				toolCount = "—"
			default:
				statusIcon = "⏳ pending"
			}
		}
		displayURL := maskServerURL(srv.URL)
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", srv.Name, displayURL, statusIcon, toolCount)
	}

	// Hint about pending tool counts.
	var connected []extension.MCPServerStatus
	for _, s := range statuses {
		if s.Status == "connected" {
			connected = append(connected, s)
		}
	}
	if len(connected) > 0 {
		b.WriteString("\n> Tool counts are available after the first agent interaction with each server.\n")
	} else if len(statuses) > 0 {
		b.WriteString("\n> MCP tool counts populate after the first agent request — start a task to connect.\n")
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: b.String(),
	})
}

// maskServerURL masks API keys in MCP server URLs for TUI display.
// Replaces query parameter values (e.g., ?key=xxx) with "***".
func maskServerURL(url string) string {
	if url == "" {
		return "_none_"
	}
	if !strings.Contains(url, "?") {
		return url
	}
	masked := url
	sensitive := []string{
		"tavilyApiKey", "apiKey", "key", "token", "secret", "password",
	}
	for _, param := range sensitive {
		prefix := param + "="
		if idx := strings.Index(masked, prefix); idx >= 0 {
			end := idx + len(prefix)
			if amp := strings.Index(masked[end:], "&"); amp >= 0 {
				masked = masked[:idx] + param + "=" + "***" + masked[end+amp:]
			} else {
				masked = masked[:idx] + param + "=***"
			}
		}
	}
	return masked
}
