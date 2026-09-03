package adapter

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	acp "github.com/coder/acp-go-sdk"
)

// callState tracks a single tool call across its start/end boundary.
// Sub-agent parents also accumulate content so nested inner calls can be
// appended without losing earlier lines — WithUpdateContent replaces the
// entire collection, so the adapter keeps the running copy here.
type callState struct {
	id      acp.ToolCallId
	name    string
	kind    acp.ToolKind
	parent  acp.ToolCallId
	content []acp.ToolCallContent
}

// toolKind maps a pi-go tool name to an ACP ToolKind. Unknown tools fall
// back to ToolKindOther, which Zed renders as a generic card.
func toolKind(name string) acp.ToolKind {
	switch name {
	case "read", "ls":
		return acp.ToolKindRead
	case "grep", "find", "glob":
		return acp.ToolKindSearch
	case "edit", "write":
		return acp.ToolKindEdit
	case "bash", "shell":
		return acp.ToolKindExecute
	case "subagent", "agent":
		return acp.ToolKindThink
	default:
		return acp.ToolKindOther
	}
}

// ToolKind maps a pi-go tool name to the ACP ToolKind the live stream
// renders it with, so a replayed transcript gets the same cards as the
// original turn did.
func ToolKind(name string) acp.ToolKind {
	return toolKind(name)
}

// isSubagentTool returns true for tool names that dispatch a sub-agent.
// Sub-agents become parent "think" cards under which inner tool calls are
// nested. pi-go's dispatch tool is named "subagent"; "agent" is kept as a
// synonym to match the spec vocabulary.
func isSubagentTool(name string) bool {
	return name == "subagent" || name == "agent"
}

