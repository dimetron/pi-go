package tui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
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
	"github.com/dimetron/pi-go/internal/retry"
)

const (
	// maxRepeatToolCalls is the number of identical consecutive tool calls
	// before the loop is considered stuck and aborted. A repeated call whose
	// result changes resets the count (see observeResult): polling tools like
	// bash_wait repeat identical args by design, and only identical results
	// make the repetition meaningless.
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

	// maxOutputRepeats is the number of back-to-back copies of one phrase in
	// the model's own output before the turn is called degenerate.
	maxOutputRepeats = 12

	// outputWindowBytes is the rolling tail of streamed output kept for
	// repetition detection. It has to hold maxOutputRepeats copies of the
	// longest phrase worth catching (~680 bytes).
	outputWindowBytes = 8192

	// outputProbeBytes is the suffix matched against earlier output to find
	// the length of the phrase being repeated.
	outputProbeBytes = 48

	// minOutputPeriod is the shortest phrase treated as a repetition unit.
	// Below this, ordinary output (indentation, ASCII art) is periodic often
	// enough to matter.
	minOutputPeriod = 16

	// minPeriodVariety is how many distinct bytes the repeating phrase must
	// contain. A rule of dashes or a run of spaces is perfectly periodic and
	// perfectly harmless; a sentence the model cannot stop restating is not.
	minPeriodVariety = 8

	// outputCheckEvery is how much new output accumulates between scans.
	// Scanning per token would put a linear search in the streaming path.
	outputCheckEvery = 512

	// maxStuckRecoveries is how many times a stuck turn is handed back to the
	// model with the detector's reason before the run is ended for real.
	// Deliberately small: repetition that survives being named is not going to
	// be fixed by naming it a third time, and each attempt costs a whole turn.
	maxStuckRecoveries = 2
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
	recent     []string // ring of fingerprints (len <= recentWindowSize)
	lastPrint  string   // fingerprint of last tool call
	lastName   string   // tool name behind lastPrint
	lastResult string   // fingerprint of the streak's previous result ("" = none yet)
	streak     int      // consecutive identical tool calls with identical results
	// Two error detectors run in parallel. The args-aware streak keys on the
	// call fingerprint, so only the same tool call failing repeatedly counts;
	// the name-only streak keys on the tool name and compounds only across
	// distinct model messages, so a batch of different calls in one message is
	// a single attempt rather than a streak. See observeError.
	callInfo       map[string]callRecord // call ID -> fingerprint + message, for response correlation
	lastCallByName map[string]callRecord // name -> most recent call, fallback when a response carries no/unknown ID
	msgSeq         int                   // per-event counter; calls in the same event share it
	lastErrFP      string                // fingerprint of the previous error (args-aware streak)
	errFPStreak    int                   // consecutive errors of the same call fingerprint
	lastErrTool    string                // name of last tool that errored
	errStreak      int                   // consecutive errors of that tool across distinct messages
	lastErrBatch   int                   // message sequence of the call the last error answered
	outBuf         string                // rolling tail of the model's own output
	outSince       int                   // bytes of output since the last repetition scan
}

// callRecord is what the detector remembers about an observed tool call so a
// later response can be correlated back to it: the call's fingerprint (for the
// args-aware error streak) and the message it appeared in (for the name-only
// cross-batch streak).
type callRecord struct {
	fp  string
	msg int
}

// volatileToolArgs address a slice of a target rather than the target itself.
// A model paging through one file — read(x, offset 1), read(x, offset 230),
// read(x, offset 240) — is repeating itself, but hashing the raw args makes
// every one of those calls unique and hides the loop from the detector.
var volatileToolArgs = map[string]bool{
	"offset":     true,
	"limit":      true,
	"head_limit": true,
	"start_line": true,
	"end_line":   true,
}

