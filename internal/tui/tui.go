// Package tui implements the interactive terminal UI using Bubble Tea v2.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"

	tea "charm.land/bubbletea/v2"
)

// model is the Bubble Tea model for the interactive TUI.
type model struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	// UI state.
	width  int
	height int

	// Input sub-model.
	inputModel InputModel

	// Chat sub-model (messages, scroll, rendering).
	chatModel ChatModel

	// Status bar sub-model.
	statusModel StatusModel

	// Theme manager.
	themeManager *ThemeManager

	// Agent state.
	running bool
	agentCh chan agentMsg // channel for receiving agent events

	// Commit flow state.
	commit *commitState

	// Login flow state.
	login *loginState

	// Plan flow state (/plan override confirmation).
	plan *planState

	// Skill-create pending overwrite confirmation.
	pendingSkillCreate *pendingSkillCreate

	// Run flow state (/run command).
	run *runState

	// Quit.
	quitting bool
}

// Run starts the interactive TUI.
func Run(ctx context.Context, cfg Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
		glamour.WithEmoji(),
	)

	// Load persistent command history from ~/.pi-go/history.
	history := loadHistory()
	if history == nil {
		history = make([]string, 0)
	}

	// Initialize theme manager.
	tm := NewThemeManager()
	if cfg.ThemeName != "" && cfg.ThemeName != "default" {
		_ = tm.SetTheme(cfg.ThemeName) // ignore error, falls back to tokyo-night
	}

	m := model{
		cfg:          cfg,
		ctx:          ctx,
		cancel:       cancel,
		inputModel:   NewInputModel(history, cfg.Skills, cfg.SkillDirs, cfg.WorkDir),
		chatModel:    NewChatModel(renderer),
		statusModel:  StatusModel{GitBranch: detectBranch(cfg.WorkDir)},
		themeManager: tm,
	}

	p := tea.NewProgram(&m, tea.WithContext(ctx))
	_, err := p.Run()
	drainTerminalResponses()
	return err
}

func (m *model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.cfg.RestartCh != nil {
		cmds = append(cmds, waitForRestart(m.cfg.RestartCh))
	}
	if m.cfg.AgentEventCh != nil {
		cmds = append(cmds, waitForSubEvent(m.cfg.AgentEventCh))
	}
	return tea.Batch(cmds...)
}

// waitForRestart returns a Cmd that listens for a restart signal from the agent.
func waitForRestart(ch chan struct{}) tea.Cmd {
	return func() tea.Msg {
		<-ch
		return restartMsg{}
	}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.statusModel.Width = m.width
		m.chatModel.UpdateRenderer(m.width)

	case tea.PasteMsg:
		if !m.running {
			m.inputModel.InsertText(msg.Content)
		}

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case InputSubmitMsg:
		if strings.HasPrefix(msg.Text, "/") {
			return m.handleSlashCommand(msg.Text)
		}
		return m.submitPrompt(msg.Text)

	case restartMsg:
		execRestart()
		return m, tea.Quit

	case agentThinkingMsg:
		return m.handleAgentThinking(msg)

	case agentTextMsg:
		return m.handleAgentText(msg)

	case agentToolCallMsg:
		return m.handleAgentToolCall(msg)

	case agentToolResultMsg:
		return m.handleAgentToolResult(msg)

	case agentSubEventMsg:
		return m.handleAgentSubEvent(msg)

	case agentDoneMsg:
		return m.handleAgentDone(msg)

	case runAgentEventMsg:
		return m.handleRunAgentEvent(msg)

	case runAgentDoneMsg:
		return m.handleRunAgentDone()

	case runGateResultMsg:
		return m.handleRunGateResult(msg)

	case runMergeResultMsg:
		return m.handleRunMergeResult(msg)

	case loginSSOResultMsg:
		return m.handleLoginSSOResult(msg)

	case commitGeneratedMsg:
		return m.handleCommitGenerated(msg)

	case commitDoneMsg:
		return m.handleCommitDone(msg)

	case pingDoneMsg:
		content := msg.output
		if msg.err != nil {
			content += fmt.Sprintf("\n\n✗ Ping failed: %v", msg.err)
		}
		// Replace the "Pinging model..." placeholder.
		if len(m.chatModel.Messages) > 0 && m.chatModel.Messages[len(m.chatModel.Messages)-1].content == "Pinging model..." {
			m.chatModel.Messages[len(m.chatModel.Messages)-1].content = content
		} else {
			m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: content})
		}
		return m, nil
	}

	// Keep the agent listener alive for any unhandled message types.
	if m.running {
		return m, waitForAgent(m.agentCh)
	}
	return m, nil
}


