package session

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	adkmodel "google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// These tests pin the branch structure of ShedSupersededToolResultsWithDedup,
// LLMSummarizer, filesTouched and indexFunctionCalls before those functions
// were flattened for cognitive complexity.
//
// Shedding deserves the detail: it decides which tool results are dropped
// before a session is sent to the model, and both failure modes are silent at
// the call site — shedding too little bloats the context, shedding too much
// discards a result the model still needed. Every skip, cutoff and tally the
// original nesting encoded therefore gets a case here.

// --- builders ---------------------------------------------------------------

// cogEvent wraps explicit parts in a user-role event.
func cogEvent(parts ...*genai.Part) *session.Event {
	ev := &session.Event{Author: "tool"}
	ev.Content = &genai.Content{Role: string(genai.RoleUser), Parts: parts}
	return ev
}

// cogCallPart builds a FunctionCall part.
func cogCallPart(id, name string, args map[string]any) *genai.Part {
	return &genai.Part{FunctionCall: &genai.FunctionCall{ID: id, Name: name, Args: args}}
}

// cogRespPart builds a FunctionResponse part.
func cogRespPart(id, name string, resp map[string]any) *genai.Part {
	return &genai.Part{FunctionResponse: &genai.FunctionResponse{ID: id, Name: name, Response: resp}}
}

// cogBody returns a payload comfortably above shedMinBytes so it is a shed
// candidate, tagged so tests can tell two payloads apart.
func cogBody(tag string) string {
	return tag + strings.Repeat("z", shedMinBytes*2)
}

// cogContent reads back the "content" field of a response part.
func cogContent(ev *session.Event, part int) string {
	s, _ := ev.Content.Parts[part].FunctionResponse.Response["content"].(string)
	return s
}

// --- shedding ---------------------------------------------------------------

// TestShedWithDedup_NoOpBoundaries pins every early return: an empty or nil
// event slice, and a keepRecent that protects the whole conversation. In all
// of them the input slice comes back unchanged and nothing is tallied.
func TestShedWithDedup_NoOpBoundaries(t *testing.T) {
	body := cogBody("a")
	full := []*session.Event{
		cogEvent(cogRespPart("c1", "read", map[string]any{"content": body})),
		cogEvent(cogRespPart("c2", "read", map[string]any{"content": body})),
	}

	tests := []struct {
		name        string
		events      []*session.Event
		keepRecent  int
		wantScanned int
	}{
		{"nil events", nil, 0, 0},
		{"empty events", []*session.Event{}, 5, 0},
		{"keepRecent equals length", full, 2, 2},
		{"keepRecent exceeds length", full, 99, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, res := ShedSupersededToolResultsWithDedup(tc.events, tc.keepRecent, nil)
			if len(out) != len(tc.events) {
				t.Fatalf("len(out) = %d, want %d", len(out), len(tc.events))
			}
			if res.EventsScanned != tc.wantScanned {
				t.Errorf("EventsScanned = %d, want %d", res.EventsScanned, tc.wantScanned)
			}
			if res.ResultsShed != 0 || res.BytesReclaimed != 0 {
				t.Errorf("ResultsShed/BytesReclaimed = %d/%d, want 0/0", res.ResultsShed, res.BytesReclaimed)
			}
		})
	}
	// The protected events must still hold their full payloads.
	for i := range full {
		if cogContent(full[i], 0) != body {
			t.Errorf("event %d was shed despite being inside the protected tail", i)
		}
	}
}

