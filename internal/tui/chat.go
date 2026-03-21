package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/glamour"

	"charm.land/lipgloss/v2"
)

// renderWelcome builds the startup welcome screen.
func renderWelcome() string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	cmd := lipgloss.NewStyle().Foreground(lipgloss.Color("75"))

	face := accent.Render(
		"  ┌─────┐\n" +
			"  │ ◕ ◕ │\n" +
			"  │  ▽  │\n" +
			"  └─────┘")

	lines := []string{
		face,
		"",
		accent.Render("  Welcome to pi-go") + dim.Render(" — your AI coding agent"),
		"",
		dim.Render("  Ask me anything or describe a task:"),
		dim.Render("    - ") + dim.Render(`"research this codebase and explain the architecture"`),
		dim.Render("    - ") + dim.Render(`"fix the failing test in auth_test.go"`),
		dim.Render("    - ") + dim.Render(`"add error handling to the upload endpoint"`),
		dim.Render("    - ") + dim.Render(`"explain how the session middleware works"`),
		dim.Render("    - ") + dim.Render(`"refactor this function to use channels"`),
		"",
		dim.Render("  Commands: ") +
			cmd.Render("/help") + dim.Render(" ") +
			cmd.Render("/commit") + dim.Render(" ") +
			cmd.Render("/plan") + dim.Render(" ") +
			cmd.Render("/run") + dim.Render(" ") +
			cmd.Render("/agents") + dim.Render(" ") +
			cmd.Render("/ping"),
		dim.Render("  Press ") + cmd.Render("Tab") + dim.Render(" to cycle commands, ") +
			cmd.Render("@") + dim.Render(" to mention files"),
	}
	return strings.Join(lines, "\n")
}

// message represents a chat message in the conversation.
type message struct {
	role      string // "user", "assistant", or "tool"
	content   string
	isWarning bool   // if true, render with warning style
	tool      string // tool name (for role=="tool")
	toolIn    string // tool input args (for role=="tool")
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

// traceEntry represents a single entry in the debug trace log.
type traceEntry struct {
	time    time.Time
	kind    string // "llm", "tool_call", "tool_result", "error"
	summary string // short one-line summary
	detail  string // full content (args, response, etc.)
}

// ChatModel manages the conversation message display, scrolling, and markdown rendering.
type ChatModel struct {
	Messages    []message
	Scroll      int // scroll offset from bottom
	Streaming   string
	Thinking    string
	Renderer    *glamour.TermRenderer
	TraceLog    []traceEntry
	Width       int
	ToolDisplay ToolDisplayModel
}

// NewChatModel creates a ChatModel with the given markdown renderer.
func NewChatModel(renderer *glamour.TermRenderer) ChatModel {
	return ChatModel{
		Messages: make([]message, 0),
		Renderer: renderer,
	}
}

// Clear removes all messages and resets scroll.
func (c *ChatModel) Clear() {
	c.Messages = c.Messages[:0]
	c.Scroll = 0
}

// AppendWarning adds a warning message styled with yellow text.
func (c *ChatModel) AppendWarning(text string) {
	c.Messages = append(c.Messages, message{
		role:      "assistant",
		content:   text,
		isWarning: true,
	})
	c.Scroll = 0
}

// ResetScroll resets the scroll offset to bottom.
func (c *ChatModel) ResetScroll() {
	c.Scroll = 0
}

// ScrollUp scrolls up by n lines, clamped to max.
func (c *ChatModel) ScrollUp(n, height int) {
	c.Scroll += n
	maxScroll := c.MaxScroll(height)
	if c.Scroll > maxScroll {
		c.Scroll = maxScroll
	}
}

// ScrollDown scrolls down by n lines, clamped to 0.
func (c *ChatModel) ScrollDown(n int) {
	c.Scroll -= n
	if c.Scroll < 0 {
		c.Scroll = 0
	}
}

// MaxScroll returns the maximum scroll offset for the given terminal height.
func (c *ChatModel) MaxScroll(height int) int {
	if len(c.Messages) == 0 {
		return 0
	}
	messagesView := c.RenderMessages(false)
	totalLines := strings.Count(messagesView, "\n") + 1

	availableHeight := height - 3
	if availableHeight < 1 {
		return 0
	}
	max := totalLines - availableHeight
	if max < 0 {
		return 0
	}
	return max
}

// UpdateRenderer recreates the glamour renderer for the given terminal width.
func (c *ChatModel) UpdateRenderer(width int) {
	c.Width = width
	contentWidth := width - 4
	if contentWidth < 40 {
		contentWidth = 40
	}
	c.Renderer, _ = glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(contentWidth),
		glamour.WithEmoji(),
	)
}

// RenderMarkdown renders text as markdown using the glamour renderer.
func (c *ChatModel) RenderMarkdown(text string) string {
	if text == "" {
		return ""
	}
	if c.Renderer == nil {
		return text
	}
	rendered, err := c.Renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(rendered, "\n")
}

// RenderMessages renders all messages into a string for display.
func (c *ChatModel) RenderMessages(running bool) string {
	if len(c.Messages) == 0 {
		return renderWelcome()
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true).Render("● ")
	sepWidth := c.Width - 4
	if sepWidth < 20 {
		sepWidth = 20
	}
	separator := dim.Render(strings.Repeat("─", sepWidth))

	var b strings.Builder
	for i, msg := range c.Messages {
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
			b.WriteString("\n")
			b.WriteString(c.ToolDisplay.RenderToolMessage(msg))

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
			if content == "" && running && i == len(c.Messages)-1 {
				content = "..."
			}
			if content != "" {
				b.WriteString("\n")
				if msg.isWarning {
					warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true)
					warnBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true).Render("⚠ ")
					b.WriteString(warnBullet)
					b.WriteString(warnStyle.Render(content))
				} else {
					b.WriteString(bullet)
					rendered := c.RenderMarkdown(content)
					b.WriteString(rendered)
				}
				b.WriteString("\n")
			}
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

// formatTokenCount formats a token count with K/M suffixes.
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