// toolFingerprint produces a short hash of a tool call for comparison.
// Pagination arguments are dropped first, so re-reading one file region by
// region collapses to a single fingerprint. json.Marshal sorts map keys, so
// the hash does not depend on argument order.
func toolFingerprint(name string, args map[string]any) string {
	h := sha256.New()
	h.Write([]byte(name))
	b, _ := json.Marshal(stableToolArgs(args))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// stableToolArgs returns args without the volatile keys. The input map belongs
// to the event being streamed, so it is copied rather than filtered in place.
func stableToolArgs(args map[string]any) map[string]any {
	stable := make(map[string]any, len(args))
	for k, v := range args {
		if volatileToolArgs[k] {
			continue
		}
		stable[k] = v
	}
	return stable
}

// beginEvent advances the message counter so calls in one model event share a
// batch identity. Responses later use it to tell a single batch of calls from
// repeated attempts spread across turns.
func (s *stuckDetector) beginEvent() {
	s.msgSeq++
}

// observe records a tool call and returns true if the loop appears stuck.
// id correlates the call to its later response (FunctionCall.ID). Calls with
// an empty id cannot be correlated by ID, so they are remembered only by name
// (see lastCallByName); observeError uses that fallback so a batch of ID-less
// calls still reads as one message.
func (s *stuckDetector) observe(id, name string, args map[string]any) (stuck bool, detail string) {
	fp := toolFingerprint(name, args)
	rec := callRecord{fp: fp, msg: s.msgSeq}
	if id != "" {
		if s.callInfo == nil {
			s.callInfo = make(map[string]callRecord)
		}
		s.callInfo[id] = rec
	}
	if s.lastCallByName == nil {
		s.lastCallByName = make(map[string]callRecord)
	}
	s.lastCallByName[name] = rec

	// Consecutive identical call detection.
	if fp == s.lastPrint {
		s.streak++
	} else {
		s.streak = 1
		s.lastPrint = fp
		s.lastName = name
		s.lastResult = ""
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

// observeResult records a tool call's response. Polling tools repeat identical
// calls by design — bash_wait on a running command sends the same handle
// every time and gets fresh output back — so a response that differs from the
// streak's previous response is progress, and resets the identical-call
// streak. A response identical to the last one keeps the streak counting:
// re-polling a finished command or re-reading an unchanged file is genuine
// repetition. A stuck loop whose responses vary trivially (timestamps) escapes
// this detector, but observeError and observeOutput still cover that shape.
func (s *stuckDetector) observeResult(name string, response map[string]any) {
	if name != s.lastName {
		return
	}
	// bash_wait includes elapsed/idle fields that change on every poll even
	// when the command produced no new output. Those fields are progress for the
	// UI, not progress from the command, so exclude them from loop detection.
	stable := response
	if isBashPoll(name) {
		stable = make(map[string]any, len(response))
		for key, value := range response {
			if key != "elapsed" && key != "idle" {
				stable[key] = value
			}
		}
	}
	b, _ := json.Marshal(stable)
	sum := sha256.Sum256(b)
	fp := hex.EncodeToString(sum[:])[:16]
	if s.lastResult != "" && fp != s.lastResult {
		s.streak = 0
	}
	s.lastResult = fp
}

// observeError records the outcome of a tool call and reports whether the loop
// is stuck. Two streaks run in parallel, catching different loop shapes:
//
//  1. Args-aware (errFPStreak): the same call — same tool and same argument
//     fingerprint — failing maxToolErrorStreak times. A model stuck re-issuing
//     one call trips here no matter how many messages pass.
//
//  2. Name-only, cross-batch (errStreak): consecutive failures of the same
//     tool name that come from calls in *different* model messages. A batch of
//     distinct calls sent in one message (e.g. fetching pricing for ten
//     models in a single turn) is one attempt, not ten, so it does not
//     compound; the same tool failing once per message across ten messages is
//     the flailing pattern and does.
//
// A success (isError == false) resets both streaks. id is the FunctionResponse
// ID, used to look up which call — and therefore which message — this result
// answers.
func (s *stuckDetector) observeError(id, name string, isError bool) (stuck bool, detail string) {
	// Correlate this response to the call it answers. By ID first: a matched
	// record gives the call's fingerprint and message, and is consumed so the
	// map stays bounded by outstanding (unanswered) calls. When the response
	// carries no usable ID, fall back to the most recent call of that name —
	// responses trail their calls in order, so this is the call being answered.
	// Only the batch is trusted from the name fallback: without an ID we cannot
	// pair a response to a specific call's arguments, so the args-aware streak
	// must not see a fingerprint — a batch of distinct ID-less calls would all
	// collapse onto the last call's fingerprint and false-abort.
	fp := ""
	batch := 0
	if rec, ok := s.callInfo[id]; ok {
		fp, batch = rec.fp, rec.msg
		delete(s.callInfo, id)
	} else if rec, ok := s.lastCallByName[name]; ok {
		batch = rec.msg
	}

	if isError {
		if fp != "" && fp == s.lastErrFP {
			s.errFPStreak++
		} else {
			s.errFPStreak = 1
			s.lastErrFP = fp
		}
		if s.errFPStreak >= maxToolErrorStreak {
			return true, fmt.Sprintf("tool %q failed %d times in a row", name, s.errFPStreak)
		}

		// Name-only streak. Compound only when this failure answers a call
		// from a different message than the previous one (or from an
		// unknown message — treat unobserved calls as new attempts).
		if name == s.lastErrTool {
			if s.lastErrBatch == 0 || batch != s.lastErrBatch {
				s.errStreak++
				s.lastErrBatch = batch
			}
		} else {
			s.errStreak = 1
			s.lastErrTool = name
			s.lastErrBatch = batch
		}
		if s.errStreak >= maxToolErrorStreak {
			return true, fmt.Sprintf("tool %q failed %d times in a row", name, s.errStreak)
		}
		return false, ""
	}

	s.errFPStreak = 0
	s.lastErrFP = ""
	s.errStreak = 0
	s.lastErrTool = ""
	s.lastErrBatch = 0
	return false, ""
}

// observeOutput records a chunk of the model's own output — reply text or
// thinking — and reports whether the turn has collapsed into repetition.
//
// The detectors above watch tool calls, so a turn that makes no calls at all is
// invisible to them. That is exactly the shape of a degenerate turn: one
// sentence restated until the output cap is hit, with nothing else emitted.
// This scans the tail of the stream for a repeating period instead.
func (s *stuckDetector) observeOutput(text string) (stuck bool, detail string) {
	if text == "" {
		return false, ""
	}
	s.outBuf += text
	if len(s.outBuf) > outputWindowBytes {
		s.outBuf = s.outBuf[len(s.outBuf)-outputWindowBytes:]
	}

	s.outSince += len(text)
	if s.outSince < outputCheckEvery {
		return false, ""
	}
	s.outSince = 0

	period := repeatPeriod(s.outBuf)
	if period < minOutputPeriod || !isPeriodic(s.outBuf, period, maxOutputRepeats) {
		return false, ""
	}
	if !hasVariety(s.outBuf[len(s.outBuf)-period:]) {
		return false, ""
	}
	return true, fmt.Sprintf("model repeated a %d-character phrase %d times", period, maxOutputRepeats)
}

// repeatPeriod returns the distance between the tail of buf and the previous
// occurrence of that same tail — the length of the phrase the model may be
// cycling on — or 0 when the tail does not recur.
func repeatPeriod(buf string) int {
	if len(buf) < outputProbeBytes*2 {
		return 0
	}
	probe := buf[len(buf)-outputProbeBytes:]
	prev := strings.LastIndex(buf[:len(buf)-outputProbeBytes], probe)
	if prev < 0 {
		return 0
	}
	return len(buf) - outputProbeBytes - prev
}

// hasVariety reports whether unit contains enough distinct bytes to be a
// phrase rather than filler.
func hasVariety(unit string) bool {
	var seen [256]bool
	distinct := 0
	for i := range len(unit) {
		if seen[unit[i]] {
			continue
		}
		seen[unit[i]] = true
		distinct++
		if distinct >= minPeriodVariety {
			return true
		}
	}
	return false
}

// isPeriodic reports whether the last period*repeats bytes of buf are one
// period-long phrase repeated back to back. Comparing bytes is safe for UTF-8
// here: a byte-exact repeat is a rune-exact repeat.
func isPeriodic(buf string, period, repeats int) bool {
	span := period * repeats
	if period <= 0 || span > len(buf) {
		return false
	}
	tail := buf[len(buf)-span:]
	for i := period; i < len(tail); i++ {
		if tail[i] != tail[i-period] {
			return false
		}
	}
	return true
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
	// id is the provider's function-call ID, the only thing that pairs a
	// result with its own call when a turn issues several calls to the same
	// tool at once. Empty when a provider omits it, or for the synthetic
	// grounding pair.
	id   string
	name string
	args map[string]any
}
type agentToolResultMsg struct {
	id      string // matches the agentToolCallMsg.id this answers
	name    string
	content string
}
type agentDoneMsg struct{ err error }

// agentUsageMsg carries the per-turn token usage block to the TUI at the end of
// a successful turn. It arrives after the reply text and before the channel
// close that synthesizes agentDoneMsg, so the summary renders directly under
// the answer.
//
// elapsed is the turn's wall-clock time, measured across the whole agent loop —
// every model call, tool call and stuck recovery — not just the final response.
type agentUsageMsg struct {
	usage   *genai.GenerateContentResponseUsageMetadata
	elapsed time.Duration
}

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
func (agentUsageMsg) agentMsg()      {}
func (agentWarningMsg) agentMsg()    {}
func (agentSubEventMsg) agentMsg()   {}

// addUsage folds one LLM response's usage block into a running per-turn total.
// It returns dst unchanged when src is nil, so callers need no nil check. A
// turn is one or more model calls (tool loops, stuck recoveries), and only
// final responses carry non-nil usage, so summing yields the turn's true token
// spend rather than the last response's.
func addUsage(dst, src *genai.GenerateContentResponseUsageMetadata) *genai.GenerateContentResponseUsageMetadata {
	if src == nil {
		return dst
	}
	if dst == nil {
		cp := *src
		return &cp
	}
	dst.PromptTokenCount += src.PromptTokenCount
	dst.CandidatesTokenCount += src.CandidatesTokenCount
	dst.CachedContentTokenCount += src.CachedContentTokenCount
	dst.ThoughtsTokenCount += src.ThoughtsTokenCount
	dst.TotalTokenCount += src.TotalTokenCount
	return dst
}

// formatTurnUsage renders a per-turn token summary as one dim line: input,
// cached reads, output, reasoning, total, and how long the turn took. It
// returns "" when u is nil or all-zero, so a provider that reports no usage
// shows no line rather than "0 in · 0 out" — the elapsed time rides along with
// the token tally rather than standing on its own.
func formatTurnUsage(u *genai.GenerateContentResponseUsageMetadata, elapsed time.Duration) string {
	if u == nil {
		return ""
	}
	in := int64(u.PromptTokenCount)
	out := int64(u.CandidatesTokenCount)
	cache := int64(u.CachedContentTokenCount)
	total := int64(u.TotalTokenCount)
	if total <= 0 {
		total = in + out + int64(u.ThoughtsTokenCount)
	}
	if in == 0 && out == 0 && cache == 0 &&
		u.ThoughtsTokenCount == 0 && total == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(formatTokenCount(in))
	b.WriteString(" in")
	if cache > 0 {
		b.WriteString(" (")
		b.WriteString(formatTokenCount(cache))
		b.WriteString(" cached)")
	}
	b.WriteString(" · ")
	b.WriteString(formatTokenCount(out))
	b.WriteString(" out")
	if u.ThoughtsTokenCount > 0 {
		b.WriteString(" · ")
		b.WriteString(formatTokenCount(int64(u.ThoughtsTokenCount)))
		b.WriteString(" reasoning")
	}
	b.WriteString(" · ")
	b.WriteString(formatTokenCount(total))
	b.WriteString(" total")
	if elapsed > 0 {
		b.WriteString(" · took ")
		b.WriteString(formatTurnDuration(elapsed))
	}
	return b.String()
}

// formatTurnDuration renders a turn's wall-clock time at a precision that suits
// its magnitude: milliseconds under a second, one decimal of seconds under a
// minute, and whole minutes and hours above that. Sub-second precision on a
// ten-minute turn is noise, and rounding a fast turn to "0s" hides it.
func formatTurnDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int(d/time.Second)%60)
	default:
		return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int(d/time.Minute)%60)
	}
}

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
	ch := make(chan agentMsg, 64)
	m.agentCh = ch
	agentCtx, agentCancel := context.WithCancel(m.ctx)
	m.agentCancel = agentCancel
	go m.runAgentLoop(agentCtx, prompt, ch, m.agentRun())
	return waitForAgent(m.agentCh)
}

