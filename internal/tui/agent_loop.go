package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/otel"
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

// agentWarningMsg carries a non-fatal problem with the turn into the
// transcript. The turn keeps running; the user just needs to know the reply
// they are reading is not the whole reply.
type agentWarningMsg struct{ text string }

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
func (agentWarningMsg) agentMsg()    {}
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

// systemNoticeMsg carries a short system message into the chat transcript.
type systemNoticeMsg struct{ text string }

// waitForSystemNotice blocks on the notice channel and delivers the next
// message. Re-armed after each delivery, like waitForSubEvent.
func waitForSystemNotice(ch <-chan string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		text, ok := <-ch
		if !ok {
			return nil
		}
		return systemNoticeMsg{text: text}
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

	// Auto-set the session title from the first line of the user prompt and
	// emit OSC 0 to update the terminal window/tab title. Best-effort: a
	// session service that doesn't support titles (or a non-TTY stdout) is
	// a no-op, never a turn blocker.
	m.applySessionTitle(text)

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

// applySessionTitle derives a short title from the user prompt, records it on
// the session via the agent, and stores it on the model so the next View()
// carries it to the terminal as the window/tab title. It is safe to call with
// empty text (no-op) or when the agent is nil (the TUI's unit tests don't wire
// one).
func (m *model) applySessionTitle(prompt string) {
	// Skip the persist+OSC step if the derived title is empty, but always run
	// deriveSessionTitle so the first-line / trim rules are shared with the
	// /title command.
	_ = m.setSessionTitle(deriveSessionTitle(prompt))
}

// setSessionTitle is the shared primitive for "make this string the session
// title now". It folds to a single line, updates the in-memory title, and
// forwards to the agent (when wired) so the title is persisted to the session
// metadata. Pass "" to clear the title back to the app default; an all-whitespace
// or all-control input also resolves to "". Returns the effective title that
// was applied (empty when cleared), which the caller can echo to the user.
//
// Errors from the agent's SetSessionTitle are deliberately swallowed: titles
// are metadata, and a service that doesn't support them (e.g. ADK in-memory)
// must not block a turn or a slash command.
func (m *model) setSessionTitle(text string) string {
	title := strings.TrimSpace(text)
	if i := strings.IndexByte(title, '\n'); i >= 0 {
		title = strings.TrimSpace(title[:i])
	}
	// Update the model field unconditionally so /clear, the auto-derive path,
	// and /title all funnel through the same View() → formatTerminalTitle
	// pipeline (which sanitizes C0 controls for the OSC 0 envelope).
	m.sessionTitle = title
	if m.cfg.Agent != nil && m.cfg.SessionID != "" {
		_ = m.cfg.Agent.SetSessionTitle(m.cfg.SessionID, title)
	}
	return title
}

// runAgentLoop runs the agent and sends events to the channel.
func (m *model) runAgentLoop(ctx context.Context, prompt string) {
	defer close(m.agentCh)
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			// The session log, not stderr: the TUI holds the alternate
			// screen, so a stack trace printed here would be painted over
			// the UI. The panic still reaches the user as the turn's error.
			m.cfg.Logger.Errorf("agent loop panicked: %v\n%s", r, stack)
			m.agentCh <- agentDoneMsg{err: fmt.Errorf("agent panic: %v", r)}
		}
	}()

	// Guard against missing agent config (unit tests)
	if m.cfg.Agent == nil {
		m.agentCh <- agentDoneMsg{err: fmt.Errorf("agent not configured")}
		return
	}

	log := m.cfg.Logger
	detector := &agent.StuckDetector{}

	// Providers like ollama/minimax emit per-token partial events AND a final
	// aggregate containing the whole turn; forwarding both duplicates the text
	// on screen (observed as "I'll spawn...I'll spawn...").
	var dedup agent.StreamDedup

	// GroundingMetadata is repeated on every streamed chunk of the response it
	// grounds, so the same search would otherwise print once per chunk. Key on
	// the query set and emit each search exactly once per turn.
	groundedSeen := map[string]bool{}

	// Warn once per turn, however many events carry the finish reason.
	truncated := false

	// Start a top-level OTEL span for the entire agent run, inheriting the
	// per-response context so Esc/Ctrl+C can interrupt it without quitting the TUI.
	tracer := otel.Tracer("pi-go")
	ctx, span := tracer.Start(ctx, "agent.prompt")
	defer span.End()
	span.SetAttributes(
		otel.AttributeInt("prompt.length", len(prompt)),
	)

	// Every exit below reports through fail, so the "tell the user, then stop"
	// pair can't drift apart. Logger methods are nil-safe (logger.Log guards a
	// nil receiver), so no call site needs to check.
	fail := func(err error) {
		log.Error(err.Error())
		m.agentCh <- agentDoneMsg{err: err}
	}

	for ev, err := range m.cfg.Agent.RunStreaming(ctx, m.cfg.SessionID, prompt) {
		if err != nil {
			fail(err)
			return
		}
		if ev == nil {
			continue
		}
		// Gemini search grounding runs server-side: it never produces a
		// FunctionCall part, so without this the search is invisible — the
		// model just answers with fresh facts and no sign it searched. The only
		// evidence is GroundingMetadata riding on the response, so surface it as
		// a synthetic tool call/result pair. Checked before the Content
		// nil-guard, since the metadata hangs off the event, not the content.
		m.emitGroundingEvents(ev.GroundingMetadata, groundedSeen, log)

		// A provider failure is a content-less event, so it has to be caught
		// before the guard below drops it. See agent.EventError.
		if evErr := agent.EventError(ev); evErr != nil {
			fail(evErr)
			return
		}

		// A turn cut short at the output cap is otherwise indistinguishable
		// from a complete one: the finish reason was mapped by every provider
		// and then read by nobody, so a truncated reply just looked short.
		if ev.FinishReason == genai.FinishReasonMaxTokens && !truncated {
			truncated = true
			m.agentCh <- agentWarningMsg{
				text: "Response truncated: the model hit its output-token limit.",
			}
		}

		if ev.Content == nil {
			continue
		}
		dedup.BeginEvent(ev)
		if abortErr := m.emitEventParts(ev, &dedup, detector, log); abortErr != nil {
			fail(abortErr)
			return
		}
	}
}

