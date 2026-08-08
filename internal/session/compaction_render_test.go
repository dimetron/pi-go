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

// --- helpers ---------------------------------------------------------------

// stubLLM is an adkmodel.LLM that replays a fixed script. LLMSummarizer only
// ever makes one call, so a slice of responses plus an optional error is
// enough to drive every branch.
type stubLLM struct {
	resps []*adkmodel.LLMResponse
	err   error
}

func (m *stubLLM) Name() string { return "stub-model" }

func (m *stubLLM) GenerateContent(_ context.Context, _ *adkmodel.LLMRequest, _ bool) iter.Seq2[*adkmodel.LLMResponse, error] {
	return func(yield func(*adkmodel.LLMResponse, error) bool) {
		if m.err != nil {
			yield(nil, m.err)
			return
		}
		for _, r := range m.resps {
			if !yield(r, nil) {
				return
			}
		}
	}
}

func modelText(text string) *adkmodel.LLMResponse {
	return &adkmodel.LLMResponse{Content: genai.NewContentFromText(text, genai.RoleModel)}
}

func textEvent(author, role, text string) *session.Event {
	ev := &session.Event{Author: author}
	ev.Content = genai.NewContentFromText(text, genai.Role(role))
	return ev
}

func callEvent(author, name, id string, args map[string]any) *session.Event {
	ev := &session.Event{Author: author}
	ev.Content = &genai.Content{
		Role:  string(genai.RoleModel),
		Parts: []*genai.Part{{FunctionCall: &genai.FunctionCall{ID: id, Name: name, Args: args}}},
	}
	return ev
}

func respEvent(name, id string, resp map[string]any) *session.Event {
	ev := &session.Event{}
	ev.Content = &genai.Content{
		Role:  string(genai.RoleUser),
		Parts: []*genai.Part{{FunctionResponse: &genai.FunctionResponse{ID: id, Name: name, Response: resp}}},
	}
	return ev
}

// nilContentEvent is an event whose Content is nil — the shape every walker in
// this package has to skip.
func nilContentEvent(author string) *session.Event {
	return &session.Event{Author: author}
}

// --- AutoCompactOutcome ----------------------------------------------------