// agentRunConfig is the slice of cfg the agent loop needs, read once on the
// Update goroutine and handed over by value.
//
// The loop used to reach through m.cfg for these while it ran. Update writes
// m.cfg from a dozen places — /model swaps LLM, ModelName, ProviderName and
// ActiveRole, /plan replaces SessionID, skill creation rewrites Skills, and
// init assigns Agent and Logger outright — so the loop's read and Update's
// write are two goroutines touching the same words. Nothing structural
// separated them; the only reason it held was that handleModelCommand happens
// to return early while m.running.
//
// Commit 35c3b25 fixed the sibling case, the agent channel, the same way: pass
// what the goroutine needs rather than let it reach for the live struct.
// Grouping the fields into a sub-struct would not have helped — the race is
// about who may read m, not about how m is laid out.
type agentRunConfig struct {
	agent     *agent.Agent
	sessionID string
	logger    *logger.Logger
}

func (m *model) agentRun() agentRunConfig {
	return agentRunConfig{
		agent:     m.cfg.Agent,
		sessionID: m.cfg.SessionID,
		logger:    m.cfg.Logger,
	}
}

type queuedPrompt struct {
	text     string
	mentions []string
}

const maxPendingPrompts = 32

func (m *model) enqueuePrompt(text string, mentions []string) (tea.Model, tea.Cmd) {
	if len(m.pendingPrompts) >= maxPendingPrompts {
		m.flash = "Prompt queue full"
		return m, nil
	}
	m.pendingPrompts = append(m.pendingPrompts, queuedPrompt{text: text, mentions: append([]string(nil), mentions...)})
	return m.startNextPrompt()
}

