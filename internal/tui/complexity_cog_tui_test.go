package tui

import (
	"context"
	"fmt"
	"iter"
	"sort"
	"strings"
	"testing"

	llmmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/subagent"
)

// This file is the behavior-preservation evidence for the cognitive-complexity
// refactor of four functions in this package:
//
//	(*model).failedAgentIDs   run.go
//	(*model).emitEventParts   agent_loop.go
//	matchToolResultCard       agent_loop.go
//	executePing               ping.go
//
// Every test here was written and run against the pre-refactor source, and the
// expected values below are the values that source produced. Passing after the
// refactor is therefore evidence the refactor is a no-op, not evidence the new
// code agrees with itself.
//
// The golden corpus in complexity_render_test.go does not reach any of these
// four: it renders chat cards, tool cards, status lines and tool summaries, and
// none of the functions here produce rendered text. executePing does build a
// markdown blob, so it gets the same treatment locally — its table pins the
// exact bytes — while the other three are pinned as classification verdicts and
// emitted-message sequences instead.

// ---------------------------------------------------------------------------
// failedAgentIDs — run.go
// ---------------------------------------------------------------------------

// cogOrchWithStatuses builds an orchestrator whose Get() answers for each
// agentID in statuses. Agents absent from the map stay unknown, which is the
// race failedAgentIDs treats as a failure.
func cogOrchWithStatuses(t *testing.T, statuses map[string]string) *subagent.Orchestrator {
	t.Helper()
	// No Shutdown cleanup: SetStatusForTest records a state with no Process
	// behind it, and ShutdownWithTimeout cancels every agent still marked
	// "running" — which nil-derefs on a synthetic one. Nothing here starts a
	// goroutine, so there is nothing to shut down.
	orch := subagent.NewOrchestrator(&config.Config{}, "", nil)
	ids := make([]string, 0, len(statuses))
	for id := range statuses {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if !orch.SetStatusForTest(id, statuses[id]) {
			t.Fatalf("SetStatusForTest(%q) refused", id)
		}
	}
	return orch
}

// TestCogFailedAgentIDs_Classification pins the failed/not-failed verdict for
// every agent state the orchestrator can be in, in both single and parallel
// mode. This is the classification the post-run summary reports and the merge
// path keys off, so each row is named for the state it encodes.
//
// Note the deliberate mismatch preserved from the original: the doc comment
// says "anything other than completed or running", but the code has always
// tested `status != "completed"`, so a still-"running" agent counts as failed.
// The refactor keeps the code's behavior, not the comment's.
func TestCogFailedAgentIDs_Classification(t *testing.T) {
	tests := []struct {
		name     string
		run      *runState
		statuses map[string]string // orchestrator records; nil map = empty orchestrator
		noOrch   bool
		want     []string
	}{
		{
			name: "nil run",
			run:  nil,
			want: nil,
		},
		{
			name:   "no orchestrator",
			run:    &runState{agentID: "a1", status: "failed"},
			noOrch: true,
			want:   nil,
		},
		{
			name: "single: local status completed",
			run:  &runState{agentID: "a1", status: "completed"},
			want: nil,
		},
		{
			name: "single: local status failed",
			run:  &runState{agentID: "a1", status: "failed"},
			want: []string{"a1"},
		},
		{
			name: "single: local status canceled",
			run:  &runState{agentID: "a1", status: "canceled"},
			want: []string{"a1"},
		},
		{
			name: "single: local status killed",
			run:  &runState{agentID: "a1", status: "killed"},
			want: []string{"a1"},
		},
		{
			name: "single: local status running counts as failed",
			run:  &runState{agentID: "a1", status: "running"},
			want: []string{"a1"},
		},
		{
			name:     "single: empty status falls back to orchestrator completed",
			run:      &runState{agentID: "a1"},
			statuses: map[string]string{"a1": "completed"},
			want:     nil,
		},
		{
			name:     "single: empty status falls back to orchestrator failed",
			run:      &runState{agentID: "a1"},
			statuses: map[string]string{"a1": "failed"},
			want:     []string{"a1"},
		},
		{
			name:     "single: empty status falls back to orchestrator running",
			run:      &runState{agentID: "a1"},
			statuses: map[string]string{"a1": "running"},
			want:     []string{"a1"},
		},
		{
			name:     "single: unknown to orchestrator is failed",
			run:      &runState{agentID: "a1"},
			statuses: map[string]string{"other": "completed"},
			want:     []string{"a1"},
		},
		{
			name: "single: no agent id yet",
			run:  &runState{},
			want: nil,
		},
		{
			name: "single: local status wins over orchestrator",
			run:  &runState{agentID: "a1", status: "completed"},
			statuses: map[string]string{
				"a1": "failed",
			},
			want: nil,
		},
		{
			name: "parallel: all completed",
			run: &runState{parallel: []*parallelAgent{
				{agentID: "p1", status: "completed"},
				{agentID: "p2", status: "completed"},
			}},
			want: nil,
		},
		{
			name: "parallel: mixed local statuses preserve order",
			run: &runState{parallel: []*parallelAgent{
				{agentID: "p1", status: "completed"},
				{agentID: "p2", status: "failed"},
				{agentID: "p3", status: "canceled"},
				{agentID: "p4", status: "completed"},
				{agentID: "p5", status: "running"},
			}},
			want: []string{"p2", "p3", "p5"},
		},
		{
			name: "parallel: empty statuses fall back per agent",
			run: &runState{parallel: []*parallelAgent{
				{agentID: "p1"},
				{agentID: "p2"},
				{agentID: "p3"},
			}},
			statuses: map[string]string{"p1": "completed", "p2": "failed"},
			want:     []string{"p2", "p3"}, // p3 unknown to orchestrator
		},
		{
			name: "parallel: empty agent id still classified",
			run: &runState{parallel: []*parallelAgent{
				{agentID: "", status: ""},
			}},
			want: []string{""},
		},
		{
			name: "parallel: single-element slice is still parallel",
			run: &runState{parallel: []*parallelAgent{
				{agentID: "p1", status: "failed"},
			}},
			want: []string{"p1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &model{}
			m.run = tt.run
			if !tt.noOrch {
				m.cfg.Orchestrator = cogOrchWithStatuses(t, tt.statuses)
			}

			got := m.failedAgentIDs()
			if !cogEqualStrings(got, tt.want) {
				t.Errorf("failedAgentIDs() = %#v, want %#v", got, tt.want)
			}
			if tt.want == nil && got != nil {
				t.Errorf("failedAgentIDs() = %#v, want a nil slice", got)
			}
		})
	}
}