// TestShedWithDedup_NegativeKeepRecentClampsToZero pins that a negative
// keepRecent is treated as zero rather than shortening the window (a negative
// cutoff would silently protect the whole conversation).
func TestShedWithDedup_NegativeKeepRecentClampsToZero(t *testing.T) {
	body := cogBody("b")
	events := []*session.Event{
		cogEvent(cogRespPart("c1", "read", map[string]any{"file_path": "/a.go", "content": body})),
		cogEvent(cogRespPart("c2", "read", map[string]any{"file_path": "/a.go", "content": body})),
	}
	_, res := ShedSupersededToolResultsWithDedup(events, -7, nil)
	if res.ResultsShed != 1 {
		t.Fatalf("ResultsShed = %d, want 1 — negative keepRecent must behave as 0", res.ResultsShed)
	}
	if cogContent(events[1], 0) != body {
		t.Error("the newest result must survive in full")
	}
	if cogContent(events[0], 0) == body {
		t.Error("the superseded result should have been shed")
	}
}

// TestShedWithDedup_SkipsNonCandidateParts pins the skip ladder inside the
// walk: a nil event, an event with nil Content, a text part, a function-call
// part, and a response with a nil Response map are all stepped over without
// stopping the pass — the real superseded result later in the slice is still
// shed.
func TestShedWithDedup_SkipsNonCandidateParts(t *testing.T) {
	body := cogBody("c")
	nilContent := &session.Event{Author: "pi"}
	events := []*session.Event{
		nil,
		nilContent,
		cogEvent(&genai.Part{Text: "thinking out loud"}),
		cogEvent(cogCallPart("cx", "read", map[string]any{"file_path": "/unpaired.go"})),
		cogEvent(&genai.Part{FunctionResponse: &genai.FunctionResponse{ID: "c9", Name: "read"}}),
		cogEvent(cogRespPart("c1", "read", map[string]any{"file_path": "/a.go", "content": body})),
		cogEvent(cogRespPart("c2", "read", map[string]any{"file_path": "/a.go", "content": body})),
	}

	out, res := ShedSupersededToolResultsWithDedup(events, 0, nil)
	if len(out) != len(events) {
		t.Fatalf("len(out) = %d, want %d — shedding must not reshape the slice", len(out), len(events))
	}
	if res.EventsScanned != len(events) {
		t.Errorf("EventsScanned = %d, want %d", res.EventsScanned, len(events))
	}
	if res.ResultsShed != 1 {
		t.Fatalf("ResultsShed = %d, want 1", res.ResultsShed)
	}
	if res.BytesReclaimed != len(body) {
		t.Errorf("BytesReclaimed = %d, want %d", res.BytesReclaimed, len(body))
	}
	if cogContent(events[6], 0) != body {
		t.Error("the newest result must survive in full")
	}
	if got := cogContent(events[5], 0); got == body || !strings.Contains(got, "superseded") {
		t.Errorf("older result = %q, want a superseded stub", got)
	}
}

// TestShedWithDedup_ShedsEveryOlderResultOnOneTarget pins the tally across
// more than one shed: with three reads of the same file only the newest
// survives, and BytesReclaimed is the sum of both dropped payloads.
func TestShedWithDedup_ShedsEveryOlderResultOnOneTarget(t *testing.T) {
	first, second, third := cogBody("1"), cogBody("22"), cogBody("333")
	events := []*session.Event{
		cogEvent(cogRespPart("c1", "read", map[string]any{"file_path": "/a.go", "content": first})),
		cogEvent(cogRespPart("c2", "read", map[string]any{"file_path": "/a.go", "content": second})),
		cogEvent(cogRespPart("c3", "read", map[string]any{"file_path": "/a.go", "content": third})),
	}

	_, res := ShedSupersededToolResultsWithDedup(events, 0, nil)
	if res.ResultsShed != 2 {
		t.Fatalf("ResultsShed = %d, want 2", res.ResultsShed)
	}
	if want := len(first) + len(second); res.BytesReclaimed != want {
		t.Errorf("BytesReclaimed = %d, want %d (both dropped payloads)", res.BytesReclaimed, want)
	}
	if cogContent(events[2], 0) != third {
		t.Error("the newest result must survive in full")
	}
}