func (m *model) startNextPrompt() (tea.Model, tea.Cmd) {
	if m.running || len(m.pendingPrompts) == 0 {
		return m, nil
	}
	next := m.pendingPrompts[0]
	m.pendingPrompts = m.pendingPrompts[1:]
	return m.submitPrompt(next.text, next.mentions)
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

// runAgentLoop runs the agent and sends events to the channel. The channel is
// passed in rather than read from m.agentCh: the goroutine outlives the Update
// call that started it, and a subsequent turn's handleAgentDone→startNextPrompt
// chain replaces m.agentCh before this goroutine finishes unwinding (defer
// close). Reading the shared field here would race with that write and, worse,
// could close the next turn's channel. Capturing the channel by value makes
// every send target the channel this turn actually owns.
func (m *model) runAgentLoop(ctx context.Context, prompt string, ch chan agentMsg, run agentRunConfig) {
	defer close(ch)
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			// The session log, not stderr: the TUI holds the alternate
			// screen, so a stack trace printed here would be painted over
			// the UI. The panic still reaches the user as the turn's error.
			run.logger.Errorf("agent loop panicked: %v\n%s", r, stack)
			ch <- agentDoneMsg{err: fmt.Errorf("agent panic: %v", r)}
		}
	}()

	// Guard against missing agent config (unit tests)
	if run.agent == nil {
		ch <- agentDoneMsg{err: fmt.Errorf("agent not configured")}
		return
	}

	log := run.logger

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

	// Surface every retry — the provider re-sending a request that died under
	// it, or WithRetry replaying a run that produced nothing — as a warning in
	// the transcript. Without this the pause reads as the model thinking, and
	// a turn that fails after the budget is spent looks like it failed once.
	ctx = retry.WithNotifier(ctx, func(a retry.Attempt) {
		log.Info(a.String())
		ch <- agentWarningMsg{text: a.String()}
	})

	// Every exit below reports through fail, so the "tell the user, then stop"
	// pair can't drift apart. Logger methods are nil-safe (logger.Log guards a
	// nil receiver), so no call site needs to check.
	fail := func(err error) {
		log.Error(err.Error())
		ch <- agentDoneMsg{err: err}
	}

	// A stuck turn is recoverable: the model gets told what the detector saw and
	// continues in the same session, so the work already done is not thrown
	// away. The budget is small on purpose — repetition that survives being
	// named is not going to be fixed by naming it again, and every attempt
	// costs a full turn.
	var turnUsage *genai.GenerateContentResponseUsageMetadata
	// Started here rather than at the top of the function so the measurement
	// covers the model work and not the setup around it; stuck recoveries are
	// part of the turn the user waited on, so they stay inside the span.
	turnStart := time.Now()
	for attempt := 0; ; attempt++ {
		usage, err := m.streamTurn(ctx, ch, prompt, run, groundedSeen, &truncated, log)
		turnUsage = addUsage(turnUsage, usage)

		var stuck *stuckError
		if !errors.As(err, &stuck) {
			if err == nil && turnUsage != nil {
				ch <- agentUsageMsg{usage: turnUsage, elapsed: time.Since(turnStart)}
			}
			if err != nil {
				fail(err)
			}
			return
		}

		if attempt >= maxStuckRecoveries {
			fail(fmt.Errorf("%w (gave up after %d recovery attempt(s))", err, attempt))
			return
		}

		log.Info(fmt.Sprintf("agent loop stuck (%s); telling the model and resuming", stuck.Detail()))
		ch <- agentWarningMsg{
			text: fmt.Sprintf("Loop detected (%s) — telling the model and resuming (attempt %d of %d).",
				stuck.Detail(), attempt+1, maxStuckRecoveries),
		}
		prompt = recoverStuckPrompt(stuck.Detail())
	}
}

