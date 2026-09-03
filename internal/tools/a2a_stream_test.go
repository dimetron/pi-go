package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1"

	"github.com/dimetron/pi-go/internal/config"
)

// kagentA2AStub serves the AgentInstance lookup and then the given canned
// SendStreamingMessage body, so a test can drive ClientCache.SendMessage all
// the way through the gRPC-Web transport.
func kagentA2AStub(t *testing.T, stream []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		if strings.Contains(r.URL.Path, "SendStreamingMessage") {
			_, _ = w.Write(stream)
			return
		}
		_, _ = w.Write(grpcWebBody(t, readyListResponse()))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// kagentCache wires a ClientCache to a stub controller under the agent name
// "k8s".
func kagentCache(t *testing.T, srv *httptest.Server) *ClientCache {
	t.Helper()
	cache := NewClientCache(&config.A2AConfig{Agents: []config.A2AAgentConfig{{
		Name: "k8s",
		URL:  srv.URL + "/api/a2a/kagent/k8s-agent",
	}}})
	t.Cleanup(cache.Close)
	return cache
}

// TestSendMessageStreamingOverGRPCWeb is the full kagent read path: instance
// lookup, streamed artifact deltas, and reassembly into one reply.
func TestSendMessageStreamingOverGRPCWeb(t *testing.T) {
	srv := kagentA2AStub(t, grpcWebStream(t,
		artifactStreamResponse("a1", "alpha", false, false),
		artifactStreamResponse("a1", " beta", true, false),
		artifactStreamResponse("a1", "alpha beta", false, true), // closing snapshot
	))

	out := kagentCache(t, srv).SendMessage(t.Context(), "k8s", "hi", true)
	if out.Status != "streaming" {
		t.Fatalf("status = %q (error %q), want streaming", out.Status, out.Error)
	}
	if out.Result != "alpha beta" {
		t.Errorf("result = %q, want the deltas without the closing snapshot repeated", out.Result)
	}
}

// TestSendMessageStreamingFailureKeepsPartial checks what the caller is told
// when a stream is cut short: the status must say failed rather than passing
// the partial text off as a complete reply.
func TestSendMessageStreamingFailureKeepsPartial(t *testing.T) {
	// A data frame with no trailer — the stream stopped without a status.
	srv := kagentA2AStub(t, grpcWebFrame(0, mustMarshal(t,
		artifactStreamResponse("a1", "partial", false, false))))

	out := kagentCache(t, srv).SendMessage(t.Context(), "k8s", "hi", true)
	if out.Status != "failed" {
		t.Errorf("status = %q, want failed", out.Status)
	}
	if !strings.Contains(out.Error, "streaming error") {
		t.Errorf("error = %q, want it to name the streaming failure", out.Error)
	}
	if out.Result != "partial" {
		t.Errorf("result = %q, want the text that did arrive to be preserved", out.Result)
	}
}

// TestGetClientWrapsCreationFailure checks the error a caller sees when the
// kagent controller cannot supply an AgentInstance: it must name the agent,
// otherwise a multi-agent config gives no clue which one failed.
func TestGetClientWrapsCreationFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cache := kagentCache(t, srv)
	_, err := cache.GetClient(t.Context(), "k8s")
	if err == nil || !strings.Contains(err.Error(), `creating A2A client for "k8s"`) {
		t.Errorf("err = %v, want the failure attributed to the agent", err)
	}

	// A failed creation must not be cached, or the agent stays broken for the
	// life of the process even once the controller recovers.
	if _, cached := cache.clients["k8s"]; cached {
		t.Error("a client was cached despite creation failing")
	}
}

// TestAppendStreamEventNonTerminalTask covers the kagent gateway's opening
// frame: a SUBMITTED Task carrying the user's own message in history. Treating
// it as terminal would end the stream and echo the prompt back as the reply.
func TestAppendStreamEventNonTerminalTask(t *testing.T) {
	c := newStreamTextCollector()
	task := &a2a.Task{
		ID:      "t1",
		Status:  a2a.TaskStatus{State: a2a.TaskStateSubmitted},
		History: []*a2a.Message{a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("my prompt"))},
	}
	if c.appendStreamEvent(task) {
		t.Error("a SUBMITTED task ended the stream")
	}
	if got := c.String(); got != "" {
		t.Errorf("text = %q, want the user's own prompt not echoed", got)
	}
}

