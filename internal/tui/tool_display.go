package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/muesli/reflow/wrap"

	"charm.land/lipgloss/v2"
)

// acpBundledAgents lists agent names backed by ACP subprocess adapters; the
// rest are regular pi-based subagents and render under the "pi" label.
var acpBundledAgents = map[string]struct{}{
	"claude": {},
	"gemini": {},
	"cursor": {},
}

// agentToolColor returns the foreground color used for tool/command lines
// emitted by the named ACP-backed subagent. Each ACP agent gets its own hue
// so parallel runs are easy to tell apart at a glance:
//
//	claude → orange (208)
//	cursor → gray   (245)
//	gemini → blue   (39)
//
// Compound types like "claude+gemini" use the color of the first ACP
// component found. Anything else returns the default tool color (35).
func agentToolColor(agentType string) string {
	for _, p := range strings.Split(agentType, "+") {
		switch strings.TrimSpace(p) {
		case "claude":
			return "208"
		case "cursor":
			return "245"
		case "gemini":
			return "39"
		}
	}
	return "35"
}

// agentBracketLabel returns the string rendered inside "agent[...]" for a
// given subagent type. ACP-backed agents (claude, gemini) keep their name;
// all other pi-based subagents collapse to "pi". Parallel/chain calls encode
// multiple agents as "claude+gemini" — each component is mapped individually
// and duplicates are deduped, so [claude+explore+task] becomes [claude+pi].
// An empty agentType yields an empty string so the caller can omit the
// bracket entirely.
func agentBracketLabel(agentType string) string {
	if agentType == "" {
		return ""
	}
	parts := strings.Split(agentType, "+")
	seen := make(map[string]struct{}, len(parts))
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var label string
		if _, ok := acpBundledAgents[p]; ok {
			label = p
		} else {
			label = "pi"
		}
		if _, dup := seen[label]; dup {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return strings.Join(out, "+")
}

// ToolDisplayModel manages the formatting and rendering of tool call/result
// messages in the chat view. It owns per-tool formatters, syntax highlighting,
// and summary generation.
type ToolDisplayModel struct {
	// Width is the terminal width for rendering.
	Width int
	// CompactTools when true shows one-line summaries instead of full output.
	CompactTools bool
}

// RenderToolMessage renders a tool message (role=="tool") into a styled string.
// It handles both agent/subagent tools (with event streams) and regular tools
// (with syntax-highlighted output). When CompactTools is true, renders a
// one-line summary instead of full output.
func (t *ToolDisplayModel) RenderToolMessage(msg message) string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	if t.CompactTools {
		return t.renderCompactTool(msg, dim)
	}
	if msg.tool == "agent" || msg.tool == "subagent" {
		return t.renderAgentTool(msg, dim)
	}
	return t.renderRegularTool(msg, dim)
}

// renderCompactTool renders a one-line tally for a tool message.
func (t *ToolDisplayModel) renderCompactTool(msg message, dim lipgloss.Style) string {
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("35")).Bold(true)
	checkStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("35"))
	toolBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("35")).Bold(true).Render("● ")

	var b strings.Builder
	b.WriteString(toolBullet)
	b.WriteString(toolStyle.Render(msg.tool))

	if msg.toolIn != "" {
		args := msg.toolIn
		if len(args) > 60 {
			args = args[:57] + "..."
		}
		b.WriteString(dim.Render("("))
		b.WriteString(dim.Render(args))
		b.WriteString(dim.Render(")"))
	}

	if msg.content != "" {
		summary := toolResultSummary(msg.content)
		if len(summary) > 60 {
			summary = summary[:57] + "..."
		}
		// Show only the first line of the summary.
		if idx := strings.IndexByte(summary, '\n'); idx >= 0 {
			summary = summary[:idx]
		}
		b.WriteString(" ")
		b.WriteString(checkStyle.Render("✓ "))
		b.WriteString(dim.Render(summary))
	}

	b.WriteString("\n")
	return b.String()
}