// streamTurn runs one streaming turn and forwards its events. It returns a
// *stuckError when the detectors call the turn dead, any other error when the
// run failed outright, and nil on a clean finish.
//
// The stuck detector and the stream deduper are per-turn state and are rebuilt
// here on every attempt: carrying the previous attempt's output window across a
// recovery would re-trip the guard on text the model has already been told to
// stop producing.
func (m *model) streamTurn(
	ctx context.Context,
	ch chan agentMsg,
	prompt string,
	run agentRunConfig,
	groundedSeen map[string]bool,
	truncated *bool,
	log *logger.Logger,
) (*genai.GenerateContentResponseUsageMetadata, error) {
	detector := &stuckDetector{}
	var dedup agent.StreamDedup
	var turnUsage *genai.GenerateContentResponseUsageMetadata

	// Same retry wrapper the print and RPC front-ends use, so a run that dies
	// before producing anything is replayed here too instead of ending the turn
	// with an error on screen. Mid-turn failures are handled a layer down, in
	// the provider, where a single request can be re-sent without replaying the
	// tool calls that already ran.
	for ev, err := range agent.WithRetryContext(ctx, agent.DefaultRetryConfig(), func() iter.Seq2[*session.Event, error] {
		return run.agent.RunStreaming(ctx, run.sessionID, prompt)
	}) {
		if err != nil {
			return turnUsage, err
		}
		if ev == nil {
			continue
		}
		turnUsage = addUsage(turnUsage, ev.UsageMetadata)
		// Gemini search grounding runs server-side: it never produces a
		// FunctionCall part, so without this the search is invisible — the
		// model just answers with fresh facts and no sign it searched. The only
		// evidence is GroundingMetadata riding on the response, so surface it as
		// a synthetic tool call/result pair. Checked before the Content
		// nil-guard, since the metadata hangs off the event, not the content.
		m.emitGroundingEvents(ch, ev.GroundingMetadata, groundedSeen, log)

		// A provider failure is a content-less event, so it has to be caught
		// before the guard below drops it. See agent.EventError.
		if evErr := agent.EventError(ev); evErr != nil {
			return turnUsage, evErr
		}

		// A turn cut short at the output cap is otherwise indistinguishable
		// from a complete one: the finish reason was mapped by every provider
		// and then read by nobody, so a truncated reply just looked short.
		if ev.FinishReason == genai.FinishReasonMaxTokens && !*truncated {
			*truncated = true
			ch <- agentWarningMsg{
				text: "Response truncated: the model hit its output-token limit.",
			}
		}

		if ev.Content == nil {
			continue
		}
		dedup.BeginEvent(ev)
		if abortErr := m.emitEventParts(ch, ev, &dedup, detector, log); abortErr != nil {
			return turnUsage, abortErr
		}
	}
	return turnUsage, nil
}

// emitEventParts forwards one event's parts to the TUI channel. It returns a
// non-nil error when the stuck detector has seen enough repetition to call the
// run dead, in which case the caller must stop iterating.
func (m *model) emitEventParts(
	ch chan agentMsg,
	ev *session.Event,
	dedup *agent.StreamDedup,
	detector *stuckDetector,
	log *logger.Logger,
) error {
	// Each event is one model message. Calls in the same event share a batch
	// identity so the error detector treats a batch of distinct calls as one
	// attempt rather than a streak (see observeError).
	detector.beginEvent()
	for _, part := range ev.Content.Parts {
		if err := m.emitEventPart(ch, ev, dedup, detector, log, part); err != nil {
			return err
		}
	}
	return nil
}

// emitEventPart forwards one part of an event. Returning nil ends this part and
// moves on to the next, which is what the dedup skip below relies on.
func (m *model) emitEventPart(
	ch chan agentMsg,
	ev *session.Event,
	dedup *agent.StreamDedup,
	detector *stuckDetector,
	log *logger.Logger,
	part *genai.Part,
) error {
	if part.Text != "" {
		skipped, err := m.emitPartText(ch, ev, dedup, detector, log, part.Text)
		if err != nil {
			return err
		}
		if skipped {
			return nil // aggregate re-send; deltas already went out
		}
	}

	if fc := part.FunctionCall; fc != nil {
		// Emit the tool call first so the user sees the offending call
		// before the loop aborts. The stuck-detector threshold still
		// fires after `maxRepeatToolCalls` observations, so the abort
		// semantics are unchanged — only the message ordering moves.
		log.ToolCall(ev.Author, fc.Name, fc.Args)
		ch <- agentToolCallMsg{id: fc.ID, name: fc.Name, args: fc.Args}

		if err := stuckErr(detector.observe(fc.ID, fc.Name, fc.Args)); err != nil {
			return err
		}
	}

	if fr := part.FunctionResponse; fr != nil {
		return m.emitPartResponse(ch, ev, detector, log, fr)
	}
	return nil
}

