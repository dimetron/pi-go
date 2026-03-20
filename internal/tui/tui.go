// Package tui implements the interactive terminal UI using Bubble Tea v2.
package tui

import (
	"bytes"
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
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// message represents a chat message in the conversation.
type message struct {
	role    string // "user", "assistant", or "tool"
	content string
	tool    string // tool name (for role=="tool")
	toolIn  string // tool input args (for role=="tool")
	// Subagent event stream (for tool=="agent" or tool=="subagent").
	agentID       string    // subagent ID for matching events
	agentType     string    // subagent type (e.g. "task", "explore")
	agentTitle    string    // short description from prompt
	agentEvents   []agentEv // streamed events from the subagent
	pipelineID    string    // pipeline ID for grouping
	pipelineMode  string    // "single", "parallel", "chain"
	pipelineStep  int       // 1-based step in pipeline
	pipelineTotal int       // total steps in pipeline
}

// agentEv is a single event from a subagent's event stream.
type agentEv struct {
	kind    string // "tool_call", "tool_result", "text"
	content string
}

// model is the Bubble Tea model for the interactive TUI.
type model struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	// UI state.
	width  int
	height int

	// Input.
	input      string
	cursorPos  int
	history    []string
	historyIdx int
	completion string // ghost autocomplete suggestion

	// Enhanced completion state.
	completionResult *CompleteResult // completion candidates
	completionMode   bool            // whether we're in completion selection mode
	selectedIndex    int             // currently selected candidate index

	// Command cycling state.
	cyclingIdx int // current index for cycling commands in prompt

	// Messages.
	messages []message
	scroll   int // scroll offset from bottom

	// Agent state.
	running     bool
	streaming   string // current streaming text accumulator
	thinking    string // current thinking text accumulator
	activeTool  string
	activeTools map[string]time.Time // parallel tool tracking: name → start time
	toolStart   time.Time
	agentCh     chan agentMsg // channel for receiving agent events

	// Markdown renderer.
	renderer *glamour.TermRenderer

	// Trace log (for status bar counter).
	traceLog []traceEntry

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

	// Git branch (detected at startup).
	gitBranch string

	// Quit.
	quitting bool
}

