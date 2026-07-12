package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	stdlog "log"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/otel"
)

const (
	// maxRepeatToolCalls is the number of identical consecutive tool calls
	// before the loop is considered stuck and aborted.
	maxRepeatToolCalls = 10

	// maxRepeatErrorCalls aliases maxRepeatToolCalls for callers that frame
	// the threshold as an error-streak rather than a call-streak. The
	// underlying detector is identical — identical fingerprint = stuck.
	maxRepeatErrorCalls = maxRepeatToolCalls

	// maxToolErrorStreak is the number of consecutive failures of the same
	// tool name (regardless of args) before the loop is aborted. Catches the
	// "flailing" pattern where the model tries a different argument each
	// turn but the call still fails.
	maxToolErrorStreak = 10

	// recentWindowSize is the sliding window of tool-call fingerprints kept
	// for repetition detection.
	recentWindowSize = 12
)

// extractAgentType returns a label for the subagent tool call by inspecting
// its args. For single-agent mode it returns the "type"/"agent" field. For
// parallel (tasks[]) or chain (chain[]) invocations it concatenates unique
// agent names with "+" — so "agent[claude+gemini]" renders for a parallel
// call. Returns "" when no type information is available.
func extractAgentType(args map[string]any) string {
	if t, _ := args["type"].(string); t != "" {
		return t
	}
	if a, _ := args["agent"].(string); a != "" {
		return a
	}
	collect := func(list []any) string {
		seen := make(map[string]struct{})
		var names []string
		for _, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["agent"].(string)
			if name == "" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		return strings.Join(names, "+")
	}
	if tasks, ok := args["tasks"].([]any); ok {
		if label := collect(tasks); label != "" {
			return label
		}
	}
	if chain, ok := args["chain"].([]any); ok {
		if label := collect(chain); label != "" {
			return label
		}
	}
	return ""
}

// stuckDetector tracks recent tool calls and detects repetition loops.
type stuckDetector struct {
	recent      []string // ring of fingerprints (len <= recentWindowSize)
	lastPrint   string   // fingerprint of last tool call
	streak      int      // consecutive identical tool calls
	lastErrTool string   // name of last tool that errored
	errStreak   int      // consecutive errors for that tool
}

