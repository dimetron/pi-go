package tools

import (
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2aclient "github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dimetron/pi-go/internal/kagentapi"
)

// grpcWebBody concatenates a data frame carrying msg with a success trailer,
// which is the shape a complete gRPC-Web response has.
func grpcWebBody(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return append(grpcWebFrame(0, body), grpcWebFrame(0x80, []byte("grpc-status:0\r\n"))...)
}

// grpcWebStream encodes each message as its own data frame and closes with a
// success trailer — the shape a server-streaming gRPC-Web reply has.
func grpcWebStream(t *testing.T, msgs ...proto.Message) []byte {
	t.Helper()
	var out []byte
	for _, msg := range msgs {
		body, err := proto.Marshal(msg)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		out = append(out, grpcWebFrame(0, body)...)
	}
	return append(out, grpcWebFrame(0x80, []byte("grpc-status:0\r\n"))...)
}

// recordingServer serves one canned gRPC-Web body and records every request it
// received, so a test can assert on the path, headers, and marshaled body the
// transport actually put on the wire.
func recordingServer(t *testing.T, body []byte) (*httptest.Server, *[]*http.Request) {
	t.Helper()
	var reqs []*http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		reqs = append(reqs, r)
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &reqs
}

// requestPayload strips the gRPC-Web data-frame header from a recorded request
// body and unmarshals what the transport sent.
func requestPayload(t *testing.T, r *http.Request, msg proto.Message) {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read recorded body: %v", err)
	}
	if len(raw) < 5 {
		t.Fatalf("recorded body is %d bytes, too short for a frame header", len(raw))
	}
	if err := proto.Unmarshal(raw[5:], msg); err != nil {
		t.Fatalf("unmarshal recorded body: %v", err)
	}
}

// closedServerURL returns the URL of a server that has already shut down, so a
// request to it fails at dial time rather than reaching a handler.
func closedServerURL(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	return url
}

func TestMethodPath(t *testing.T) {
	if got := methodPath("GetTask"); got != "/lf.a2a.v1.A2AService/GetTask" {
		t.Errorf("methodPath() = %q", got)
	}
}

// TestCallAppliesA2APrefix pins the behavior that made the kagent create
// call fail: call always prepends the A2A service prefix, so a caller with an
// already-complete path must use callPath instead.
func TestCallAppliesA2APrefix(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{}))
	tr := newGRPCWebTransport(srv.URL, nil)

	if err := tr.call(t.Context(), "GetTask",
		&kagentapi.ListAgentInstancesRequest{}, &kagentapi.ListAgentInstancesResponse{}); err != nil {
		t.Fatalf("call() = %v", err)
	}
	if len(*reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(*reqs))
	}
	if got := (*reqs)[0].URL.Path; got != "/lf.a2a.v1.A2AService/GetTask" {
		t.Errorf("path = %q, want the A2A prefix applied", got)
	}
}

// TestCallPathSendsGRPCWebHeaders checks the framing and routing contract with
// the kagent controller: the controller only serves gRPC-Web when the
// content-type says so, and it routes to an AgentInstance purely by header.
func TestCallPathSendsGRPCWebHeaders(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{}))
	tr := newGRPCWebTransport(srv.URL, map[string]string{
		kagentNamespaceHeader:  "kagent",
		kagentInstanceIDHeader: "inst-7",
	})

	if err := tr.callPath(t.Context(), "/svc/M",
		&kagentapi.ListAgentInstancesRequest{}, &kagentapi.ListAgentInstancesResponse{}); err != nil {
		t.Fatalf("callPath() = %v", err)
	}
	if len(*reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(*reqs))
	}
	got := (*reqs)[0].Header
	want := map[string]string{
		"Content-Type":         "application/grpc-web+proto",
		"X-Grpc-Web":           "1",
		kagentNamespaceHeader:  "kagent",
		kagentInstanceIDHeader: "inst-7",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("header %s = %q, want %q", k, got.Get(k), v)
		}
	}
}