// emitPartText forwards one part's text, as thinking or as reply text. It
// reports whether the text was the deduplicated aggregate re-send, in which
// case the caller must skip the rest of the part rather than emit it twice.
func (m *model) emitPartText(
	ch chan agentMsg,
	ev *session.Event,
	dedup *agent.StreamDedup,
	detector *stuckDetector,
	log *logger.Logger,
	text string,
) (skipped bool, err error) {
	if ev.Content.Role == "thinking" {
		log.Thinking(ev.Author, text)
		ch <- agentThinkingMsg{text: text}
		return false, stuckErr(detector.observeOutput(text))
	}

	if dedup.SkipText(ev) {
		return true, nil
	}
	log.LLMText(ev.Author, text)
	ch <- agentTextMsg{text: text}
	return false, stuckErr(detector.observeOutput(text))
}

// emitPartResponse forwards one tool result and feeds it to the stuck detector.
func (m *model) emitPartResponse(
	ch chan agentMsg,
	ev *session.Event,
	detector *stuckDetector,
	log *logger.Logger,
	fr *genai.FunctionResponse,
) error {
	respJSON, _ := json.Marshal(fr.Response)
	log.ToolResult(ev.Author, fr.Name, string(respJSON))
	ch <- agentToolResultMsg{id: fr.ID, name: fr.Name, content: string(respJSON)}

	// A changed result on a repeated call is progress, not a loop
	// (a poll returning fresh output) — let it reset the
	// identical-call streak before the next call is observed.
	detector.observeResult(fr.Name, fr.Response)

	// Track per-tool error streaks: ADK wraps tool errors as
	// map[string]any{"error": ...}. Anything else (including a
	// missing key) is treated as success and resets the streak.
	_, isErr := fr.Response["error"]
	return stuckErr(detector.observeError(fr.ID, fr.Name, isErr))
}

// stuckError is a loop abort the run can recover from. It is distinguished
// from a fatal error so runAgentLoop can hand the detail back to the model and
// let it try again, rather than ending the turn on the model's first bad
// stretch. See recoverStuckPrompt.
type stuckError struct{ detail string }

func (e *stuckError) Error() string { return "agent loop aborted: " + e.detail }

// Detail returns the human-readable reason the loop was called dead.
func (e *stuckError) Detail() string { return e.detail }

// stuckErr adapts a stuckDetector verdict into an error, so both detector call
// sites read as a single guard instead of a repeated five-line block.
func stuckErr(stuck bool, detail string) error {
	if !stuck {
		return nil
	}
	return &stuckError{detail: detail}
}

// recoverStuckPrompt is what the model is told after it gets stuck. It names
// the specific failure the detector saw, because "try again" on its own tends
// to produce the same output a second time — the model has no way to know what
// tripped the guard unless it is told.
func recoverStuckPrompt(detail string) string {
	return fmt.Sprintf(
		`Your previous turn was stopped automatically: %s.

That is a loop, not progress, and repeating it will stop the turn again. Do not
continue where you left off and do not restate what you already said.

Change approach:
- If you were repeating text, say the point once and move on.
- If you were repeating a tool call, the call is not going to start working —
  use a different tool, different arguments, or reason from what you already have.
- If you are genuinely blocked, say so plainly and stop, rather than filling
  the turn.

Continue the original task from here.`, detail)
}