// toolFingerprint produces a short hash of a tool call for comparison.
func toolFingerprint(name string, args map[string]any) string {
	h := sha256.New()
	h.Write([]byte(name))
	b, _ := json.Marshal(args)
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// observe records a tool call and returns true if the loop appears stuck.
func (s *stuckDetector) observe(name string, args map[string]any) (stuck bool, detail string) {
	fp := toolFingerprint(name, args)

	// Consecutive identical call detection.
	if fp == s.lastPrint {
		s.streak++
	} else {
		s.streak = 1
		s.lastPrint = fp
	}

	// Sliding window.
	s.recent = append(s.recent, fp)
	if len(s.recent) > recentWindowSize {
		s.recent = s.recent[1:]
	}

	if s.streak >= maxRepeatToolCalls {
		return true, fmt.Sprintf("identical tool call %q repeated %d times", name, s.streak)
	}

	// Detect short repeating cycles (AB AB AB) in the window.
	if cycle := s.detectCycle(); cycle != "" {
		return true, fmt.Sprintf("repeating tool cycle detected: %s", cycle)
	}

	return false, ""
}

// observeError records the outcome of a tool call by name. Consecutive errors
// of the same tool name — regardless of args — trip the detector once the
// streak reaches maxToolErrorStreak. A success (isError == false) or a switch
// to a different tool name resets the streak.
func (s *stuckDetector) observeError(name string, isError bool) (stuck bool, detail string) {
	if isError && name == s.lastErrTool {
		s.errStreak++
	} else {
		s.errStreak = 1
		s.lastErrTool = name
	}
	if s.errStreak >= maxToolErrorStreak {
		return true, fmt.Sprintf("tool %q failed %d times in a row", name, s.errStreak)
	}
	return false, ""
}

// detectCycle checks the recent window for repeating subsequences.
// Returns a description if found, empty string otherwise.
//
// A "cycle" requires that consecutive elements differ — a uniform window
// like [a,a,a,a,a,a] is a streak, not a cycle, and the identical-call
// detector above already handles that case at maxRepeatToolCalls.
func (s *stuckDetector) detectCycle() string {
	n := len(s.recent)
	if n < 6 {
		return ""
	}
	// Check cycle lengths 2 and 3.
	for cycleLen := 2; cycleLen <= 3; cycleLen++ {
		need := cycleLen * 3 // require 3 full repetitions
		if n < need {
			continue
		}
		tail := s.recent[n-need:]
		cycle := tail[:cycleLen]
		// Require adjacent elements in the candidate cycle to differ —
		// otherwise it's a uniform streak, not an alternating cycle.
		cycleValid := true
		for i := 1; i < cycleLen; i++ {
			if cycle[i] == cycle[i-1] {
				cycleValid = false
				break
			}
		}
		if !cycleValid {
			continue
		}
		match := true
		for i := cycleLen; i < need; i++ {
			if tail[i] != cycle[i%cycleLen] {
				match = false
				break
			}
		}
		if match {
			return fmt.Sprintf("length-%d cycle repeated %d times", cycleLen, need/cycleLen)
		}
	}
	return ""
}

// agentMsg wraps messages coming from the agent goroutine via a channel.
type agentMsg interface{ agentMsg() }

type agentTextMsg struct{ text string }
type agentThinkingMsg struct{ text string }
type agentToolCallMsg struct {
	name string
	args map[string]any
}
type agentToolResultMsg struct {
	name    string
	content string
}
type agentDoneMsg struct{ err error }

// agentSubEventMsg carries a streamed event from a running subagent to the TUI.
type agentSubEventMsg struct {
	agentID       string // which subagent
	kind          string // "tool_call", "tool_result", "text"
	content       string
	pipelineID    string // groups agents in same call
	pipelineMode  string // "single", "parallel", "chain"
	pipelineStep  int    // 1-based position
	pipelineTotal int    // total agents in pipeline
}

func (agentTextMsg) agentMsg()       {}
func (agentThinkingMsg) agentMsg()   {}
func (agentToolCallMsg) agentMsg()   {}
func (agentToolResultMsg) agentMsg() {}
func (agentDoneMsg) agentMsg()       {}
func (agentSubEventMsg) agentMsg()   {}

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

func waitForSubEvent(ch <-chan AgentSubEvent) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return agentSubEventMsg{
			agentID:       ev.AgentID,
			kind:          ev.Kind,
			content:       ev.Content,
			pipelineID:    ev.PipelineID,
			pipelineMode:  ev.Mode,
			pipelineStep:  ev.Step,
			pipelineTotal: ev.Total,
		}
	}
}

// cancelAgent stops a running agent and drains its channel.
func (m *model) cancelAgent() {
	if m.agentCancel != nil {
		m.agentCancel()
		m.agentCancel = nil
	}
	m.running = false
	m.statusModel.ActiveTool = ""
	m.statusModel.ActiveTools = nil
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	if m.face != nil {
		m.face.SetMood(MoodIdle)
	}
	if m.agentCh != nil {
		go func(ch chan agentMsg) {
			// Drain remaining messages. The agent loop closes the channel
			// via defer close(m.agentCh) when it exits. If the agent loop
			// is stuck (e.g. blocked on an LLM call that ignores context
			// cancellation), the close may never happen — guard with a
			// timeout so this goroutine doesn't leak forever.
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
			for {
				select {
				case _, ok := <-ch:
					if !ok {
						return
					}
				case <-timer.C:
					return
				}
			}
		}(m.agentCh)
		m.agentCh = nil
	}
}

func (m *model) startAgentLoop(prompt string) tea.Cmd {
	m.agentCh = make(chan agentMsg, 64)
	agentCtx, agentCancel := context.WithCancel(m.ctx)
	m.agentCancel = agentCancel
	go m.runAgentLoop(agentCtx, prompt)
	return waitForAgent(m.agentCh)
}

