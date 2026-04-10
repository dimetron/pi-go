package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/glamour"

	"charm.land/lipgloss/v2"
)

// wordWrap wraps text to fit within maxWidth columns.
func wordWrap(text string, maxWidth int) []string {
	if maxWidth < 10 || text == "" {
		return []string{text}
	}
	var lines []string
	var current strings.Builder
	for _, r := range text {
		if r == '\n' {
			lines = append(lines, current.String())
			current.Reset()
			continue
		}
		if current.Len() >= maxWidth {
			// Break at word boundary.
			s := current.String()
			lastSpace := strings.LastIndexByte(s, ' ')
			if lastSpace > 0 {
				lines = append(lines, strings.TrimRightFunc(s[:lastSpace], unicode.IsSpace))
				remaining := strings.TrimLeftFunc(s[lastSpace+1:], unicode.IsSpace)
				current.Reset()
				current.WriteString(remaining)
				if r != ' ' {
					current.WriteRune(r)
				}
			} else {
				lines = append(lines, s)
				current.Reset()
				current.WriteRune(r)
			}
		} else {
			current.WriteRune(r)
		}
	}
	if current.Len() > 0 {
		lines = append(lines, current.String())
	}
	return lines
}

// renderWelcome builds the startup welcome screen, constrained to available width.
func (c *ChatModel) renderWelcome() string {
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
			cmd.Render("/subagents") + dim.Render(" ") +
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
	// Render cache: stores pre-rendered output to avoid repeated glamour calls.
	renderCache      string // cached rendered output
	renderCacheWidth int    // terminal width when cached
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
	c.ToolDisplay.Width = width
	if width < 40 {
		width = 40
	}
	c.Renderer, _ = glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
		glamour.WithEmoji(),
	)
	// Invalidate render caches — width changed so all cached output is stale.
	c.invalidateRenderCaches()
}

// invalidateRenderCaches clears cached rendered output for all messages.
func (c *ChatModel) invalidateRenderCaches() {
	for i := range c.Messages {
		c.Messages[i].renderCache = ""
		c.Messages[i].renderCacheWidth = 0
	}
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
		return c.renderWelcome()
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true).Render("● ")
	sepWidth := c.Width
	if sepWidth < 20 {
		sepWidth = 20
	}
	separator := dim.Render(strings.Repeat("─", sepWidth))

	var b strings.Builder
	lastIdx := len(c.Messages) - 1
	for i := range c.Messages {
		msg := &c.Messages[i]

		// Use cached render if available and width hasn't changed.
		// Skip cache for the last message while streaming — it's still being updated.
		isLastAndStreaming := running && i == lastIdx
		if !isLastAndStreaming && msg.renderCache != "" && msg.renderCacheWidth == c.Width {
			b.WriteString(msg.renderCache)
			continue
		}

		var msgBuf strings.Builder
		switch msg.role {
		case "user":
			if i > 0 {
				msgBuf.WriteString(separator)
				msgBuf.WriteString("\n")
			}
			label := lipgloss.NewStyle().
				Foreground(lipgloss.Color("39")).
				Bold(true).
				Render("> ")
			msgBuf.WriteString(label)
			contentWidth := c.Width - 3 // "> " = 3 visible chars
			if contentWidth < 20 {
				contentWidth = 20
			}
			wrapped := wordWrap(msg.content, contentWidth)
			for j, line := range wrapped {
				if j > 0 {
					msgBuf.WriteString("\n   ")
				}
				msgBuf.WriteString(line)
			}
			msgBuf.WriteString("\n")

		case "tool":
			msgBuf.WriteString("\n")
			msgBuf.WriteString(c.ToolDisplay.RenderToolMessage(*msg))

		case "thinking":
			if msg.content != "" {
				thinkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Italic(true)
				thinkBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("💭 ")
				msgBuf.WriteString("\n")
				msgBuf.WriteString(thinkBullet)
				// Show last few lines of thinking to keep it compact.
				lines := strings.Split(msg.content, "\n")
				maxLines := 6
				if len(lines) > maxLines {
					lines = lines[len(lines)-maxLines:]
				}
				// Content width accounting for the bullet prefix.
				contentWidth := c.Width - 3 // "💭 " = 3 visible chars
				if contentWidth < 20 {
					contentWidth = 20
				}
				for j, line := range lines {
					if j > 0 {
						msgBuf.WriteString("   ")
					}
					// Wrap each line to fit available width.
					wrapped := wordWrap(line, contentWidth)
					for k, wl := range wrapped {
						if k > 0 {
							msgBuf.WriteString("\n")
						}
						msgBuf.WriteString(thinkStyle.Render(wl))
					}
					if j < len(lines)-1 {
						msgBuf.WriteString("\n")
					}
				}
				msgBuf.WriteString("\n")
			}

		case "assistant":
			content := msg.content
			if content == "" && isLastAndStreaming {
				content = "..."
			}
			if content != "" {
				msgBuf.WriteString("\n")
				if msg.isWarning {
					warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true)
					warnBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("226")).Bold(true).Render("⚠ ")
					msgBuf.WriteString(warnBullet)
					msgBuf.WriteString(warnStyle.Render(content))
				} else {
					msgBuf.WriteString(bullet)
					rendered := c.RenderMarkdown(content)
					msgBuf.WriteString(rendered)
				}
				msgBuf.WriteString("\n")
			}
		}

		rendered := msgBuf.String()
		b.WriteString(rendered)

		// Cache the rendered output for non-streaming messages.
		if !isLastAndStreaming {
			msg.renderCache = rendered
			msg.renderCacheWidth = c.Width
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