// emitEventParts forwards one event's parts to the TUI channel. It returns a
// non-nil error when the stuck detector has seen enough repetition to call the
// run dead, in which case the caller must stop iterating.
func (m *model) emitEventParts(
	ev *session.Event,
	dedup *agent.StreamDedup,
	detector *agent.StuckDetector,
	log *logger.Logger,
) error {
	for _, part := range ev.Content.Parts {
		switch {
		case part.Text != "" && ev.Content.Role == "thinking":
			log.Thinking(ev.Author, part.Text)
			m.agentCh <- agentThinkingMsg{text: part.Text}
			if err := agent.StuckErr(detector.ObserveOutput(part.Text)); err != nil {
				return err
			}

		case part.Text != "":
			if dedup.SkipText(ev) {
				continue // aggregate re-send; deltas already went out
			}
			log.LLMText(ev.Author, part.Text)
			m.agentCh <- agentTextMsg{text: part.Text}
			if err := agent.StuckErr(detector.ObserveOutput(part.Text)); err != nil {
				return err
			}
		}

		if fc := part.FunctionCall; fc != nil {
			// Emit the tool call first so the user sees the offending call
			// before the loop aborts. The stuck-detector threshold still
			// fires after `maxRepeatToolCalls` observations, so the abort
			// semantics are unchanged — only the message ordering moves.
			log.ToolCall(ev.Author, fc.Name, fc.Args)
			m.agentCh <- agentToolCallMsg{name: fc.Name, args: fc.Args}

			if err := agent.StuckErr(detector.Observe(fc.Name, fc.Args)); err != nil {
				return err
			}
		}

		if fr := part.FunctionResponse; fr != nil {
			respJSON, _ := json.Marshal(fr.Response)
			log.ToolResult(ev.Author, fr.Name, string(respJSON))
			m.agentCh <- agentToolResultMsg{name: fr.Name, content: string(respJSON)}

			// A changed result on a repeated call is progress, not a loop
			// (a poll returning fresh output) — let it reset the
			// identical-call streak before the next call is observed.
			detector.ObserveResult(fr.Name, fr.Response)

			// Track per-tool error streaks: ADK wraps tool errors as
			// map[string]any{"error": ...}. Anything else (including a
			// missing key) is treated as success and resets the streak.
			_, isErr := fr.Response["error"]
			if err := agent.StuckErr(detector.ObserveError(fr.Name, isErr)); err != nil {
				return err
			}
		}
	}
	return nil
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

// handleAgentWarning processes an agentWarningMsg.
func (m *model) handleAgentWarning(msg agentWarningMsg) (tea.Model, tea.Cmd) {
	m.chatModel.AppendWarning(msg.text)
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
	// toolResultSummary exists to condense raw tool JSON into one line. The
	// grounding result is not raw output — it is already a formatted, one-source-
	// per-line list — so summarizing it would flatten the newlines into spaces
	// and truncate at 120 chars, which ran every source together and cut the last
	// one mid-word.
	content := msg.content
	if msg.name != groundingToolName {
		content = toolResultSummary(msg.content)
	}
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].role == "tool" && m.chatModel.Messages[i].tool == msg.name && m.chatModel.Messages[i].content == "" {
			m.chatModel.Messages[i].content = content
			break
		}
	}
	m.refreshDiffStats()
	return m, waitForAgent(m.agentCh)
}

// bashEventPrefix namespaces live shell-command events on the same channel the
// subagent stream uses, so a command's output cannot be mistaken for a
// subagent's.
const bashEventPrefix = "bash:"

