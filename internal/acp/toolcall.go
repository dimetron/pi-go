package acp

import (
	"fmt"
	"strings"
	"sync"
)

// rawInputTitleKeys is the ordered list of RawInput keys EnrichToolCallTitle
// looks for to produce a one-line snippet. Order matters — earlier keys win.
// These cover the parameter names used by Claude Code, Gemini CLI, and Cursor
// for their built-in tools (Bash, Read, Edit, Write, Task, Search, Fetch).
var rawInputTitleKeys = []string{
	"command", "cmd",
	"description",
	"prompt",
	"query",
	"pattern",
	"file_path", "filePath", "path",
	"url",
	"name",
}

// EnrichToolCallTitle returns a title that combines the agent-provided
// display title with a short snippet pulled from RawInput. ACP agents often
// send generic titles like "Terminal", "TaskOutput", or "Read" with the real
// detail (the command, the file path, the task description) buried in
// RawInput, which produces a stream of unhelpful "TaskOutput / TaskOutput /
// TaskOutput" lines in the parent UI. Pulling the first useful field out of
// RawInput restores per-call context.
//
// If RawInput is nil or carries no recognizable string field, the original
// title is returned unchanged.
func EnrichToolCallTitle(title string, rawInput any) string {
	title = strings.TrimSpace(title)
	snippet := rawInputSnippet(rawInput)
	switch {
	case snippet == "":
		return title
	case title == "":
		return snippet
	default:
		return title + ": " + snippet
	}
}

// rawInputSnippet pulls the first usable string value from RawInput, in the
// order defined by rawInputTitleKeys. The result is whitespace-collapsed and
// truncated so it fits on a single event line.
func rawInputSnippet(rawInput any) string {
	m, ok := rawInput.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	for _, k := range rawInputTitleKeys {
		v, ok := m[k]
		if !ok {
			continue
		}
		s := stringifyRawValue(v)
		if s == "" {
			continue
		}
		s = strings.Join(strings.Fields(s), " ")
		const max = 120
		if len(s) > max {
			s = s[:max-3] + "..."
		}
		return s
	}
	return ""
}

// stringifyRawValue coerces a RawInput leaf into a string. JSON unmarshal
// produces float64 for numbers, so handle a few common scalar shapes rather
// than only string.
func stringifyRawValue(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	case float64, int, int64, bool:
		return fmt.Sprintf("%v", t)
	default:
		return ""
	}
}

// ToolCallTitleFilter dedupes tool-call title emissions per tool_call_id.
//
// Most ACP agents send a generic ToolCall first (Title: "Terminal", "Read",
// "Bash") and then a ToolCallUpdate with a far more useful Title (the actual
// command, file path, etc.). Streaming both produces redundant lines like:
//
//   - Terminal
//   - `go test ./internal/acp/... 2>&1`
//
// This filter buffers the initial title; if a ToolCallUpdate arrives for the
// same id with a non-empty title, the buffered title is dropped and only the
// update's title is emitted. If no update ever arrives (rare), Flush emits
// the buffered title at session end so the call is still visible.
type ToolCallTitleFilter struct {
	mu      sync.Mutex
	pending map[string]string // tool_call_id -> initial title not yet emitted
	emit    func(title string)
}

// NewToolCallTitleFilter wires the filter to the per-session emit callback.
// emit is invoked with the chosen title each time the filter decides to surface
// one — the caller is responsible for wrapping it in the appropriate event.
func NewToolCallTitleFilter(emit func(title string)) *ToolCallTitleFilter {
	return &ToolCallTitleFilter{
		pending: make(map[string]string),
		emit:    emit,
	}
}

// OnToolCall records an initial tool-call title without emitting it. Empty
// titles are ignored. If id is empty the title is emitted immediately, since
// without an id we cannot correlate a later update.
func (f *ToolCallTitleFilter) OnToolCall(id, title string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	if id == "" {
		f.emit(title)
		return
	}
	f.mu.Lock()
	f.pending[id] = title
	f.mu.Unlock()
}

// OnToolCallUpdate emits the update's title (when non-empty) and forgets any
// buffered initial title for the same id. If the update carries no title and
// nothing is pending, this is a no-op.
func (f *ToolCallTitleFilter) OnToolCallUpdate(id, title string) {
	title = strings.TrimSpace(title)
	f.mu.Lock()
	delete(f.pending, id)
	f.mu.Unlock()
	if title != "" {
		f.emit(title)
	}
	// No update title: the initial pending entry was already dropped above to
	// keep the stream clean. The completed status alone is enough signal.
}

// Flush emits any pending titles whose tool calls produced no update before
// the session ended. Safe to call multiple times.
func (f *ToolCallTitleFilter) Flush() {
	f.mu.Lock()
	pending := f.pending
	f.pending = make(map[string]string)
	f.mu.Unlock()
	for _, title := range pending {
		f.emit(title)
	}
}