// locationFromArgs pulls a file path out of common tool-argument shapes.
// pi-go tools use both "path" and "file_path" — either yields a
// ToolCallLocation that drives Zed's follow-along UI.
func locationFromArgs(args map[string]any) string {
	for _, key := range []string{"path", "file_path"} {
		if v, ok := args[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// OnToolStart records a new tool invocation and emits the initial
// SessionUpdate for it. The returned call id pairs this start with its
// later OnToolEnd — the runtime bridge (Zed-08) threads it through the
// ADK Before/After tool callbacks.
//
// When the tool is a sub-agent dispatch, the call is marked as the
// active parent: subsequent tool starts fold their progress into the
// parent's content instead of creating new top-level cards. Nesting is
// single-level — a sub-agent spawned inside another sub-agent is treated
// as a plain nested call.
//
// The mutex is held only for state mutations. Top-level tool updates are
// sent after unlock so concurrent parallel tool calls do not serialize on
// the protocol write. Nested (sub-agent child) updates keep the lock
// during the write to preserve content line-ordering on the parent card.
func (s *Stream) OnToolStart(ctx context.Context, name string, args map[string]any) (string, error) {
	s.mu.Lock()

	s.nextCallSeq++
	id := acp.ToolCallId("call_" + strconv.Itoa(s.nextCallSeq))
	state := &callState{id: id, name: name, kind: toolKind(name)}

	switch {
	case isSubagentTool(name):
		s.subagentID = string(id)
	case s.subagentID != "":
		state.parent = acp.ToolCallId(s.subagentID)
	}
	s.toolCalls[string(id)] = state

	if s.updater == nil {
		s.mu.Unlock()
		return string(id), nil
	}

	// Nested into a sub-agent card: hold the lock during the Update to
	// preserve content line-ordering in the parent.
	if state.parent != "" {
		argsLine := formatArgsForDisplay(args)
		var line string
		if argsLine != "" {
			line = fmt.Sprintf("▶ %s(%s)", name, argsLine)
		} else {
			line = "▶ " + name
		}
		err := s.appendToParentLocked(ctx, state.parent, line)
		s.mu.Unlock()
		return string(id), err
	}

	// Top-level tool: capture what is needed for the Update, then release
	// the lock before network I/O so concurrent tool starts don't block
	// each other at the protocol layer.
	updater := s.updater
	kind := state.kind
	s.mu.Unlock()

	opts := []acp.ToolCallStartOpt{
		acp.WithStartKind(kind),
		acp.WithStartStatus(acp.ToolCallStatusInProgress),
		acp.WithStartRawInput(args),
	}
	if loc := locationFromArgs(args); loc != "" {
		opts = append(opts, acp.WithStartLocations([]acp.ToolCallLocation{{Path: loc}}))
	}
	if err := updater.Update(ctx, acp.StartToolCall(id, buildTitle(name, args), opts...)); err != nil {
		return string(id), fmt.Errorf("stream: tool-call start: %w", err)
	}
	return string(id), nil
}

// OnToolEnd emits the terminal SessionUpdate for callID. Unknown ids are
// a no-op so the adapter tolerates a dropped/filtered start without
// surfacing a protocol error; the call simply never materializes in Zed.
//
// Like OnToolStart, the lock is released before network I/O for top-level
// calls and held through the write for nested calls.
func (s *Stream) OnToolEnd(ctx context.Context, callID string, args map[string]any, result any, runErr error) error {
	s.mu.Lock()

	state, ok := s.toolCalls[callID]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(s.toolCalls, callID)
	if s.subagentID == callID {
		s.subagentID = ""
	}

	if s.updater == nil {
		s.mu.Unlock()
		return nil
	}

	// Nested into a sub-agent card: hold the lock during the Update.
	if state.parent != "" {
		marker := "✓"
		if runErr != nil {
			marker = "✗"
		}
		argsLine := formatArgsForDisplay(args)
		var line string
		if argsLine != "" {
			line = fmt.Sprintf("%s %s(%s)", marker, state.name, argsLine)
		} else {
			line = marker + " " + state.name
		}
		err := s.appendToParentLocked(ctx, state.parent, line)
		s.mu.Unlock()
		return err
	}

	// Top-level: build the update payload while the lock is held (state is
	// deleted from the map so no other goroutine can reach it after we
	// unlock), then send after releasing.
	updater := s.updater
	stateID := state.id
	status := acp.ToolCallStatusCompleted
	if runErr != nil {
		status = acp.ToolCallStatusFailed
	}
	opts := []acp.ToolCallUpdateOpt{acp.WithUpdateStatus(status)}
	switch {
	case runErr != nil:
		opts = append(opts, acp.WithUpdateRawOutput(map[string]any{"error": runErr.Error()}))
	case result != nil:
		opts = append(opts, acp.WithUpdateRawOutput(result))
	}
	if len(state.content) > 0 {
		opts = append(opts, acp.WithUpdateContent(state.content))
	}
	s.mu.Unlock()

	if err := updater.Update(ctx, acp.UpdateToolCall(stateID, opts...)); err != nil {
		return fmt.Errorf("stream: tool-call end: %w", err)
	}
	return nil
}

// buildTitle produces the ACP tool-call title — the string Zed renders as
// the card header. The bare tool name ("bash", "read") tells the user
// *which* tool is running but not *what* it's doing; buildTitle folds the
// most informative argument into the title so the header conveys both.
//
// Rules, by tool name:
//   - bash/shell: the command itself replaces the name — a command like
//     "git status -s" is self-describing and the kind icon already marks
//     it as an execute.
//   - read/ls/edit/write: append the path.
//   - grep/find/glob: append the pattern or query.
//   - subagent/agent: append the target agent name.
//   - anything else: append a compact arg summary, or fall back to the
//     bare name if nothing useful is available.
func buildTitle(name string, args map[string]any) string {
	switch name {
	case "bash", "shell":
		if cmd := firstString(args, "command", "cmd"); cmd != "" {
			return truncate(cmd, 80)
		}
	case "read", "ls", "edit", "write":
		if p := locationFromArgs(args); p != "" {
			return name + " " + p
		}
	case "grep", "find", "glob":
		if q := firstString(args, "pattern", "query"); q != "" {
			return name + " " + truncate(q, 60)
		}
	case "subagent", "agent":
		if a := firstString(args, "agent", "name"); a != "" {
			return name + ": " + a
		}
	}
	if summary := formatArgsForDisplay(args); summary != "" {
		return name + " " + summary
	}
	return name
}

// firstString returns the first non-empty string value in args among the
// given keys, in order. Returns "" if none match.
func firstString(args map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// truncate clips s to at most n runes, appending a single-character
// ellipsis when truncation occurs. n must be >= 1.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// formatArgsForDisplay creates a compact string representation of tool arguments
// for display in nested tool content. Returns a summary of the most relevant args.
func formatArgsForDisplay(args map[string]any) string {
	if args == nil {
		return ""
	}
	if s := priorityArgValue(args); s != "" {
		return s
	}
	return summarizeArgs(args)
}

// priorityArgValue returns the value of the first display-worthy key present
// in args, truncated for display. Keys are tried in the order listed — the
// most tool-identifying first — and a missing, non-string or empty value falls
// through to the next key. Returns "" when no priority key yields a value.
func priorityArgValue(args map[string]any) string {
	// Keys to prioritize for display (most useful for tool visibility)
	priorityKeys := []string{"path", "file_path", "command", "cmd", "prompt", "query", "pattern", "name", "url", "description"}

	for _, key := range priorityKeys {
		s, ok := args[key].(string)
		if !ok || s == "" {
			continue
		}
		// Truncate long values
		return clipArg(s, 50, 47)
	}
	return ""
}

// summarizeArgs is the fallback for arguments carrying no priority key: it
// joins up to three "key=value" pairs. Map iteration order is random, so which
// three entries are visited is not defined — and the budget is spent on every
// entry visited, including non-string ones that contribute nothing.
func summarizeArgs(args map[string]any) string {
	var parts []string
	i := 0
	for k, v := range args {
		if i >= 3 {
			break
		}
		i++
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%s", k, clipArg(s, 30, 27)))
	}
	return strings.Join(parts, ", ")
}

// clipArg shortens s to keep bytes plus an ellipsis when it is longer than
// limit bytes, and returns it unchanged otherwise.
func clipArg(s string, limit, keep int) string {
	if len(s) <= limit {
		return s
	}
	return s[:keep] + "..."
}

// appendToParentLocked records line as nested content on the sub-agent parent
// and re-sends the full accumulated collection via WithUpdateContent
// (which replaces, not appends, server-side). Caller must hold mu.
func (s *Stream) appendToParentLocked(ctx context.Context, parentID acp.ToolCallId, line string) error {
	parent, ok := s.toolCalls[string(parentID)]
	if !ok {
		return nil
	}
	parent.content = append(parent.content, acp.ToolCallContent{
		Content: &acp.ToolCallContentContent{
			Type:    "content",
			Content: acp.TextBlock(line),
		},
	})
	if err := s.updater.Update(ctx, acp.UpdateToolCall(parentID, acp.WithUpdateContent(parent.content))); err != nil {
		return fmt.Errorf("stream: nested tool-call content: %w", err)
	}
	return nil
}