// TestCallPathMarshalsRequestIntoDataFrame checks the frame header the server
// parses: flag 0 and a big-endian length matching the payload.
func TestCallPathMarshalsRequestIntoDataFrame(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{}))
	tr := newGRPCWebTransport(srv.URL, nil)

	req := &kagentapi.ListAgentInstancesRequest{Namespace: "kagent", AgentTemplate: "pi-go"}
	if err := tr.callPath(t.Context(), "/svc/M", req, &kagentapi.ListAgentInstancesResponse{}); err != nil {
		t.Fatalf("callPath() = %v", err)
	}

	raw, err := io.ReadAll((*reqs)[0].Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if raw[0] != 0 {
		t.Errorf("frame flag = %#x, want a data frame", raw[0])
	}
	if n := int(raw[1])<<24 | int(raw[2])<<16 | int(raw[3])<<8 | int(raw[4]); n != len(raw)-5 {
		t.Errorf("frame length = %d, want %d", n, len(raw)-5)
	}
	var got kagentapi.ListAgentInstancesRequest
	if err := proto.Unmarshal(raw[5:], &got); err != nil {
		t.Fatalf("unmarshal frame payload: %v", err)
	}
	if got.GetNamespace() != "kagent" || got.GetAgentTemplate() != "pi-go" {
		t.Errorf("payload = %+v, want the request fields round-tripped", &got)
	}
}

// TestCallPathTrimsBaseURLSlash guards against a doubled slash in the gRPC
// path, which servers route as a different method.
func TestCallPathTrimsBaseURLSlash(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{}))
	tr := newGRPCWebTransport(srv.URL+"/", nil)

	if err := tr.callPath(t.Context(), "/svc/M",
		&kagentapi.ListAgentInstancesRequest{}, &kagentapi.ListAgentInstancesResponse{}); err != nil {
		t.Fatalf("callPath() = %v", err)
	}
	if got := (*reqs)[0].URL.Path; got != "/svc/M" {
		t.Errorf("path = %q, want no doubled slash", got)
	}
}

// TestCallPathAssemblesMultipleDataFrames covers a server that splits one
// unary response across frames: the payload is the concatenation, so parsing
// only the first frame would unmarshal a truncated message.
func TestCallPathAssemblesMultipleDataFrames(t *testing.T) {
	full, err := proto.Marshal(&kagentapi.ListAgentInstancesResponse{
		AgentInstances: []*kagentapi.AgentInstance{{Id: "a"}, {Id: "b"}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	split := len(full) / 2
	body := append(grpcWebFrame(0, full[:split]), grpcWebFrame(0, full[split:])...)
	body = append(body, grpcWebFrame(0x80, []byte("grpc-status:0\r\n"))...)

	srv, _ := recordingServer(t, body)
	var resp kagentapi.ListAgentInstancesResponse
	if err := testTransport(srv).callPath(t.Context(), "/svc/M", &kagentapi.ListAgentInstancesRequest{}, &resp); err != nil {
		t.Fatalf("callPath() = %v", err)
	}
	if len(resp.GetAgentInstances()) != 2 {
		t.Errorf("instances = %d, want both frames assembled", len(resp.GetAgentInstances()))
	}
}

// testTransport is a shorthand for a transport pointed at a test server.
func testTransport(srv *httptest.Server) *grpcWebTransport {
	return newGRPCWebTransport(srv.URL, nil)
}

func TestCallPathErrors(t *testing.T) {
	// Each case serves a body that is malformed in one specific way, and
	// names the substring the resulting error must carry so a regression
	// cannot swap one failure mode for another.
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "http error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "nope", http.StatusServiceUnavailable)
			},
			want: "HTTP 503",
		},
		{
			name: "grpc status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(grpcWebFrame(0, nil))
				_, _ = w.Write(grpcWebFrame(0x80, []byte("grpc-status:3\r\ngrpc-message:bad\r\n")))
			},
			want: "grpc-status 3",
		},
		{
			name:    "no frames at all",
			handler: func(http.ResponseWriter, *http.Request) {},
			want:    "empty response",
		},
		{
			name: "trailer without a data frame",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(grpcWebFrame(0x80, []byte("grpc-status:0\r\n")))
			},
			want: "empty response",
		},
		{
			name: "frame length exceeds the body",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				// Header claims 100 bytes; only 3 follow.
				_, _ = w.Write(append(grpcWebFrame(0, make([]byte, 100))[:5], 'a', 'b', 'c'))
			},
			want: "truncated frame",
		},
		{
			name: "payload is not the expected message",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				// Field 1 declared as a varint, but the value bytes never
				// terminate, so no message type can decode it.
				_, _ = w.Write(grpcWebFrame(0, []byte{0x08, 0xff, 0xff}))
				_, _ = w.Write(grpcWebFrame(0x80, []byte("grpc-status:0\r\n")))
			},
			want: "unmarshal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			err := testTransport(srv).callPath(t.Context(), "/svc/M",
				&kagentapi.ListAgentInstancesRequest{}, &kagentapi.ListAgentInstancesResponse{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// TestCallPathRejectsUnbuildableURL covers the request-construction failure,
// which is the only error reported before any connection is attempted.
func TestCallPathRejectsUnbuildableURL(t *testing.T) {
	transport := newGRPCWebTransport("://not-a-url", nil)
	err := transport.callPath(t.Context(), "/svc/M",
		&kagentapi.ListAgentInstancesRequest{}, &kagentapi.ListAgentInstancesResponse{})
	if err == nil || !strings.Contains(err.Error(), "create /svc/M request") {
		t.Errorf("err = %v, want a request-construction failure", err)
	}
}

// TestCallPathReportsDialFailure covers an unreachable controller, the common
// failure when kagent is not port-forwarded.
func TestCallPathReportsDialFailure(t *testing.T) {
	transport := newGRPCWebTransport(closedServerURL(t), nil)
	err := transport.callPath(t.Context(), "/svc/M",
		&kagentapi.ListAgentInstancesRequest{}, &kagentapi.ListAgentInstancesResponse{})
	if err == nil || !strings.HasPrefix(err.Error(), "/svc/M: ") {
		t.Errorf("err = %v, want the path-prefixed transport error", err)
	}
}

// TestCallPathReportsBodyReadFailure covers a connection dropped mid-body: the
// status line promised more bytes than arrived, so the payload is incomplete
// and must not be unmarshaled.
func TestCallPathReportsBodyReadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 64\r\n\r\nshort"))
		_ = conn.Close()
	}))
	defer srv.Close()

	err := testTransport(srv).callPath(t.Context(), "/svc/M",
		&kagentapi.ListAgentInstancesRequest{}, &kagentapi.ListAgentInstancesResponse{})
	if err == nil || !strings.Contains(err.Error(), "read /svc/M response") {
		t.Errorf("err = %v, want a body-read failure", err)
	}
}