// renderAgentTool renders an agent/subagent tool message with type, title,
// event stream, and result summary.
func (t *ToolDisplayModel) renderAgentTool(msg message, dim lipgloss.Style) string {
	agentBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true).Render("● ")
	typeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("213")).Bold(true)
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))

	var b strings.Builder
	b.WriteString(agentBullet)
	b.WriteString(typeStyle.Render("agent"))
	if label := agentBracketLabel(msg.agentType); label != "" {
		b.WriteString(typeStyle.Render("[" + label + "]"))
	}
	if msg.agentTitle != "" {
		b.WriteString(" ")
		b.WriteString(titleStyle.Render(msg.agentTitle))
	}
	b.WriteString("\n")

	cw := t.contentWidth()

	// Show event stream. Structural events (message_start/end/done/spawn)
	// are filtered out first so they never crowd the visible window; from the
	// renderable remainder, keep the newest maxVisibleAgentEvents so the user
	// always sees the latest activity — not a stream truncated into silence.
	if len(msg.agentEvents) > 0 {
		evStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
		evToolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(agentToolColor(msg.agentType)))

		renderable := make([]agentEv, 0, len(msg.agentEvents))
		for _, ev := range msg.agentEvents {
			switch ev.kind {
			case "message_start", "message_end", "done", "spawn":
				continue
			case "text", "text_delta":
				if strings.TrimSpace(ev.content) == "" {
					continue
				}
			}
			renderable = append(renderable, ev)
		}

		const maxVisibleAgentEvents = 5
		events := renderable
		if len(events) > maxVisibleAgentEvents {
			skipped := len(events) - maxVisibleAgentEvents
			events = events[len(events)-maxVisibleAgentEvents:]
			b.WriteString("  ")
			b.WriteString(dim.Render("│ "))
			b.WriteString(dim.Render(fmt.Sprintf("... %d earlier events", skipped)))
			b.WriteString("\n")
		}
		for _, ev := range events {
			var evLine string
			switch ev.kind {
			case "tool_call":
				// Collapse embedded newlines so tool-call headers occupy one
				// visual row — otherwise markdown prose inside a tool title
				// (e.g. Gemini's "**Identifying...**\n\n\n...") drops blank
				// rows into the card gutter.
				evLine = evToolStyle.Render("⚙ " + collapseToSingleLine(ev.content))
			case "tool_result":
				summary := collapseToSingleLine(ev.content)
				if len(summary) > 80 {
					summary = summary[:77] + "..."
				}
				evLine = evStyle.Render("  ✓ " + summary)
			case "stderr":
				// Subprocess stderr — diagnostic chatter. Color it with the
				// per-agent hue (orange/gray/blue) so users can tell at a
				// glance which subagent is writing what when several run in
				// parallel. The thin "▎" marker still distinguishes stderr
				// from real tool calls, which use the "⚙" prefix.
				summary := collapseToSingleLine(ev.content)
				if len(summary) > 120 {
					summary = summary[:117] + "..."
				}
				evLine = evToolStyle.Render("▎ " + summary)
			case "text", "text_delta":
				// Subagent message text — what the agent actually said.
				// Collapse internal blank-line runs so paragraph spacing
				// from streamed chunks doesn't produce wide gaps in the card.
				evLine = evStyle.Render("» " + collapseToSingleLine(ev.content))
			default:
				content := collapseToSingleLine(ev.content)
				if content == "" {
					evLine = evStyle.Render(ev.kind)
				} else {
					evLine = evStyle.Render(ev.kind + ": " + content)
				}
			}
			for _, sl := range softWrap(evLine, cw) {
				b.WriteString("  ")
				b.WriteString(dim.Render("│ "))
				b.WriteString(sl)
				b.WriteString("\n")
			}
		}
	}

	// Show result summary when done. Collapse newlines so multiline JSON
	// results render as a single wrapped line under the "│ " gutter.
	if msg.content != "" {
		summary := collapseToSingleLine(msg.content)
		if len(summary) > 160 {
			summary = summary[:157] + "..."
		}
		for _, sl := range softWrap(dim.Render("→ "+summary), cw) {
			b.WriteString("  ")
			b.WriteString(dim.Render("│ "))
			b.WriteString(sl)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// contentWidth returns the available width for tool/subagent output content.
// Uses 80% of terminal width minus the "  │ " prefix (4 visible chars).
func (t *ToolDisplayModel) contentWidth() int {
	w := t.Width
	if w < 40 {
		w = 80 // sensible default when width unknown
	}
	return w*8/10 - 4
}

// collapseToSingleLine replaces newlines and tabs with spaces and collapses
// runs of whitespace, so long multi-line content renders on a single wrapped
// line under the agent tool's "│ " gutter rather than drifting to column 0.
func collapseToSingleLine(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.Join(strings.Fields(s), " ")
}

// softWrap wraps a string to fit within width, returning sub-lines.
// It is ANSI-aware so it handles syntax-highlighted text correctly.
func softWrap(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	wrapped := wrap.String(s, width)
	return strings.Split(wrapped, "\n")
}

// renderRegularTool renders a standard tool message with name, args, and
// syntax-highlighted output.
func (t *ToolDisplayModel) renderRegularTool(msg message, dim lipgloss.Style) string {
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("35")).Bold(true)
	argStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	toolBullet := lipgloss.NewStyle().Foreground(lipgloss.Color("35")).Bold(true).Render("● ")

	var b strings.Builder
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
		cw := t.contentWidth()
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
				for _, sl := range softWrap(line, cw) {
					b.WriteString("  ")
					b.WriteString(dim.Render("│ "))
					b.WriteString(sl)
					b.WriteString("\n")
				}
			}
		} else {
			for _, line := range lines {
				for _, sl := range softWrap(line, cw) {
					b.WriteString("  ")
					b.WriteString(dim.Render("│ "))
					b.WriteString(dim.Render(sl))
					b.WriteString("\n")
				}
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
	// lsp_diagnostics: show diagnostics (already prefixed with ⚠ by formatDiagnosticsForDisplay)
	if diag, ok := data["lsp_diagnostics"].(string); ok && diag != "" {
		return diag
	}
	// bash tool: show exit code + first 2 and last 2 output lines (preserve newlines for better visibility)
	if code, ok := data["exit_code"].(float64); ok {
		stdout, _ := data["stdout"].(string)
		stderr, _ := data["stderr"].(string)

		// For bash streaming display, show first 2 and last 2 lines of stdout (or stderr if stdout is empty).
		var preview string
		output := stdout
		if output == "" {
			output = stderr
		}
		if output != "" {
			lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
			if len(lines) > 4 {
				preview = strings.Join([]string{lines[0], lines[1], lines[len(lines)-2], lines[len(lines)-1]}, "\n")
			} else {
				preview = strings.Join(lines, "\n")
			}
		} else {
			preview = "(No output)"
		}

		// Truncate each line to 80 chars for display
		var truncated []string
		for _, line := range strings.Split(preview, "\n") {
			if len(line) > 80 {
				line = line[:77] + "..."
			}
			truncated = append(truncated, line)
		}
		result := strings.Join(truncated, "\n")

		if int(code) != 0 {
			return fmt.Sprintf("exit %d: %s", int(code), result)
		}
		return result
	}
	// Fallback: compact JSON
	b, _ := json.Marshal(data)
	s := string(b)
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
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
		lexer = lexers.Analyse(code) //nolint:misspell // chroma API uses British spelling
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
