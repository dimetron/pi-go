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

	"charm.land/lipgloss/v2"
)

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
	return b.String()
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