// traceEntry represents a single entry in the debug trace log.
type traceEntry struct {
	time    time.Time
	kind    string // "llm", "tool_call", "tool_result", "error"
	summary string // short one-line summary
	detail  string // full content (args, response, etc.)
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
		cfg:        cfg,
		ctx:        ctx,
		cancel:     cancel,
		history:    history,
		historyIdx: -1,
		cyclingIdx: -1,
		messages:   make([]message, 0),
		renderer:   renderer,
		gitBranch:  detectBranch(cfg.WorkDir),
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
		m.updateRenderer()

	case tea.PasteMsg:
		if !m.running {
			text := msg.Content
			m.input = m.input[:m.cursorPos] + text + m.input[m.cursorPos:]
			m.cursorPos += len(text)
		}

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case restartMsg:
		execRestart()
		return m, tea.Quit

	case agentThinkingMsg:
		m.thinking += msg.text
		// Update the thinking message in the chat.
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "thinking" {
			m.messages[len(m.messages)-1].content = m.thinking
		} else {
			m.messages = append(m.messages, message{
				role: "thinking", content: m.thinking,
			})
		}
		m.scroll = 0
		return m, waitForAgent(m.agentCh)

	case agentTextMsg:
		// When text starts arriving after thinking, replace the thinking message
		// with the assistant message.
		if m.thinking != "" {
			m.thinking = ""
			// Remove the thinking message and add an assistant message.
			if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "thinking" {
				m.messages[len(m.messages)-1] = message{role: "assistant", content: ""}
			}
		}
		m.streaming += msg.text
		// Find the assistant message to update (may not be last if tool messages intervene).
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].role == "assistant" {
				m.messages[i].content = m.streaming
				break
			}
		}
		m.scroll = 0
		// Trace: accumulate LLM text (don't log every delta, update last llm entry).
		if len(m.traceLog) > 0 && m.traceLog[len(m.traceLog)-1].kind == "llm" {
			m.traceLog[len(m.traceLog)-1].detail = m.streaming
		} else {
			m.traceLog = append(m.traceLog, traceEntry{
				time: time.Now(), kind: "llm", summary: "LLM response", detail: msg.text,
			})
		}

		return m, waitForAgent(m.agentCh)

	case agentToolCallMsg:
		if m.activeTools == nil {
			m.activeTools = make(map[string]time.Time)
		}
		m.activeTools[msg.name] = time.Now()
		m.activeTool = msg.name
		m.toolStart = time.Now()
		argsJSON, _ := json.MarshalIndent(msg.args, "", "  ")
		m.traceLog = append(m.traceLog, traceEntry{
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
		m.messages = append(m.messages, newMsg)

		return m, waitForAgent(m.agentCh)

	case agentToolResultMsg:
		delete(m.activeTools, msg.name)
		// Update activeTool to show remaining running tool, or clear.
		m.activeTool = ""
		for name := range m.activeTools {
			m.activeTool = name
			m.toolStart = m.activeTools[name]
			break
		}
		m.traceLog = append(m.traceLog, traceEntry{
			time:    time.Now(),
			kind:    "tool_result",
			summary: fmt.Sprintf("<<< %s", msg.name),
			detail:  msg.content,
		})
		// Update the tool message with the result.
		for i := len(m.messages) - 1; i >= 0; i-- {
			if m.messages[i].role == "tool" && m.messages[i].tool == msg.name && m.messages[i].content == "" {
				m.messages[i].content = toolResultSummary(msg.content)
				break
			}
		}

		return m, waitForAgent(m.agentCh)

	case agentSubEventMsg:
		// Handle subagent event: associate with the correct agent message.
		if msg.kind == "spawn" {
			// Assign agentID to the most recent agent/subagent message without one.
			for i := len(m.messages) - 1; i >= 0; i-- {
				if (m.messages[i].tool == "agent" || m.messages[i].tool == "subagent") && m.messages[i].agentID == "" {
					m.messages[i].agentID = msg.agentID
					m.messages[i].pipelineID = msg.pipelineID
					m.messages[i].pipelineMode = msg.pipelineMode
					m.messages[i].pipelineStep = msg.pipelineStep
					m.messages[i].pipelineTotal = msg.pipelineTotal
					break
				}
			}
		} else {
			// Append event to the matching agent message.
			for i := len(m.messages) - 1; i >= 0; i-- {
				if (m.messages[i].tool == "agent" || m.messages[i].tool == "subagent") && m.messages[i].agentID == msg.agentID {
					evKind := msg.kind
					if evKind == "text_delta" {
						evKind = "text"
					}
					m.messages[i].agentEvents = append(m.messages[i].agentEvents, agentEv{
						kind:    evKind,
						content: msg.content,
					})
					break
				}
			}
		}
		m.scroll = 0
		return m, waitForSubEvent(m.cfg.AgentEventCh)

	case agentDoneMsg:
		m.running = false
		m.activeTool = ""
		m.activeTools = nil
		if msg.err != nil {
			m.messages = append(m.messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error: %v", msg.err),
			})
			m.traceLog = append(m.traceLog, traceEntry{
				time: time.Now(), kind: "error", summary: "Error", detail: msg.err.Error(),
			})
		}
		m.streaming = ""
		m.thinking = ""
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
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].content == "Pinging model..." {
			m.messages[len(m.messages)-1].content = content
		} else {
			m.messages = append(m.messages, message{role: "assistant", content: content})
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

	// Handle commit confirmation mode first so Esc cancels the commit
	// instead of quitting the TUI.
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

	// Handle login flow (manual key entry or SSO/device waiting).
	if !m.running && m.login != nil {
		switch {
		case key.Code == tea.KeyEsc:
			return m.handleLoginCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handleLoginCancel()
		case key.Code == tea.KeyEnter && m.login.phase == "waiting":
			apiKey := strings.TrimSpace(m.input)
			if apiKey == "" {
				return m, nil
			}
			m.input = ""
			m.cursorPos = 0
			return m.handleLoginSave(apiKey)
		}
		// For "waiting" phase, fall through to normal input handling for typing.
		// For "sso"/"device" phases, block other keys while waiting.
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

	switch {
	case key.Code == tea.KeyEsc:
		// Esc dismisses completion/cycling or cancels running agent, never quits
		if m.completionMode || m.cyclingIdx >= 0 {
			m.completionMode = false
			m.completionResult = nil
			m.selectedIndex = 0
			m.cyclingIdx = -1
			m.input = ""
			m.cursorPos = 0
			return m, nil
		}
		if m.running {
			m.cancel()
			m.running = false
			m.activeTool = ""
			m.activeTools = nil
			m.streaming = ""
			m.thinking = ""
			if m.agentCh != nil {
				go func(ch chan agentMsg) {
					for range ch {
					}
				}(m.agentCh)
				m.agentCh = nil
			}
			return m, nil
		}
		return m, nil

	case key.Code == 'c' && key.Mod == tea.ModCtrl:
		// Ctrl+C: dismiss completion/cycling, cancel agent, or quit
		if m.completionMode || m.cyclingIdx >= 0 {
			m.completionMode = false
			m.completionResult = nil
			m.selectedIndex = 0
			m.cyclingIdx = -1
			m.input = ""
			m.cursorPos = 0
			return m, nil
		}
		if m.running {
			m.cancel()
			m.running = false
			m.activeTool = ""
			m.activeTools = nil
			m.streaming = ""
			m.thinking = ""
			if m.agentCh != nil {
				go func(ch chan agentMsg) {
					for range ch {
					}
				}(m.agentCh)
				m.agentCh = nil
			}
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

	switch {
	case key.Code == tea.KeyEnter:
		// If in cycling mode, place command in input and dismiss menu
		if m.cyclingIdx >= 0 {
			// Input already set by cycling, just dismiss the menu
			m.cyclingIdx = -1
			m.cursorPos = len(m.input)
			return m, nil
		}
		// If in completion mode with a selection, apply it
		if m.completionMode && m.completionResult != nil && len(m.completionResult.Candidates) > 0 {
			m.input = m.completionResult.ApplySelection(m.selectedIndex)
			m.cursorPos = len(m.input)
			m.completionMode = false
			m.completionResult = nil
			m.selectedIndex = 0
			return m, nil
		}
		return m.submit()

	case key.Code == tea.KeyTab && key.Mod == tea.ModShift:
		// Shift+Tab cycles backwards through completions or commands
		if m.completionMode && m.completionResult != nil && len(m.completionResult.Candidates) > 0 {
			m.completionResult.CycleSelection(-1)
			m.selectedIndex = m.completionResult.Selected
		} else if m.input == "/" || m.cyclingIdx >= 0 {
			// Cycle backwards through all available commands
			allCmds := m.allCommandNames()
			if len(allCmds) > 0 {
				if m.cyclingIdx <= 0 {
					m.cyclingIdx = len(allCmds) - 1
				} else {
					m.cyclingIdx--
				}
				m.input = allCmds[m.cyclingIdx]
				m.cursorPos = len(m.input)
			}
		}

	case key.Code == tea.KeyTab:
		// Handle Tab for completion cycling and application
		if m.completionMode && m.completionResult != nil && len(m.completionResult.Candidates) > 0 {
			// Cycle through matches
			m.completionResult.CycleSelection(1)
			m.selectedIndex = m.completionResult.Selected
		} else if m.input == "/" || m.cyclingIdx >= 0 {
			// Cycle through all available commands (built-in + skills)
			allCmds := m.allCommandNames()
			if len(allCmds) > 0 {
				m.cyclingIdx = (m.cyclingIdx + 1) % len(allCmds)
				m.input = allCmds[m.cyclingIdx]
				m.cursorPos = len(m.input)
			}
		} else {
			// Try completion
			m.completionResult = Complete(m.input, m.cfg.Skills, m.cfg.WorkDir)

			if len(m.completionResult.Candidates) == 1 {
				// Single match - apply as ghost text
				m.input = m.completionResult.Candidates[0].Text
				m.cursorPos = len(m.input)
				m.completionResult = nil
			} else if len(m.completionResult.Candidates) > 1 {
				// Multiple matches - enter completion mode
				m.completionMode = true
				m.selectedIndex = 0
				m.completionResult.Selected = 0
			}
		}

	case key.Code == tea.KeyBackspace:
		if m.cursorPos > 0 {
			m.input = m.input[:m.cursorPos-1] + m.input[m.cursorPos:]
			m.cursorPos--
			// Reset cycling when input becomes empty
			if m.input == "" {
				m.cyclingIdx = -1
			}
		}

	case key.Code == tea.KeyDelete:
		if m.cursorPos < len(m.input) {
			m.input = m.input[:m.cursorPos] + m.input[m.cursorPos+1:]
		}

	case key.Code == tea.KeyLeft:
		if m.cursorPos > 0 {
			m.cursorPos--
		}

	case key.Code == tea.KeyRight:
		if m.cursorPos < len(m.input) {
			m.cursorPos++
		}

	case key.Code == tea.KeyHome || (key.Code == 'a' && key.Mod == tea.ModCtrl):
		m.cursorPos = 0

	case key.Code == tea.KeyEnd || (key.Code == 'e' && key.Mod == tea.ModCtrl):
		m.cursorPos = len(m.input)

	case key.Code == tea.KeyUp:
		if m.cyclingIdx >= 0 {
			// Navigate command menu up
			allCmds := m.allCommandNames()
			if len(allCmds) > 0 {
				if m.cyclingIdx <= 0 {
					m.cyclingIdx = len(allCmds) - 1
				} else {
					m.cyclingIdx--
				}
				m.input = allCmds[m.cyclingIdx]
				m.cursorPos = len(m.input)
			}
		} else if len(m.history) > 0 {
			if m.historyIdx < 0 {
				m.historyIdx = len(m.history) - 1
			} else if m.historyIdx > 0 {
				m.historyIdx--
			}
			m.input = m.history[m.historyIdx]
			m.cursorPos = len(m.input)
		}

	case key.Code == tea.KeyDown:
		if m.cyclingIdx >= 0 {
			// Navigate command menu down
			allCmds := m.allCommandNames()
			if len(allCmds) > 0 {
				m.cyclingIdx = (m.cyclingIdx + 1) % len(allCmds)
				m.input = allCmds[m.cyclingIdx]
				m.cursorPos = len(m.input)
			}
		} else if m.historyIdx >= 0 {
			m.historyIdx++
			if m.historyIdx >= len(m.history) {
				m.historyIdx = -1
				m.input = ""
			} else {
				m.input = m.history[m.historyIdx]
			}
			m.cursorPos = len(m.input)
		}

	case key.Code == tea.KeyPgUp:
		m.scroll += 5
		maxScroll := m.maxScroll()
		if m.scroll > maxScroll {
			m.scroll = maxScroll
		}

	case key.Code == tea.KeyPgDown:
		m.scroll -= 5
		if m.scroll < 0 {
			m.scroll = 0
		}

	default:
		if key.Text != "" && isUserInput(key.Text) {
			// Show command menu when "/" is typed into empty input
			if key.Text == "/" && m.input == "" {
				m.reloadSkills()
				m.input = "/"
				m.cursorPos = 1
				m.cyclingIdx = 0
				allCmds := m.allCommandNames()
				if len(allCmds) > 0 {
					m.input = allCmds[0]
					m.cursorPos = len(m.input)
				}
				return m, nil
			}
			m.input = m.input[:m.cursorPos] + key.Text + m.input[m.cursorPos:]
			m.cursorPos += len(key.Text)
			// Reset cycling when user starts typing
			m.cyclingIdx = -1
		}
	}

	// Update autocomplete suggestion using new completion engine.
	if m.cursorPos == len(m.input) {
		result := Complete(m.input, m.cfg.Skills, m.cfg.WorkDir)
		if result != nil && len(result.Candidates) > 0 {
			// Only show ghost text for single matches
			if len(result.Candidates) == 1 {
				m.completion = result.Candidates[0].Text
			} else {
				m.completion = ""
			}
		} else {
			m.completion = ""
		}
	} else {
		m.completion = ""
	}

	// Clear completion mode when user types (but not when Tab/Shift-Tab cycles).
	if key.Code != tea.KeyTab {
		m.completionMode = false
		m.completionResult = nil
		m.selectedIndex = 0
	}

	return m, nil
}

func (m *model) submit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.input)
	if input == "" {
		return m, nil
	}

	// Handle slash commands.
	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
	}

	// Add to history (skip duplicates of the last entry).
	if len(m.history) == 0 || m.history[len(m.history)-1] != input {
		m.history = append(m.history, input)
		appendHistory(input)
	}
	m.historyIdx = -1

	// Log user message.
	if m.cfg.Logger != nil {
		m.cfg.Logger.UserMessage(input)
	}

	// Add user message.
	m.messages = append(m.messages, message{role: "user", content: input})

	// Add empty assistant message for streaming.
	m.messages = append(m.messages, message{role: "assistant", content: ""})
	m.streaming = ""
	m.thinking = ""

	// Clear input.
	prompt := input
	m.input = ""
	m.cursorPos = 0
	m.running = true
	m.scroll = 0

	// Create agent channel and start goroutine.
	m.agentCh = make(chan agentMsg, 64)
	go m.runAgentLoop(prompt)

	// Start listening for agent events.
	return m, waitForAgent(m.agentCh)
}

