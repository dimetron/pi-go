// Package tui implements the interactive terminal UI using Bubble Tea v2.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	stdlog "log"
	"os"
	"os/exec"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	llmmodel "google.golang.org/adk/model"
)

// agentMsg wraps messages coming from the agent goroutine via a channel.
type agentMsg interface{ agentMsg() }

type agentTextMsg struct{ text string }
type agentThinkingMsg struct{ text string }
type agentToolCallMsg struct {
	name string
	args map[string]any
}
type agentToolResultMsg struct {
	name    string
	content string
}
type agentDoneMsg struct{ err error }

// agentSubEventMsg carries a streamed event from a running subagent to the TUI.
type agentSubEventMsg struct {
	agentID      string // which subagent
	kind         string // "tool_call", "tool_result", "text"
	content      string
	pipelineID   string // groups agents in same call
	pipelineMode string // "single", "parallel", "chain"
	pipelineStep int    // 1-based position
	pipelineTotal int   // total agents in pipeline
}

func (agentTextMsg) agentMsg()       {}
func (agentThinkingMsg) agentMsg()   {}
func (agentToolCallMsg) agentMsg()   {}
func (agentToolResultMsg) agentMsg() {}
func (agentDoneMsg) agentMsg()       {}
func (agentSubEventMsg) agentMsg()   {}

// Config holds configuration for the TUI.
type Config struct {
	Agent          *agent.Agent
	LLM            llmmodel.LLM // The active LLM, used by /ping.
	SessionID      string
	ModelName      string
	ProviderName   string
	ActiveRole     string
	Roles          map[string]config.RoleConfig
	SessionService *pisession.FileService
	WorkDir        string
	Orchestrator   *subagent.Orchestrator
	// GenerateCommitMsg is called by /commit to generate a conventional commit message from diffs.
	// If nil, /commit is disabled.
	GenerateCommitMsg func(ctx context.Context, diffs string) (string, error)
	// Logger is the session logger. If nil, logging is disabled.
	Logger *logger.Logger
	// Screen receives screen content updates for the screen tool.
	// If nil, the screen tool won't have access to TUI content.
	Screen *Screen
	// Skills is loaded from skill directories for command completion.
	Skills []extension.Skill
	// SkillDirs are the directories to re-scan for skills on each completion.
	SkillDirs []string
	// RestartCh receives a signal when the agent calls the restart tool.
	RestartCh chan struct{}
	// AgentEventCh receives subagent events from the agent tool for live display.
	AgentEventCh <-chan AgentSubEvent
	// TokenTracker tracks daily token usage and enforces limits. May be nil.
	TokenTracker TokenTracker
	// CompactMetrics tracks output compaction statistics. May be nil.
	CompactMetrics CompactStatsProvider
}

// CompactStatsProvider provides compaction statistics for TUI display.
type CompactStatsProvider interface {
	FormatStats() string
}

// TokenTracker provides read access to daily token usage for the status bar.
type TokenTracker interface {
	Limit() int64
	Remaining() int64     // -1 if unlimited
	PercentUsed() float64 // 0-100+
	TotalUsed() int64     // total tokens consumed today
}

// AgentSubEvent carries a subagent event from the agent tool to the TUI.
type AgentSubEvent struct {
	AgentID      string
	Kind         string // "tool_call", "tool_result", "text_delta", etc.
	Content      string
	PipelineID   string // groups agents in same call
	Mode         string // "single", "parallel", "chain"
	Step         int    // 1-based position in pipeline
	Total        int    // total agents in pipeline
}

// Screen provides thread-safe access to the current TUI screen content.
// It implements tools.ScreenProvider so the LLM can read what the user sees.
type Screen struct {
	mu      sync.Mutex
	content string
}

// ScreenContent returns the current screen content.
func (s *Screen) ScreenContent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.content
}

