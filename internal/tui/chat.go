package tui

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/x/ansi"

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
		"" +
			"  ╱╲___╱╲\n" +
			"  ( ◕ ◕ )\n" +
			"   ╱ π ╲")

	lines := []string{
		face,
		"",
		accent.Render("  Welcome to pi-go.sh") + dim.Render(" — your AI coding agent"),
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
	// Render cache: stores pre-rendered output to avoid repeated glamour and
	// chroma work. renderCacheKey fingerprints every input the render depends
	// on, so a message that changes re-renders itself — no caller has to
	// remember to invalidate. See renderKey.
	renderCache    string // cached rendered output
	renderCacheKey uint64 // fingerprint of the inputs that produced renderCache
	renderCached   bool   // whether renderCache holds a valid render
}

// FNV-1a, inlined so fingerprinting a message allocates nothing. hash/fnv would
// return an interface and heap-allocate on every message every frame, which is
// exactly the garbage this cache exists to avoid.
const (
	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211
)

func fnvByte(h uint64, b byte) uint64 {
	h ^= uint64(b)
	return h * fnvPrime64
}

// fnvStr folds s into h, terminated so that ("ab","c") and ("a","bc") differ.
func fnvStr(h uint64, s string) uint64 {
	for i := range len(s) {
		h = fnvByte(h, s[i])
	}
	return fnvByte(h, 0)
}

func fnvInt(h uint64, v int) uint64 {
	u := uint64(v)
	for i := range 8 {
		h = fnvByte(h, byte(u>>(8*i)))
	}
	return h
}

// renderKey fingerprints every input RenderMessages reads to produce this
// message's output: the message's own fields, the terminal width, the tool
// display mode, and the two positional flags that change what is drawn.
//
// This is what makes the cache safe to use mid-turn. The cache used to be
// disabled whenever the agent was running, on the grounds that an earlier
// message could still be mutated — with the result that every frame re-rendered
// (and re-syntax-highlighted) the entire scrollback, several times a second.
// Keying on the inputs gets the same safety without the cost: a mutated message
// simply gets a different key and re-renders.
func (m *message) renderKey(width int, compactTools, hasSeparator, streamingPlaceholder bool) uint64 {
	h := fnvOffset64
	h = fnvStr(h, m.role)
	h = fnvStr(h, m.content)
	h = fnvStr(h, m.tool)
	h = fnvStr(h, m.toolIn)
	h = fnvStr(h, m.agentID)
	h = fnvStr(h, m.agentType)
	h = fnvStr(h, m.agentTitle)
	h = fnvStr(h, m.pipelineID)
	h = fnvStr(h, m.pipelineMode)
	for _, ev := range m.agentEvents {
		h = fnvStr(h, ev.kind)
		h = fnvStr(h, ev.content)
	}
	h = fnvInt(h, width)
	h = fnvInt(h, m.pipelineStep)
	h = fnvInt(h, m.pipelineTotal)

	var flags byte
	if m.isWarning {
		flags |= 1 << 0
	}
	if compactTools {
		flags |= 1 << 1
	}
	if hasSeparator {
		flags |= 1 << 2
	}
	if streamingPlaceholder {
		flags |= 1 << 3
	}
	return fnvByte(h, flags)
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
//
// Width and tool-display changes are already covered by renderKey, so this is
// belt-and-braces for anything that changes rendering without touching a keyed
// input — notably the glamour renderer being rebuilt.
func (c *ChatModel) invalidateRenderCaches() {
	for i := range c.Messages {
		c.Messages[i].renderCache = ""
		c.Messages[i].renderCacheKey = 0
		c.Messages[i].renderCached = false
	}
}

// markdownLinkRe matches markdown links with both text and URL.
// Pattern: [text](url) where url starts with file://, http://, or https://
var markdownLinkRe = regexp.MustCompile(`\[([^\]]+)\]\((file://[^)]+|http://[^)]+|https://[^)]+)\)`)

// expandLinks expands markdown links into inline format when the link text differs from the URL.
// For example: [Link Text](file:///path) becomes "Link Text (/path)"
// But: [https://example.com](https://example.com) stays as-is since text equals URL.
func expandLinks(text string) string {
	return markdownLinkRe.ReplaceAllStringFunc(text, func(match string) string {
		// Extract text and URL from the match.
		parts := markdownLinkRe.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match // safety check, should not happen
		}
		textPart := parts[1]
		urlPart := parts[2]

		// Skip if text equals URL (already expanded/stylized by glamour).
		if textPart == urlPart {
			return match
		}

		// Return inline format: "text (url)" - keep the full URL including file://
		return textPart + " (" + urlPart + ")"
	})
}