// TestShedWithDedup_ShedsBothPartsOfOneEvent pins that the inner part loop
// keeps going after the first candidate: two superseded results carried by a
// single event are both shed.
func TestShedWithDedup_ShedsBothPartsOfOneEvent(t *testing.T) {
	bodyA, bodyB := cogBody("A"), cogBody("B")
	events := []*session.Event{
		cogEvent(
			cogRespPart("c1", "read", map[string]any{"file_path": "/a.go", "content": bodyA}),
			cogRespPart("c2", "read", map[string]any{"file_path": "/b.go", "content": bodyB}),
		),
		cogEvent(
			cogRespPart("c3", "read", map[string]any{"file_path": "/a.go", "content": bodyA}),
			cogRespPart("c4", "read", map[string]any{"file_path": "/b.go", "content": bodyB}),
		),
	}

	_, res := ShedSupersededToolResultsWithDedup(events, 0, nil)
	if res.ResultsShed != 2 {
		t.Fatalf("ResultsShed = %d, want 2 — both parts of the older event are superseded", res.ResultsShed)
	}
	if cogContent(events[0], 0) == bodyA || cogContent(events[0], 1) == bodyB {
		t.Error("both older payloads should have been shed")
	}
	if cogContent(events[1], 0) != bodyA || cogContent(events[1], 1) != bodyB {
		t.Error("both newer payloads must survive in full")
	}
}

// TestShedWithDedup_KeepsFirstSeenPerToolName pins that the (tool, target)
// key includes the tool name: the same path read by two different tools is
// two targets, not one.
func TestShedWithDedup_KeepsFirstSeenPerToolName(t *testing.T) {
	body := cogBody("d")
	events := []*session.Event{
		cogEvent(cogRespPart("c1", "read", map[string]any{"file_path": "/a.go", "content": body})),
		cogEvent(cogRespPart("c2", "grep", map[string]any{"file_path": "/a.go", "content": body})),
	}
	if _, res := ShedSupersededToolResultsWithDedup(events, 0, nil); res.ResultsShed != 0 {
		t.Fatalf("ResultsShed = %d, want 0 — different tools are different targets", res.ResultsShed)
	}
}

// TestShedWithDedup_EmptyPointerSetBehavesLikeNil pins that passing an empty
// (rather than nil) dedup set does not change what is shed.
func TestShedWithDedup_EmptyPointerSetBehavesLikeNil(t *testing.T) {
	body := cogBody("e")
	build := func() []*session.Event {
		return []*session.Event{
			cogEvent(cogRespPart("c1", "read", map[string]any{"file_path": "/a.go", "content": body})),
			cogEvent(cogRespPart("c2", "read", map[string]any{"file_path": "/a.go", "content": body})),
		}
	}
	_, withNil := ShedSupersededToolResultsWithDedup(build(), 0, nil)
	_, withEmpty := ShedSupersededToolResultsWithDedup(build(), 0, map[string]bool{})
	if withNil != withEmpty {
		t.Errorf("nil set gave %+v, empty set gave %+v", withNil, withEmpty)
	}
}

// TestIsDedupPointer_FieldLadder pins which response fields are inspected for
// a deduper pointer and which value shapes are ignored.
func TestIsDedupPointer_FieldLadder(t *testing.T) {
	const pointer = "[identical to an earlier result]"
	set := map[string]bool{pointer: true}

	tests := []struct {
		name string
		resp map[string]any
		set  map[string]bool
		want bool
	}{
		{"nil pointer set", map[string]any{"content": pointer}, nil, false},
		{"empty pointer set", map[string]any{"content": pointer}, map[string]bool{}, false},
		{"content field", map[string]any{"content": pointer}, set, true},
		{"stdout field", map[string]any{"stdout": pointer}, set, true},
		{"output field", map[string]any{"output": pointer}, set, true},
		{"result field", map[string]any{"result": pointer}, set, true},
		{"unwatched field", map[string]any{"diff": pointer}, set, false},
		{"non-string value", map[string]any{"content": 42}, set, false},
		{"unrelated string", map[string]any{"content": "something else"}, set, false},
		{"empty response map", map[string]any{}, set, false},
		{"later field matches", map[string]any{"content": "no", "result": pointer}, set, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fr := &genai.FunctionResponse{Name: "read", Response: tc.resp}
			if got := isDedupPointer(fr, tc.set); got != tc.want {
				t.Errorf("isDedupPointer = %v, want %v", got, tc.want)
			}
		})
	}

	t.Run("nil response", func(t *testing.T) {
		if isDedupPointer(nil, set) {
			t.Error("isDedupPointer(nil) = true, want false")
		}
		if isDedupPointer(&genai.FunctionResponse{Name: "read"}, set) {
			t.Error("isDedupPointer on a nil Response map = true, want false")
		}
	})
}