func (s *Screen) update(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.content = content
}


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

	m := model{
		cfg:         cfg,
		ctx:         ctx,
		cancel:      cancel,
		inputModel:  NewInputModel(history, cfg.Skills, cfg.SkillDirs, cfg.WorkDir),
		chatModel:   NewChatModel(renderer),
		statusModel: StatusModel{GitBranch: detectBranch(cfg.WorkDir)},
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
		m.chatModel.Thinking += msg.text
		// Update the thinking message in the chat.
		if len(m.chatModel.Messages) > 0 && m.chatModel.Messages[len(m.chatModel.Messages)-1].role == "thinking" {
			m.chatModel.Messages[len(m.chatModel.Messages)-1].content = m.chatModel.Thinking
		} else {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role: "thinking", content: m.chatModel.Thinking,
			})
		}
		m.chatModel.Scroll = 0
		return m, waitForAgent(m.agentCh)

	case agentTextMsg:
		// When text starts arriving after thinking, replace the thinking message
		// with the assistant message.
		if m.chatModel.Thinking != "" {
			m.chatModel.Thinking = ""
			// Remove the thinking message and add an assistant message.
			if len(m.chatModel.Messages) > 0 && m.chatModel.Messages[len(m.chatModel.Messages)-1].role == "thinking" {
				m.chatModel.Messages[len(m.chatModel.Messages)-1] = message{role: "assistant", content: ""}
			}
		}
		m.chatModel.Streaming += msg.text
		// Find the assistant message to update (may not be last if tool messages intervene).
		for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
			if m.chatModel.Messages[i].role == "assistant" {
				m.chatModel.Messages[i].content = m.chatModel.Streaming
				break
			}
		}
		m.chatModel.Scroll = 0
		// Trace: accumulate LLM text (don't log every delta, update last llm entry).
		if len(m.chatModel.TraceLog) > 0 && m.chatModel.TraceLog[len(m.chatModel.TraceLog)-1].kind == "llm" {
			m.chatModel.TraceLog[len(m.chatModel.TraceLog)-1].detail = m.chatModel.Streaming
		} else {
			m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
				time: time.Now(), kind: "llm", summary: "LLM response", detail: msg.text,
			})
		}

		return m, waitForAgent(m.agentCh)

	case agentToolCallMsg:
		if m.statusModel.ActiveTools == nil {
			m.statusModel.ActiveTools = make(map[string]time.Time)
		}
		m.statusModel.ActiveTools[msg.name] = time.Now()
		m.statusModel.ActiveTool = msg.name
		m.statusModel.ToolStart = time.Now()
		argsJSON, _ := json.MarshalIndent(msg.args, "", "  ")
		m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
			time:    time.Now(),
			kind:    "tool_call",
			summary: fmt.Sprintf(">>> %s", msg.name),
			detail:  string(argsJSON),
		})
		// Show tool call inline in chat.
		toolIn := toolCallSummary(msg.name, msg.args)
		newMsg := message{
			role: "tool", tool: msg.name, toolIn: toolIn,
		}
		// For agent/subagent tool, store type and title for display.
		if msg.name == "agent" || msg.name == "subagent" {
			// Support both legacy "type" and new "agent" key for agent name.
			agentType, _ := msg.args["type"].(string)
			if agentType == "" {
				agentType, _ = msg.args["agent"].(string)
			}
			newMsg.agentType = agentType
			// Support both legacy "prompt" and new "task" key.
			prompt, _ := msg.args["prompt"].(string)
			if prompt == "" {
				prompt, _ = msg.args["task"].(string)
			}
			if idx := strings.IndexByte(prompt, '\n'); idx > 0 {
				prompt = prompt[:idx]
			}
			if len(prompt) > 60 {
				prompt = prompt[:57] + "..."
			}
			newMsg.agentTitle = prompt
		}
		m.chatModel.Messages = append(m.chatModel.Messages, newMsg)

		return m, waitForAgent(m.agentCh)

	case agentToolResultMsg:
		delete(m.statusModel.ActiveTools, msg.name)
		// Update activeTool to show remaining running tool, or clear.
		m.statusModel.ActiveTool = ""
		for name := range m.statusModel.ActiveTools {
			m.statusModel.ActiveTool = name
			m.statusModel.ToolStart = m.statusModel.ActiveTools[name]
			break
		}
		m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
			time:    time.Now(),
			kind:    "tool_result",
			summary: fmt.Sprintf("<<< %s", msg.name),
			detail:  msg.content,
		})
		// Update the tool message with the result.
		for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
			if m.chatModel.Messages[i].role == "tool" && m.chatModel.Messages[i].tool == msg.name && m.chatModel.Messages[i].content == "" {
				m.chatModel.Messages[i].content = toolResultSummary(msg.content)
				break
			}
		}

		return m, waitForAgent(m.agentCh)

	case agentSubEventMsg:
		// Handle subagent event: associate with the correct agent message.
		if msg.kind == "spawn" {
			// Assign agentID to the most recent agent/subagent message without one.
			for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
				if (m.chatModel.Messages[i].tool == "agent" || m.chatModel.Messages[i].tool == "subagent") && m.chatModel.Messages[i].agentID == "" {
					m.chatModel.Messages[i].agentID = msg.agentID
					m.chatModel.Messages[i].pipelineID = msg.pipelineID
					m.chatModel.Messages[i].pipelineMode = msg.pipelineMode
					m.chatModel.Messages[i].pipelineStep = msg.pipelineStep
					m.chatModel.Messages[i].pipelineTotal = msg.pipelineTotal
					break
				}
			}
		} else {
			// Append event to the matching agent message.
			for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
				if (m.chatModel.Messages[i].tool == "agent" || m.chatModel.Messages[i].tool == "subagent") && m.chatModel.Messages[i].agentID == msg.agentID {
					evKind := msg.kind
					if evKind == "text_delta" {
						evKind = "text"
					}
					m.chatModel.Messages[i].agentEvents = append(m.chatModel.Messages[i].agentEvents, agentEv{
						kind:    evKind,
						content: msg.content,
					})
					break
				}
			}
		}
		m.chatModel.Scroll = 0
		return m, waitForSubEvent(m.cfg.AgentEventCh)

	case agentDoneMsg:
		m.running = false
		m.statusModel.ActiveTool = ""
		m.statusModel.ActiveTools = nil
		if msg.err != nil {
			m.chatModel.Messages = append(m.chatModel.Messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error: %v", msg.err),
			})
			m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
				time: time.Now(), kind: "error", summary: "Error", detail: msg.err.Error(),
			})
		}
		m.chatModel.Streaming = ""
		m.chatModel.Thinking = ""
		m.agentCh = nil
		return m, nil

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