// handleAgentThinking processes an agentThinkingMsg.
func (m *model) handleAgentThinking(msg agentThinkingMsg) (tea.Model, tea.Cmd) {
	if m.face != nil {
		m.face.SetMood(MoodThinking)
	}
	m.matrix.feed(msg.text, m.mainWidth())
	// Thinking buffers the open reasoning block only, for the same reason
	// Streaming does in handleAgentText: a tool call between two reasoning
	// blocks starts a new message, and a carried-over buffer would repeat the
	// earlier block inside it.
	if len(m.chatModel.Messages) > 0 && m.chatModel.Messages[len(m.chatModel.Messages)-1].role == "thinking" {
		m.chatModel.Thinking += msg.text
		m.chatModel.Messages[len(m.chatModel.Messages)-1].content = m.chatModel.Thinking
	} else {
		m.chatModel.Thinking = msg.text
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

// handleAgentUsage appends the per-turn token summary to the transcript. A nil
// or all-zero usage block renders nothing, so providers that omit usage are
// indistinguishable from a turn that happened to report zero.
func (m *model) handleAgentUsage(msg agentUsageMsg) (tea.Model, tea.Cmd) {
	if s := formatTurnUsage(msg.usage, msg.elapsed); s != "" {
		m.chatModel.AppendMeta(s)
	}
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
			// The reasoning block becomes the shell for the answer that follows
			// it. That is a brand-new empty message, so the text accumulator has
			// to restart here too — otherwise the block below appends to a
			// buffer left over from the message before the last tool call.
			m.chatModel.Messages[len(m.chatModel.Messages)-1] = message{role: "assistant", content: ""}
			m.chatModel.Streaming = ""
		}
	}
	m.matrix.feed(msg.text, m.mainWidth())
	// Keep chronology stable: only update a trailing assistant message.
	// If the latest message is a tool event, append a new assistant message
	// so rendered order matches event order.
	//
	// Streaming buffers the message currently being rendered, not the whole
	// turn: a tool card closes the open message, so the next text delta starts
	// a fresh block and the accumulator restarts with it. Carrying the buffer
	// across a tool call made every later block re-render every earlier one,
	// glued together without a separator ("...say hi." + "agy ran cleanly").
	if n := len(m.chatModel.Messages); n > 0 && m.chatModel.Messages[n-1].role == "assistant" {
		m.chatModel.Streaming += msg.text
		m.chatModel.Messages[n-1].content = m.chatModel.Streaming
	} else {
		m.chatModel.Streaming = msg.text
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
	handle, _ := msg.args["handle"].(string)
	if isBashControl(msg.name) {
		toolIn = m.bashControlToolIn(msg.name, msg.args, toolIn)
	}
	if isBashPoll(msg.name) {
		if i := findPollCard(m.chatModel.Messages, handle); i >= 0 {
			card := &m.chatModel.Messages[i]
			card.toolID = msg.id
			card.pollCount++
			card.pendingRefresh = true
			return m, waitForAgent(m.agentCh)
		}
	}
	newMsg := message{
		role: "tool", tool: msg.name, toolIn: toolIn, toolID: msg.id,
		// The handle identifies the command this card polls, so the next poll of
		// the same handle can find and refresh this card instead of adding one.
		// Only bash cards use agentID for live-event routing (handleBashEvent
		// filters on tool=="bash"), so reusing the field here cannot cross wires.
		agentID: handle, pollCount: 1,
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

// maxStoredAgentTitle bounds the prompt preview stored on an agent card. It is
// deliberately generous so a wide terminal can show a longer title — the actual
// visible length is decided at render time by agentCardHeader against the
// terminal width. This is only a storage cap, so a pathological prompt cannot
// bloat the message.
const maxStoredAgentTitle = 200

// truncatePrompt shortens a prompt to a single-line preview for the agent card
// header. It strips the prompt to its first line and bounds its length; the
// precise visible length is fixed at render time so a wide terminal gets a
// longer line.
func truncatePrompt(prompt string) string {
	if idx := strings.IndexByte(prompt, '\n'); idx > 0 {
		prompt = prompt[:idx]
	}
	if len(prompt) > maxStoredAgentTitle {
		prompt = prompt[:maxStoredAgentTitle-3] + "..."
	}
	return prompt
}

// handleAgentToolResult processes an agentToolResultMsg.
func (m *model) handleAgentToolResult(msg agentToolResultMsg) (tea.Model, tea.Cmd) {
	m.invalidatePlanPhases()
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
	if i := matchToolResultCard(m.chatModel.Messages, msg.id, msg.name); i >= 0 {
		m.chatModel.Messages[i].content = content
		m.chatModel.Messages[i].pendingRefresh = false
		// Bind the card to its background handle when the bash result carries
		// one. The bash:start event that normally stamps agentID travels on a
		// separate channel and can arrive before the card exists, dropping the
		// binding; the result is the reliable place to recover it so a later
		// bash_wait/bash_kill card can still find the command.
		if msg.name == "bash" {
			if handle := bashHandleFromResult(msg.content); handle != "" {
				m.chatModel.Messages[i].agentID = handle
			}
		}
		// A poll names its command in its own result, so a card that had to
		// settle for a bare handle at call time can say what it is polling as
		// soon as the answer lands. bashControlToolIn can only find the command
		// when the bash card that started it is still in this transcript; after a
		// /resume, a compaction, or a lost bash:start binding it is not, and the
		// header read "bash_wait(bg_4)" for the rest of the command's life.
		if isBashControl(msg.name) {
			if header := bashControlHeaderFromResult(msg.content); header != "" {
				m.chatModel.Messages[i].toolIn = header
			}
		}
	}
	m.refreshDiffStats()
	return m, waitForAgent(m.agentCh)
}

// bashControlHeaderFromResult builds the "handle: command" header for a
// bash_wait/bash_kill card out of the poll's own result, which carries both
// (BashStatus.Command). Returns "" when either is missing, so the header the
// card already has survives.
//
// The command is collapsed to one line: a header is one line by construction,
// and a backgrounded heredoc or a multi-line pipeline would otherwise write its
// newlines straight into the card and knock the rail out of its column.
func bashControlHeaderFromResult(content string) string {
	var data map[string]any
	if json.Unmarshal([]byte(content), &data) != nil {
		return ""
	}
	handle, _ := data["handle"].(string)
	command, _ := data["command"].(string)
	if handle == "" || command == "" {
		return ""
	}
	return handle + ": " + collapseToSingleLine(command)
}

// bashHandleFromResult extracts the background handle from a raw bash tool
// result, or "" when the command finished in the foreground (no handle).
func bashHandleFromResult(content string) string {
	var data map[string]any
	if json.Unmarshal([]byte(content), &data) != nil {
		return ""
	}
	h, _ := data["handle"].(string)
	return h
}

// findPollCard returns the index of the card a repeated bash_wait poll should
// refresh in place, or -1 when the poll deserves a card of its own.
//
// The rule is deliberately narrow: only the newest card in the transcript
// qualifies, and only when it polls the same handle. Folding into an older card
// would move fresh output above whatever the model did in between — a card that
// jumps up the scrollback is worse than a duplicate one — and folding across
// handles would merge two commands' windows. Anything the model says or does
// between two polls therefore starts a new card, which is right: that text is
// what makes the second poll a separate beat rather than a repeat.
func findPollCard(messages []message, handle string) int {
	if handle == "" || len(messages) == 0 {
		return -1
	}
	i := len(messages) - 1
	last := messages[i]
	if last.role != "tool" || !isBashPoll(last.tool) || last.agentID != handle {
		return -1
	}
	return i
}

// matchToolResultCard finds the chat card an arriving tool result belongs to,
// or -1 when none is waiting for it.
//
// The call ID is what makes this correct. A model turn routinely emits several
// FunctionCall parts for the same tool in one response — two `read`s, three
// `bash`es, six `edit`s are all ordinary — and ADK runs them concurrently and
// answers with one merged event carrying every response. Picking "the newest
// same-named card with no content yet" therefore handed the first result to the
// last card and the last result to the first, so the user read each call's
// output under the other call's arguments.
//
// Name matching survives as a fallback for cards that carry no ID: the
// synthetic grounding pair, restored transcripts written before IDs were kept,
// and any provider that leaves FunctionCall.ID empty. Unidentified cards are
// preferred there so an ID-less result cannot steal a card that belongs to an
// identified call, but an identified card is still accepted as a last resort
// rather than leaving the result unrendered.
func matchToolResultCard(messages []message, id, name string) int {
	if id != "" {
		if i, claimed := matchToolCardByID(messages, id); i >= 0 || claimed {
			return i
		}
	}
	return matchToolCardByName(messages, name)
}

// matchToolCardByID finds the card waiting on call id, newest first. It reports
// claimed when a card for that id exists but already holds its result — a
// duplicate re-send, which must be dropped rather than fall through to name
// matching and spill onto a different call's card. (-1, false) means no card
// carries the id at all.
func matchToolCardByID(messages []message, id string) (idx int, claimed bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].role != "tool" || messages[i].toolID != id {
			continue
		}
		// pendingRefresh: a repeated poll folded into this card and its
		// result is the one arriving now. The card still shows the previous
		// poll's window — that is the point, it keeps the card from blanking
		// while the poll runs — so the non-empty content must not read as
		// "already answered" here.
		if messages[i].content == "" || messages[i].pendingRefresh {
			return i, false
		}
		claimed = true
	}
	return -1, claimed
}

// matchToolCardByName finds an unanswered card for the named tool, preferring
// an ID-less one so an ID-less result cannot steal a card that belongs to an
// identified call. Returns the newest identified candidate as a last resort,
// or -1 when nothing is waiting.
func matchToolCardByName(messages []message, name string) int {
	fallback := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].role != "tool" || messages[i].tool != name || messages[i].content != "" {
			continue
		}
		if messages[i].toolID == "" {
			return i
		}
		if fallback == -1 {
			fallback = i
		}
	}
	return fallback
}

