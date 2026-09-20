package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/adk/v2/session"

	"github.com/dimetron/pi-go/internal/agent"
	"github.com/dimetron/pi-go/internal/logger"
)

// Grouping limits for streamed text in --mode json. A sentence boundary is the
// preferred flush point; softLimit is the fallback for prose that runs on
// without one, and hardLimit bounds a run with no whitespace at all (a long
// URL, a base64 blob) — the only case a delta is split mid-token, because there
// is no word boundary to break at.
const (
	jsonDeltaSoftLimit = 180
	jsonDeltaHardLimit = 360
)

// jsonEmitter writes the JSONL event stream for --mode json.
//
// Streamed text and reasoning arrive as one delta per SSE chunk, a few
// characters each, so emitting per delta buries the tool calls and results in
// hundreds of lines: a one-sentence reply measured 254 text_delta events
// carrying 798 characters. The emitter accumulates streamed text instead and
// flushes at a sentence boundary, so one event carries a sentence rather than a
// token. Field names and types are unchanged, which is what keeps every
// existing consumer working — they all concatenate the `delta` field.
//
// --json-deltas full restores per-delta emission for consumers that want the
// model's own chunking.
type jsonEmitter struct {
	enc *json.Encoder
	log *logger.Logger
	// raw disables grouping: every delta becomes its own event.
	raw bool

	// Pending streamed text. A run is broken by a change of event type or
	// agent: reasoning and reply text stream from the same agent back to back,
	// and merging them would emit a model's thinking as its answer.
	kind  string
	agent string
	buf   strings.Builder
}

func newJSONEmitter(enc *json.Encoder, log *logger.Logger, raw bool) *jsonEmitter {
	return &jsonEmitter{enc: enc, log: log, raw: raw}
}

// emit writes one event. Errors are ignored deliberately, exactly as before:
// stdout is a pipe or a terminal, and a write failure there is not something
// the event stream can report on itself.
func (e *jsonEmitter) emit(ev jsonEvent) { _ = e.enc.Encode(ev) }

// text records streamed text or reasoning.
//
// In grouped mode the text is buffered until it can be cut at a sentence
// boundary, at a word boundary past the soft limit, or until flush is called.
// In raw mode it is emitted immediately, one event per delta.
func (e *jsonEmitter) text(kind, agentName, s string) {
	if e.raw {
		e.emit(jsonEvent{Type: kind, Agent: agentName, Delta: s})
		e.logText(kind, agentName, s)
		return
	}
	if e.kind != kind || e.agent != agentName {
		e.flush()
		e.kind, e.agent = kind, agentName
	}
	e.buf.WriteString(s)
	if n := e.breakIndex(); n > 0 {
		e.flushAt(n)
	}
}

// flush emits whatever streamed text is buffered, or nothing when the buffer is
// empty. Call it before any non-text event so the text that preceded it keeps
// its place in the stream.
func (e *jsonEmitter) flush() {
	if e.buf.Len() == 0 {
		return
	}
	e.flushAt(e.buf.Len())
}

// flushAt emits the first n bytes of the buffer and keeps the remainder.
func (e *jsonEmitter) flushAt(n int) {
	s := e.buf.String()
	chunk, rest := s[:n], s[n:]
	e.buf.Reset()
	e.buf.WriteString(rest)

	e.emit(jsonEvent{Type: e.kind, Agent: e.agent, Delta: chunk})
	e.logText(e.kind, e.agent, chunk)
}

// logText routes a flushed chunk to the session log. Both calls are nil-safe,
// which is what lets tests pass a nil logger.
func (e *jsonEmitter) logText(kind, agentName, s string) {
	if kind == "thinking_delta" {
		e.log.Thinking(agentName, s)
		return
	}
	e.log.LLMText(agentName, s)
}

// breakIndex returns the length of the prefix to flush now, or 0 to keep
// accumulating.
func (e *jsonEmitter) breakIndex() int {
	s := e.buf.String()
	if n := sentenceBreak(s); n > 0 {
		return n
	}
	if len(s) < jsonDeltaSoftLimit {
		return 0
	}
	// Past the soft limit, cut at the last space so the split lands between
	// words rather than inside one.
	if i := strings.LastIndexByte(s, ' '); i > 0 {
		return i + 1
	}
	// No whitespace to break at; only cap the buffer once it is well past the
	// limit, so a spaceless token is still emitted in bounded chunks.
	if len(s) >= jsonDeltaHardLimit {
		return len(s)
	}
	return 0
}

// sentenceBreak returns the index just past the last sentence terminator in s
// that is followed by whitespace, or 0 when there is none.
//
// A terminator only counts when whitespace follows it. A delta can end in the
// middle of a number — the "." of "3." arrives one chunk ahead of the "14" — and
// breaking on the terminator alone would split "3.14" across two events.
func sentenceBreak(s string) int {
	for i := 1; i < len(s); i++ {
		if !isSpaceByte(s[i]) {
			continue
		}
		j := i - 1
		// Let a closing quote or bracket sit between the terminator and the
		// whitespace, as in `He said "stop." Then ...`.
		for j >= 0 && isClosingByte(s[j]) {
			j--
		}
		if j >= 0 && isTerminatorByte(s[j]) {
			return i + 1
		}
	}
	return 0
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\n' || b == '\t' || b == '\r'
}

func isTerminatorByte(b byte) bool {
	return b == '.' || b == '!' || b == '?'
}

func isClosingByte(b byte) bool { return strings.IndexByte(`"')]*_`, b) >= 0 }

// parts emits one event's content parts: streamed reasoning and text, tool
// calls, and tool results. The caller must have called dedup.BeginEvent for ev.
func (e *jsonEmitter) parts(ev *session.Event, dedup *agent.StreamDedup) {
	for _, part := range ev.Content.Parts {
		if part.Text != "" && ev.Content.Role == "thinking" {
			e.text("thinking_delta", ev.Author, part.Text)
			continue
		}
		if part.Text != "" {
			if dedup.SkipText(ev) {
				continue
			}
			e.text("text_delta", ev.Author, part.Text)
		}
		if part.FunctionCall != nil {
			e.flush()
			e.emit(jsonEvent{
				Type:      "tool_call",
				Agent:     ev.Author,
				ToolName:  part.FunctionCall.Name,
				ToolInput: part.FunctionCall.Args,
			})
			e.log.ToolCall(ev.Author, part.FunctionCall.Name, part.FunctionCall.Args)
		}
		if part.FunctionResponse != nil {
			e.flush()
			respJSON, err := json.Marshal(part.FunctionResponse.Response)
			if err != nil {
				respJSON = []byte(fmt.Sprintf("%v", part.FunctionResponse.Response))
			}
			e.emit(jsonEvent{
				Type:     "tool_result",
				Agent:    ev.Author,
				ToolName: part.FunctionResponse.Name,
				Content:  string(respJSON),
			})
			e.log.ToolResult(ev.Author, part.FunctionResponse.Name, string(respJSON))
		}
	}
}

// jsonRawDeltas reports whether --json-deltas asked for one event per streamed
// chunk instead of sentence-grouped text.
func jsonRawDeltas() (bool, error) {
	switch flagJSONDeltas {
	case "", "group":
		return false, nil
	case "full":
		return true, nil
	default:
		return false, fmt.Errorf("invalid --json-deltas %q: want group or full", flagJSONDeltas)
	}
}