func (m *model) handleSlashCommand(input string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(input)
	cmd := strings.ToLower(parts[0])

	// Log all slash commands.
	if m.cfg.Logger != nil {
		m.cfg.Logger.UserMessage(input)
	}

	// Add to history (skip duplicates).
	if len(m.history) == 0 || m.history[len(m.history)-1] != input {
		m.history = append(m.history, input)
		appendHistory(input)
	}
	m.historyIdx = -1

	switch cmd {
	case "/help":
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: m.formatHelp(),
		})
	case "/clear":
		m.messages = m.messages[:0]
		m.scroll = 0
	case "/model":
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: m.formatModelInfo(),
		})
	case "/session":
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Session: `%s`", m.cfg.SessionID),
		})
	case "/context":
		m.messages = append(m.messages, message{
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
		m.input = ""
		m.cursorPos = 0
		return m, tea.Quit
	default:
		// Check if it's a dynamic skill command.
		skillName := strings.TrimPrefix(cmd, "/")
		if skill, ok := extension.FindSkill(m.cfg.Skills, skillName); ok {
			return m.handleSkillCommand(skill, parts[1:])
		}
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Unknown command: `%s`. Type `/help` for available commands.", cmd),
		})
	}

	m.input = ""
	m.cursorPos = 0
	return m, nil
}