func TestAutoCompactOutcome_Reclaimed(t *testing.T) {
	tests := []struct {
		name   string
		before int
		after  int
		want   int
	}{
		{"freed tokens", 10_000, 4_000, 6_000},
		{"no change", 5_000, 5_000, 0},
		{"grew (never negative)", 4_000, 9_000, 0},
		{"zero value", 0, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := AutoCompactOutcome{TokensBefore: tc.before, TokensAfter: tc.after}
			if got := o.Reclaimed(); got != tc.want {
				t.Errorf("Reclaimed() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAutoCompactOutcome_String(t *testing.T) {
	tests := []struct {
		name    string
		outcome AutoCompactOutcome
		want    []string
	}{
		{
			name:    "shed names the result count and both token totals",
			outcome: AutoCompactOutcome{Action: CompactionShed, ResultsShed: 3, TokensBefore: 120_000, TokensAfter: 90_500},
			want:    []string{"Shed 3 superseded", "120.0k", "90.5k"},
		},
		{
			name:    "summarize names the dropped event count",
			outcome: AutoCompactOutcome{Action: CompactionSummarize, EventsDropped: 42, TokensBefore: 180_000, TokensAfter: 20_000},
			want:    []string{"Summarized 42 event(s)", "180.0k", "20.0k"},
		},
		{
			name:    "none says so plainly",
			outcome: AutoCompactOutcome{Action: CompactionNone},
			want:    []string{"No compaction needed."},
		},
		{
			name:    "sub-1k counts render without the k suffix",
			outcome: AutoCompactOutcome{Action: CompactionShed, ResultsShed: 1, TokensBefore: 999, TokensAfter: 12},
			want:    []string{"999", "12"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.outcome.String()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("String() = %q, want it to contain %q", got, want)
				}
			}
		})
	}
}

func TestFormatCount(t *testing.T) {
	tests := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1.0k"},
		{1500, "1.5k"},
		{123_456, "123.5k"},
	}
	for _, tc := range tests {
		if got := formatCount(tc.in); got != tc.want {
			t.Errorf("formatCount(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCompactionAction_String(t *testing.T) {
	tests := []struct {
		action CompactionAction
		want   string
	}{
		{CompactionNone, "none"},
		{CompactionShed, "shed"},
		{CompactionSummarize, "summarize"},
		{CompactionAction(99), "none"},
	}
	for _, tc := range tests {
		if got := tc.action.String(); got != tc.want {
			t.Errorf("CompactionAction(%d).String() = %q, want %q", tc.action, got, tc.want)
		}
	}
}

// --- renderTranscript / truncateForPrompt ----------------------------------

func TestRenderTranscript(t *testing.T) {
	events := []*session.Event{
		nil,                     // skipped
		nilContentEvent("user"), // skipped
		textEvent("user", string(genai.RoleUser), "fix it"), // author wins over role
		callEvent("assistant", "read", "c1", map[string]any{"file_path": "a.go"}),
		respEvent("read", "c1", map[string]any{"content": "package main"}),
	}

	got := renderTranscript(events)

	for _, want := range []string{
		"user: fix it",
		"assistant called read(",
		`"file_path":"a.go"`,
		"read result:",
		"package main",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("transcript missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestRenderTranscript_FallsBackToRoleWhenAuthorEmpty(t *testing.T) {
	events := []*session.Event{textEvent("", string(genai.RoleModel), "hello")}
	if got := renderTranscript(events); !strings.HasPrefix(got, "model: hello") {
		t.Errorf("want role-prefixed line, got %q", got)
	}
}

func TestRenderTranscript_TruncatesLargeToolResults(t *testing.T) {
	huge := strings.Repeat("x", 5000)
	events := []*session.Event{respEvent("bash", "c1", map[string]any{"stdout": huge})}

	got := renderTranscript(events)

	if len(got) > 3000 {
		t.Errorf("transcript not truncated: %d bytes", len(got))
	}
	if !strings.Contains(got, "more bytes)") {
		t.Errorf("truncation not annotated: %q", got[max(0, len(got)-120):])
	}
}

func TestRenderTranscript_Empty(t *testing.T) {
	if got := renderTranscript(nil); got != "" {
		t.Errorf("renderTranscript(nil) = %q, want empty", got)
	}
}

func TestTruncateForPrompt(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"under limit is untouched", "short", 10, "short"},
		{"exactly at limit is untouched", "12345", 5, "12345"},
		{"over limit is annotated", "1234567890", 4, "1234... (6 more bytes)"},
		{"empty", "", 5, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateForPrompt(tc.in, tc.max); got != tc.want {
				t.Errorf("truncateForPrompt(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

// --- LLMSummarizer ---------------------------------------------------------

func TestLLMSummarizer_UsesModelOutput(t *testing.T) {
	llm := &stubLLM{resps: []*adkmodel.LLMResponse{modelText("did "), modelText("the thing")}}
	events := []*session.Event{textEvent("user", string(genai.RoleUser), "do the thing")}

	got, err := LLMSummarizer(context.Background(), llm)(events)
	if err != nil {
		t.Fatalf("LLMSummarizer: %v", err)
	}
	if got != "did the thing" {
		t.Errorf("summary = %q, want %q", got, "did the thing")
	}
}

func TestLLMSummarizer_NilModelFallsBackToSimple(t *testing.T) {
	events := []*session.Event{textEvent("user", string(genai.RoleUser), "hi")}

	got, err := LLMSummarizer(context.Background(), nil)(events)
	if err != nil {
		t.Fatalf("LLMSummarizer: %v", err)
	}
	want, _ := SimpleSummarizer(events)
	if got != want {
		t.Errorf("summary = %q, want the SimpleSummarizer output %q", got, want)
	}
}

func TestLLMSummarizer_EmptyTranscriptFallsBackToSimple(t *testing.T) {
	// Events with no renderable parts produce an empty transcript, which must
	// not be sent to the model.
	llm := &stubLLM{resps: []*adkmodel.LLMResponse{modelText("should not be used")}}
	events := []*session.Event{nil, nilContentEvent("user")}

	got, err := LLMSummarizer(context.Background(), llm)(events)
	if err != nil {
		t.Fatalf("LLMSummarizer: %v", err)
	}
	if strings.Contains(got, "should not be used") {
		t.Errorf("model was called for an empty transcript: %q", got)
	}
}

func TestLLMSummarizer_ModelErrorDegradesInsteadOfFailing(t *testing.T) {
	// A failed summarization must still let the caller reclaim context.
	llm := &stubLLM{err: errors.New("model exploded")}
	events := []*session.Event{
		textEvent("user", string(genai.RoleUser), "do it"),
		callEvent("assistant", "edit", "c1", map[string]any{"file_path": "main.go"}),
	}

	got, err := LLMSummarizer(context.Background(), llm)(events)
	if err != nil {
		t.Fatalf("LLMSummarizer must not propagate model errors, got %v", err)
	}
	for _, want := range []string{"Compaction summary unavailable", "model exploded", "main.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("degraded summary missing %q, got:\n%s", want, got)
		}
	}
}

func TestLLMSummarizer_EmptyModelOutputDegrades(t *testing.T) {
	llm := &stubLLM{resps: []*adkmodel.LLMResponse{modelText("   "), {}, nil}}
	events := []*session.Event{textEvent("user", string(genai.RoleUser), "do it")}

	got, err := LLMSummarizer(context.Background(), llm)(events)
	if err != nil {
		t.Fatalf("LLMSummarizer: %v", err)
	}
	if !strings.Contains(got, "Compaction summary unavailable") {
		t.Errorf("want degraded summary, got %q", got)
	}
	if strings.Contains(got, "Summarizer error") {
		t.Errorf("no error occurred, so none should be reported: %q", got)
	}
}

// --- responseMapKey / isPayloadField ---------------------------------------

func TestIsPayloadField(t *testing.T) {
	for _, k := range []string{"stdout", "content", "output", "diff", "result", "data"} {
		if !isPayloadField(k) {
			t.Errorf("isPayloadField(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"file_path", "path", "error", "exit_code", ""} {
		if isPayloadField(k) {
			t.Errorf("isPayloadField(%q) = true, want false", k)
		}
	}
}

func TestResponseMapKey_PrefersNamedIdentifiers(t *testing.T) {
	tests := []struct {
		name string
		resp map[string]any
		want string
	}{
		{"file_path wins", map[string]any{"file_path": "a.go", "content": "x"}, "file_path=a.go"},
		{"path", map[string]any{"path": "b.go"}, "path=b.go"},
		{"filePath", map[string]any{"filePath": "c.go"}, "filePath=c.go"},
		{"pattern", map[string]any{"pattern": "foo"}, "pattern=foo"},
		{"command", map[string]any{"command": "ls"}, "command=ls"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := responseMapKey(tc.resp); got != tc.want {
				t.Errorf("responseMapKey = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResponseMapKey_SkipsNonStringAndEmptyIdentifiers(t *testing.T) {
	// A non-string or empty file_path must not be used as the key; the
	// field-set fallback takes over.
	got := responseMapKey(map[string]any{"file_path": 42, "exit_code": 0})
	if strings.HasPrefix(got, "file_path=") {
		t.Errorf("non-string identifier was used as key: %q", got)
	}
	if !strings.Contains(got, "exit_code=0") {
		t.Errorf("want field-set fallback keyed on exit_code, got %q", got)
	}
}

func TestResponseMapKey_FieldSetFallbackIgnoresPayload(t *testing.T) {
	// Same non-payload fields, different payload => same key (a genuine repeat).
	a := responseMapKey(map[string]any{"exit_code": 0, "stdout": "one"})
	b := responseMapKey(map[string]any{"exit_code": 0, "stdout": "two"})
	if a != b {
		t.Errorf("payload changed the key: %q vs %q", a, b)
	}

	// Different non-payload fields => different keys.
	c := responseMapKey(map[string]any{"exit_code": 1, "stdout": "one"})
	if a == c {
		t.Errorf("distinct non-payload values collapsed to one key: %q", a)
	}
}

func TestResponseMapKey_PayloadOnlyFallsBackToContentHash(t *testing.T) {
	a := responseMapKey(map[string]any{"stdout": "same"})
	b := responseMapKey(map[string]any{"stdout": "same"})
	c := responseMapKey(map[string]any{"stdout": "different"})

	if !strings.HasPrefix(a, "hash=") {
		t.Errorf("want a content hash for a payload-only response, got %q", a)
	}
	if a != b {
		t.Error("identical payload-only responses must produce the same key")
	}
	if a == c {
		t.Error("different payload-only responses must produce different keys")
	}
}

func TestResponseMapKey_Empty(t *testing.T) {
	if got := responseMapKey(map[string]any{}); !strings.HasPrefix(got, "hash=") {
		t.Errorf("empty response should hash, got %q", got)
	}
}

// --- clean-boundary adjustment ---------------------------------------------

func TestAdvanceToCleanBoundary(t *testing.T) {
	tests := []struct {
		name   string
		events []*session.Event
		split  int
		want   int
	}{
		{
			name:   "no function calls leaves the split alone",
			events: []*session.Event{textEvent("user", "user", "a"), textEvent("model", "model", "b")},
			split:  1,
			want:   1,
		},
		{
			name: "a call whose response is on the tail moves the split past it",
			events: []*session.Event{
				callEvent("model", "read", "c1", nil),
				respEvent("read", "c1", map[string]any{"content": "x"}),
				textEvent("model", "model", "done"),
			},
			split: 1,
			want:  2,
		},
		{
			name: "a response whose call is on the compacted side moves the split",
			events: []*session.Event{
				callEvent("model", "read", "c1", nil),
				respEvent("read", "c1", map[string]any{"content": "x"}),
			},
			split: 1,
			want:  2,
		},
		{
			name: "an unmatched call does not move the split",
			events: []*session.Event{
				callEvent("model", "read", "c1", nil),
				textEvent("model", "model", "gave up"),
			},
			split: 1,
			want:  1,
		},
		{
			name: "a call with no ID is ignored",
			events: []*session.Event{
				callEvent("model", "read", "", nil),
				respEvent("read", "", map[string]any{"content": "x"}),
			},
			split: 1,
			want:  1,
		},
		{
			// The loop never runs, so the function clamps down to len(events)
			// rather than handing back an out-of-range split.
			name:   "split past the end is clamped to len(events)",
			events: []*session.Event{textEvent("user", "user", "a")},
			split:  5,
			want:   1,
		},
		{
			name: "nil events and nil content are skipped",
			events: []*session.Event{
				nil,
				nilContentEvent("x"),
				textEvent("user", "user", "a"),
			},
			split: 2,
			want:  2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := advanceToCleanBoundary(tc.events, tc.split); got != tc.want {
				t.Errorf("advanceToCleanBoundary(_, %d) = %d, want %d", tc.split, got, tc.want)
			}
		})
	}
}

func TestAdvanceToCleanBoundary_RunsOutOfEvents(t *testing.T) {
	// Every candidate split orphans a pair, so the boundary walks to the end.
	events := []*session.Event{
		callEvent("model", "read", "c1", nil),
		callEvent("model", "read", "c2", nil),
		respEvent("read", "c1", map[string]any{"content": "x"}),
		respEvent("read", "c2", map[string]any{"content": "y"}),
	}
	if got := advanceToCleanBoundary(events, 1); got != len(events) {
		t.Errorf("advanceToCleanBoundary = %d, want %d", got, len(events))
	}
}

func TestCallOrphansResponseOnTail_OutOfRangeIndex(t *testing.T) {
	events := []*session.Event{textEvent("user", "user", "a")}
	if callOrphansResponseOnTail(events, -1, 0, len(events)) {
		t.Error("negative index must report false")
	}
	if callOrphansResponseOnTail(events, 5, 0, len(events)) {
		t.Error("index past the end must report false")
	}
}

func TestResponseOrphansCallOnCompacted_EdgeCases(t *testing.T) {
	events := []*session.Event{textEvent("user", "user", "a")}
	if responseOrphansCallOnCompacted(events, len(events)) {
		t.Error("split at the end must report false")
	}
	if responseOrphansCallOnCompacted([]*session.Event{nil}, 0) {
		t.Error("nil event must report false")
	}
	if responseOrphansCallOnCompacted([]*session.Event{nilContentEvent("x")}, 0) {
		t.Error("nil content must report false")
	}
	// A response with no ID cannot orphan anything.
	noID := []*session.Event{
		callEvent("model", "read", "c1", nil),
		respEvent("read", "", map[string]any{"content": "x"}),
	}
	if responseOrphansCallOnCompacted(noID, 1) {
		t.Error("response with an empty ID must report false")
	}
}
