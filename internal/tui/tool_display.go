package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"strings"
	"sync"

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
func agentToolColor(agentType string, pal Palette) color.Color {
	pal = paletteOrDark(pal)
	for _, p := range strings.Split(agentType, "+") {
		switch strings.TrimSpace(p) {
		case "claude":
			return pal.Peach
		case "cursor":
			return pal.Overlay
		case "gemini":
			return pal.Blue
		}
	}
	return pal.Tool
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
	// Palette is the resolved theme palette, set each frame by the model before
	// rendering. Zero means the dark default.
	Palette Palette
	// BlinkOn is the current phase of the pending-tool bullet blink, set each
	// frame by the model. A tool card with no result yet blinks its ◉ bullet on
	// and off so an active tool is visible at a glance; finished cards render
	// the bullet solid.
	BlinkOn bool
}

// toolBullet renders the header bullet for a tool card. While the card is still
// pending (no result yet) the bullet blinks on and off; once the result lands
// it renders solid. The off phase is a same-width blank so the header does not
// shift columns.
func (t *ToolDisplayModel) toolBullet(style lipgloss.Style, pending bool) string {
	if pending && !t.BlinkOn {
		return "  "
	}
	return style.Render("◉ ")
}

// RenderToolMessage renders a tool message (role=="tool") into a styled string.
// It handles both agent/subagent tools (with event streams) and regular tools
// (with syntax-highlighted output). When CompactTools is true, renders a
// one-line summary instead of full output.
func (t *ToolDisplayModel) RenderToolMessage(msg message) string {
	p := paletteOrDark(t.Palette)
	dim := lipgloss.NewStyle().Foreground(p.Dim)

	if t.CompactTools {
		return t.renderCompactTool(msg, dim, p)
	}
	if msg.tool == "agent" || msg.tool == "subagent" {
		return t.renderAgentTool(msg, dim, p)
	}
	return t.renderRegularTool(msg, dim, p)
}