// handleBranchCommand handles /branch subcommands: create, switch, list.
func (m *model) handleBranchCommand(args []string) {
	if m.cfg.SessionService == nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Branching not available (no session service configured).",
		})
		return
	}

	if len(args) == 0 {
		m.messages = append(m.messages, message{
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
			m.messages = append(m.messages, message{
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
		m.messages = append(m.messages, message{role: "assistant", content: b.String()})

	case "switch":
		if len(args) < 2 {
			m.messages = append(m.messages, message{
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
			m.messages = append(m.messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error switching branch: %v", err),
			})
			return
		}
		m.messages = append(m.messages, message{
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
			m.messages = append(m.messages, message{
				role:    "assistant",
				content: fmt.Sprintf("Error creating branch: %v", err),
			})
			return
		}
		// Auto-switch to the new branch.
		_ = m.cfg.SessionService.SwitchBranch(
			m.cfg.SessionID, agent.AppName, agent.DefaultUserID, branchName,
		)
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Created and switched to branch `%s`.", branchName),
		})
	}
}

// handleCompactCommand triggers session compaction.
func (m *model) handleCompactCommand() {
	if m.cfg.SessionService == nil {
		m.messages = append(m.messages, message{
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
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Compaction error: %v", err),
		})
		return
	}
	m.messages = append(m.messages, message{
		role:    "assistant",
		content: "Session context compacted.",
	})
}

// handleAgentsCommand shows the status of running and recent subagents.
func (m *model) handleAgentsCommand() {
	if m.cfg.Orchestrator == nil {
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Subagent system not available.",
		})
		return
	}

	agents := m.cfg.Orchestrator.List()
	if len(agents) == 0 {
		m.messages = append(m.messages, message{
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

	m.messages = append(m.messages, message{
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
	for _, msg := range m.messages {
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
		formatTokenCount(userTokens), countByRole(m.messages, "user")))
	b.WriteString(fmt.Sprintf("- **Assistant messages**: ~%s tokens (%d msgs)\n",
		formatTokenCount(assistantTokens), countByRole(m.messages, "assistant")))
	b.WriteString(fmt.Sprintf("- **Tool calls**: ~%s tokens (%d calls)\n",
		formatTokenCount(toolTokens), countByRole(m.messages, "tool")))
	b.WriteString(fmt.Sprintf("- **Total context**: ~%s tokens (%d messages)\n",
		formatTokenCount(totalTokens), len(m.messages)))

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

// countByRole counts messages with the given role.
func countByRole(msgs []message, role string) int {
	n := 0
	for _, msg := range msgs {
		if msg.role == role {
			n++
		}
	}
	return n
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
	messagesView := m.renderMessages()
	statusBar := m.renderStatusBar()
	inputArea := m.renderInput()

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

	startLine := totalLines - availableHeight - m.scroll
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

// toolCallSummary returns a short one-line summary of tool arguments.
func toolCallSummary(name string, args map[string]any) string {
	switch name {
	case "read":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "write":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "edit":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			if len(cmd) > 80 {
				cmd = cmd[:77] + "..."
			}
			return cmd
		}
	case "grep":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	case "find":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	case "ls":
		if p, ok := args["path"].(string); ok {
			return p
		}
		return "."
	case "tree":
		p, _ := args["path"].(string)
		if p == "" {
			p = "."
		}
		if d, ok := args["depth"].(float64); ok && d > 0 {
			return fmt.Sprintf("%s (depth %d)", p, int(d))
		}
		return p
	case "agent":
		typ, _ := args["type"].(string)
		prompt, _ := args["prompt"].(string)
		// Truncate prompt to first line, max 60 chars.
		if idx := strings.IndexByte(prompt, '\n'); idx > 0 {
			prompt = prompt[:idx]
		}
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		if typ != "" && prompt != "" {
			return fmt.Sprintf("%s: %s", typ, prompt)
		}
		if typ != "" {
			return typ
		}
		return prompt
	}
	return ""
}

// toolResultSummary returns a short one-line summary of a tool result.
func toolResultSummary(content string) string {
	// Try to parse as JSON and extract a friendly summary.
	var data map[string]any
	if json.Unmarshal([]byte(content), &data) == nil {
		return formatToolResult(data)
	}
	// Collapse to single line.
	content = strings.ReplaceAll(content, "\n", " ")
	if len(content) > 120 {
		return content[:117] + "..."
	}
	return content
}

// formatToolResult extracts a readable summary from a parsed tool result.
func formatToolResult(data map[string]any) string {
	// ls tool: show file/dir names
	if entries, ok := data["entries"].([]any); ok {
		var names []string
		for _, e := range entries {
			if m, ok := e.(map[string]any); ok {
				name, _ := m["name"].(string)
				if isDir, ok := m["is_dir"].(bool); ok && isDir {
					name += "/"
				}
				names = append(names, name)
			}
		}
		result := strings.Join(names, "  ")
		if len(result) > 120 {
			return result[:117] + "..."
		}
		return result
	}
	// tree tool: show dirs/files count
	if _, ok := data["tree"].(string); ok {
		d, _ := data["dirs"].(float64)
		f, _ := data["files"].(float64)
		return fmt.Sprintf("%d dirs, %d files", int(d), int(f))
	}
	// grep tool: show matches with file:line: content
	if matchList, ok := data["matches"].([]any); ok {
		total, _ := data["total_matches"].(float64)
		trunc, _ := data["truncated"].(bool)
		var sb strings.Builder
		for _, m := range matchList {
			if entry, ok := m.(map[string]any); ok {
				file, _ := entry["file"].(string)
				line, _ := entry["line"].(float64)
				content, _ := entry["content"].(string)
				fmt.Fprintf(&sb, "%s:%d: %s\n", file, int(line), content)
			}
		}
		if trunc {
			fmt.Fprintf(&sb, "... (%d total matches, truncated)", int(total))
		}
		return strings.TrimRight(sb.String(), "\n")
	}
	if matches, ok := data["total_matches"].(float64); ok {
		return fmt.Sprintf("%d matches", int(matches))
	}
	// find tool: show file list
	if fileList, ok := data["files"].([]any); ok {
		total, _ := data["total_files"].(float64)
		trunc, _ := data["truncated"].(bool)
		var sb strings.Builder
		for _, f := range fileList {
			if name, ok := f.(string); ok {
				sb.WriteString(name)
				sb.WriteByte('\n')
			}
		}
		if trunc {
			fmt.Fprintf(&sb, "... (%d total files, truncated)", int(total))
		}
		return strings.TrimRight(sb.String(), "\n")
	}
	if total, ok := data["total_files"].(float64); ok {
		return fmt.Sprintf("%d files", int(total))
	}
	// read tool: show actual content with line numbers
	if content, ok := data["content"].(string); ok {
		total, _ := data["total_lines"].(float64)
		trunc, _ := data["truncated"].(bool)
		if trunc {
			content += fmt.Sprintf("\n... (%d total lines, truncated)", int(total))
		}
		return content
	}
	if total, ok := data["total_lines"].(float64); ok {
		trunc := ""
		if t, ok := data["truncated"].(bool); ok && t {
			trunc = " (truncated)"
		}
		return fmt.Sprintf("%d lines%s", int(total), trunc)
	}
	// write tool: show bytes written
	if bw, ok := data["bytes_written"].(float64); ok {
		if p, ok := data["path"].(string); ok {
			return fmt.Sprintf("%s (%d bytes)", p, int(bw))
		}
	}
	// edit tool: show replacements
	if r, ok := data["replacements"].(float64); ok {
		return fmt.Sprintf("%d replacements", int(r))
	}
	// bash tool: show exit code + truncated stdout
	if code, ok := data["exit_code"].(float64); ok {
		stdout, _ := data["stdout"].(string)
		stdout = strings.ReplaceAll(stdout, "\n", " ")
		if len(stdout) > 80 {
			stdout = stdout[:77] + "..."
		}
		if int(code) != 0 {
			return fmt.Sprintf("exit %d: %s", int(code), stdout)
		}
		if stdout == "" {
			return "(No output)"
		}
		return stdout
	}
	// Fallback: compact JSON
	b, _ := json.Marshal(data)
	s := string(b)
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
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

// isUserInput returns true if the string looks like actual user-typed text
// rather than a terminal response sequence leaked through as KeyPressMsg.
// Terminal responses (OSC replies, CSI cursor reports, Kitty protocol) arrive
// with ESC stripped, leaving patterns like "]11;rgb:..." or "[21;1R".
func isUserInput(s string) bool {
	// Reject non-printable characters.
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	// Reject known terminal response patterns (ESC already stripped).
	// OSC responses: ]<digit>; ...
	if len(s) > 2 && s[0] == ']' && s[1] >= '0' && s[1] <= '9' {
		return false
	}
	// CSI responses: [<params><letter> e.g. "[21;1R" (cursor position report)
	if len(s) > 2 && s[0] == '[' && (s[len(s)-1] >= 'A' && s[len(s)-1] <= 'Z') {
		return false
	}
	// Kitty keyboard protocol responses containing ";rgb:" or ending in "u"
	if strings.Contains(s, ";rgb:") || strings.Contains(s, "rgb:") {
		return false
	}
	return true
}

// slashCommands is the list of available slash commands for autocomplete.
var slashCommands = []string{
	"/help",
	"/clear",
	"/model",
	"/session",
	"/context",
	"/branch",
	"/compact",
	"/agents",
	"/history",
	"/login",
	"/commit",
	"/plan",
	"/run",
	"/skills",
	"/ping",
	"/rtk",
	"/restart",
	"/exit",
	"/quit",
}

// completeSlashCommand returns the best matching slash command for the current input,
// or "" if no match. Only completes when cursor is at end of input.
func completeSlashCommand(input string) string {
	if !strings.HasPrefix(input, "/") || len(input) < 2 {
		return ""
	}
	prefix := strings.ToLower(input)
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, prefix) && cmd != prefix {
			return cmd
		}
	}
	return ""
}

// matchingSlashCommands returns all slash commands matching the given prefix.
func matchingSlashCommands(input string) []string {
	prefix := strings.ToLower(input)
	var matches []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

// reloadSkills re-scans skill directories from disk and updates the cached list.
func (m *model) reloadSkills() {
	if len(m.cfg.SkillDirs) > 0 {
		if fresh, err := extension.LoadSkills(m.cfg.SkillDirs...); err == nil {
			m.cfg.Skills = fresh
		}
	}
}

// allCommandNames returns a sorted list of all command names: built-in + skills.
func (m *model) allCommandNames() []string {
	seen := make(map[string]bool)
	var cmds []string
	for _, cmd := range slashCommands {
		if !seen[cmd] {
			seen[cmd] = true
			cmds = append(cmds, cmd)
		}
	}
	for _, skill := range m.cfg.Skills {
		name := "/" + skill.Name
		if !seen[name] {
			seen[name] = true
			cmds = append(cmds, name)
		}
	}
	sort.Strings(cmds)
	return cmds
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
	m.messages = append(m.messages, message{
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
		m.input = ""
		m.cursorPos = 0
		m.messages = append(m.messages, message{
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
			m.messages = append(m.messages, message{
				role:    "assistant",
				content: "Output compactor is not active.",
			})
			return
		}
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: m.cfg.CompactMetrics.FormatStats(),
		})
	default:
		m.messages = append(m.messages, message{
			role:    "assistant",
			content: "Usage: `/rtk` or `/rtk stats` — Show output compaction statistics",
		})
	}
}

// slashCommandDesc returns a short description for a slash command.
func slashCommandDesc(cmd string) string {
	switch cmd {
	case "/help":
		return "Show help"
	case "/clear":
		return "Clear conversation"
	case "/model":
		return "Show current model"
	case "/session":
		return "Show session info"
	case "/context":
		return "Show context usage"
	case "/branch":
		return "Manage branches"
	case "/compact":
		return "Compact context"
	case "/agents":
		return "Show subagents"
	case "/rtk":
		return "Output compaction stats"
	case "/history":
		return "Command history"
	case "/login":
		return "Configure API keys (codex, openai, anthropic, gemini)"
	case "/commit":
		return "Create commit from staged changes"
	case "/plan":
		return "Start PDD planning session"
	case "/run":
		return "Execute a spec with task agent"
	case "/skills":
		return "List skills (create, load)"
	case "/ping":
		return "Test LLM connectivity"
	case "/restart":
		return "Restart pi process"
	case "/exit", "/quit":
		return "Exit"
	default:
		return ""
	}
}

func (m *model) updateRenderer() {
	contentWidth := m.width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}
	m.renderer, _ = glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(contentWidth),
		glamour.WithEmoji(),
	)
}

func (m *model) renderMessages() string {
	if len(m.messages) == 0 {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		return dim.Render("  Welcome to pi-go! Type a prompt, /command, or press Tab to cycle commands.")
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true).Render("● ")
	toolBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("35")).Bold(true).Render("● ")
	sepWidth := m.width - 4
	if sepWidth < 20 {
		sepWidth = 20
	}
	separator := dim.Render(strings.Repeat("─", sepWidth))

	var b strings.Builder
	for i, msg := range m.messages {
		switch msg.role {
		case "user":
			if i > 0 {
				b.WriteString(separator)
				b.WriteString("\n")
			}
			label := lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true).
				Render("> ")
			b.WriteString(label)
			b.WriteString(msg.content)
			b.WriteString("\n")

		case "tool":
			toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("35")).Bold(true)
			argStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
			b.WriteString("\n")

			// Special rendering for agent tool: show type, title, and event stream.
			if msg.tool == "agent" || msg.tool == "subagent" {
				agentBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true).Render("● ")
				typeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
				titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
				b.WriteString(agentBullet)
				b.WriteString(typeStyle.Render("agent"))
				if msg.agentType != "" {
					b.WriteString(dim.Render("["))
					b.WriteString(typeStyle.Render(msg.agentType))
					b.WriteString(dim.Render("]"))
				}
				if msg.agentTitle != "" {
					b.WriteString(" ")
					b.WriteString(titleStyle.Render(msg.agentTitle))
				}
				b.WriteString("\n")

				// Show event stream (last N events).
				if len(msg.agentEvents) > 0 {
					evStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
					evToolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
					maxEvents := 8
					events := msg.agentEvents
					if len(events) > maxEvents {
						skipped := len(events) - maxEvents
						events = events[len(events)-maxEvents:]
						b.WriteString("  ")
						b.WriteString(dim.Render(fmt.Sprintf("│ ... %d earlier events\n", skipped)))
					}
					for _, ev := range events {
						b.WriteString("  ")
						b.WriteString(dim.Render("│ "))
						switch ev.kind {
						case "tool_call":
							b.WriteString(evToolStyle.Render("⚙ " + ev.content))
						case "tool_result":
							summary := ev.content
							if len(summary) > 80 {
								summary = summary[:77] + "..."
							}
							b.WriteString(evStyle.Render("  ✓ " + summary))
						case "text":
							// Skip text deltas in event stream to avoid clutter.
						default:
							b.WriteString(evStyle.Render(ev.kind + ": " + ev.content))
						}
						b.WriteString("\n")
					}
				}

				// Show result summary when done.
				if msg.content != "" {
					b.WriteString("  ")
					b.WriteString(dim.Render("│ "))
					summary := msg.content
					if len(summary) > 100 {
						summary = summary[:97] + "..."
					}
					b.WriteString(dim.Render("→ " + summary))
					b.WriteString("\n")
				}
			} else {
				b.WriteString(toolBullet)
				b.WriteString(toolStyle.Render(msg.tool))
				if msg.toolIn != "" {
					args := msg.toolIn
					if len(args) > 80 {
						args = args[:77] + "..."
					}
					b.WriteString(dim.Render("("))
					b.WriteString(argStyle.Render(args))
					b.WriteString(dim.Render(")"))
				}
				b.WriteString("\n")
				if msg.content != "" {
					content := msg.content
					lines := strings.Split(content, "\n")
					maxLines := 15
					if len(lines) > maxLines {
						lines = append(lines[:maxLines], dim.Render(fmt.Sprintf("... (%d more lines)", len(lines)-maxLines)))
					}
					var styled []string
					switch {
					case msg.tool == "read" && msg.toolIn != "":
						styled = highlightReadOutput(lines, msg.toolIn)
					case msg.tool == "grep":
						styled = highlightGrepOutput(lines)
					case msg.tool == "find":
						styled = highlightFindOutput(lines)
					}
					if styled != nil {
						for _, line := range styled {
							b.WriteString("  ")
							b.WriteString(dim.Render("│ "))
							b.WriteString(line)
							b.WriteString("\n")
						}
					} else {
						for _, line := range lines {
							b.WriteString("  ")
							b.WriteString(dim.Render("│ "))
							b.WriteString(dim.Render(line))
							b.WriteString("\n")
						}
					}
				}
			}

		case "thinking":
			if msg.content != "" {
				thinkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
				thinkBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("💭 ")
				b.WriteString("\n")
				b.WriteString(thinkBullet)
				// Show last few lines of thinking to keep it compact.
				lines := strings.Split(msg.content, "\n")
				maxLines := 6
				if len(lines) > maxLines {
					lines = lines[len(lines)-maxLines:]
				}
				for j, line := range lines {
					if j > 0 {
						b.WriteString("   ")
					}
					b.WriteString(thinkStyle.Render(line))
					if j < len(lines)-1 {
						b.WriteString("\n")
					}
				}
				b.WriteString("\n")
			}

		case "assistant":
			content := msg.content
			if content == "" && m.running && i == len(m.messages)-1 {
				content = "..."
			}
			if content != "" {
				b.WriteString("\n")
				b.WriteString(bullet)
				rendered := m.renderMarkdown(content)
				b.WriteString(rendered)
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

func (m *model) renderMarkdown(text string) string {
	if text == "" {
		return ""
	}
	if m.renderer == nil {
		return text
	}
	rendered, err := m.renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(rendered, "\n")
}

// highlightReadOutput applies syntax highlighting to read tool output lines.
// Each line has format "     1\tcontent" — line numbers are styled separately.
func highlightReadOutput(lines []string, filename string) []string {
	numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// Separate line numbers from code
	var codeLines []string
	var lineNums []string
	for _, line := range lines {
		if parts := strings.SplitN(line, "\t", 2); len(parts) == 2 {
			lineNums = append(lineNums, parts[0])
			codeLines = append(codeLines, parts[1])
		} else {
			lineNums = append(lineNums, "")
			codeLines = append(codeLines, line)
		}
	}

	// Highlight all code at once for proper multi-line token handling
	code := strings.Join(codeLines, "\n")
	highlighted := highlightCode(code, filename)
	highlightedLines := strings.Split(highlighted, "\n")

	// Recombine with styled line numbers
	result := make([]string, 0, len(lines))
	for i := range lines {
		if i < len(highlightedLines) {
			if i < len(lineNums) && lineNums[i] != "" {
				result = append(result, numStyle.Render(lineNums[i])+" "+highlightedLines[i])
			} else {
				result = append(result, highlightedLines[i])
			}
		}
	}
	return result
}

// highlightCode applies chroma syntax highlighting based on filename extension.
func highlightCode(code, filename string) string {
	lexer := lexers.Match(filename)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	style := styles.Get("monokai")
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}

	iterator, err := lexer.Tokenise(nil, code)
	if err != nil {
		return code
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return code
	}
	return strings.TrimRight(buf.String(), "\n")
}

// highlightGrepOutput styles grep result lines of the form "file:line: content".
// File path and line number get distinct colors; content is syntax-highlighted
// based on the file extension.
func highlightGrepOutput(lines []string) []string {
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39"))     // blue
	lineNumStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240")) // gray
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	result := make([]string, 0, len(lines))
	for _, line := range lines {
		// Try to parse "file:line: content"
		first := strings.IndexByte(line, ':')
		if first < 0 {
			// Not a match line (e.g. truncation note) — dim it.
			result = append(result, lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(line))
			continue
		}
		second := strings.IndexByte(line[first+1:], ':')
		if second < 0 {
			result = append(result, lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(line))
			continue
		}
		second += first + 1 // absolute index of second colon

		filePart := line[:first]
		linePart := line[first+1 : second]
		contentPart := ""
		if second+1 < len(line) {
			contentPart = strings.TrimPrefix(line[second+1:], " ")
		}

		// Highlight the content portion using the file extension.
		highlighted := highlightCode(contentPart, filePart)

		var sb strings.Builder
		sb.WriteString(fileStyle.Render(filePart))
		sb.WriteString(sepStyle.Render(":"))
		sb.WriteString(lineNumStyle.Render(linePart))
		sb.WriteString(sepStyle.Render(": "))
		sb.WriteString(highlighted)
		result = append(result, sb.String())
	}
	return result
}

// highlightFindOutput styles find/glob result lines as file paths.
// Directories get a trailing "/" and different color.
func highlightFindOutput(lines []string) []string {
	fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("39")) // blue
	dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33")).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "...") {
			// Truncation note.
			result = append(result, dimStyle.Render(line))
		} else if strings.HasSuffix(line, "/") {
			result = append(result, dirStyle.Render(line))
		} else {
			result = append(result, fileStyle.Render(line))
		}
	}
	return result
}