// TestCallPathRejectsUnmarshalableRequest covers the marshal failure: proto3
// string fields must be valid UTF-8, so a name carrying raw bytes never
// reaches the wire.
func TestCallPathRejectsUnmarshalableRequest(t *testing.T) {
	srv, _ := recordingServer(t, grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{}))
	req := &kagentapi.ListAgentInstancesRequest{Namespace: "\xff\xfe"}
	err := testTransport(srv).callPath(t.Context(), "/svc/M", req, &kagentapi.ListAgentInstancesResponse{})
	if err == nil || !strings.Contains(err.Error(), "marshal /svc/M request") {
		t.Errorf("err = %v, want a request-marshal failure", err)
	}
}

// --- Transport method coverage ---

// TestGetTask drives the one unary A2A method the transport implements, end to
// end: proto conversion out, gRPC-Web framing, and conversion back.
func TestGetTask(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &a2apb.Task{
		Id:        "task-1",
		ContextId: "ctx-1",
		Status:    &a2apb.TaskStatus{State: a2apb.TaskState_TASK_STATE_COMPLETED},
		Artifacts: []*a2apb.Artifact{{
			ArtifactId: "art-1",
			Parts:      []*a2apb.Part{{Content: &a2apb.Part_Text{Text: "hello"}}},
		}},
	}))

	task, err := testTransport(srv).GetTask(t.Context(), a2aclient.ServiceParams{}, &a2a.GetTaskRequest{ID: "task-1"})
	if err != nil {
		t.Fatalf("GetTask() = %v", err)
	}
	if task.ID != "task-1" || task.ContextID != "ctx-1" {
		t.Errorf("task = %+v, want the proto response converted", task)
	}
	if got := extractTaskResult(task); got != "hello" {
		t.Errorf("artifact text = %q, want %q", got, "hello")
	}

	if got := (*reqs)[0].URL.Path; got != "/lf.a2a.v1.A2AService/GetTask" {
		t.Errorf("path = %q", got)
	}
	var sent a2apb.GetTaskRequest
	requestPayload(t, (*reqs)[0], &sent)
	if sent.GetId() != "task-1" {
		t.Errorf("sent id = %q, want the requested task id", sent.GetId())
	}
}