func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()

	// Handle commit confirmation mode first.
	if !m.running && m.commit != nil && m.commit.phase == "confirming" {
		switch {
		case key.Code == tea.KeyEnter:
			return m.handleCommitConfirm()
		case key.Code == tea.KeyEsc:
			return m.handleCommitCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handleCommitCancel()
		default:
			return m, nil
		}
	}

	// Handle login flow.
	if !m.running && m.login != nil {
		switch {
		case key.Code == tea.KeyEsc:
			return m.handleLoginCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handleLoginCancel()
		case key.Code == tea.KeyEnter && m.login.phase == "waiting":
			apiKey := strings.TrimSpace(m.inputModel.Text)
			if apiKey == "" {
				return m, nil
			}
			m.inputModel.Clear()
			return m.handleLoginSave(apiKey)
		}
		if m.login.phase != "waiting" {
			return m, nil
		}
	}

	// Handle skill-create overwrite confirmation.
	if !m.running && m.pendingSkillCreate != nil {
		switch {
		case key.Code == tea.KeyEnter:
			return m.handleSkillCreateConfirm()
		case key.Code == tea.KeyEsc:
			return m.handleSkillCreateCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handleSkillCreateCancel()
		default:
			return m, nil
		}
	}

	// Handle plan override confirmation.
	if !m.running && m.plan != nil && m.plan.phase == "confirming_override" {
		switch {
		case key.Code == tea.KeyEnter:
			return m.handlePlanOverride()
		case key.Code == tea.KeyEsc:
			return m.handlePlanCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handlePlanCancel()
		default:
			return m, nil
		}
	}

	// Esc / Ctrl+C: dismiss completion, cancel agent, or quit.
	switch {
	case key.Code == tea.KeyEsc:
		if m.inputModel.InCompletionMode() {
			m.inputModel.DismissCompletion()
			return m, nil
		}
		if m.running {
			m.cancelAgent()
			return m, nil
		}
		return m, nil

	case key.Code == 'c' && key.Mod == tea.ModCtrl:
		if m.inputModel.InCompletionMode() {
			m.inputModel.DismissCompletion()
			return m, nil
		}
		if m.running {
			m.cancelAgent()
			return m, nil
		}
		m.quitting = true
		return m, tea.Quit

	case key.Code == tea.KeyF12:
		return m, nil
	}

	if m.running {
		return m, nil
	}

	// Scroll keys stay in root model.
	switch {
	case key.Code == tea.KeyPgUp:
		m.chatModel.ScrollUp(5, m.height)
		return m, nil

	case key.Code == tea.KeyPgDown:
		m.chatModel.ScrollDown(5)
		return m, nil
	}

	// Delegate all other keys to InputModel.
	cmd := m.inputModel.HandleKey(msg)
	return m, cmd
}





func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("Goodbye!\n")
	}

	if m.width == 0 {
		return tea.NewView("Loading...")
	}

	// Layout.

	// Render components.
	messagesView := m.chatModel.RenderMessages(m.running)
	statusBar := m.statusModel.Render(m.statusRenderInput())
	inputArea := m.inputModel.View(m.running)

	// Calculate available height for messages.
	statusLines := strings.Count(statusBar, "\n") + 1
	inputLines := strings.Count(inputArea, "\n") + 1

	availableHeight := m.height - statusLines - inputLines - 1
	if availableHeight < 1 {
		availableHeight = 1
	}

	// Truncate messages to fit viewport.
	msgLines := strings.Split(messagesView, "\n")
	totalLines := len(msgLines)

	startLine := totalLines - availableHeight - m.chatModel.Scroll
	if startLine < 0 {
		startLine = 0
	}
	endLine := startLine + availableHeight
	if endLine > totalLines {
		endLine = totalLines
	}

	visibleMessages := strings.Join(msgLines[startLine:endLine], "\n")

	// Pad to fill available space.
	visibleLineCount := strings.Count(visibleMessages, "\n") + 1
	for visibleLineCount < availableHeight {
		visibleMessages += "\n"
		visibleLineCount++
	}

	var b strings.Builder
	b.WriteString(visibleMessages)
	b.WriteString("\n")

	b.WriteString(statusBar)
	b.WriteString("\n")
	b.WriteString(inputArea)

	// Update screen provider so the screen tool can read current content.
	if m.cfg.Screen != nil {
		m.cfg.Screen.update(visibleMessages)
	}

	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// drainTerminalResponses discards any pending terminal response sequences
// (e.g. cursor position reports, DECRQM replies) that may arrive after the
// TUI exits. Without this, late responses leak into the shell prompt as garbage
// like "[14;1R[?2026;2$y".
func drainTerminalResponses() {
	f := os.Stdin
	// Switch stdin to non-blocking so we can read without waiting.
	if err := setNonBlock(f); err != nil {
		return
	}
	defer setBlock(f) //nolint:errcheck

	buf := make([]byte, 256)
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		n, _ := f.Read(buf)
		if n == 0 {
			break
		}
	}
}


// statusRenderInput builds the StatusRenderInput from the current model state.
func (m *model) statusRenderInput() StatusRenderInput {
	var rc *runCycleInfo
	if m.run != nil && m.run.phase != "done" && m.run.phase != "failed" {
		rc = &runCycleInfo{
			SpecName:   m.run.specName,
			Cycle:      m.run.retries + 1,
			MaxRetries: m.run.maxRetries,
		}
	}
	return StatusRenderInput{
		ProviderName: m.cfg.ProviderName,
		ModelName:    m.cfg.ModelName,
		Running:      m.running,
		Messages:     m.chatModel.Messages,
		TokenTracker: m.cfg.TokenTracker,
		Orchestrator: m.cfg.Orchestrator,
		TraceCount:   len(m.chatModel.TraceLog),
		RunCycle:     rc,
	}
}

// detectBranch returns the current git branch name, or empty string.
func detectBranch(workDir string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