// submitPrompt sends a user prompt to the agent.
func (m *model) submitPrompt(text string, mentions []string) (tea.Model, tea.Cmd) {
	// Append referenced file annotations for @mentions.
	promptText := text
	if len(mentions) > 0 {
		var refs strings.Builder
		refs.WriteString(text)
		refs.WriteString("\n")
		for _, path := range mentions {
			refs.WriteString("\n[Referenced file: ")
			refs.WriteString(path)
			refs.WriteString("]")
		}
		promptText = refs.String()
	}

	if m.cfg.Logger != nil {
		m.cfg.Logger.UserMessage(promptText)
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "user", content: text})
	m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: ""})
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.running = true
	m.chatModel.Scroll = 0
	if m.face != nil {
		m.face.SetMood(MoodThinking)
	}

	m.matrix.feed("init", m.mainWidth())

	return m, tea.Batch(m.startAgentLoop(promptText), matrixTickCmd())
}

// runAgentLoop runs the agent and sends events to the channel.
func (m *model) runAgentLoop(ctx context.Context, prompt string) {
	defer close(m.agentCh)
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			stdlog.Printf("agent loop panicked: %v\n%s", r, stack)
			m.agentCh <- agentDoneMsg{err: fmt.Errorf("agent panic: %v", r)}
		}
	}()

	// Guard against missing agent config (unit tests)
	if m.cfg.Agent == nil {
		m.agentCh <- agentDoneMsg{err: fmt.Errorf("agent not configured")}
		return
	}

	log := m.cfg.Logger
	detector := &stuckDetector{}

	// streamedText tracks whether any Partial=true text delta has been
	// forwarded for the current turn. Providers like ollama/minimax emit
	// per-token partial events AND a final Partial=false event containing
	// the full aggregated text — forwarding both would duplicate the text
	// on screen (observed in the TUI as "I'll spawn...I'll spawn..."). Skip
	// the aggregate when deltas already covered the turn.
	streamedText := false

	// Start a top-level OTEL span for the entire agent run, inheriting the
	// per-response context so Esc/Ctrl+C can interrupt it without quitting the TUI.
	tracer := otel.Tracer("pi-go")
	ctx, span := tracer.Start(ctx, "agent.prompt")
	defer span.End()
	span.SetAttributes(
		otel.AttributeInt("prompt.length", len(prompt)),
	)

	for ev, err := range m.cfg.Agent.RunStreaming(ctx, m.cfg.SessionID, prompt) {
		if err != nil {
			if log != nil {
				log.Error(err.Error())
			}
			m.agentCh <- agentDoneMsg{err: err}
			return
		}
		if ev == nil || ev.Content == nil {
			continue
		}
		// A new turn begins when we see a user/tool-result event; reset the
		// dedup guard so the next turn's aggregate can pass through when no
		// deltas precede it. "model" / "thinking" are the model-author roles.
		role := ev.Content.Role
		if role != "model" && role != "thinking" {
			streamedText = false
		}
		for _, part := range ev.Content.Parts {
			if part.Text != "" && ev.Content.Role == "thinking" {
				m.agentCh <- agentThinkingMsg{text: part.Text}
				continue
			}
			if part.Text != "" {
				if !ev.Partial && streamedText {
					// Aggregate final event — text already forwarded via deltas.
					continue
				}
				if ev.Partial {
					streamedText = true
				}
				if log != nil {
					log.LLMText(ev.Author, part.Text)
				}
				m.agentCh <- agentTextMsg{text: part.Text}
			}
			if part.FunctionCall != nil {
				// Emit the tool call first so the user sees the offending call
				// before the loop aborts. The stuck-detector threshold still
				// fires after `maxRepeatToolCalls` observations, so the abort
				// semantics are unchanged — only the message ordering moves.
				if log != nil {
					log.ToolCall(ev.Author, part.FunctionCall.Name, part.FunctionCall.Args)
				}
				m.agentCh <- agentToolCallMsg{
					name: part.FunctionCall.Name,
					args: part.FunctionCall.Args,
				}

				if stuck, detail := detector.observe(part.FunctionCall.Name, part.FunctionCall.Args); stuck {
					if log != nil {
						log.Error("agent loop stuck: " + detail)
					}
					m.agentCh <- agentDoneMsg{
						err: fmt.Errorf("agent loop aborted: %s", detail),
					}
					return
				}
			}
			if part.FunctionResponse != nil {
				respJSON, _ := json.Marshal(part.FunctionResponse.Response)
				if log != nil {
					log.ToolResult(ev.Author, part.FunctionResponse.Name, string(respJSON))
				}
				m.agentCh <- agentToolResultMsg{
					name:    part.FunctionResponse.Name,
					content: string(respJSON),
				}
				// Track per-tool error streaks: ADK wraps tool errors as
				// map[string]any{"error": ...}. Anything else (including a
				// missing key) is treated as success and resets the streak.
				_, isErr := part.FunctionResponse.Response["error"]
				if stuck, detail := detector.observeError(part.FunctionResponse.Name, isErr); stuck {
					if log != nil {
						log.Error("agent loop stuck: " + detail)
					}
					m.agentCh <- agentDoneMsg{
						err: fmt.Errorf("agent loop aborted: %s", detail),
					}
					return
				}
			}
		}
	}
}