// bashEventPrefix namespaces live shell-command events on the same channel the
// subagent stream uses, so a command's output cannot be mistaken for a
// subagent's.
const bashEventPrefix = "bash:"

// maxLiveBashEvents caps the events retained per running command. Only the last
// few are ever drawn, and a command that prints for two minutes would otherwise
// grow this without bound behind a window that shows five lines.
const maxLiveBashEvents = 64

// bashControlToolIn builds the header text for a bash_wait/bash_kill card.
//
// A poll's args carry only a handle, which on its own says nothing about what
// is being polled. The original command lives on the bash card the supervisor
// bound to this handle (handleBashEvent stamps it into agentID), so the header
// folds that command in: "bash_wait(bg_1): sleep 10 && echo done" reads far
// better than a bare "bash_wait". Falls back to the handle alone when no card
// is bound — the supervisor may have forgotten the command, or the transcript
// was restored without one.
func (m *model) bashControlToolIn(name string, args map[string]any, fallback string) string {
	handle, _ := args["handle"].(string)
	if handle == "" {
		return fallback
	}
	for i := len(m.chatModel.Messages) - 1; i >= 0; i-- {
		msg := m.chatModel.Messages[i]
		if msg.tool == "bash" && msg.agentID == handle && msg.toolIn != "" {
			return fmt.Sprintf("%s: %s", handle, msg.toolIn)
		}
	}
	return handle
}

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
	m.invalidatePlanPhases()
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
	m.invalidatePlanPhases()
	m.running = false
	m.agentCancel = nil
	m.matrix.clear()
	m.statusModel.ActiveTool = ""
	m.statusModel.ActiveTools = nil
	if msg.err == nil && m.mode == "plan" && m.planWorktree != nil {
		if err := m.finishPlanWorktree(); err != nil {
			msg.err = fmt.Errorf("finalize PDD worktree: %w", err)
		}
	}
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
	if len(m.pendingPrompts) > 0 {
		return m.startNextPrompt()
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