// TestCogResolvedAgentStatus pins the status-resolution helper directly,
// including the nil-orchestrator branch. failedAgentIDs cannot reach that
// branch — it returns early on a nil orchestrator — but the original code
// carried the same redundant `&& m.cfg.Orchestrator != nil` guard, so the
// helper keeps it and this test says what it does.
func TestCogResolvedAgentStatus(t *testing.T) {
	tests := []struct {
		name     string
		status   string
		agentID  string
		statuses map[string]string
		noOrch   bool
		want     string
	}{
		{name: "explicit status is returned as-is", status: "failed", agentID: "a1", want: "failed"},
		{
			name:     "explicit status is not overridden by the orchestrator",
			status:   "completed",
			agentID:  "a1",
			statuses: map[string]string{"a1": "failed"},
			want:     "completed",
		},
		{name: "nil orchestrator resolves to empty", status: "", agentID: "a1", noOrch: true, want: ""},
		{
			name:     "empty status resolves from the orchestrator",
			status:   "",
			agentID:  "a1",
			statuses: map[string]string{"a1": "canceled"},
			want:     "canceled",
		},
		{
			name:     "unknown agent resolves to empty",
			status:   "",
			agentID:  "a1",
			statuses: map[string]string{"other": "completed"},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &model{}
			if !tt.noOrch {
				m.cfg.Orchestrator = cogOrchWithStatuses(t, tt.statuses)
			}
			if got := m.resolvedAgentStatus(tt.status, tt.agentID); got != tt.want {
				t.Errorf("resolvedAgentStatus(%q, %q) = %q, want %q", tt.status, tt.agentID, got, tt.want)
			}
		})
	}
}

// cogEqualStrings compares two string slices, treating nil and empty as equal
// in content (nil-ness is asserted separately where it matters).
func cogEqualStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// matchToolResultCard — agent_loop.go
// ---------------------------------------------------------------------------

