package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/agent"
)

// jsonEvents decodes everything written to buf so far.
func jsonEvents(t *testing.T, buf *bytes.Buffer) []jsonEvent {
	t.Helper()
	var out []jsonEvent
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev jsonEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// deltasOf joins the delta field of every event of the given type, which is how
// a consumer reconstructs the text.
func deltasOf(events []jsonEvent, kind string) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Type == kind {
			b.WriteString(ev.Delta)
		}
	}
	return b.String()
}

// feed sends s one byte at a time, the worst case for a streaming accumulator:
// a terminator arrives in a chunk by itself, ahead of the whitespace that makes
// it a real sentence boundary.
func feed(em *jsonEmitter, kind, agentName, s string) {
	for i := 0; i < len(s); i++ {
		em.text(kind, agentName, s[i:i+1])
	}
}

func TestJSONGroupingReassemblesTextExactly(t *testing.T) {
	text := "First sentence here. Second one follows! And a third? Then a final clause without one"

	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	feed(em, "text_delta", "pi", text)
	em.flush()

	events := jsonEvents(t, &buf)
	if got := deltasOf(events, "text_delta"); got != text {
		t.Errorf("reassembled text = %q, want %q", got, text)
	}
	// The point of the change: far fewer events than deltas fed in.
	if len(events) >= len(text) {
		t.Errorf("grouped into %d events for %d deltas; expected substantially fewer", len(events), len(text))
	}
	if len(events) < 4 {
		t.Errorf("grouped into %d events; sentence boundaries should not collapse everything into one", len(events))
	}
}

func TestJSONGroupingBreaksOnSentenceBoundaries(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	feed(em, "text_delta", "pi", "One. Two. Three.")
	em.flush()

	events := jsonEvents(t, &buf)
	want := []string{"One. ", "Two. ", "Three."}
	var got []string
	for _, ev := range events {
		got = append(got, ev.Delta)
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("chunks = %q, want %q", got, want)
	}
}

// A decimal point can land at the end of a delta; splitting there would put
// "3." and "14" in separate events.
func TestJSONGroupingDoesNotSplitDecimals(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	feed(em, "text_delta", "pi", "Pi is 3.14 exactly.")
	em.flush()

	events := jsonEvents(t, &buf)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1 (the decimal must not break the sentence): %q", len(events), buf.String())
	}
	if events[0].Delta != "Pi is 3.14 exactly." {
		t.Errorf("delta = %q", events[0].Delta)
	}
}

// A closing quote before the whitespace is still a sentence boundary:
// `He said "stop." Then ...`
func TestJSONGroupingSentenceBreakAfterClosingQuote(t *testing.T) {
	if got := sentenceBreak(`He said "stop." Then more`); got != len(`He said "stop." `) {
		t.Errorf("sentenceBreak = %d, want %d", got, len(`He said "stop." `))
	}
}

// Prose with no sentence breaker must still be bounded, cut at a word boundary
// so no event splits a word.
func TestJSONGroupingSoftLimitBreaksAtWordBoundary(t *testing.T) {
	// No terminators at all, so only the soft limit can flush it.
	text := strings.Repeat("word ", 80) // 400 chars

	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	feed(em, "text_delta", "pi", text)
	em.flush()

	events := jsonEvents(t, &buf)
	if len(events) < 2 {
		t.Fatalf("got %d events, want the soft limit to flush at least once", len(events))
	}
	for i, ev := range events {
		if len(ev.Delta) > jsonDeltaHardLimit {
			t.Errorf("event %d is %d bytes, want <= %d", i, len(ev.Delta), jsonDeltaHardLimit)
		}
		// Every chunk except the last ends on whitespace, i.e. between words.
		if i < len(events)-1 && !strings.HasSuffix(ev.Delta, " ") {
			t.Errorf("event %d = %q, want it to end at a word boundary", i, ev.Delta)
		}
	}
	if got := deltasOf(events, "text_delta"); got != text {
		t.Errorf("reassembled text != input")
	}
}

// A spaceless run has no word boundary to break at, so the hard limit is the
// only thing bounding it.
func TestJSONGroupingHardLimitBoundsSpacelessRun(t *testing.T) {
	text := strings.Repeat("x", jsonDeltaHardLimit*2+50)

	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	feed(em, "text_delta", "pi", text)
	em.flush()

	events := jsonEvents(t, &buf)
	for i, ev := range events {
		if len(ev.Delta) > jsonDeltaHardLimit {
			t.Errorf("event %d is %d bytes, want <= %d", i, len(ev.Delta), jsonDeltaHardLimit)
		}
	}
	if got := deltasOf(events, "text_delta"); got != text {
		t.Errorf("reassembled text != input")
	}
}

// Reasoning and reply text stream back to back from the same agent; they must
// not be concatenated into one event, or the model's thinking is emitted as its
// answer.
func TestJSONGroupingSeparatesThinkingFromText(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	em.text("thinking_delta", "pi", "considering the question ")
	em.text("text_delta", "pi", "the answer is 42")
	em.flush()

	events := jsonEvents(t, &buf)
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2: %q", len(events), buf.String())
	}
	if events[0].Type != "thinking_delta" || events[0].Delta != "considering the question " {
		t.Errorf("event 0 = %+v", events[0])
	}
	if events[1].Type != "text_delta" || events[1].Delta != "the answer is 42" {
		t.Errorf("event 1 = %+v", events[1])
	}
}

// A tool call must not overtake the text that preceded it.
func TestJSONGroupingFlushesBeforeToolCall(t *testing.T) {
	ev := modelEvent(
		&genai.Part{Text: "Looking into it now."},
		&genai.Part{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"cmd": "ls"}}},
	)

	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	var dedup agent.StreamDedup
	dedup.BeginEvent(ev)
	em.parts(ev, &dedup)
	em.flush()

	events := jsonEvents(t, &buf)
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	want := "text_delta,tool_call"
	if strings.Join(types, ",") != want {
		t.Errorf("event types = %v, want %v (text must precede the tool call)", types, want)
	}
}