// maxLiveBashEvents caps the events retained per running command. Only the last
// few are ever drawn, and a command that prints for two minutes would otherwise
// grow this without bound behind a window that shows five lines.
const maxLiveBashEvents = 64

// handleBashEvent routes one live event from a running shell command to its
// card.
//
// Binding is by command text on the "start" event, not by position: the model
// can issue several bash calls in one turn, and matching "the most recent bash
// card" would interleave two commands' output into one card.
func (m *model) handleBashEvent(msg agentSubEventMsg) (tea.Model, tea.Cmd) {
	kind := strings.TrimPrefix(msg.kind, bashEventPrefix)

	if kind == "start" {
		if idx := findUnassignedBashCard(m.chatModel.Messages, msg.content); idx >= 0 {
			m.chatModel.Messages[idx].agentID = msg.agentID
		}
		return m, waitForSubEvent(m.cfg.AgentEventCh)
	}

	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].tool != "bash" || m.chatModel.Messages[i].agentID != msg.agentID {
			continue
		}
		evs := append(m.chatModel.Messages[i].agentEvents, agentEv{kind: kind, content: msg.content})
		if len(evs) > maxLiveBashEvents {
			evs = evs[len(evs)-maxLiveBashEvents:]
		}
		m.chatModel.Messages[i].agentEvents = evs
		break
	}
	m.chatModel.Scroll = 0
	return m, waitForSubEvent(m.cfg.AgentEventCh)
}

// findUnassignedBashCard returns the newest bash card still waiting for a
// command to claim it, preferring an exact command match.
func findUnassignedBashCard(messages []message, command string) int {
	fallback := -1
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.tool != "bash" || msg.agentID != "" || msg.content != "" {
			continue
		}
		if msg.toolIn == command {
			return i
		}
		if fallback == -1 {
			fallback = i
		}
	}
	return fallback
}

// handleAgentSubEvent processes an agentSubEventMsg.
func (m *model) handleAgentSubEvent(msg agentSubEventMsg) (tea.Model, tea.Cmd) {
	if strings.HasPrefix(msg.kind, bashEventPrefix) {
		return m.handleBashEvent(msg)
	}
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
		m.chatModel.AppendError(fmt.Sprintf("Error: %v", msg.err))
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
	// Every terminal outcome completes the turn, but only a successful turn has
	// returned to an input-ready state. Do not tell lifecycle consumers that a
	// failed or canceled run is awaiting input.
	m.runLifecycleHooks("turn_complete", map[string]any{
		"error": msg.err != nil,
	})
	if msg.err == nil {
		m.runLifecycleHooks("user_input_required", map[string]any{})
	}
	return m, nil
}

// runLifecycleHooks fires every configured lifecycle hook for the given event,
// passing the event name and data as JSON on stdin. Hooks are best-effort and
// must stay invisible to the TUI, which owns the terminal:
//
//   - Each hook runs on its own goroutine. This is called from Update, and a
//     hook that hangs would otherwise freeze the render loop for its whole
//     timeout (10s by default, per configured hook).
//   - A failure goes to the session log file, never to stderr. Writing to
//     stderr paints raw text over the alternate screen — the Bubble Tea
//     renderer does not know those cells were touched, so the damage persists
//     until a full redraw.
//
// The child's own stdout and stderr are already captured by RunLifecycleHook
// and never reach the terminal.
func (m *model) runLifecycleHooks(event string, data map[string]any) {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	log := m.cfg.Logger
	for _, h := range m.cfg.LifecycleHooks {
		if h.Event != event {
			continue
		}
		m.enqueueHook(func() {
			if err := extension.RunLifecycleHook(ctx, h, event, data); err != nil {
				log.Errorf("lifecycle hook %q failed for event %q: %v", h.Command, event, err)
			}
		})
	}
}

// hookQueueDepth bounds the backlog of pending lifecycle hooks. A hook that
// blocks for its full timeout while turns keep completing must not grow an
// unbounded queue, so submissions past this depth are dropped and logged.
const hookQueueDepth = 32

// enqueueHook hands a hook to the model's single hook worker, which runs
// submissions one at a time in the order they were queued. Serializing matters
// because the events describe a state machine: turn_complete then
// user_input_required means "done, now waiting", and a hook that maps those
// onto an external status (agtermctl, a notifier) would settle on the wrong
// final state if the two raced.
//
// Only ever called from Update, so the lazy channel init needs no lock.
func (m *model) enqueueHook(fn func()) {
	if m.hookQueue == nil {
		m.hookQueue = make(chan func(), hookQueueDepth)
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		go func(q <-chan func()) {
			// The queue is never closed — the worker's exit signal is the
			// session context, so a drained queue is not a shutdown.
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-q:
					job()
				}
			}
		}(m.hookQueue)
	}
	select {
	case m.hookQueue <- fn:
	default:
		m.cfg.Logger.Errorf("lifecycle hook dropped: queue full (%d pending)", hookQueueDepth)
	}
}