// handleAgentThinking processes an agentThinkingMsg.
func (m *model) handleAgentThinking(msg agentThinkingMsg) (tea.Model, tea.Cmd) {
	if m.face != nil {
		m.face.SetMood(MoodThinking)
	}
	m.matrix.feed(msg.text, m.mainWidth())
	m.chatModel.Thinking += msg.text
	if len(m.chatModel.Messages) > 0 && m.chatModel.Messages[len(m.chatModel.Messages)-1].role == "thinking" {
		m.chatModel.Messages[len(m.chatModel.Messages)-1].content = m.chatModel.Thinking
	} else {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role: "thinking", content: m.chatModel.Thinking,
		})
	}
	m.chatModel.Scroll = 0
	return m, waitForAgent(m.agentCh)
}

// handleAgentText processes an agentTextMsg.
func (m *model) handleAgentText(msg agentTextMsg) (tea.Model, tea.Cmd) {
	if m.face != nil {
		m.face.SetMood(MoodSpeaking)
	}
	if m.chatModel.Thinking != "" {
		m.chatModel.Thinking = ""
		if len(m.chatModel.Messages) > 0 && m.chatModel.Messages[len(m.chatModel.Messages)-1].role == "thinking" {
			m.chatModel.Messages[len(m.chatModel.Messages)-1] = message{role: "assistant", content: ""}
		}
	}
	m.matrix.feed(msg.text, m.mainWidth())
	m.chatModel.Streaming += msg.text
	// Keep chronology stable: only update a trailing assistant message.
	// If the latest message is a tool event, append a new assistant message
	// so rendered order matches event order.
	if n := len(m.chatModel.Messages); n > 0 && m.chatModel.Messages[n-1].role == "assistant" {
		m.chatModel.Messages[n-1].content = m.chatModel.Streaming
	} else {
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: m.chatModel.Streaming,
		})
	}
	m.chatModel.Scroll = 0
	if len(m.chatModel.TraceLog) > 0 && m.chatModel.TraceLog[len(m.chatModel.TraceLog)-1].kind == "llm" {
		m.chatModel.TraceLog[len(m.chatModel.TraceLog)-1].detail = m.chatModel.Streaming
	} else {
		m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
			time: time.Now(), kind: "llm", summary: "LLM response", detail: msg.text,
		})
	}
	return m, waitForAgent(m.agentCh)
}

