package tui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	stdlog "log"
	"runtime/debug"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	// maxRepeatToolCalls is the number of identical consecutive tool calls
	// before the loop is considered stuck and aborted.
	maxRepeatToolCalls = 10

	// recentWindowSize is the sliding window of tool-call fingerprints kept
	// for repetition detection.
	recentWindowSize = 12
)

// stuckDetector tracks recent tool calls and detects repetition loops.
type stuckDetector struct {
	recent    []string // ring of fingerprints (len <= recentWindowSize)
	lastPrint string   // fingerprint of last tool call
	streak    int      // consecutive identical tool calls
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

// detectCycle checks the recent window for repeating subsequences.
// Returns a description if found, empty string otherwise.
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
	m.cancel()
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
			for range ch {
			}
		}(m.agentCh)
		m.agentCh = nil
	}
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

	m.agentCh = make(chan agentMsg, 64)
	m.matrix.feed("init", m.mainWidth())
	go m.runAgentLoop(promptText)

	return m, tea.Batch(waitForAgent(m.agentCh), matrixTickCmd())
}

// runAgentLoop runs the agent and sends events to the channel.
func (m *model) runAgentLoop(prompt string) {
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

	for ev, err := range m.cfg.Agent.RunStreaming(m.ctx, m.cfg.SessionID, prompt) {
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
		for _, part := range ev.Content.Parts {
			if part.Text != "" && ev.Content.Role == "thinking" {
				m.agentCh <- agentThinkingMsg{text: part.Text}
				continue
			}
			if part.Text != "" {
				if log != nil {
					log.LLMText(ev.Author, part.Text)
				}
				m.agentCh <- agentTextMsg{text: part.Text}
			}
			if part.FunctionCall != nil {
				// Check for stuck/repeating tool calls.
				if stuck, detail := detector.observe(part.FunctionCall.Name, part.FunctionCall.Args); stuck {
					if log != nil {
						log.Error("agent loop stuck: " + detail)
					}
					m.agentCh <- agentDoneMsg{
						err: fmt.Errorf("agent loop aborted: %s", detail),
					}
					return
				}

				if log != nil {
					log.ToolCall(ev.Author, part.FunctionCall.Name, part.FunctionCall.Args)
				}
				m.agentCh <- agentToolCallMsg{
					name: part.FunctionCall.Name,
					args: part.FunctionCall.Args,
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
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		if m.chatModel.Messages[i].role == "assistant" {
			m.chatModel.Messages[i].content = m.chatModel.Streaming
			break
		}
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
		agentType, _ := msg.args["type"].(string)
		if agentType == "" {
			agentType, _ = msg.args["agent"].(string)
		}
		newMsg.agentType = agentType
		prompt, _ := msg.args["prompt"].(string)
		if prompt == "" {
			prompt, _ = msg.args["task"].(string)
		}
		if idx := strings.IndexByte(prompt, '\n'); idx > 0 {
			prompt = prompt[:idx]
		}
		if len(prompt) > 60 {
			prompt = prompt[:57] + "..."
		}
		newMsg.agentTitle = prompt
	}
	m.chatModel.Messages = append(m.chatModel.Messages, newMsg)
	return m, waitForAgent(m.agentCh)
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
		for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
			if (m.chatModel.Messages[i].tool == "agent" || m.chatModel.Messages[i].tool == "subagent") && m.chatModel.Messages[i].agentID == "" {
				m.chatModel.Messages[i].agentID = msg.agentID
				m.chatModel.Messages[i].pipelineID = msg.pipelineID
				m.chatModel.Messages[i].pipelineMode = msg.pipelineMode
				m.chatModel.Messages[i].pipelineStep = msg.pipelineStep
				m.chatModel.Messages[i].pipelineTotal = msg.pipelineTotal
				break
			}
		}
	} else {
		for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
			if (m.chatModel.Messages[i].tool == "agent" || m.chatModel.Messages[i].tool == "subagent") && m.chatModel.Messages[i].agentID == msg.agentID {
				evKind := msg.kind
				if evKind == "text_delta" {
					evKind = "text"
				}
				m.chatModel.Messages[i].agentEvents = append(m.chatModel.Messages[i].agentEvents, agentEv{
					kind:    evKind,
					content: msg.content,
				})
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
