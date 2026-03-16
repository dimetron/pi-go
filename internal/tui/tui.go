// Package tui implements the interactive terminal UI using Bubble Tea v2.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/glamour"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	pisession "github.com/dimetron/pi-go/internal/session"
	"github.com/dimetron/pi-go/internal/subagent"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// agentMsg wraps messages coming from the agent goroutine via a channel.
type agentMsg interface{ agentMsg() }

type agentTextMsg struct{ text string }
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

	// Messages.
	messages []message
	scroll   int // scroll offset from bottom

	// Agent state.
	running    bool
	streaming  string // current streaming text accumulator
	activeTool string
	toolStart  time.Time
	agentCh    chan agentMsg // channel for receiving agent events

	// Markdown renderer.
	renderer *glamour.TermRenderer

	// Trace log (for status bar counter).
	traceLog []traceEntry

	// Commit flow state.
	commit *commitState

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

	m := model{
		cfg:        cfg,
		ctx:        ctx,
		cancel:     cancel,
		history:    make([]string, 0),
		historyIdx: -1,
		messages:   make([]message, 0),
		renderer:   renderer,
	}

	p := tea.NewProgram(&m, tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

func (m *model) Init() tea.Cmd {
	return nil
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateRenderer()
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case agentTextMsg:
		m.streaming += msg.text
		if len(m.messages) > 0 && m.messages[len(m.messages)-1].role == "assistant" {
			m.messages[len(m.messages)-1].content = m.streaming
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
		m.agentCh = nil
		return m, nil

	case commitGeneratedMsg:
		return m.handleCommitGenerated(msg)

	case commitDoneMsg:
		return m.handleCommitDone(msg)
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

	switch {
	case key.Code == tea.KeyEsc || (key.Code == 'c' && key.Mod == tea.ModCtrl):
		if m.running {
			m.cancel()
			m.running = false
			m.activeTool = ""
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
		return m.submit()

	case key.Code == tea.KeyBackspace:
		if m.cursorPos > 0 {
			m.input = m.input[:m.cursorPos-1] + m.input[m.cursorPos:]
			m.cursorPos--
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
		if len(m.history) > 0 {
			if m.historyIdx < 0 {
				m.historyIdx = len(m.history) - 1
			} else if m.historyIdx > 0 {
				m.historyIdx--
			}
			m.input = m.history[m.historyIdx]
			m.cursorPos = len(m.input)
		}

	case key.Code == tea.KeyDown:
		if m.historyIdx >= 0 {
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
			m.input = m.input[:m.cursorPos] + key.Text + m.input[m.cursorPos:]
			m.cursorPos += len(key.Text)
		}
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

	// Add to history.
	m.history = append(m.history, input)
	m.historyIdx = -1

	// Add user message.
	m.messages = append(m.messages, message{role: "user", content: input})

	// Add empty assistant message for streaming.
	m.messages = append(m.messages, message{role: "assistant", content: ""})
	m.streaming = ""

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
				"- `/compact` — Compact session context\n" +
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
	case "/commit":
		return m.handleCommitCommand()
	case "/exit", "/quit":
		m.quitting = true
		m.input = ""
		m.cursorPos = 0
		return m, tea.Quit
	default:
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

	for ev, err := range m.cfg.Agent.Run(m.ctx, m.cfg.SessionID, prompt) {
		if err != nil {
			m.agentCh <- agentDoneMsg{err: err}
			return
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" {
				m.agentCh <- agentTextMsg{text: part.Text}
			}
			if part.FunctionCall != nil {
				m.agentCh <- agentToolCallMsg{
					name: part.FunctionCall.Name,
					args: part.FunctionCall.Args,
				}
			}
			if part.FunctionResponse != nil {
				respJSON, _ := json.Marshal(part.FunctionResponse.Response)
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
		return dim.Render("  Welcome to pi-go! Type a prompt or /help for commands.")
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
	style := lipgloss.NewStyle().
		Background(lipgloss.Color("236")).
		Foreground(lipgloss.Color("252")).
		Width(m.width)

	var parts []string
	parts = append(parts, fmt.Sprintf(" model: %s", m.cfg.ModelName))

	if m.activeTool != "" {
		elapsed := time.Since(m.toolStart).Truncate(time.Millisecond)
		parts = append(parts, fmt.Sprintf("tool: %s (%s)", m.activeTool, elapsed))
	} else if m.running {
		parts = append(parts, "thinking...")
	}

	if len(m.traceLog) > 0 {
		parts = append(parts, fmt.Sprintf("trace: %d", len(m.traceLog)))
	}

	return style.Render(strings.Join(parts, "  |  "))
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

	return prefix + before + cursor + after
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
