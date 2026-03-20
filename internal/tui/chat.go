package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"

	"charm.land/lipgloss/v2"
)

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

// traceEntry represents a single entry in the debug trace log.
type traceEntry struct {
	time    time.Time
	kind    string // "llm", "tool_call", "tool_result", "error"
	summary string // short one-line summary
	detail  string // full content (args, response, etc.)
}

// ChatModel manages the conversation message display, scrolling, and markdown rendering.
type ChatModel struct {
	Messages  []message
	Scroll    int // scroll offset from bottom
	Streaming string
	Thinking  string
	Renderer  *glamour.TermRenderer
	TraceLog  []traceEntry
	Width     int
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
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		return dim.Render("  Welcome to pi-go! Type a prompt, /command, or press Tab to cycle commands.")
	}

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	bullet := lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true).Render("● ")
	toolBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("35")).Bold(true).Render("● ")
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
			if content == "" && running && i == len(c.Messages)-1 {
				content = "..."
			}
			if content != "" {
				b.WriteString("\n")
				b.WriteString(bullet)
				rendered := c.RenderMarkdown(content)
				b.WriteString(rendered)
				b.WriteString("\n")
			}
		}
	}

	return b.String()
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