// TestCogMatchToolResultCard_Boundaries pins the branch structure of the
// ID-then-name match: which card an arriving result lands on, and when a
// result is dropped rather than spilled onto a card that belongs to a
// different call.
func TestCogMatchToolResultCard_Boundaries(t *testing.T) {
	tests := []struct {
		name string
		msgs []message
		id   string
		tool string
		want int
	}{
		{
			name: "no messages",
			msgs: nil,
			id:   "c1",
			tool: "read",
			want: -1,
		},
		{
			name: "id matches the only empty card",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c1"},
			},
			id: "c1", tool: "read", want: 0,
		},
		{
			name: "id picks its own card, not the newest same-named one",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c1"},
				{role: "tool", tool: "read", toolID: "c2"},
			},
			id: "c1", tool: "read", want: 0,
		},
		{
			name: "id scan walks newest first among duplicate ids",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c1"},
				{role: "tool", tool: "read", toolID: "c1"},
			},
			id: "c1", tool: "read", want: 1,
		},
		{
			name: "id card already answered: duplicate re-send is dropped",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c1", content: "done"},
				{role: "tool", tool: "read", toolID: "c2"},
			},
			id: "c1", tool: "read", want: -1,
		},
		{
			name: "pendingRefresh reclaims a card that already has content",
			msgs: []message{
				{role: "tool", tool: "bash_wait", toolID: "c1", content: "old window", pendingRefresh: true},
			},
			id: "c1", tool: "bash_wait", want: 0,
		},
		{
			name: "answered card older than an empty one with the same id",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c1", content: "done"},
				{role: "tool", tool: "read", toolID: "c1"},
			},
			id: "c1", tool: "read", want: 1,
		},
		{
			name: "unknown id falls through to name matching",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c9"},
			},
			id: "c1", tool: "read", want: 0,
		},
		{
			name: "unknown id, name match prefers the id-less card",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c8"},
				{role: "tool", tool: "read"},
				{role: "tool", tool: "read", toolID: "c9"},
			},
			id: "c1", tool: "read", want: 1,
		},
		{
			name: "unknown id, only identified cards: newest is the fallback",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c8"},
				{role: "tool", tool: "read", toolID: "c9"},
			},
			id: "c1", tool: "read", want: 1,
		},
		{
			name: "empty id skips the id scan entirely",
			msgs: []message{
				{role: "tool", tool: "read", toolID: "c1"},
				{role: "tool", tool: "read"},
			},
			id: "", tool: "read", want: 1,
		},
		{
			name: "empty id, answered card is not reused",
			msgs: []message{
				{role: "tool", tool: "read", content: "done"},
			},
			id: "", tool: "read", want: -1,
		},
		{
			name: "empty id ignores pendingRefresh (name path has no such rule)",
			msgs: []message{
				{role: "tool", tool: "read", content: "old", pendingRefresh: true},
			},
			id: "", tool: "read", want: -1,
		},
		{
			name: "name mismatch",
			msgs: []message{
				{role: "tool", tool: "bash"},
			},
			id: "", tool: "read", want: -1,
		},
		{
			name: "non-tool role is never matched by id",
			msgs: []message{
				{role: "assistant", tool: "read", toolID: "c1"},
			},
			id: "c1", tool: "read", want: -1,
		},
		{
			name: "non-tool role is never matched by name",
			msgs: []message{
				{role: "assistant", tool: "read"},
			},
			id: "", tool: "read", want: -1,
		},
		{
			name: "id claimed on a non-tool row does not block the name path",
			msgs: []message{
				{role: "assistant", tool: "read", toolID: "c1", content: "x"},
				{role: "tool", tool: "read"},
			},
			id: "c1", tool: "read", want: 1,
		},
		{
			name: "grounding-style synthetic pair matches by name",
			msgs: []message{
				{role: "tool", tool: groundingToolName},
			},
			id: "", tool: groundingToolName, want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchToolResultCard(tt.msgs, tt.id, tt.tool); got != tt.want {
				t.Errorf("matchToolResultCard(%q, %q) = %d, want %d", tt.id, tt.tool, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// emitEventParts — agent_loop.go
// ---------------------------------------------------------------------------

// cogEmitTrace runs emitEventParts over ev and renders what it put on the
// channel, one line per message, followed by the error it returned. The exact
// sequence is the specification: order, content and count all matter, because
// this is what the transcript is built from.
func cogEmitTrace(t *testing.T, ev *session.Event, dedup *agent.StreamDedup, det *stuckDetector) string {
	t.Helper()
	m := &model{}
	ch := make(chan agentMsg, 64)
	err := m.emitEventParts(ch, ev, dedup, det, nil)
	close(ch)

	var b strings.Builder
	for msg := range ch {
		switch v := msg.(type) {
		case agentThinkingMsg:
			fmt.Fprintf(&b, "thinking(%s)\n", v.text)
		case agentTextMsg:
			fmt.Fprintf(&b, "text(%s)\n", v.text)
		case agentToolCallMsg:
			fmt.Fprintf(&b, "call(%s,%s,%v)\n", v.id, v.name, v.args)
		case agentToolResultMsg:
			fmt.Fprintf(&b, "result(%s,%s,%s)\n", v.id, v.name, v.content)
		default:
			fmt.Fprintf(&b, "other(%T)\n", v)
		}
	}
	if err != nil {
		fmt.Fprintf(&b, "err(%s)\n", err.Error())
	} else {
		b.WriteString("err(nil)\n")
	}
	return b.String()
}

// cogEvent builds a model-authored event carrying the given parts.
func cogEvent(role string, partial bool, parts ...*genai.Part) *session.Event {
	ev := &session.Event{}
	ev.Author = "cog"
	ev.Partial = partial
	ev.Content = &genai.Content{Role: role, Parts: parts}
	return ev
}

// cogStuckPhrase is a phrase long and varied enough to trip observeOutput:
// 32 bytes repeated past outputCheckEvery, well over minOutputPeriod and
// minPeriodVariety.
var cogStuckPhrase = strings.Repeat("the quick brown fox jumps over! ", 24)

// TestCogEmitEventParts_Sequences pins the message stream emitted for each
// shape of part, including the dedup skip that must also suppress the rest of
// its part, and the four points at which the stuck detector can abort mid-part.
func TestCogEmitEventParts_Sequences(t *testing.T) {
	fpRead := toolFingerprint("read", map[string]any{"file_path": "a.go"})

	tests := []struct {
		name  string
		ev    *session.Event
		dedup func() *agent.StreamDedup
		det   func() *stuckDetector
		want  string
	}{
		{
			name: "no parts",
			ev:   cogEvent("model", false),
			want: "err(nil)\n",
		},
		{
			name: "plain model text",
			ev:   cogEvent("model", false, &genai.Part{Text: "hello"}),
			want: "text(hello)\nerr(nil)\n",
		},
		{
			name: "thinking text uses the thinking message",
			ev:   cogEvent("thinking", false, &genai.Part{Text: "pondering"}),
			want: "thinking(pondering)\nerr(nil)\n",
		},
		{
			name: "empty text emits nothing",
			ev:   cogEvent("model", false, &genai.Part{Text: ""}),
			want: "err(nil)\n",
		},
		{
			name: "empty text in a thinking event emits nothing",
			ev:   cogEvent("thinking", false, &genai.Part{Text: ""}),
			want: "err(nil)\n",
		},
		{
			name: "several text parts emit in order",
			ev: cogEvent("model", false,
				&genai.Part{Text: "one"},
				&genai.Part{Text: "two"},
			),
			want: "text(one)\ntext(two)\nerr(nil)\n",
		},
		{
			name: "function call",
			ev: cogEvent("model", false, &genai.Part{
				FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": "a.go"}},
			}),
			want: "call(c1,read,map[file_path:a.go])\nerr(nil)\n",
		},
		{
			name: "function response, success",
			ev: cogEvent("model", false, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"content": "hi"}},
			}),
			want: "result(c1,read,{\"content\":\"hi\"})\nerr(nil)\n",
		},
		{
			name: "function response, error payload still emits normally",
			ev: cogEvent("model", false, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"error": "boom"}},
			}),
			want: "result(c1,read,{\"error\":\"boom\"})\nerr(nil)\n",
		},
		{
			name: "nil response map marshals to null",
			ev: cogEvent("model", false, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read"},
			}),
			want: "result(c1,read,null)\nerr(nil)\n",
		},
		{
			name: "text and call in one part: both emit",
			ev: cogEvent("model", false, &genai.Part{
				Text:         "calling",
				FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": "a.go"}},
			}),
			want: "text(calling)\ncall(c1,read,map[file_path:a.go])\nerr(nil)\n",
		},
		{
			name: "thinking text and call in one part: both emit",
			ev: cogEvent("thinking", false, &genai.Part{
				Text:         "planning",
				FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": "a.go"}},
			}),
			want: "thinking(planning)\ncall(c1,read,map[file_path:a.go])\nerr(nil)\n",
		},
		{
			name: "call and response in one part: call first",
			ev: cogEvent("model", false, &genai.Part{
				FunctionCall:     &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": "a.go"}},
				FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"content": "hi"}},
			}),
			want: "call(c1,read,map[file_path:a.go])\nresult(c1,read,{\"content\":\"hi\"})\nerr(nil)\n",
		},
		{
			name: "dedup skips the aggregate re-send",
			ev:   cogEvent("model", false, &genai.Part{Text: "hello"}),
			dedup: func() *agent.StreamDedup {
				d := &agent.StreamDedup{}
				d.SkipText(cogEvent("model", true, &genai.Part{Text: "hel"})) // record a delta
				return d
			},
			want: "err(nil)\n",
		},
		{
			name: "dedup skip also suppresses the call in the same part",
			ev: cogEvent("model", false, &genai.Part{
				Text:         "hello",
				FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": "a.go"}},
			}),
			dedup: func() *agent.StreamDedup {
				d := &agent.StreamDedup{}
				d.SkipText(cogEvent("model", true, &genai.Part{Text: "hel"}))
				return d
			},
			want: "err(nil)\n",
		},
		{
			name: "dedup skip does not suppress a later part",
			ev: cogEvent("model", false,
				&genai.Part{Text: "hello", FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read"}},
				&genai.Part{FunctionCall: &genai.FunctionCall{ID: "c2", Name: "bash"}},
			),
			dedup: func() *agent.StreamDedup {
				d := &agent.StreamDedup{}
				d.SkipText(cogEvent("model", true, &genai.Part{Text: "hel"}))
				return d
			},
			want: "call(c2,bash,map[])\nerr(nil)\n",
		},
		{
			name: "dedup never skips thinking text",
			ev:   cogEvent("thinking", false, &genai.Part{Text: "pondering"}),
			dedup: func() *agent.StreamDedup {
				d := &agent.StreamDedup{}
				d.SkipText(cogEvent("model", true, &genai.Part{Text: "hel"}))
				return d
			},
			want: "thinking(pondering)\nerr(nil)\n",
		},
		{
			name: "stuck on thinking output aborts after emitting",
			ev:   cogEvent("thinking", false, &genai.Part{Text: cogStuckPhrase}),
			want: "thinking(" + cogStuckPhrase + ")\nerr(agent loop aborted: model repeated a 64-character phrase 12 times)\n",
		},
		{
			name: "stuck on model output aborts after emitting",
			ev:   cogEvent("model", false, &genai.Part{Text: cogStuckPhrase}),
			want: "text(" + cogStuckPhrase + ")\nerr(agent loop aborted: model repeated a 64-character phrase 12 times)\n",
		},
		{
			name: "stuck output abort stops before a later part",
			ev: cogEvent("model", false,
				&genai.Part{Text: cogStuckPhrase},
				&genai.Part{FunctionCall: &genai.FunctionCall{ID: "c2", Name: "bash"}},
			),
			want: "text(" + cogStuckPhrase + ")\nerr(agent loop aborted: model repeated a 64-character phrase 12 times)\n",
		},
		{
			name: "stuck on repeated tool call: the call is emitted first",
			ev: cogEvent("model", false, &genai.Part{
				FunctionCall: &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": "a.go"}},
			}),
			det: func() *stuckDetector {
				return &stuckDetector{lastPrint: fpRead, lastName: "read", streak: maxRepeatToolCalls - 1}
			},
			want: "call(c1,read,map[file_path:a.go])\nerr(agent loop aborted: identical tool call \"read\" repeated 10 times)\n",
		},
		{
			name: "stuck call abort stops before the response in the same part",
			ev: cogEvent("model", false, &genai.Part{
				FunctionCall:     &genai.FunctionCall{ID: "c1", Name: "read", Args: map[string]any{"file_path": "a.go"}},
				FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"content": "hi"}},
			}),
			det: func() *stuckDetector {
				return &stuckDetector{lastPrint: fpRead, lastName: "read", streak: maxRepeatToolCalls - 1}
			},
			want: "call(c1,read,map[file_path:a.go])\nerr(agent loop aborted: identical tool call \"read\" repeated 10 times)\n",
		},
		{
			name: "stuck on tool-error streak: the result is emitted first",
			ev: cogEvent("model", false, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"error": "boom"}},
			}),
			det: func() *stuckDetector {
				return &stuckDetector{lastErrTool: "read", errStreak: maxToolErrorStreak - 1}
			},
			want: "result(c1,read,{\"error\":\"boom\"})\nerr(agent loop aborted: tool \"read\" failed 10 times in a row)\n",
		},
		{
			name: "a successful result resets the error streak",
			ev: cogEvent("model", false, &genai.Part{
				FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"content": "hi"}},
			}),
			det: func() *stuckDetector {
				return &stuckDetector{lastErrTool: "read", errStreak: maxToolErrorStreak - 1}
			},
			want: "result(c1,read,{\"content\":\"hi\"})\nerr(nil)\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dedup := &agent.StreamDedup{}
			if tt.dedup != nil {
				dedup = tt.dedup()
			}
			det := &stuckDetector{}
			if tt.det != nil {
				det = tt.det()
			}
			if got := cogEmitTrace(t, tt.ev, dedup, det); got != tt.want {
				t.Errorf("emitEventParts trace:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestCogEmitEventParts_ObserveResultResetsCallStreak pins the interaction the
// original code documents inline: a repeated call whose response changed resets
// the identical-call streak, so the next observation of that call does not
// abort. It needs two events, so it lives outside the table.
func TestCogEmitEventParts_ObserveResultResetsCallStreak(t *testing.T) {
	args := map[string]any{"handle": "bg_1"}
	fp := toolFingerprint("bash_wait", args)
	// lastResult must be non-empty for observeResult to treat a differing
	// response as progress: the first result of a streak only records a
	// fingerprint, it does not reset.
	det := &stuckDetector{
		lastPrint:  fp,
		lastName:   "bash_wait",
		lastResult: "0000000000000000",
		streak:     maxRepeatToolCalls - 2,
	}
	dedup := &agent.StreamDedup{}

	// A fresh result arrives: progress, so the streak drops back to 1.
	got := cogEmitTrace(t, cogEvent("model", false, &genai.Part{
		FunctionResponse: &genai.FunctionResponse{ID: "r1", Name: "bash_wait", Response: map[string]any{"stdout": "new output"}},
	}), dedup, det)
	if want := "result(r1,bash_wait,{\"stdout\":\"new output\"})\nerr(nil)\n"; got != want {
		t.Fatalf("result event trace:\n got: %q\nwant: %q", got, want)
	}

	// The same call again would have been the 10th and aborted; after the
	// reset it is the 2nd and passes.
	got = cogEmitTrace(t, cogEvent("model", false, &genai.Part{
		FunctionCall: &genai.FunctionCall{ID: "c1", Name: "bash_wait", Args: args},
	}), dedup, det)
	if want := "call(c1,bash_wait,map[handle:bg_1])\nerr(nil)\n"; got != want {
		t.Fatalf("call event trace:\n got: %q\nwant: %q", got, want)
	}
	if det.streak != 1 {
		t.Errorf("streak = %d, want 1 after observeResult reset", det.streak)
	}
}

// TestCogEmitEventParts_StuckErrorIsStuckError pins the error type, not just
// its text: runAgentLoop distinguishes a recoverable stuck abort from a fatal
// error by type assertion, so a refactor that returned a plain error would
// silently turn a recoverable turn into a dead one.
func TestCogEmitEventParts_StuckErrorIsStuckError(t *testing.T) {
	m := &model{}
	ch := make(chan agentMsg, 8)
	det := &stuckDetector{lastErrTool: "read", errStreak: maxToolErrorStreak - 1}
	err := m.emitEventParts(ch, cogEvent("model", false, &genai.Part{
		FunctionResponse: &genai.FunctionResponse{ID: "c1", Name: "read", Response: map[string]any{"error": "boom"}},
	}), &agent.StreamDedup{}, det, nil)

	var stuck *stuckError
	if !cogAsStuckError(err, &stuck) {
		t.Fatalf("error %T (%v) is not a *stuckError", err, err)
	}
	if got := stuck.Detail(); got != `tool "read" failed 10 times in a row` {
		t.Errorf("Detail() = %q", got)
	}
}

func cogAsStuckError(err error, out **stuckError) bool {
	se, ok := err.(*stuckError) //nolint:errorlint // exact type is the point
	if ok {
		*out = se
	}
	return ok
}

// ---------------------------------------------------------------------------
// executePing — ping.go
// ---------------------------------------------------------------------------

// cogPingLLM yields a scripted sequence of responses, so a test can control
// part layout, usage metadata and error position independently.
type cogPingLLM struct {
	steps []cogPingStep
}

type cogPingStep struct {
	texts []string // parts to return; nil means a nil Content
	usage *genai.GenerateContentResponseUsageMetadata
	err   error
}

func (l *cogPingLLM) Name() string { return "cog-ping" }

func (l *cogPingLLM) GenerateContent(_ context.Context, _ *llmmodel.LLMRequest, _ bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		for _, step := range l.steps {
			if step.err != nil {
				if !yield(nil, step.err) {
					return
				}
				continue
			}
			resp := &llmmodel.LLMResponse{UsageMetadata: step.usage}
			if step.texts != nil {
				parts := make([]*genai.Part, 0, len(step.texts))
				for _, txt := range step.texts {
					parts = append(parts, &genai.Part{Text: txt})
				}
				resp.Content = &genai.Content{Role: string(genai.RoleModel), Parts: parts}
			}
			if !yield(resp, nil) {
				return
			}
		}
	}
}

func cogUsage(in, out int32) *genai.GenerateContentResponseUsageMetadata {
	return &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: in, CandidatesTokenCount: out}
}