// RenderMarkdown renders text as markdown using the glamour renderer.
// It first expands markdown links (like [text](url)) into inline format
// when the link has both a display name and an actual file:// or http:// URL.
func (c *ChatModel) RenderMarkdown(text string) string {
	if text == "" {
		return ""
	}
	// Expand markdown links into inline format for better visibility in terminal.
	text = expandLinks(text)
	if c.Renderer == nil {
		return text
	}
	rendered, err := c.Renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimRight(rendered, "\n")
}

// PlainTranscript returns the conversation as copy-friendly plain text.
func (c *ChatModel) PlainTranscript() string {
	if len(c.Messages) == 0 {
		return ""
	}

	var b strings.Builder
	for _, msg := range c.Messages {
		content := strings.TrimSpace(msg.content)
		if content == "" {
			continue
		}

		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(transcriptLabel(msg))
		b.WriteString("\n")
		b.WriteString(content)
	}
	return b.String()
}

func transcriptLabel(msg message) string {
	switch msg.role {
	case "user":
		return "User:"
	case "assistant":
		return "Assistant:"
	case "thinking":
		return "Thinking:"
	case "tool":
		if msg.tool != "" {
			return "Tool " + msg.tool + ":"
		}
		return "Tool:"
	default:
		if msg.role == "" {
			return "Message:"
		}
		return msg.role + ":"
	}
}

// RenderMessages renders all messages into a string for display.
func (c *ChatModel) RenderMessages(running bool) string {
	text, _ := c.renderMessages(running)
	return text
}

// renderMessages renders the chat and classifies every line it produces, which
// is what the minimap colors its bars from.
//
// The kinds slice is built in lockstep with the text: each message claims the
// lines it opened, and collapseBlankLines then drops entries from both together,
// so kinds[i] always describes the i-th line of the returned string.
func (c *ChatModel) renderMessages(running bool) (string, []blockKind) {
	if len(c.Messages) == 0 {
		welcome := c.renderWelcome()
		return welcome, make([]blockKind, strings.Count(welcome, "\n")+1)
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true).Render("● ")
	sepWidth := c.Width
	if sepWidth < 20 {
		sepWidth = 20
	}
	separator := dim.Render(strings.Repeat("─", sepWidth))

	var b strings.Builder
	var kinds []blockKind
	lastIdx := len(c.Messages) - 1
	for i := range c.Messages {
		msg := &c.Messages[i]

		isLastAndStreaming := running && i == lastIdx
		key := msg.renderKey(c.Width, c.ToolDisplay.CompactTools, i > 0, isLastAndStreaming)
		if msg.renderCached && msg.renderCacheKey == key {
			b.WriteString(msg.renderCache)
			kinds = appendKind(kinds, kindOf(msg), strings.Count(msg.renderCache, "\n"))
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
		kinds = appendKind(kinds, kindOf(msg), strings.Count(rendered, "\n"))

		// Cache unconditionally: correctness comes from the key, not from
		// guessing which messages are still in flux. The streaming message
		// re-keys on every new token and so re-renders anyway.
		msg.renderCache = rendered
		msg.renderCacheKey = key
		msg.renderCached = true
	}

	// Subagent blocks, assistant markdown, and tool transitions each emit
	// their own surrounding "\n", and they compound to runs of 2-3 blank
	// lines between blocks — visually noisy when several subagents run in
	// parallel. Collapse to at most one blank line between content.
	return collapseBlankLines(b.String(), kinds)
}

// appendKind records that a message opened n lines of output.
func appendKind(kinds []blockKind, kind blockKind, n int) []blockKind {
	for range n {
		kinds = append(kinds, kind)
	}
	return kinds
}

// collapseBlankLines replaces every run of two or more blank/whitespace-only
// lines with a single empty line, preserving all non-blank content verbatim
// (including its leading whitespace, gutter prefixes, and ANSI styling). The
// final trailing newline state is also preserved.
//
// kinds is filtered alongside the lines so the two stay index-aligned: a line
// that survives keeps its kind, and a dropped blank line drops its kind too.
// A nil or short kinds is padded, so callers that only want the text can pass
// nil and ignore the second result.
func collapseBlankLines(s string, kinds []blockKind) (string, []blockKind) {
	if s == "" {
		return s, nil
	}
	lines := strings.Split(s, "\n")

	// Each message contributes as many kinds as it opened lines; the final line
	// after the last newline belongs to no message, hence the padding.
	for len(kinds) < len(lines) {
		kinds = append(kinds, blockNone)
	}

	out := make([]string, 0, len(lines))
	outKinds := make([]blockKind, 0, len(lines))
	prevBlank := false
	for i, line := range lines {
		if strings.TrimSpace(ansi.Strip(line)) == "" {
			if prevBlank {
				continue
			}
			prevBlank = true
			out = append(out, "")
			outKinds = append(outKinds, blockNone)
			continue
		}
		prevBlank = false
		out = append(out, line)
		outKinds = append(outKinds, kinds[i])
	}
	return strings.Join(out, "\n"), outKinds
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