func (m *model) renderStatusBar() string {
	bg := lipgloss.Color("236")
	fg := lipgloss.Color("252")
	dimFg := lipgloss.Color("243")

	bright := lipgloss.NewStyle().Background(bg).Foreground(fg)
	dim := lipgloss.NewStyle().Background(bg).Foreground(dimFg)
	bar := lipgloss.NewStyle().Background(bg).Width(m.width)

	sep := dim.Render("  |  ")

	var parts []string

	// Provider | Model.
	if m.cfg.ProviderName != "" {
		parts = append(parts, bright.Render(fmt.Sprintf(" %s | %s", m.cfg.ProviderName, m.cfg.ModelName)))
	} else {
		parts = append(parts, bright.Render(fmt.Sprintf(" %s", m.cfg.ModelName)))
	}

	// Context size estimate (rough: ~4 chars per token).
	ctxChars := 0
	for _, msg := range m.messages {
		ctxChars += len(msg.content) + len(msg.tool) + len(msg.toolIn)
	}
	ctxTokens := ctxChars / 4
	switch {
	case ctxTokens >= 1000:
		parts = append(parts, dim.Render(fmt.Sprintf("ctx: %.1fk", float64(ctxTokens)/1000)))
	default:
		parts = append(parts, dim.Render(fmt.Sprintf("ctx: %d", ctxTokens)))
	}

	// Token usage guardrail.
	if tt := m.cfg.TokenTracker; tt != nil {
		total := tt.TotalUsed()
		limit := tt.Limit()
		if limit > 0 {
			pct := tt.PercentUsed()
			var tokenStyle lipgloss.Style
			switch {
			case pct >= 100:
				tokenStyle = lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("196")) // red
			case pct >= 80:
				tokenStyle = lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("214")) // orange
			default:
				tokenStyle = dim
			}
			parts = append(parts, tokenStyle.Render(fmt.Sprintf("tokens: %s/%s (%.0f%%)",
				formatTokenCount(total), formatTokenCount(limit), pct)))
		} else if total > 0 {
			parts = append(parts, dim.Render(fmt.Sprintf("tokens: %s", formatTokenCount(total))))
		}
	}

	// Git branch.
	if m.gitBranch != "" {
		parts = append(parts, bright.Render(fmt.Sprintf("\u2387 %s", m.gitBranch)))
	}

	// Active tools or thinking status.
	if len(m.activeTools) > 1 {
		// Multiple tools running in parallel.
		var toolNames []string
		for name := range m.activeTools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		parts = append(parts, bright.Render(fmt.Sprintf("tools[%d]: %s", len(toolNames), strings.Join(toolNames, ", "))))
	} else if m.activeTool != "" {
		elapsed := time.Since(m.toolStart).Truncate(time.Millisecond)
		parts = append(parts, bright.Render(fmt.Sprintf("tool: %s (%s)", m.activeTool, elapsed)))
	} else if m.running {
		parts = append(parts, dim.Render("thinking..."))
	}

	// Subagent status.
	if m.cfg.Orchestrator != nil {
		agents := m.cfg.Orchestrator.List()
		if len(agents) > 0 {
			var runningNames []string
			total := len(agents)
			failed := 0
			for _, a := range agents {
				switch a.Status {
				case "running":
					name := a.Type
					if len(name) > 12 {
						name = name[:12]
					}
					runningNames = append(runningNames, name)
				case "failed":
					failed++
				}
			}
			agentFg := lipgloss.Color("35") // green
			if len(runningNames) > 0 {
				agentFg = lipgloss.Color("214") // orange when active
			}
			if failed > 0 {
				agentFg = lipgloss.Color("196") // red if any failed
			}
			agentStyle := lipgloss.NewStyle().Background(bg).Foreground(agentFg)
			var label string
			if len(runningNames) > 0 {
				label = fmt.Sprintf("agents[%d]: %s", total, strings.Join(runningNames, ", "))
			} else {
				label = fmt.Sprintf("agents: %d done", total)
			}
			if failed > 0 {
				label += fmt.Sprintf(" (%d failed)", failed)
			}
			parts = append(parts, agentStyle.Render(label))
		}
	}

	// /run cycle indicator.
	if m.run != nil && m.run.phase != "done" && m.run.phase != "failed" {
		cycle := m.run.retries + 1
		runStyle := lipgloss.NewStyle().Background(bg).Foreground(lipgloss.Color("214"))
		parts = append(parts, runStyle.Render(fmt.Sprintf("run[%s]: cycle %d/%d",
			m.run.specName, cycle, m.run.maxRetries)))
	}

	// Trace count.
	if len(m.traceLog) > 0 {
		parts = append(parts, dim.Render(fmt.Sprintf("trace: %d", len(m.traceLog))))
	}

	return bar.Render(strings.Join(parts, sep))
}

func formatTokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
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

func (m *model) renderInput() string {
	prefix := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true).
		Render("> ")

	if m.running {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		return prefix + dim.Render("(waiting for response...)")
	}

	// Show input with cursor.
	before := m.input[:m.cursorPos]
	after := m.input[m.cursorPos:]
	cursor := lipgloss.NewStyle().
		Background(lipgloss.Color("252")).
		Foreground(lipgloss.Color("0")).
		Render(" ")
	if m.cursorPos < len(m.input) {
		cursor = lipgloss.NewStyle().
			Background(lipgloss.Color("252")).
			Foreground(lipgloss.Color("0")).
			Render(string(m.input[m.cursorPos]))
		after = m.input[m.cursorPos+1:]
	}

	// Show completion menu or command cycling menu.
	if m.completionMode && m.completionResult != nil && len(m.completionResult.Candidates) > 0 {
		// Render completion menu below the input line.
		inputLine := prefix + before + cursor + after
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		sel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)

		var menu strings.Builder
		for i, c := range m.completionResult.Candidates {
			if i == m.selectedIndex {
				menu.WriteString(sel.Render("  > " + c.Text))
			} else {
				menu.WriteString(dim.Render("    " + c.Text))
			}
			if c.Description != "" {
				menu.WriteString(dim.Render(" — " + c.Description))
			}
			menu.WriteString("\n")
		}
		return inputLine + "\n" + menu.String()
	}

	if m.cyclingIdx >= 0 {
		// Render command cycling menu below the input line.
		inputLine := prefix + before + cursor + after
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		sel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
		descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

		allCmds := m.allCommandNames()
		var menu strings.Builder
		for i, cmd := range allCmds {
			desc := slashCommandDesc(cmd)
			if desc == "" {
				for _, skill := range m.cfg.Skills {
					if "/"+skill.Name == cmd {
						desc = skill.Description
						break
					}
				}
			}
			if i == m.cyclingIdx {
				menu.WriteString(sel.Render("  > " + cmd))
			} else {
				menu.WriteString(dim.Render("    " + cmd))
			}
			if desc != "" {
				menu.WriteString(descStyle.Render(" — " + desc))
			}
			menu.WriteString("\n")
		}
		return inputLine + "\n" + menu.String()
	}

	// Show ghost autocomplete suggestion after cursor.
	ghost := ""
	if m.completion != "" && m.cursorPos == len(m.input) {
		suffix := m.completion[len(m.input):]
		ghost = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(suffix + " [tab]")
	}

	return prefix + before + cursor + after + ghost
}

func (m *model) maxScroll() int {
	if len(m.messages) == 0 {
		return 0
	}
	messagesView := m.renderMessages()
	totalLines := strings.Count(messagesView, "\n") + 1

	availableHeight := m.height - 3
	if availableHeight < 1 {
		return 0
	}
	max := totalLines - availableHeight
	if max < 0 {
		return 0
	}
	return max
}