// renderCompactTool renders a one-line tally for a tool message.
func (t *ToolDisplayModel) renderCompactTool(msg message, dim lipgloss.Style, p Palette) string {
	toolStyle := lipgloss.NewStyle().Foreground(p.Tool).Bold(true)
	checkStyle := lipgloss.NewStyle().Foreground(p.Tool)
	toolBullet := t.toolBullet(lipgloss.NewStyle().Foreground(p.Tool).Bold(true), msg.content == "")

	var b strings.Builder
	b.WriteString(toolBullet)
	b.WriteString(toolStyle.Render(msg.tool))

	if msg.toolIn != "" {
		args := msg.toolIn
		if max := t.argWidth(); len(args) > max {
			args = truncateRunes(args, max)
		}
		b.WriteString(dim.Render("("))
		b.WriteString(dim.Render(args))
		b.WriteString(dim.Render(")"))
	}

	if msg.content != "" {
		summary := toolResultSummary(msg.content)
		if max := t.argWidth(); len(summary) > max {
			summary = truncateRunes(summary, max)
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
func (t *ToolDisplayModel) renderAgentTool(msg message, dim lipgloss.Style, p Palette) string {
	agentBullet := t.toolBullet(lipgloss.NewStyle().Foreground(p.Mauve).Bold(true), msg.content == "")
	typeStyle := lipgloss.NewStyle().Foreground(p.Mauve).Bold(true)
	titleStyle := lipgloss.NewStyle().Foreground(p.Text)

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

	// Show event stream. Structural events (message_start/end/done/spawn) are
	// filtered out first so they never crowd the visible window; from the
	// renderable remainder, keep the newest maxAgentOutputLines so the user
	// always sees the latest activity — not a stream truncated into silence.
	if len(msg.agentEvents) > 0 {
		evStyle := lipgloss.NewStyle().Foreground(p.Dim)
		evToolStyle := lipgloss.NewStyle().Foreground(agentToolColor(msg.agentType, p))

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

		// Budget in rendered lines, not events. A single event carries an
		// unbounded amount of text — a subagent's final analysis is one "text"
		// event — so an event count caps nothing: five of them still soft-wrap
		// into a screenful. Walk newest-first and stop once the window is full.
		var lines []string
		used := 0
		for i := len(renderable) - 1; i >= 0 && len(lines) < maxAgentOutputLines; i-- {
			lines = append(agentEventLines(renderable[i], evStyle, evToolStyle, cw), lines...)
			used++
		}
		skipped := len(renderable) - used
		clipped := len(lines) > maxAgentOutputLines
		if clipped {
			// The oldest event still in the window overflows it; show its tail,
			// which is the part nearest the newer output below it.
			lines = lines[len(lines)-maxAgentOutputLines:]
		}

		// Say so whenever output was withheld. A single huge event is clipped
		// without any whole event being dropped, and hiding 60-odd lines with no
		// mark would read as if that were all the agent said.
		note := ""
		switch {
		case skipped > 0:
			note = fmt.Sprintf("... %d earlier events", skipped)
		case clipped:
			note = "... earlier output"
		}
		if note != "" {
			b.WriteString("  ")
			b.WriteString(dim.Render("│ "))
			b.WriteString(dim.Render(note))
			b.WriteString("\n")
		}
		for _, sl := range lines {
			b.WriteString("  ")
			b.WriteString(dim.Render("│ "))
			b.WriteString(sl)
			b.WriteString("\n")
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

// maxLiveOutputLines bounds the rolling window shown under a running command.
// It is smaller than the finished-output window on purpose: while a command
// runs, what matters is that it is alive and roughly where it has got to, not
// the content.
const maxLiveOutputLines = 5

// renderLiveOutput draws the tail of a running command's output, plus its
// current state.
//
// The state line is the point of the whole exercise. A command that prints
// nothing is indistinguishable from a hung one, and that ambiguity is what let
// two sessions sit wedged for an hour without anyone noticing. "1m30s — no
// output" removes it.
func (t *ToolDisplayModel) renderLiveOutput(msg message, dim lipgloss.Style, p Palette) string {
	cw := t.contentWidth()
	outStyle := lipgloss.NewStyle().Foreground(p.Dim)
	errStyle := lipgloss.NewStyle().Foreground(p.Error)
	stateStyle := lipgloss.NewStyle().Foreground(p.Warning)

	var (
		lines []string
		state string
	)
	for _, ev := range msg.agentEvents {
		switch ev.kind {
		case "heartbeat", "stall", "background":
			// Only the latest state matters; earlier ones are stale by
			// definition.
			state = ev.content
		case "stderr":
			lines = append(lines, errStyle.Render("▎ "+collapseToSingleLine(ev.content)))
		case "output":
			lines = append(lines, outStyle.Render("│ "+collapseToSingleLine(ev.content)))
		}
	}
	if len(lines) > maxLiveOutputLines {
		lines = lines[len(lines)-maxLiveOutputLines:]
	}

	var b strings.Builder
	for _, l := range lines {
		for _, sl := range softWrap(l, cw) {
			b.WriteString("  ")
			b.WriteString(dim.Render("│ "))
			b.WriteString(sl)
			b.WriteString("\n")
		}
	}
	if state != "" {
		b.WriteString("  ")
		b.WriteString(dim.Render("│ "))
		b.WriteString(stateStyle.Render("⏳ " + state))
		b.WriteString("\n")
	}
	return b.String()
}

// maxAgentOutputLines bounds a subagent card's live output window. The card is
// a progress indicator, not a transcript: the agent's full answer arrives in the
// result summary and in the parent's own reply, so the stream only has to show
// enough to see what the agent is doing right now.
const maxAgentOutputLines = 3

// agentEventLines renders one subagent event into the lines it occupies in the
// card, already styled and soft-wrapped to width.
func agentEventLines(ev agentEv, evStyle, evToolStyle lipgloss.Style, width int) []string {
	var line string
	switch ev.kind {
	case "tool_call":
		// Collapse embedded newlines so tool-call headers occupy one visual
		// row — otherwise markdown prose inside a tool title (e.g. Gemini's
		// "**Identifying...**\n\n\n...") drops blank rows into the card gutter.
		line = evToolStyle.Render("⚙ " + collapseToSingleLine(ev.content))
	case "tool_result":
		line = evStyle.Render("  ✓ " + truncateRunes(toolResultSummary(ev.content), 80))
	case "stderr":
		// Subprocess stderr — diagnostic chatter. Color it with the per-agent
		// hue (orange/gray/blue) so users can tell at a glance which subagent
		// is writing what when several run in parallel. The thin "▎" marker
		// still distinguishes stderr from real tool calls, which use "⚙".
		line = evToolStyle.Render("▎ " + truncateRunes(collapseToSingleLine(ev.content), 120))
	case "text", "text_delta":
		// Subagent message text — what the agent actually said. Collapse
		// internal blank-line runs so paragraph spacing from streamed chunks
		// doesn't produce wide gaps in the card.
		line = evStyle.Render("» " + collapseToSingleLine(ev.content))
	case "thinking_delta":
		// Subagent reasoning — show it with the same 💭 marker the main
		// chat uses for thinking, so it reads as thought, not speech.
		line = evStyle.Render("💭 " + collapseToSingleLine(ev.content))
	default:
		content := collapseToSingleLine(ev.content)
		if content == "" {
			line = evStyle.Render(ev.kind)
		} else {
			line = evStyle.Render(ev.kind + ": " + content)
		}
	}
	return softWrap(line, width)
}

// truncateRunes clips s to at most n runes, marking the cut. Slicing by byte
// would split a multi-byte rune and emit a replacement character.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-3]) + "..."
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

// argWidth returns the max rune count for a tool's argument/command shown in
// the header line. It scales with the terminal width so wide terminals show
// longer commands instead of truncating everything to a fixed 60/80 chars.
// The header is "◉ tool(args)" — reserve room for the bullet, tool name, and
// parens, then give the rest to the args. A floor of 60 keeps narrow
// terminals readable.
func (t ToolDisplayModel) argWidth() int {
	w := t.Width
	if w < 40 {
		w = 80 // sensible default when width unknown
	}
	// Reserve ~20 chars for "◉ tool(" + ")" and the trailing summary space.
	if w-20 < 60 {
		return 60
	}
	return w - 20
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
func (t *ToolDisplayModel) renderRegularTool(msg message, dim lipgloss.Style, p Palette) string {
	toolStyle := lipgloss.NewStyle().Foreground(p.Tool).Bold(true)
	argStyle := lipgloss.NewStyle().Foreground(p.Dim)
	toolBullet := t.toolBullet(lipgloss.NewStyle().Foreground(p.Tool).Bold(true), msg.content == "")

	var b strings.Builder
	b.WriteString(toolBullet)
	b.WriteString(toolStyle.Render(msg.tool))
	if msg.toolIn != "" {
		args := truncateRunes(msg.toolIn, t.argWidth())
		b.WriteString(dim.Render("("))
		b.WriteString(argStyle.Render(args))
		b.WriteString(dim.Render(")"))
	}
	b.WriteString("\n")
	if msg.content == "" && len(msg.agentEvents) > 0 {
		// Still running: show the live tail instead of an empty card. Once the
		// result arrives, msg.content takes over and the final output replaces
		// this window — the stream is a progress indicator, not a transcript.
		b.WriteString(t.renderLiveOutput(msg, dim, p))
	}
	if msg.content != "" {
		lines := strings.Split(msg.content, "\n")

		// Clip first, and keep the "N more lines" marker OUT of the clipped set.
		// It used to be styled and appended to lines before they were handed to
		// the syntax highlighter, so chroma re-tokenized a string that already
		// held ANSI escapes and shredded them — that is what printed a literal
		// "[38;5;240m... (81 more lines)[m" into the chat. A half-eaten escape
		// also makes the terminal swallow columns, which knocked the rail out of
		// its column.
		const maxLines = 15
		hidden := 0
		if len(lines) > maxLines {
			hidden = len(lines) - maxLines
			lines = lines[:maxLines]
		}

		var styled []string
		switch {
		case msg.tool == "read" && msg.toolIn != "":
			styled = highlightReadOutput(lines, msg.toolIn, p)
		case msg.tool == "bash" || msg.tool == "bash_output" || msg.tool == "bash_kill":
			// Bash output is plain text most of the time but frequently contains
			// runnable snippets (`cat file`, `curl ...`, `go test` output). Run it
			// through chroma's content-sniffing lexer so it gets at least some
			// coloring; if chroma cannot place it the fallback lexer still gives
			// a non-gray foreground. Without this branch the output is dim (240)
			// and reads as an afterthought next to the brightly-highlighted
			// read/grep/find blocks above it.
			styled = highlightBashOutput(lines, p)
		// The grep tool registers itself as "ripgrep" whenever rg is installed
		// (internal/tools/grep.go), which is the common case — so matching only
		// "grep" here meant grep output was never highlighted in practice and
		// fell through to the dim default.
		case msg.tool == "grep" || msg.tool == "ripgrep":
			styled = highlightGrepOutput(lines, p)
		case msg.tool == "find":
			styled = highlightFindOutput(lines, p)
		default:
			styled = make([]string, len(lines))
			for i, line := range lines {
				styled[i] = dim.Render(line)
			}
		}

		// Style the marker only now that highlighting is done.
		if hidden > 0 {
			styled = append(styled, dim.Render(fmt.Sprintf("... (%d more lines)", hidden)))
		}

		cw := t.contentWidth()
		for _, line := range styled {
			for _, sl := range softWrap(line, cw) {
				b.WriteString("  ")
				b.WriteString(dim.Render("│ "))
				b.WriteString(sl)
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
			return cmd
		}
	case "bash_output", "bash_kill":
		if h, ok := args["handle"].(string); ok {
			return h
		}
	// "ripgrep" is the name the grep tool registers under when rg is installed.
	case "grep", "ripgrep":
		if p, ok := args["pattern"].(string); ok {
			return p
		}
	// Gemini's server-side search, surfaced as a synthetic tool call so a
	// grounded answer shows the query it searched for.
	case groundingToolName:
		if q, ok := args["query"].(string); ok {
			return q
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
	// A backgrounded command, either at the moment of handoff (the bash tool) or
	// on any later poll of it (bash_output, bash_kill). Both carry a handle, and
	// both have to be taken before the exit-code branch below.
	//
	// While such a command is still running it has no exit status: the bash tool
	// reports the -1 placeholder, which the exit-code branch renders as
	// "exit -1: <first line of output>" — a live lint run reading as a crashed
	// one. A poll is worse: BashStatus omits exit_code entirely while running, so
	// it missed the branch altogether and fell through to the raw-JSON fallback,
	// printing &-escaped argument soup instead of the command's output.
	if handle, ok := data["handle"].(string); ok && handle != "" {
		return formatBashWindow(handle, data)
	}
	// bash tool: show exit code + first 2 and last 2 output lines (preserve newlines for better visibility)
	if code, ok := data["exit_code"].(float64); ok {
		stdout, _ := data["stdout"].(string)
		stderr, _ := data["stderr"].(string)
		result := bashOutputPreview(stdout, stderr)
		if result == "" {
			result = "(No output)"
		}
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

// formatBashWindow renders the card for a backgrounded command: what state it
// is in, what it has printed, and — while it is still running — the limits it
// was started under.
//
// The state line is the point of the card and is never omitted. A poll that
// returns no new output is otherwise a blank card, indistinguishable from a
// finished one, which is how fifty-five consecutive polls of the same handle
// scrolled past without saying anything at all.
func formatBashWindow(handle string, data map[string]any) string {
	running, _ := data["running"].(bool)
	stdout, _ := data["stdout"].(string)
	stderr, _ := data["stderr"].(string)
	code, hasCode := data["exit_code"].(float64)
	timing := bashTiming(data)

	var lines []string
	switch {
	case running:
		lines = append(lines, "⏳"+timing)
	case hasCode && int(code) == -1:
		// Killed, or killed and not reaped before the wait delay expired. Either
		// way there is no status to report, and printing "exit -1" invites the
		// reader to look for a exit code that never existed.
		lines = append(lines, fmt.Sprintf("killed, no exit status (%s)%s", handle, commaTiming(timing)))
	default:
		lines = append(lines, fmt.Sprintf("exit %d (%s)%s", int(code), handle, commaTiming(timing)))
	}

	if preview := bashOutputPreview(stdout, stderr); preview != "" {
		lines = append(lines, preview)
	} else if running {
		lines = append(lines, "(no new output)")
	}
	if running {
		if hint := bashLimitsHint(data); hint != "" {
			lines = append(lines, hint)
		}
	}
	return strings.Join(lines, "\n")
}

func commaTiming(timing string) string {
	if timing == "" {
		return ""
	}
	return "," + timing
}

// bashTiming renders however much of the elapsed/idle pair the result carries.
func bashTiming(data map[string]any) string {
	elapsed, _ := data["elapsed"].(string)
	idle, _ := data["idle"].(string)
	switch {
	case elapsed != "" && idle != "":
		return fmt.Sprintf(" %s elapsed, %s without output", elapsed, idle)
	case elapsed != "":
		return " " + elapsed + " elapsed"
	case idle != "":
		return " " + idle + " without output"
	}
	return ""
}

// bashLimitsHint names the limits a running command was started under.
//
// Without it the card says a command went to the background but not that the
// threshold it crossed was one the caller picked — which is the difference
// between "this build is slow" and "idle_timeout was set to one second".
func bashLimitsHint(data map[string]any) string {
	timeout, _ := data["timeout"].(string)
	idle, _ := data["idle_timeout"].(string)
	switch {
	case timeout != "" && idle != "":
		return fmt.Sprintf("limits: idle_timeout %s, timeout %s", idle, timeout)
	case timeout != "":
		return "limits: timeout " + timeout
	case idle != "":
		return "limits: idle_timeout " + idle
	}
	return ""
}

// bashOutputPreview condenses command output to its first and last two lines,
// each clipped to 80 columns. Returns "" when there was no output at all, which
// callers report in their own words.
func bashOutputPreview(stdout, stderr string) string {
	output := stdout
	if output == "" {
		output = stderr
	}
	if output == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	if len(lines) > 4 {
		lines = []string{lines[0], lines[1], lines[len(lines)-2], lines[len(lines)-1]}
	}
	clipped := make([]string, len(lines))
	for i, line := range lines {
		clipped[i] = truncateRunes(line, 80)
	}
	return strings.Join(clipped, "\n")
}

// highlightReadOutput applies syntax highlighting to read tool output lines.
// Each line has format "     1\tcontent" — line numbers are styled separately.
func highlightReadOutput(lines []string, filename string, p Palette) []string {
	numStyle := lipgloss.NewStyle().Foreground(p.Faint)

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
	highlighted := highlightCodeWithPalette(code, filename, p)
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

// lexerCache memoizes lexers.Match by filename.
//
// lexers.Match glob-matches the filename against the patterns of every lexer
// chroma has registered — hundreds of filepath.Match calls per lookup. It is a
// pure function of the filename, so it only ever needs to run once per file.
// Negative results are cached too: a filename chroma cannot place is exactly
// the case that scans the whole registry and finds nothing.
var (
	lexerCacheMu sync.RWMutex
	lexerCache   = map[string]chroma.Lexer{}
)

// matchLexer returns the coalesced lexer for filename, or nil if chroma has
// none. The result is cached; the map is bounded by the number of distinct
// files displayed in a session.
func matchLexer(filename string) chroma.Lexer {
	lexerCacheMu.RLock()
	lexer, ok := lexerCache[filename]
	lexerCacheMu.RUnlock()
	if ok {
		return lexer
	}

	if lexer = lexers.Match(filename); lexer != nil {
		lexer = chroma.Coalesce(lexer)
	}

	lexerCacheMu.Lock()
	lexerCache[filename] = lexer
	lexerCacheMu.Unlock()
	return lexer
}

// cachedHostname resolves the hostname once. os.Hostname is a sysctl on Darwin,
// and both the status bar and the sidebar called it on every frame — at the
// TUI's re-render cadence that was 18.7% of process CPU, spent re-asking the
// kernel for a string that cannot change while the process is alive.
var cachedHostname = sync.OnceValue(func() string {
	name, _ := os.Hostname()
	return name
})

// highlightStyle and lightHighlightStyle are resolved once; both are registry
// lookups whose result never changes. styles.Get returns styles.Fallback for an
// unknown name rather than nil, so neither needs a nil guard.
var highlightStyle = sync.OnceValue(func() *chroma.Style {
	return styles.Get("monokai")
})

// lightHighlightStyle is the chroma style for light palettes.
var lightHighlightStyle = sync.OnceValue(func() *chroma.Style {
	return styles.Get("github")
})

func highlightStyleForPalette(p Palette) *chroma.Style {
	if paletteOrDark(p).IsLight {
		return lightHighlightStyle()
	}
	return highlightStyle()
}

var highlightFormatter = sync.OnceValue(func() chroma.Formatter {
	if formatter := formatters.Get("terminal256"); formatter != nil {
		return formatter
	}
	return formatters.Fallback
})

func highlightCodeWithPalette(code, filename string, p Palette) string {
	p = paletteOrDark(p)
	lexer := matchLexer(filename)
	if lexer == nil {
		// Content sniffing depends on the code, not the filename, so it cannot
		// be cached by name.
		lexer = lexers.Analyse(code) //nolint:misspell // chroma API uses British spelling
		if lexer == nil {
			lexer = lexers.Fallback
		}
		lexer = chroma.Coalesce(lexer)
	}

	style := highlightStyleForPalette(p)
	formatter := highlightFormatter()

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

// highlightBashOutput applies chroma content-sniffing to bash output lines.
// Bash output is heterogeneous (program output, file dumps, error traces) so
// the filename-based lexer always misses; the content-sniffing fallback picks
// something close to the actual language, and chroma's fallback lexer gives
// the rest a non-gray foreground either way.
func highlightBashOutput(lines []string, p Palette) []string {
	p = paletteOrDark(p)
	code := strings.Join(lines, "\n")
	highlighted := highlightCodeWithPalette(code, "", p)
	if highlighted == code {
		// Lexer did not tokenize — every line came back unchanged. Fall back
		// to a non-gray foreground so the block stands out from the gutter
		// instead of looking like unread dim text.
		styled := make([]string, len(lines))
		nonDim := lipgloss.NewStyle().Foreground(p.Text)
		for i, line := range lines {
			styled[i] = nonDim.Render(line)
		}
		return styled
	}
	return strings.Split(highlighted, "\n")
}

// highlightGrepOutput styles grep result lines of the form "file:line: content".
func highlightGrepOutput(lines []string, p Palette) []string {
	fileStyle := lipgloss.NewStyle().Foreground(p.Blue)     // blue
	lineNumStyle := lipgloss.NewStyle().Foreground(p.Faint) // gray
	sepStyle := lipgloss.NewStyle().Foreground(p.Faint)

	result := make([]string, 0, len(lines))
	for _, line := range lines {
		// Try to parse "file:line: content"
		first := strings.IndexByte(line, ':')
		if first < 0 {
			// Not a match line (e.g. truncation note) — dim it.
			result = append(result, lipgloss.NewStyle().Foreground(p.Faint).Render(line))
			continue
		}
		second := strings.IndexByte(line[first+1:], ':')
		if second < 0 {
			result = append(result, lipgloss.NewStyle().Foreground(p.Faint).Render(line))
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
		highlighted := highlightCodeWithPalette(contentPart, filePart, p)

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
func highlightFindOutput(lines []string, p Palette) []string {
	fileStyle := lipgloss.NewStyle().Foreground(p.Blue) // blue
	dirStyle := lipgloss.NewStyle().Foreground(p.Cyan).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(p.Faint)

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