// TestCogExecutePing_Output pins the exact markdown executePing produces, plus
// the reply and error it returns, across every branch: ping-pong vs custom
// prompt, both truncation boundaries, multi-part and multi-chunk accumulation,
// missing usage metadata, a nil Content chunk, the error path and the
// empty-reply path.
func TestCogExecutePing_Output(t *testing.T) {
	long90 := strings.Repeat("R", 90)
	long50 := strings.Repeat("P", 50)

	tests := []struct {
		name      string
		steps     []cogPingStep
		provider  string
		modelName string
		prompt    string
		wantOut   string
		wantReply string
		wantErr   string
	}{
		{
			name:      "ping-pong default prompt",
			steps:     []cogPingStep{{texts: []string{"prompt-prompt"}, usage: cogUsage(10, 5)}},
			provider:  "anthropic",
			modelName: "claude-sonnet-4",
			prompt:    "",
			wantOut: "**Provider:** anthropic  \n" +
				"**Model:** claude-sonnet-4  \n" +
				"**Test:** prompt-prompt  \n" +
				"**Tokens:** 10in / 5out  \n" +
				"**Reply:** prompt-prompt  \n\n" +
				"✓ Model **claude-sonnet-4** is ALIVE",
			wantReply: "prompt-prompt",
		},
		{
			name:      "custom prompt echoes the prompt line",
			steps:     []cogPingStep{{texts: []string{"4"}, usage: cogUsage(7, 1)}},
			provider:  "openai",
			modelName: "gpt-4o",
			prompt:    "What is 2+2",
			wantOut: "**Provider:** openai  \n" +
				"**Model:** gpt-4o  \n" +
				"**Prompt:** What is 2+2  \n" +
				"**Tokens:** 7in / 1out  \n" +
				"**Reply:** 4  \n\n" +
				"✓ Model **gpt-4o** is ALIVE",
			wantReply: "4",
		},
		{
			name:      "reply is trimmed before the empty check and before display",
			steps:     []cogPingStep{{texts: []string{"  \n pong \n "}, usage: cogUsage(1, 2)}},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** hi  \n" +
				"**Tokens:** 1in / 2out  \n" +
				"**Reply:** pong  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: "pong",
		},
		{
			name:      "prompt of exactly 40 chars is not truncated",
			steps:     []cogPingStep{{texts: []string{"ok"}, usage: cogUsage(1, 1)}},
			provider:  "p",
			modelName: "m",
			prompt:    strings.Repeat("P", 40),
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** " + strings.Repeat("P", 40) + "  \n" +
				"**Tokens:** 1in / 1out  \n" +
				"**Reply:** ok  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: "ok",
		},
		{
			name:      "prompt over 40 chars is truncated with an ellipsis",
			steps:     []cogPingStep{{texts: []string{"ok"}, usage: cogUsage(1, 1)}},
			provider:  "p",
			modelName: "m",
			prompt:    long50,
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** " + strings.Repeat("P", 40) + "...  \n" +
				"**Tokens:** 1in / 1out  \n" +
				"**Reply:** ok  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: "ok",
		},
		{
			name:      "reply of exactly 80 chars is not truncated",
			steps:     []cogPingStep{{texts: []string{strings.Repeat("R", 80)}, usage: cogUsage(1, 1)}},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** hi  \n" +
				"**Tokens:** 1in / 1out  \n" +
				"**Reply:** " + strings.Repeat("R", 80) + "  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: strings.Repeat("R", 80),
		},
		{
			name:      "reply over 80 chars is truncated in display but returned whole",
			steps:     []cogPingStep{{texts: []string{long90}, usage: cogUsage(1, 1)}},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** hi  \n" +
				"**Tokens:** 1in / 1out  \n" +
				"**Reply:** " + strings.Repeat("R", 80) + "...  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: long90,
		},
		{
			name: "multiple parts and chunks concatenate; last usage wins",
			steps: []cogPingStep{
				{texts: []string{"po", "ng"}, usage: cogUsage(1, 1)},
				{texts: []string{" again"}, usage: cogUsage(11, 22)},
			},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** hi  \n" +
				"**Tokens:** 11in / 22out  \n" +
				"**Reply:** pong again  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: "pong again",
		},
		{
			name: "a chunk with nil usage leaves the previous counts standing",
			steps: []cogPingStep{
				{texts: []string{"pong"}, usage: cogUsage(3, 4)},
				{texts: []string{"!"}},
			},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** hi  \n" +
				"**Tokens:** 3in / 4out  \n" +
				"**Reply:** pong!  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: "pong!",
		},
		{
			name: "a nil-Content chunk is skipped",
			steps: []cogPingStep{
				{usage: cogUsage(2, 0)},
				{texts: []string{"pong"}, usage: cogUsage(2, 3)},
			},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Prompt:** hi  \n" +
				"**Tokens:** 2in / 3out  \n" +
				"**Reply:** pong  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: "pong",
		},
		{
			name:      "no usage metadata at all reports zeros",
			steps:     []cogPingStep{{texts: []string{"pong"}}},
			provider:  "p",
			modelName: "m",
			prompt:    "",
			wantOut: "**Provider:** p  \n" +
				"**Model:** m  \n" +
				"**Test:** prompt-prompt  \n" +
				"**Tokens:** 0in / 0out  \n" +
				"**Reply:** pong  \n\n" +
				"✓ Model **m** is ALIVE",
			wantReply: "pong",
		},
		{
			name:      "empty text parts are dropped and the reply is empty",
			steps:     []cogPingStep{{texts: []string{"", ""}, usage: cogUsage(1, 0)}},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut:   "",
			wantReply: "",
			wantErr:   "model returned empty response",
		},
		{
			name:      "whitespace-only reply is empty after trimming",
			steps:     []cogPingStep{{texts: []string{"  \n\t "}, usage: cogUsage(1, 0)}},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut:   "",
			wantReply: "",
			wantErr:   "model returned empty response",
		},
		{
			name:      "no chunks at all is an empty response",
			steps:     nil,
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut:   "",
			wantReply: "",
			wantErr:   "model returned empty response",
		},
		{
			name:      "error on the first chunk",
			steps:     []cogPingStep{{err: fmt.Errorf("connection refused")}},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut:   "**✗ Error:** connection refused",
			wantReply: "",
			wantErr:   "connection refused",
		},
		{
			name: "error after text discards the text",
			steps: []cogPingStep{
				{texts: []string{"pong"}, usage: cogUsage(1, 1)},
				{err: fmt.Errorf("stream broke")},
			},
			provider:  "p",
			modelName: "m",
			prompt:    "hi",
			wantOut:   "**✗ Error:** stream broke",
			wantReply: "",
			wantErr:   "stream broke",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &cogPingLLM{steps: tt.steps}
			out, reply, err := executePing(context.Background(), llm, tt.provider, tt.modelName, tt.prompt)

			if out != tt.wantOut {
				t.Errorf("output:\n got: %q\nwant: %q", out, tt.wantOut)
			}
			if reply != tt.wantReply {
				t.Errorf("reply = %q, want %q", reply, tt.wantReply)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("expected error %q, got nil", tt.wantErr)
			case tt.wantErr != "" && err.Error() != tt.wantErr:
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestCogExecutePing_SystemInstruction pins the two system prompts and the
// user content executePing sends, which the output cannot show. The ping-pong
// instruction is what makes an "is it alive" check answerable exactly, so a
// refactor that swapped the two branches would still print a plausible report.
func TestCogExecutePing_SystemInstruction(t *testing.T) {
	tests := []struct {
		name       string
		prompt     string
		wantUser   string
		wantSystem string
	}{
		{
			name:       "empty prompt uses the ping-pong instruction",
			prompt:     "",
			wantUser:   "prompt-prompt",
			wantSystem: `You are a connectivity test. When the user says "prompt-prompt", reply with exactly "prompt-prompt" and nothing else.`,
		},
		{
			name:       "custom prompt uses the brief instruction",
			prompt:     "What is 2+2",
			wantUser:   "What is 2+2",
			wantSystem: "You are a connectivity test. Reply briefly and concisely.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &cogCapturingLLM{}
			if _, _, err := executePing(context.Background(), capture, "p", "m", tt.prompt); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if capture.req == nil {
				t.Fatal("no request captured")
			}
			if got := cogContentText(capture.req.Contents[0]); got != tt.wantUser {
				t.Errorf("user content = %q, want %q", got, tt.wantUser)
			}
			if got := cogContentText(capture.req.Config.SystemInstruction); got != tt.wantSystem {
				t.Errorf("system instruction = %q, want %q", got, tt.wantSystem)
			}
			if got := capture.req.Contents[0].Role; got != string(genai.RoleUser) {
				t.Errorf("user role = %q, want %q", got, genai.RoleUser)
			}
			if capture.streaming {
				t.Error("executePing must call GenerateContent with streaming=false")
			}
		})
	}
}

// TestCogCollectPingReply_DiscardsTextBeforeAnError pins the helper's error
// contract directly rather than through executePing. executePing returns ""
// for the reply on any error, so text accumulated before the error cannot
// escape through it — which makes "keep the partial text" an unobservable
// change at that level. Pinning the helper says the discard is deliberate.
func TestCogCollectPingReply_DiscardsTextBeforeAnError(t *testing.T) {
	llm := &cogPingLLM{steps: []cogPingStep{
		{texts: []string{"partial"}, usage: cogUsage(9, 9)},
		{err: fmt.Errorf("stream broke")},
	}}

	res, err := collectPingReply(context.Background(), llm, &llmmodel.LLMRequest{})
	if err == nil || err.Error() != "stream broke" {
		t.Fatalf("err = %v, want \"stream broke\"", err)
	}
	if res != (pingReply{}) {
		t.Errorf("res = %+v, want the zero pingReply (text and counts discarded)", res)
	}
}

// cogCapturingLLM records the request it is handed and answers with "pong".
type cogCapturingLLM struct {
	req       *llmmodel.LLMRequest
	streaming bool
}

func (l *cogCapturingLLM) Name() string { return "cog-capture" }

func (l *cogCapturingLLM) GenerateContent(_ context.Context, req *llmmodel.LLMRequest, streaming bool) iter.Seq2[*llmmodel.LLMResponse, error] {
	l.req = req
	l.streaming = streaming
	return func(yield func(*llmmodel.LLMResponse, error) bool) {
		yield(&llmmodel.LLMResponse{
			Content: &genai.Content{Role: string(genai.RoleModel), Parts: []*genai.Part{{Text: "pong"}}},
		}, nil)
	}
}

func cogContentText(c *genai.Content) string {
	if c == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range c.Parts {
		b.WriteString(p.Text)
	}
	return b.String()
}