// TestAppendStreamEventTerminalTaskStates checks the other side of the same
// rule, including the bare Task simple servers return with no state set.
func TestAppendStreamEventTerminalTaskStates(t *testing.T) {
	tests := []struct {
		name  string
		state a2a.TaskState
		want  bool
	}{
		{"completed", a2a.TaskStateCompleted, true},
		{"failed", a2a.TaskStateFailed, true},
		{"canceled", a2a.TaskStateCanceled, true},
		{"unset", a2a.TaskStateUnspecified, true},
		{"working", a2a.TaskStateWorking, false},
		{"submitted", a2a.TaskStateSubmitted, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newStreamTextCollector()
			task := &a2a.Task{
				ID:     "t1",
				Status: a2a.TaskStatus{State: tt.state},
				Artifacts: []*a2a.Artifact{{
					ID:    "a1",
					Parts: a2a.ContentParts{a2a.NewTextPart("reply")},
				}},
			}
			if got := c.appendStreamEvent(task); got != tt.want {
				t.Errorf("appendStreamEvent(%s) = %v, want %v", tt.state, got, tt.want)
			}
			want := ""
			if tt.want {
				want = "reply"
			}
			if got := c.String(); got != want {
				t.Errorf("text = %q, want %q", got, want)
			}
		})
	}
}

// TestAppendStreamEventStatusUpdate covers the status-only event: it carries
// no text, and only a terminal state ends the stream.
func TestAppendStreamEventStatusUpdate(t *testing.T) {
	c := newStreamTextCollector()
	working := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}
	if c.appendStreamEvent(working) {
		t.Error("a WORKING status update ended the stream")
	}
	done := &a2a.TaskStatusUpdateEvent{Status: a2a.TaskStatus{State: a2a.TaskStateCompleted}}
	if !c.appendStreamEvent(done) {
		t.Error("a COMPLETED status update did not end the stream")
	}
	if got := c.String(); got != "" {
		t.Errorf("text = %q, want status updates to contribute none", got)
	}
}

// TestAppendStreamEventNilArtifact guards the frame kagent sends when it
// closes an artifact it never opened: there is nothing to append, and reading
// through the nil would panic mid-stream.
func TestAppendStreamEventNilArtifact(t *testing.T) {
	c := newStreamTextCollector()
	if c.appendStreamEvent(&a2a.TaskArtifactUpdateEvent{LastChunk: true}) {
		t.Error("an artifact-less event ended the stream")
	}
	if got := c.String(); got != "" {
		t.Errorf("text = %q, want nothing written", got)
	}
}

// TestAppendStreamEventTerminalMessage covers the plain-Message reply shape,
// which ends the stream and is itself the answer.
func TestAppendStreamEventTerminalMessage(t *testing.T) {
	c := newStreamTextCollector()
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("done"))
	if !c.appendStreamEvent(msg) {
		t.Error("a Message event did not end the stream")
	}
	if got := c.String(); got != "done" {
		t.Errorf("text = %q, want %q", got, "done")
	}
}

// TestA2AToolRunReachesAgent drives the registered ADK tool the way a model
// does, through the alias map: "agent"/"message" have to land on the same
// call "agent_name"/"prompt" would.
func TestA2AToolRunReachesAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		if strings.HasSuffix(r.URL.Path, "/SendMessage") {
			_, _ = w.Write(grpcWebBody(t, &a2apb.SendMessageResponse{
				Payload: &a2apb.SendMessageResponse_Message{Message: &a2apb.Message{
					MessageId: "m1",
					Role:      a2apb.Role_ROLE_AGENT,
					Parts:     []*a2apb.Part{{Content: &a2apb.Part_Text{Text: "pong"}}},
				}},
			}))
			return
		}
		_, _ = w.Write(grpcWebBody(t, readyListResponse()))
	}))
	defer srv.Close()

	out, err := runNamedTool(t, A2ATools(kagentCache(t, srv)), "a2a", map[string]any{
		"agent":   "k8s",
		"message": "ping",
	})
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if got := out["status"]; got != "completed" {
		t.Errorf("status = %v, want completed (output %v)", got, out)
	}
	if got := out["result"]; got != "pong" {
		t.Errorf("result = %v, want the agent's reply", got)
	}
}

// TestExtractSendMessageResultUnknown covers the fallback for a result the
// transport could not classify: an empty string, not a panic.
func TestExtractSendMessageResultUnknown(t *testing.T) {
	if got := extractSendMessageResult(nil); got != "" {
		t.Errorf("extractSendMessageResult(nil) = %q, want an empty string", got)
	}
}
