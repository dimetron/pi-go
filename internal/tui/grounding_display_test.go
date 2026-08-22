package tui

import (
	"testing"

	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/logger"
	"github.com/dimetron/pi-go/internal/testenv"
)

// drainAgentCh collects everything queued on the model's agent channel without
// blocking.
func drainAgentCh(m *model) []agentMsg {
	var msgs []agentMsg
	for {
		select {
		case msg := <-m.agentCh:
			msgs = append(msgs, msg)
		default:
			return msgs
		}
	}
}

func groundedMetadata(query string, sources ...string) *genai.GroundingMetadata {
	gm := &genai.GroundingMetadata{WebSearchQueries: []string{query}}
	for _, s := range sources {
		gm.GroundingChunks = append(gm.GroundingChunks,
			&genai.GroundingChunk{Web: &genai.GroundingChunkWeb{Title: s, URI: "https://redirect/" + s}})
	}
	return gm
}

// A grounded response has to surface as a tool call/result pair, otherwise the
// search Gemini ran server-side is invisible in the chat.
func TestEmitGroundingEvents(t *testing.T) {
	t.Parallel()

	m := &model{agentCh: make(chan agentMsg, 8)}
	seen := map[string]bool{}

	m.emitGroundingEvents(m.agentCh, groundedMetadata("who won", "a.test", "b.test"), seen, nil)

	msgs := drainAgentCh(m)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2 (call + result)", len(msgs))
	}

	call, ok := msgs[0].(agentToolCallMsg)
	if !ok {
		t.Fatalf("first message is %T, want agentToolCallMsg", msgs[0])
	}
	if call.name != groundingToolName {
		t.Errorf("call name = %q, want %q", call.name, groundingToolName)
	}
	if got := call.args["query"]; got != "who won" {
		t.Errorf("call args query = %v, want %q", got, "who won")
	}

	result, ok := msgs[1].(agentToolResultMsg)
	if !ok {
		t.Fatalf("second message is %T, want agentToolResultMsg", msgs[1])
	}
	if result.name != groundingToolName {
		t.Errorf("result name = %q, want %q", result.name, groundingToolName)
	}
	if want := "a.test\nb.test"; result.content != want {
		t.Errorf("result content = %q, want %q", result.content, want)
	}
}

// GroundingMetadata is repeated on every streamed chunk of the response it
// grounds, so a search must be reported exactly once per turn.
func TestEmitGroundingEventsReportsEachSearchOnce(t *testing.T) {
	t.Parallel()

	m := &model{agentCh: make(chan agentMsg, 8)}
	seen := map[string]bool{}
	gm := groundedMetadata("who won", "a.test")

	m.emitGroundingEvents(m.agentCh, gm, seen, nil)
	m.emitGroundingEvents(m.agentCh, gm, seen, nil) // same metadata, next streamed chunk

	if n := len(drainAgentCh(m)); n != 2 {
		t.Errorf("got %d messages across two chunks, want 2 (reported once)", n)
	}

	// A genuinely different search in the same turn is still reported.
	m.emitGroundingEvents(m.agentCh, groundedMetadata("something else", "c.test"), seen, nil)
	if n := len(drainAgentCh(m)); n != 2 {
		t.Errorf("got %d messages for a second distinct search, want 2", n)
	}
}

func TestEmitGroundingEventsIgnoresUngroundedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		gm   *genai.GroundingMetadata
	}{
		{"nil metadata", nil},
		{"no search queries", &genai.GroundingMetadata{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := &model{agentCh: make(chan agentMsg, 8)}
			m.emitGroundingEvents(m.agentCh, tt.gm, map[string]bool{}, nil)
			if n := len(drainAgentCh(m)); n != 0 {
				t.Errorf("got %d messages for an ungrounded response, want 0", n)
			}
		})
	}
}

// With a logger attached the search is also traced. The log gets the
// full-fidelity sources (URIs included); the chat still gets labels only.
func TestEmitGroundingEventsTracesToLog(t *testing.T) {
	testenv.SetHome(t, t.TempDir())

	log, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	defer log.Close()

	m := &model{agentCh: make(chan agentMsg, 8)}
	m.emitGroundingEvents(m.agentCh, groundedMetadata("who won", "a.test"), map[string]bool{}, log)

	msgs := drainAgentCh(m)
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	result := msgs[1].(agentToolResultMsg)
	if result.content != "a.test" {
		t.Errorf("chat content = %q, want the label only (no redirect URI)", result.content)
	}
}