func TestGetTaskPropagatesCallError(t *testing.T) {
	transport := newGRPCWebTransport(closedServerURL(t), nil)
	if _, err := transport.GetTask(t.Context(), a2aclient.ServiceParams{}, &a2a.GetTaskRequest{ID: "t"}); err == nil {
		t.Error("GetTask() = nil, want the transport failure surfaced")
	}
}

// TestGetTaskRejectsIDLessTask covers the response-conversion failure: a Task
// with no id is not a usable result even though the RPC itself succeeded.
func TestGetTaskRejectsIDLessTask(t *testing.T) {
	srv, _ := recordingServer(t, grpcWebBody(t, &a2apb.Task{}))
	_, err := testTransport(srv).GetTask(t.Context(), a2aclient.ServiceParams{}, &a2a.GetTaskRequest{ID: "t"})
	if err == nil || !strings.Contains(err.Error(), "task id") {
		t.Errorf("err = %v, want a conversion failure", err)
	}
}

// TestSendMessage covers both payload shapes the A2A SendMessage response can
// carry, since the transport hands the result straight to the caller.
func TestSendMessage(t *testing.T) {
	tests := []struct {
		name string
		resp *a2apb.SendMessageResponse
		want string
	}{
		{
			name: "message payload",
			resp: &a2apb.SendMessageResponse{Payload: &a2apb.SendMessageResponse_Message{
				Message: &a2apb.Message{
					MessageId: "m1",
					Role:      a2apb.Role_ROLE_AGENT,
					Parts:     []*a2apb.Part{{Content: &a2apb.Part_Text{Text: "pong"}}},
				},
			}},
			want: "pong",
		},
		{
			name: "task payload",
			resp: &a2apb.SendMessageResponse{Payload: &a2apb.SendMessageResponse_Task{
				Task: &a2apb.Task{
					Id:     "t1",
					Status: &a2apb.TaskStatus{State: a2apb.TaskState_TASK_STATE_COMPLETED},
					Artifacts: []*a2apb.Artifact{{
						ArtifactId: "a1",
						Parts:      []*a2apb.Part{{Content: &a2apb.Part_Text{Text: "done"}}},
					}},
				},
			}},
			want: "done",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, reqs := recordingServer(t, grpcWebBody(t, tt.resp))
			req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))}

			got, err := testTransport(srv).SendMessage(t.Context(), a2aclient.ServiceParams{}, req)
			if err != nil {
				t.Fatalf("SendMessage() = %v", err)
			}
			if text := extractSendMessageResult(got); text != tt.want {
				t.Errorf("result text = %q, want %q", text, tt.want)
			}
			if path := (*reqs)[0].URL.Path; path != "/lf.a2a.v1.A2AService/SendMessage" {
				t.Errorf("path = %q", path)
			}
			var sent a2apb.SendMessageRequest
			requestPayload(t, (*reqs)[0], &sent)
			if len(sent.GetMessage().GetParts()) != 1 {
				t.Fatalf("sent message = %+v, want the prompt part", sent.GetMessage())
			}
			if text := sent.GetMessage().GetParts()[0].GetText(); text != "ping" {
				t.Errorf("sent text = %q, want %q", text, "ping")
			}
		})
	}
}

// unconvertibleSendRequest carries a part with no content, which pbconv cannot
// express as a protobuf oneof — the cheapest way to exercise the conversion
// failure that precedes every request.
func unconvertibleSendRequest() *a2a.SendMessageRequest {
	return &a2a.SendMessageRequest{
		Message: &a2a.Message{ID: "m", Parts: a2a.ContentParts{{}}},
	}
}

func TestSendMessageRejectsUnconvertibleRequest(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &a2apb.SendMessageResponse{}))
	_, err := testTransport(srv).SendMessage(t.Context(), a2aclient.ServiceParams{}, unconvertibleSendRequest())
	if err == nil {
		t.Fatal("SendMessage() = nil, want a conversion failure")
	}
	if len(*reqs) != 0 {
		t.Error("a request reached the server despite the conversion failing")
	}
}