// handleAgentToolCall processes an agentToolCallMsg.
func (m *model) handleAgentToolCall(msg agentToolCallMsg) (tea.Model, tea.Cmd) {
	if m.face != nil {
		m.face.SetMood(MoodToolCall)
	}
	if m.statusModel.ActiveTools == nil {
		m.statusModel.ActiveTools = make(map[string]time.Time)
	}
	m.statusModel.ActiveTools[msg.name] = time.Now()
	m.statusModel.ActiveTool = msg.name
	m.statusModel.ToolStart = time.Now()
	m.matrix.feed(msg.name, m.mainWidth())
	argsJSON, _ := json.MarshalIndent(msg.args, "", "  ")
	m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
		time:    time.Now(),
		kind:    "tool_call",
		summary: fmt.Sprintf(">>> %s", msg.name),
		detail:  string(argsJSON),
	})
	toolIn := toolCallSummary(msg.name, msg.args)
	newMsg := message{
		role: "tool", tool: msg.name, toolIn: toolIn,
	}
	if msg.name == "agent" || msg.name == "subagent" {
		// A single subagent tool call in parallel/chain mode spawns N children.
		// Render one card per child so the user sees agent[pi], agent[claude],
		// ... instead of a collapsed agent[pi+claude+...] card. Each card
		// carries its own type + title and will later be matched to its spawn
		// event by agent-ID prefix.
		subMsgs := splitSubagentCards(newMsg, msg.args)
		m.chatModel.Messages = append(m.chatModel.Messages, subMsgs...)
		return m, waitForAgent(m.agentCh)
	}
	m.chatModel.Messages = append(m.chatModel.Messages, newMsg)
	return m, waitForAgent(m.agentCh)
}

// splitSubagentCards fans a single subagent tool call out into one visual
// tool-message card per spawned child. Single-agent mode returns one card
// with the agent/type name and prompt; parallel (tasks[]) and chain (chain[])
// modes return one card per entry so the event stream for each child renders
// under its own agent[...] header.
func splitSubagentCards(base message, args map[string]any) []message {
	if cards := buildListCards(base, args["tasks"]); len(cards) > 0 {
		return cards
	}
	if cards := buildListCards(base, args["chain"]); len(cards) > 0 {
		return cards
	}
	single := base
	single.agentType = extractAgentType(args)
	prompt, _ := args["prompt"].(string)
	if prompt == "" {
		prompt, _ = args["task"].(string)
	}
	single.agentTitle = truncatePrompt(prompt)
	return []message{single}
}

// buildListCards expands a tasks[]/chain[] array into one message per entry.
// Returns nil when the value isn't an array of {agent, task} maps.
func buildListCards(base message, raw any) []message {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	var out []message
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		agent, _ := m["agent"].(string)
		if agent == "" {
			continue
		}
		prompt, _ := m["task"].(string)
		if prompt == "" {
			prompt, _ = m["prompt"].(string)
		}
		card := base
		card.agentType = agent
		card.agentTitle = truncatePrompt(prompt)
		out = append(out, card)
	}
	return out
}

// findUnassignedAgentCard locates the best tool-message card to bind to an
// incoming spawn event. Preference order:
//  1. Walk newest-to-oldest, pick an unassigned card whose agentType is the
//     name prefix of agentID (e.g. agentID "claude-1720…" matches the card
//     with agentType "claude").
//  2. Fall back to the first unassigned card, so single-agent invocations
//     (where the spawned ID may not carry a matching prefix) still bind.
//
// Returns -1 if no unassigned card exists.
func findUnassignedAgentCard(messages []message, agentID string) int {
	agentName := agentID
	if dash := strings.IndexByte(agentID, '-'); dash > 0 {
		agentName = agentID[:dash]
	}
	fallback := -1
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.tool != "agent" && m.tool != "subagent" {
			continue
		}
		if m.agentID != "" {
			continue
		}
		if agentName != "" && m.agentType == agentName {
			return i
		}
		if fallback == -1 {
			fallback = i
		}
	}
	return fallback
}

// truncatePrompt shortens a prompt to a single-line 60-char preview for the
// agent card header.
func truncatePrompt(prompt string) string {
	if idx := strings.IndexByte(prompt, '\n'); idx > 0 {
		prompt = prompt[:idx]
	}
	if len(prompt) > 60 {
		prompt = prompt[:57] + "..."
	}
	return prompt
}

