// Package tui implements the interactive terminal UI using Bubble Tea v2.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/charmbracelet/glamour"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

func (agentTextMsg) agentMsg()       {}
func (agentThinkingMsg) agentMsg()   {}
func (agentToolCallMsg) agentMsg()   {}
func (agentToolResultMsg) agentMsg() {}
func (agentDoneMsg) agentMsg()       {}

// Config holds configuration for the TUI.
type Config struct {
	Agent          *agent.Agent
	SessionID      string
	ModelName      string
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
	running    bool
	streaming  string // current streaming text accumulator
	thinking   string // current thinking text accumulator
	activeTool string
	toolStart  time.Time
	agentCh    chan agentMsg // channel for receiving agent events

	// Markdown renderer.
	renderer *glamour.TermRenderer

	// Trace log (for status bar counter).
	traceLog []traceEntry

	// Commit flow state.
	commit *commitState

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
		glamour.WithStandardStyle("dark"),
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
	return err
}

func (m *model) Init() tea.Cmd {
	if m.cfg.RestartCh != nil {
		return waitForRestart(m.cfg.RestartCh)
	}
	return nil
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
		m.messages = append(m.messages, message{
			role: "tool", tool: msg.name, toolIn: toolIn,
		})

		return m, waitForAgent(m.agentCh)

	case agentToolResultMsg:
		m.activeTool = ""
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

	case agentDoneMsg:
		m.running = false
		m.activeTool = ""
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

	case commitGeneratedMsg:
		return m.handleCommitGenerated(msg)

	case commitDoneMsg:
		return m.handleCommitDone(msg)
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
			role: "assistant",
			content: "**Available commands:**\n" +
				"- `/help` — Show this help\n" +
				"- `/clear` — Clear conversation\n" +
				"- `/model` — Show current model and configured roles\n" +
				"- `/session` — Show current session info\n" +
				"- `/branch <name>` — Create a new branch\n" +
				"- `/branch switch <name>` — Switch to a branch\n" +
				"- `/branch list` — List all branches\n" +
				"- `/agents` — Show running and recent subagents\n" +
				"- `/commit` — Generate and create a conventional commit from staged changes\n" +
				"- `/plan <idea>` — Start a PDD planning session to design and spec a feature\n" +
				"- `/run <spec-name>` — Execute a spec's PROMPT.md using an isolated task agent\n" +
				"- `/skill-create <name> [desc]` — Create a new skill\n" +
				"- `/skill-list` — List loaded skills\n" +
				"- `/skill-load` — Reload skills from disk\n" +
				"- `/restart` — Restart pi process\n" +
				"- `/compact` — Compact session context\n" +
				"- `/history [query]` — Show command history (optionally filter by query)\n" +
				"- `/exit`, `/quit` — Exit\n\n" +
				"**Keyboard shortcuts:**\n" +
				"- `Enter` — Submit prompt\n" +
				"- `Ctrl+C` / `Esc` — Cancel/quit\n" +
				"- `Up/Down` — Command history\n" +
				"- `PgUp/PgDown` — Scroll messages\n" +
				"",
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
	case "/skill-create":
		return m.handleSkillCreateCommand(parts[1:])
	case "/skill-load":
		return m.handleSkillLoadCommand()
	case "/skill-list":
		return m.handleSkillListCommand()
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

	var b strings.Builder
	b.WriteString("**Subagents:**\n\n")
	for _, a := range agents {
		status := a.Status
		prompt := a.Prompt
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		dur := a.Duration
		if dur == "" {
			dur = time.Since(a.StartedAt).Truncate(time.Millisecond).String()
		}
		fmt.Fprintf(&b, "- `%s` [%s] %s — %s (%s)\n", a.AgentID, a.Type, status, prompt, dur)
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

// runAgentLoop runs the agent and sends events to the channel.
func (m *model) runAgentLoop(prompt string) {
	defer close(m.agentCh)
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
	// grep tool: show match count
	if matches, ok := data["total_matches"].(float64); ok {
		return fmt.Sprintf("%d matches", int(matches))
	}
	// find tool: show file count
	if total, ok := data["total_files"].(float64); ok {
		return fmt.Sprintf("%d files", int(total))
	}
	// read tool: show line count
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
	"/branch",
	"/compact",
	"/agents",
	"/history",
	"/commit",
	"/plan",
	"/run",
	"/skill-create",
	"/skill-list",
	"/skill-load",
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
	b.WriteString("**Available commands:**\n")
	for _, cmd := range m.allCommandNames() {
		b.WriteString("  `" + cmd + "`")
		desc := slashCommandDesc(cmd)
		if desc == "" {
			// Check skills for description
			for _, skill := range m.cfg.Skills {
				if "/"+skill.Name == cmd {
					desc = skill.Description
					break
				}
			}
		}
		if desc != "" {
			b.WriteString(" — " + desc)
		}
		b.WriteString("\n")
	}
	m.messages = append(m.messages, message{
		role:    "assistant",
		content: b.String(),
	})
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
	case "/branch":
		return "Manage branches"
	case "/compact":
		return "Compact context"
	case "/agents":
		return "Show subagents"
	case "/history":
		return "Command history"
	case "/commit":
		return "Create commit from staged changes"
	case "/plan":
		return "Start PDD planning session"
	case "/run":
		return "Execute a spec with task agent"
	case "/skill-create":
		return "Create a new skill"
	case "/skill-list":
		return "List loaded skills"
	case "/skill-load":
		return "Reload skills from disk"
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
		glamour.WithStandardStyle("dark"),
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
			b.WriteString("\n")
			b.WriteString(toolBullet)
			b.WriteString(toolStyle.Render(msg.tool))
			b.WriteString(dim.Render("("))
			b.WriteString(dim.Render(msg.toolIn))
			b.WriteString(dim.Render(")"))
			b.WriteString("\n")
			if msg.content != "" {
				b.WriteString("  ")
				b.WriteString(dim.Render("└ "))
				b.WriteString(dim.Render(msg.content))
				b.WriteString("\n")
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

func (m *model) renderStatusBar() string {
	bg := lipgloss.Color("236")
	fg := lipgloss.Color("252")
	dimFg := lipgloss.Color("243")

	bright := lipgloss.NewStyle().Background(bg).Foreground(fg)
	dim := lipgloss.NewStyle().Background(bg).Foreground(dimFg)
	bar := lipgloss.NewStyle().Background(bg).Width(m.width)

	sep := dim.Render("  |  ")

	var parts []string

	// Model name.
	parts = append(parts, bright.Render(fmt.Sprintf(" %s", m.cfg.ModelName)))

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

	// Git branch.
	if m.gitBranch != "" {
		parts = append(parts, bright.Render(fmt.Sprintf("\u2387 %s", m.gitBranch)))
	}

	// Active tool or thinking status.
	if m.activeTool != "" {
		elapsed := time.Since(m.toolStart).Truncate(time.Millisecond)
		parts = append(parts, bright.Render(fmt.Sprintf("tool: %s (%s)", m.activeTool, elapsed)))
	} else if m.running {
		parts = append(parts, dim.Render("thinking..."))
	}

	// Trace count.
	if len(m.traceLog) > 0 {
		parts = append(parts, dim.Render(fmt.Sprintf("trace: %d", len(m.traceLog))))
	}

	return bar.Render(strings.Join(parts, sep))
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