func TestSendMessagePropagatesCallError(t *testing.T) {
	transport := newGRPCWebTransport(closedServerURL(t), nil)
	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("ping"))}
	if _, err := transport.SendMessage(t.Context(), a2aclient.ServiceParams{}, req); err == nil {
		t.Error("SendMessage() = nil, want the transport failure surfaced")
	}
}

// --- SendStreamingMessage ---

// artifactStreamResponse builds one streamed artifact frame.
func artifactStreamResponse(artifactID, text string, appendChunk, last bool) *a2apb.StreamResponse {
	return &a2apb.StreamResponse{Payload: &a2apb.StreamResponse_ArtifactUpdate{
		ArtifactUpdate: &a2apb.TaskArtifactUpdateEvent{
			TaskId: "t1",
			Artifact: &a2apb.Artifact{
				ArtifactId: artifactID,
				Parts:      []*a2apb.Part{{Content: &a2apb.Part_Text{Text: text}}},
			},
			Append:    appendChunk,
			LastChunk: last,
		},
	}}
}

// collectStream drains a stream, returning the events it yielded and the first
// error, so tests can assert on both without repeating the loop.
func collectStream(seq func(func(a2a.Event, error) bool)) ([]a2a.Event, error) {
	var events []a2a.Event
	var firstErr error
	for event, err := range seq {
		if err != nil {
			firstErr = err
			break
		}
		events = append(events, event)
	}
	return events, firstErr
}

// TestSendStreamingMessage covers the whole streaming path: one frame per
// event, in order, terminated by a success trailer.
func TestSendStreamingMessage(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebStream(t,
		artifactStreamResponse("a1", "alpha", false, false),
		artifactStreamResponse("a1", " beta", true, true),
	))

	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))}
	events, err := collectStream(testTransport(srv).SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req))
	if err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}

	// Feeding the events through the collector is the behavior that matters:
	// the transport must preserve delta/replacement framing well enough for
	// the reply to reassemble.
	c := newStreamTextCollector()
	for _, e := range events {
		c.appendStreamEvent(e)
	}
	if got := c.String(); got != "alpha beta" {
		t.Errorf("reassembled text = %q, want %q", got, "alpha beta")
	}
	if path := (*reqs)[0].URL.Path; path != "/lf.a2a.v1.A2AService/SendStreamingMessage" {
		t.Errorf("path = %q", path)
	}
	if ct := (*reqs)[0].Header.Get("Content-Type"); ct != "application/grpc-web+proto" {
		t.Errorf("content-type = %q, want the gRPC-Web type", ct)
	}
}

// TestSendStreamingMessageAppliesHeaders checks that the AgentInstance routing
// headers reach the streaming call too — they are set on a separate request
// from the unary path, so covering one does not cover the other.
func TestSendStreamingMessageAppliesHeaders(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebStream(t, artifactStreamResponse("a1", "x", false, true)))
	transport := newGRPCWebTransport(srv.URL, map[string]string{kagentInstanceIDHeader: "inst-9"})

	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))}
	if _, err := collectStream(transport.SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req)); err != nil {
		t.Fatalf("stream error: %v", err)
	}
	if got := (*reqs)[0].Header.Get(kagentInstanceIDHeader); got != "inst-9" {
		t.Errorf("%s = %q, want the instance routing header", kagentInstanceIDHeader, got)
	}
}

// TestSendStreamingMessageStopsWhenConsumerBreaks checks the yield contract:
// abandoning the range must end the iterator rather than keep decoding.
func TestSendStreamingMessageStopsWhenConsumerBreaks(t *testing.T) {
	srv, _ := recordingServer(t, grpcWebStream(t,
		artifactStreamResponse("a1", "first", false, false),
		artifactStreamResponse("a1", "second", true, false),
		artifactStreamResponse("a1", "third", true, true),
	))

	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))}
	seen := 0
	for range testTransport(srv).SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req) {
		seen++
		break
	}
	if seen != 1 {
		t.Errorf("consumed %d events after break, want 1", seen)
	}
}