// waitForAgent returns a Cmd that waits for the next message on the agent channel.
func waitForAgent(ch chan agentMsg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return agentDoneMsg{}
		}
		return msg
	}
}

func waitForSubEvent(ch <-chan AgentSubEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return agentSubEventMsg{
			agentID:       ev.AgentID,
			kind:          ev.Kind,
			content:       ev.Content,
			pipelineID:    ev.PipelineID,
			pipelineMode:  ev.Mode,
			pipelineStep:  ev.Step,
			pipelineTotal: ev.Total,
		}
	}
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

// cancelAgent stops a running agent and drains its channel.
func (m *model) cancelAgent() {
	m.cancel()
	m.running = false
	m.statusModel.ActiveTool = ""
	m.statusModel.ActiveTools = nil
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	if m.agentCh != nil {
		go func(ch chan agentMsg) {
			for range ch {
			}
		}(m.agentCh)
		m.agentCh = nil
	}
}

// submitPrompt sends a user prompt to the agent.
func (m *model) submitPrompt(text string) (tea.Model, tea.Cmd) {
	if m.cfg.Logger != nil {
		m.cfg.Logger.UserMessage(text)
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: text})
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0

	m.agentCh = make(chan agentMsg, 64)
	go m.runAgentLoop(text)

	return m, waitForAgent(m.agentCh)
}

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

// runAgentLoop runs the agent and sends events to the channel.
func (m *model) runAgentLoop(prompt string) {
	defer close(m.agentCh)
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			stdlog.Printf("agent loop panicked: %v\n%s", r, stack)
			m.agentCh <- agentDoneMsg{err: fmt.Errorf("agent panic: %v", r)}
		}
	}()
	log := m.cfg.Logger

	for ev, err := range m.cfg.Agent.RunStreaming(m.ctx, m.cfg.SessionID, prompt) {
		if err != nil {
			if log != nil {
				log.Error(err.Error())
			}
			m.agentCh <- agentDoneMsg{err: err}
			return
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" && ev.Content.Role == "thinking" {
				m.agentCh <- agentThinkingMsg{text: part.Text}
				continue
			}
			if part.Text != "" {
				if log != nil {
					log.LLMText(ev.Author, part.Text)
				}
				m.agentCh <- agentTextMsg{text: part.Text}
			}
			if part.FunctionCall != nil {
				if log != nil {
					log.ToolCall(ev.Author, part.FunctionCall.Name, part.FunctionCall.Args)
				}
				m.agentCh <- agentToolCallMsg{
					name: part.FunctionCall.Name,
					args: part.FunctionCall.Args,
				}
			}
			if part.FunctionResponse != nil {
				respJSON, _ := json.Marshal(part.FunctionResponse.Response)
				if log != nil {
					log.ToolResult(ev.Author, part.FunctionResponse.Name, string(respJSON))
				}
				m.agentCh <- agentToolResultMsg{
					name:    part.FunctionResponse.Name,
					content: string(respJSON),
				}
			}
		}
	}
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