// --- call indexing -----------------------------------------------------------

// TestIndexFunctionCalls_SkipsAndFirstWins pins that the index skips nil
// events, nil Content, nil parts, non-call parts and empty IDs, and that the
// first sighting of a duplicate ID is the one kept.
func TestIndexFunctionCalls_SkipsAndFirstWins(t *testing.T) {
	events := []*session.Event{
		nil,
		{Author: "pi"}, // nil Content
		cogEvent(nil),
		cogEvent(&genai.Part{Text: "no call here"}),
		cogEvent(cogRespPart("r1", "read", map[string]any{"content": "x"})),
		cogEvent(cogCallPart("", "read", map[string]any{"file_path": "/anon.go"})),
		cogEvent(cogCallPart("dup", "read", map[string]any{"file_path": "/first.go"})),
		cogEvent(cogCallPart("dup", "read", map[string]any{"file_path": "/second.go"})),
		cogEvent(cogCallPart("solo", "grep", map[string]any{"pattern": "TODO"})),
	}

	got := indexFunctionCalls(events)
	if len(got) != 2 {
		t.Fatalf("index size = %d, want 2 (dup + solo)", len(got))
	}
	if _, ok := got[""]; ok {
		t.Error("an empty call ID must not be indexed")
	}
	if fc := got["dup"]; fc == nil || fc.Args["file_path"] != "/first.go" {
		t.Errorf("dup resolved to %v, want the first sighting (/first.go)", fc)
	}
	if fc := got["solo"]; fc == nil || fc.Name != "grep" {
		t.Errorf("solo resolved to %v, want the grep call", fc)
	}
}

// --- filesTouched -------------------------------------------------------------