func TestSendStreamingMessageErrors(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "http error",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "nope", http.StatusBadGateway)
			},
			want: "HTTP 502",
		},
		{
			name: "non-zero grpc status",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(grpcWebFrame(0x80, []byte("grpc-status:14\r\n")))
			},
			want: "grpc-status 14",
		},
		{
			name: "frame is not a StreamResponse",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(grpcWebFrame(0, []byte{0x08, 0xff, 0xff}))
			},
			want: "cannot parse invalid wire-format data",
		},
		{
			name: "artifact without an id",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				body, _ := proto.Marshal(&a2apb.StreamResponse{
					Payload: &a2apb.StreamResponse_ArtifactUpdate{
						ArtifactUpdate: &a2apb.TaskArtifactUpdateEvent{Artifact: &a2apb.Artifact{}},
					},
				})
				_, _ = w.Write(grpcWebFrame(0, body))
			},
			want: "artifact id",
		},
		{
			name: "frame truncated mid-payload",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(append(grpcWebFrame(0, make([]byte, 50))[:5], 'a'))
			},
			want: "unexpected EOF",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()

			req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))}
			_, err := collectStream(testTransport(srv).SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

// TestSendStreamingMessageRejectsMissingTrailer is the streaming half of the
// truncation guard: a stream that just stops never reported grpc-status, so
// the partial text it delivered is not a complete reply.
func TestSendStreamingMessageRejectsMissingTrailer(t *testing.T) {
	srv, _ := recordingServer(t, grpcWebFrame(0, mustMarshal(t,
		artifactStreamResponse("a1", "partial", false, false))))

	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))}
	events, err := collectStream(testTransport(srv).SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req))
	if !errors.Is(err, errMissingGRPCWebTrailer) {
		t.Fatalf("err = %v, want errMissingGRPCWebTrailer", err)
	}
	if len(events) != 1 {
		t.Errorf("got %d events before the truncation, want the one that arrived", len(events))
	}
}

func TestSendStreamingMessageRejectsUnconvertibleRequest(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebStream(t))
	_, err := collectStream(testTransport(srv).SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, unconvertibleSendRequest()))
	if err == nil {
		t.Fatal("stream error = nil, want a conversion failure")
	}
	if len(*reqs) != 0 {
		t.Error("a request reached the server despite the conversion failing")
	}
}

// TestSendStreamingMessageRejectsUnmarshalableRequest covers the failure
// between conversion and the wire: a prompt carrying bytes that are not valid
// UTF-8 converts to proto fine but cannot be marshaled, since proto3 string
// fields must be valid UTF-8.
func TestSendStreamingMessageRejectsUnmarshalableRequest(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebStream(t))
	req := &a2a.SendMessageRequest{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("\xff\xfe")),
	}
	_, err := collectStream(testTransport(srv).SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req))
	if err == nil {
		t.Fatal("stream error = nil, want a marshal failure")
	}
	if len(*reqs) != 0 {
		t.Error("a request reached the server despite the marshal failing")
	}
}

func TestSendStreamingMessageRejectsUnbuildableURL(t *testing.T) {
	transport := newGRPCWebTransport("://not-a-url", nil)
	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))}
	if _, err := collectStream(transport.SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req)); err == nil {
		t.Error("stream error = nil, want a request-construction failure")
	}
}