// handleAgentToolResult processes an agentToolResultMsg.
func (m *model) handleAgentToolResult(msg agentToolResultMsg) (tea.Model, tea.Cmd) {
	if m.face != nil {
		m.face.SetMood(MoodProcessing)
	}
	delete(m.statusModel.ActiveTools, msg.name)
	m.statusModel.ActiveTool = ""
	for name := range m.statusModel.ActiveTools {
		m.statusModel.ActiveTool = name
		m.statusModel.ToolStart = m.statusModel.ActiveTools[name]
		break
	}
	m.matrix.feed(msg.name+msg.content, m.mainWidth())
	m.matrix.shiftLeft()
	m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
		time:    time.Now(),
		kind:    "tool_result",
		summary: fmt.Sprintf("<<< %s", msg.name),
		detail:  msg.content,
	})
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].role == "tool" && m.chatModel.Messages[i].tool == msg.name && m.chatModel.Messages[i].content == "" {
			m.chatModel.Messages[i].content = toolResultSummary(msg.content)
			break
		}
	}
	m.refreshDiffStats()
	return m, waitForAgent(m.agentCh)
}

// handleAgentSubEvent processes an agentSubEventMsg.
func (m *model) handleAgentSubEvent(msg agentSubEventMsg) (tea.Model, tea.Cmd) {
	m.matrix.feed(msg.kind+msg.content, m.mainWidth())
	if msg.kind == "spawn" {
		// Agent IDs from the orchestrator are "<agent-name>-<unix-nano>".
		// Prefer matching the spawn to an unassigned card whose agentType
		// matches the name prefix; fall back to the first unassigned card
		// so legacy single-agent calls still work.
		idx := findUnassignedAgentCard(m.chatModel.Messages, msg.agentID)
		if idx >= 0 {
			m.chatModel.Messages[idx].agentID = msg.agentID
			m.chatModel.Messages[idx].pipelineID = msg.pipelineID
			m.chatModel.Messages[idx].pipelineMode = msg.pipelineMode
			m.chatModel.Messages[idx].pipelineStep = msg.pipelineStep
			m.chatModel.Messages[idx].pipelineTotal = msg.pipelineTotal
		}
	} else {
		for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
			if (m.chatModel.Messages[i].tool == "agent" || m.chatModel.Messages[i].tool == "subagent") && m.chatModel.Messages[i].agentID == msg.agentID {
				evKind := msg.kind
				if evKind == "text_delta" {
					evKind = "text"
				}
				// Merge consecutive text chunks so streaming deltas render as
				// one growing line instead of a stack of one-char rows.
				evs := m.chatModel.Messages[i].agentEvents
				if evKind == "text" && len(evs) > 0 && evs[len(evs)-1].kind == "text" {
					evs[len(evs)-1].content += msg.content
					m.chatModel.Messages[i].agentEvents = evs
				} else {
					m.chatModel.Messages[i].agentEvents = append(evs, agentEv{
						kind:    evKind,
						content: msg.content,
					})
				}
				break
			}
		}
	}
	m.chatModel.Scroll = 0
	return m, waitForSubEvent(m.cfg.AgentEventCh)
}

// handleAgentDone processes an agentDoneMsg.
func (m *model) handleAgentDone(msg agentDoneMsg) (tea.Model, tea.Cmd) {
	m.running = false
	m.agentCancel = nil
	m.matrix.clear()
	m.statusModel.ActiveTool = ""
	m.statusModel.ActiveTools = nil
	if msg.err != nil {
		if m.face != nil {
			m.face.SetMood(MoodSad)
		}
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: fmt.Sprintf("Error: %v", msg.err),
		})
		m.chatModel.TraceLog = append(m.chatModel.TraceLog, traceEntry{
			time: time.Now(), kind: "error", summary: "Error", detail: msg.err.Error(),
		})
	} else {
		if m.face != nil {
			m.face.SetMood(MoodHappy)
		}
	}
	m.chatModel.Streaming = ""
	m.chatModel.Thinking = ""
	m.agentCh = nil
	m.refreshDiffStats()
	return m, nil
}