// TestFilesTouched_KeyLadderAndDedup pins which call-argument keys contribute
// a path, in what order, and which values are ignored.
func TestFilesTouched_KeyLadderAndDedup(t *testing.T) {
	tests := []struct {
		name   string
		events []*session.Event
		want   []string
	}{
		{"nil events", nil, nil},
		{"nil event and nil content", []*session.Event{nil, {Author: "pi"}}, nil},
		{"no function call parts", []*session.Event{cogEvent(&genai.Part{Text: "hi"})}, nil},
		{
			"response parts contribute nothing",
			[]*session.Event{cogEvent(cogRespPart("r", "read", map[string]any{"file_path": "/a.go"}))},
			nil,
		},
		{
			"no recognized key",
			[]*session.Event{cogEvent(cogCallPart("c", "bash", map[string]any{"command": "ls"}))},
			nil,
		},
		{
			"nil args map",
			[]*session.Event{cogEvent(cogCallPart("c", "read", nil))},
			nil,
		},
		{
			"file_path",
			[]*session.Event{cogEvent(cogCallPart("c", "read", map[string]any{"file_path": "/a.go"}))},
			[]string{"/a.go"},
		},
		{
			"path",
			[]*session.Event{cogEvent(cogCallPart("c", "read", map[string]any{"path": "/b.go"}))},
			[]string{"/b.go"},
		},
		{
			"filePath",
			[]*session.Event{cogEvent(cogCallPart("c", "read", map[string]any{"filePath": "/c.go"}))},
			[]string{"/c.go"},
		},
		{
			"all three keys are collected in ladder order",
			[]*session.Event{cogEvent(cogCallPart("c", "read", map[string]any{
				"filePath": "/third.go", "path": "/second.go", "file_path": "/first.go",
			}))},
			[]string{"/first.go", "/second.go", "/third.go"},
		},
		{
			"non-string and empty values are ignored",
			[]*session.Event{cogEvent(cogCallPart("c", "read", map[string]any{
				"file_path": 42, "path": "", "filePath": "/kept.go",
			}))},
			[]string{"/kept.go"},
		},
		{
			"repeats across events are deduped, first order wins",
			[]*session.Event{
				cogEvent(cogCallPart("c1", "read", map[string]any{"file_path": "/a.go"})),
				cogEvent(cogCallPart("c2", "read", map[string]any{"file_path": "/b.go"})),
				cogEvent(cogCallPart("c3", "read", map[string]any{"file_path": "/a.go"})),
			},
			[]string{"/a.go", "/b.go"},
		},
		{
			"two call parts on one event",
			[]*session.Event{cogEvent(
				cogCallPart("c1", "read", map[string]any{"file_path": "/a.go"}),
				cogCallPart("c2", "read", map[string]any{"file_path": "/b.go"}),
			)},
			[]string{"/a.go", "/b.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filesTouched(tc.events)
			if len(got) != len(tc.want) {
				t.Fatalf("filesTouched = %v, want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("filesTouched[%d] = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

// TestDegradedSummary_ListsTouchedFiles pins the one caller of filesTouched:
// a degraded summary must still leave the next model a trail to follow.
func TestDegradedSummary_ListsTouchedFiles(t *testing.T) {
	events := []*session.Event{
		cogEvent(cogCallPart("c1", "read", map[string]any{"file_path": "/x.go"})),
		cogEvent(cogCallPart("c2", "read", map[string]any{"path": "/y.go"})),
	}
	got := degradedSummary(events, errors.New("boom"))
	for _, want := range []string{"/x.go", "/y.go", "boom", "Files touched before compaction"} {
		if !strings.Contains(got, want) {
			t.Errorf("degradedSummary missing %q; got:\n%s", want, got)
		}
	}

	// With no calls at all the file list is omitted entirely.
	bare := degradedSummary([]*session.Event{cogEvent(&genai.Part{Text: "hi"})}, nil)
	if strings.Contains(bare, "Files touched") {
		t.Errorf("degradedSummary listed files when none were touched:\n%s", bare)
	}
	if strings.Contains(bare, "Summarizer error") {
		t.Errorf("degradedSummary reported an error when none was given:\n%s", bare)
	}
}

// --- LLMSummarizer ------------------------------------------------------------

// cogScriptLLM is an in-process LLM that yields a fixed script of
// (response, error) steps. It never touches the network, so these tests behave
// identically with and without credentials in the environment.
type cogScriptLLM struct {
	steps  []cogStep
	called int
}

type cogStep struct {
	resp *adkmodel.LLMResponse
	err  error
}

func (m *cogScriptLLM) Name() string { return "cog-script-model" }

func (m *cogScriptLLM) GenerateContent(
	_ context.Context, _ *adkmodel.LLMRequest, _ bool,
) iter.Seq2[*adkmodel.LLMResponse, error] {
	m.called++
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		for _, s := range m.steps {
			if !yield(s.resp, s.err) {
				return
			}
		}
	}
}

// cogMultiPart builds a response whose Content carries several parts, so the
// concatenation loop is exercised with text and non-text parts mixed.
func cogMultiPart(texts ...string) *adkmodel.LLMResponse {
	parts := make([]*genai.Part, 0, len(texts))
	for _, s := range texts {
		parts = append(parts, &genai.Part{Text: s})
	}
	return &adkmodel.LLMResponse{Content: &genai.Content{Role: string(genai.RoleModel), Parts: parts}}
}

// TestLLMSummarizer_StreamAssembly pins how the streamed response is turned
// into a summary: text parts concatenate across parts and across responses,
// empty-text parts contribute nothing, and a nil response or nil Content is
// skipped rather than ending the stream.
func TestLLMSummarizer_StreamAssembly(t *testing.T) {
	events := []*session.Event{textEvent("user", "user", "please summarize")}

	llm := &cogScriptLLM{steps: []cogStep{
		{resp: nil},
		{resp: &adkmodel.LLMResponse{}}, // nil Content
		{resp: cogMultiPart("Alpha ", "", "Beta")},
		{resp: cogMultiPart(" Gamma")},
	}}

	got, err := LLMSummarizer(context.Background(), llm)(events)
	if err != nil {
		t.Fatalf("summarizer: %v", err)
	}
	if got != "Alpha Beta Gamma" {
		t.Errorf("summary = %q, want %q", got, "Alpha Beta Gamma")
	}
	if llm.called != 1 {
		t.Errorf("GenerateContent called %d times, want 1", llm.called)
	}
}

// TestLLMSummarizer_ErrorMidStreamDiscardsPartialText pins that an error
// arriving after usable text still degrades: a half-written handoff summary
// would read as complete to the resuming model, which is worse than an
// explicit statement that detail was lost.
func TestLLMSummarizer_ErrorMidStreamDiscardsPartialText(t *testing.T) {
	events := []*session.Event{
		textEvent("user", "user", "please summarize"),
		cogEvent(cogCallPart("c1", "read", map[string]any{"file_path": "/trail.go"})),
	}
	llm := &cogScriptLLM{steps: []cogStep{
		{resp: cogMultiPart("half a summary")},
		{err: errors.New("stream broke")},
		{resp: cogMultiPart("never reached")},
	}}

	got, err := LLMSummarizer(context.Background(), llm)(events)
	if err != nil {
		t.Fatalf("summarizer must not fail the compaction: %v", err)
	}
	if strings.Contains(got, "half a summary") {
		t.Errorf("partial text leaked into the degraded summary:\n%s", got)
	}
	for _, want := range []string{"Compaction summary unavailable", "stream broke", "/trail.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("degraded summary missing %q; got:\n%s", want, got)
		}
	}
}

// TestLLMSummarizer_WhitespaceOnlyOutputDegrades pins that a response made
// only of whitespace counts as empty, not as a summary.
func TestLLMSummarizer_WhitespaceOnlyOutputDegrades(t *testing.T) {
	events := []*session.Event{textEvent("user", "user", "please summarize")}
	llm := &cogScriptLLM{steps: []cogStep{{resp: cogMultiPart("   \n\t  ")}}}

	got, err := LLMSummarizer(context.Background(), llm)(events)
	if err != nil {
		t.Fatalf("summarizer: %v", err)
	}
	if !strings.Contains(got, "Compaction summary unavailable") {
		t.Errorf("whitespace-only output was accepted as a summary:\n%s", got)
	}
	if strings.Contains(got, "Summarizer error") {
		t.Errorf("an empty (not failed) response must not report an error:\n%s", got)
	}
}

// TestLLMSummarizer_SkipsModelForEmptyTranscript pins that the transcript
// guard runs before the call: events with nothing renderable never reach the
// model at all.
func TestLLMSummarizer_SkipsModelForEmptyTranscript(t *testing.T) {
	llm := &cogScriptLLM{steps: []cogStep{{resp: cogMultiPart("should not be used")}}}

	for _, tc := range []struct {
		name   string
		events []*session.Event
	}{
		{"no events", nil},
		{"nil content only", []*session.Event{{Author: "pi"}}},
		{"empty text part", []*session.Event{cogEvent(&genai.Part{Text: ""})}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := LLMSummarizer(context.Background(), llm)(tc.events)
			if err != nil {
				t.Fatalf("summarizer: %v", err)
			}
			if strings.Contains(got, "should not be used") {
				t.Error("the model was consulted for an empty transcript")
			}
		})
	}
	if llm.called != 0 {
		t.Errorf("GenerateContent called %d times, want 0", llm.called)
	}
}