// --json-deltas full restores one event per chunk.
func TestJSONRawDeltasEmitsPerChunk(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, true)
	for _, s := range []string{"One", ".", " Two", ".", " Three."} {
		em.text("text_delta", "pi", s)
	}
	em.flush()

	events := jsonEvents(t, &buf)
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5 (one per chunk): %q", len(events), buf.String())
	}
	for i, want := range []string{"One", ".", " Two", ".", " Three."} {
		if events[i].Delta != want {
			t.Errorf("event %d delta = %q, want %q", i, events[i].Delta, want)
		}
	}
}

func TestJSONRawDeltasFlag(t *testing.T) {
	for _, tc := range []struct {
		flag string
		raw  bool
	}{
		{"", false},
		{"group", false},
		{"full", true},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			old := flagJSONDeltas
			t.Cleanup(func() { flagJSONDeltas = old })
			flagJSONDeltas = tc.flag

			raw, err := jsonRawDeltas()
			if err != nil {
				t.Fatalf("jsonRawDeltas: %v", err)
			}
			if raw != tc.raw {
				t.Errorf("raw = %v, want %v", raw, tc.raw)
			}
		})
	}
}

func TestJSONRawDeltasFlagRejectsUnknownValue(t *testing.T) {
	old := flagJSONDeltas
	t.Cleanup(func() { flagJSONDeltas = old })
	flagJSONDeltas = "sometimes"

	if _, err := jsonRawDeltas(); err == nil {
		t.Error("expected an error for an unknown --json-deltas value")
	}
}

// A flush with nothing buffered must emit nothing, or callers that flush
// defensively would inject empty events into the stream.
func TestJSONGroupingEmptyFlushEmitsNothing(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	em.flush()
	em.flush()
	if buf.Len() != 0 {
		t.Errorf("output = %q, want empty", buf.String())
	}
}

// An empty text part must not open a delta run. Guarded because the flush
// bookkeeping is what tells the emitter a run is open.
func TestJSONGroupingIgnoresEmptyText(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	ev := modelEvent(
		&genai.Part{Text: ""},
		&genai.Part{FunctionCall: &genai.FunctionCall{Name: "bash", Args: map[string]any{"cmd": "ls"}}},
	)

	var dedup agent.StreamDedup
	dedup.BeginEvent(ev)
	em.parts(ev, &dedup)
	em.flush()

	events := jsonEvents(t, &buf)
	if len(events) != 1 || events[0].Type != "tool_call" {
		t.Errorf("events = %+v, want just one tool_call", events)
	}
}

// A nil logger must not panic: tests and headless callers pass one.
func TestJSONGroupingNilLogger(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	em.text("thinking_delta", "pi", "thought. ")
	em.text("text_delta", "pi", "said. ")
	em.flush()
	if buf.Len() == 0 {
		t.Error("expected output")
	}
}

// The event type must stay "text_delta" so existing consumers keep matching it.
func TestJSONGroupingKeepsEventTypeAndFields(t *testing.T) {
	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	feed(em, "text_delta", "pi", "Hello there. Second sentence.")
	em.flush()

	events := jsonEvents(t, &buf)
	for _, ev := range events {
		if ev.Type != "text_delta" {
			t.Errorf("type = %q, want text_delta", ev.Type)
		}
		if ev.Agent != "pi" {
			t.Errorf("agent = %q, want pi", ev.Agent)
		}
	}
}

// cliChunkedLLM streams its reply as partial events, one small chunk at a time,
// which is what an SSE provider does. It pins the end-to-end contract of
// --mode json: one text_delta per sentence rather than one per chunk.
type cliChunkedLLM struct {
	name   string
	chunks []string
}

func (m *cliChunkedLLM) Name() string { return m.name }

func (m *cliChunkedLLM) GenerateContent(_ context.Context, _ *model.LLMRequest, _ bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		for _, c := range m.chunks {
			resp := &model.LLMResponse{
				Content: genai.NewContentFromText(c, genai.RoleModel),
				Partial: true,
			}
			if !yield(resp, nil) {
				return
			}
		}
	}
}

// splitChunks breaks s into n-character chunks, emulating SSE framing.
func splitChunks(s string, n int) []string {
	var out []string
	for i := 0; i < len(s); i += n {
		end := min(i+n, len(s))
		out = append(out, s[i:end])
	}
	return out
}

func TestJSONGroupingStreamedChunksBySentence(t *testing.T) {
	reply := "Go is a compiled language. It came from Google in 2009. People use it for servers."
	llm := &cliChunkedLLM{name: "chunked", chunks: splitChunks(reply, 3)}

	var buf bytes.Buffer
	em := newJSONEmitter(json.NewEncoder(&buf), nil, false)
	var dedup agent.StreamDedup
	for resp, err := range llm.GenerateContent(context.Background(), nil, true) {
		if err != nil {
			t.Fatalf("GenerateContent: %v", err)
		}
		ev := modelEvent(resp.Content.Parts...)
		ev.Partial = true
		dedup.BeginEvent(ev)
		em.parts(ev, &dedup)
	}
	em.flush()

	events := jsonEvents(t, &buf)
	if got := deltasOf(events, "text_delta"); got != reply {
		t.Errorf("reassembled = %q, want %q", got, reply)
	}
	if len(events) != 3 {
		t.Errorf("got %d events, want 3 (one per sentence) from %d chunks: %q",
			len(events), len(llm.chunks), buf.String())
	}
}