func TestSendStreamingMessageReportsDialFailure(t *testing.T) {
	transport := newGRPCWebTransport(closedServerURL(t), nil)
	req := &a2a.SendMessageRequest{Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hi"))}
	_, err := collectStream(transport.SendStreamingMessage(t.Context(), a2aclient.ServiceParams{}, req))
	var netErr net.Error
	if err == nil || (!errors.As(err, &netErr) && !strings.Contains(err.Error(), "connect")) {
		t.Errorf("err = %v, want a dial failure", err)
	}
}

// mustMarshal marshals msg or fails the test.
func mustMarshal(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	body, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

// TestUnimplementedTransportMethods covers the deliberate stubs, so a later
// implementation cannot silently start returning nil.
func TestUnimplementedTransportMethods(t *testing.T) {
	transport := newGRPCWebTransport("http://127.0.0.1:1", nil)
	ctx := t.Context()
	var p a2aclient.ServiceParams

	if _, err := transport.ListTasks(ctx, p, &a2a.ListTasksRequest{}); err == nil {
		t.Error("ListTasks: want an error")
	}
	if _, err := transport.CancelTask(ctx, p, &a2a.CancelTaskRequest{}); err == nil {
		t.Error("CancelTask: want an error")
	}
	if _, err := transport.GetTaskPushConfig(ctx, p, &a2a.GetTaskPushConfigRequest{}); err == nil {
		t.Error("GetTaskPushConfig: want an error")
	}
	if _, err := transport.ListTaskPushConfigs(ctx, p, &a2a.ListTaskPushConfigRequest{}); err == nil {
		t.Error("ListTaskPushConfigs: want an error")
	}
	if _, err := transport.CreateTaskPushConfig(ctx, p, &a2a.PushConfig{}); err == nil {
		t.Error("CreateTaskPushConfig: want an error")
	}
	if err := transport.DeleteTaskPushConfig(ctx, p, &a2a.DeleteTaskPushConfigRequest{}); err == nil {
		t.Error("DeleteTaskPushConfig: want an error")
	}
	if _, err := transport.GetExtendedAgentCard(ctx, p, &a2a.GetExtendedAgentCardRequest{}); err == nil {
		t.Error("GetExtendedAgentCard: want an error")
	}
	if err := transport.Destroy(); err != nil {
		t.Errorf("Destroy() = %v, want nil", err)
	}

	events, err := collectStream(transport.SubscribeToTask(ctx, p, &a2a.SubscribeToTaskRequest{}))
	if err == nil {
		t.Error("SubscribeToTask: want an error")
	}
	if len(events) != 0 {
		t.Errorf("SubscribeToTask yielded %d events, want none", len(events))
	}
}

// TestGRPCWebTransportFactoryCreate checks that the factory hands the a2a
// client a transport carrying the routing headers — without them the
// controller cannot tell which AgentInstance the call is for.
func TestGRPCWebTransportFactoryCreate(t *testing.T) {
	f := &grpcWebTransportFactory{
		baseURL: "http://example/",
		headers: map[string]string{kagentInstanceIDHeader: "inst-3"},
	}
	created, err := f.Create(t.Context(), nil, nil)
	if err != nil {
		t.Fatalf("Create() = %v", err)
	}
	got, ok := created.(*grpcWebTransport)
	if !ok {
		t.Fatalf("Create() returned %T, want *grpcWebTransport", created)
	}
	if got.baseURL != "http://example" {
		t.Errorf("baseURL = %q, want the trailing slash trimmed", got.baseURL)
	}
	if got.headers[kagentInstanceIDHeader] != "inst-3" {
		t.Errorf("headers = %v, want the instance id carried through", got.headers)
	}
}

func TestFrameReader(t *testing.T) {
	data := append(grpcWebFrame(0, []byte("abc")), grpcWebFrame(0x80, []byte("grpc-status:0\r\n"))...)
	r := newFrameReader(bytes.NewReader(data))

	flag, payload, err := r.Next()
	if err != nil || flag != 0 || string(payload) != "abc" {
		t.Fatalf("first frame = %v/%q/%v", flag, payload, err)
	}
	flag, _, err = r.Next()
	if err != nil || flag&0x80 == 0 {
		t.Fatalf("second frame = %v/%v, want the trailer", flag, err)
	}
	if _, _, err = r.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("third Next() = %v, want io.EOF", err)
	}
}

func TestFrameReaderTruncatedPayload(t *testing.T) {
	// Header claims 10 bytes but only 2 follow.
	data := append(grpcWebFrame(0, []byte("0123456789"))[:5], '0', '1')
	r := newFrameReader(bytes.NewReader(data))
	if _, _, err := r.Next(); err == nil {
		t.Error("Next() = nil, want a short-read error")
	}
}

// TestFrameReaderTruncatedHeader covers a stream that ends inside the 5-byte
// frame header, which is distinct from a clean EOF between frames.
func TestFrameReaderTruncatedHeader(t *testing.T) {
	r := newFrameReader(bytes.NewReader([]byte{0, 0, 0}))
	if _, _, err := r.Next(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("Next() = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestParseTrailer(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"status present", "grpc-status:0\r\ngrpc-message:ok\r\n", "0"},
		{"spaces trimmed", "grpc-status: 5 \r\n", "5"},
		{"absent", "grpc-message:boom\r\n", ""},
		{"empty", "", ""},
		{"no colon", "grpc-status\r\n", ""},
		// The trailer is matched on the key, not on position, so a
		// grpc-status after other trailers must still be found.
		{"not first", "content-type:x\r\ngrpc-status:7\r\n", "7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseTrailer([]byte(tt.in)); got != tt.want {
				t.Errorf("parseTrailer(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
