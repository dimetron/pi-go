package tools

import (
	"encoding/binary"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"google.golang.org/protobuf/proto"

	"github.com/dimetron/pi-go/internal/kagentapi"
)

// artifactEvent builds one artifact frame for the given artifact id.
func artifactEvent(id, text string, appendChunk, last bool) *a2a.TaskArtifactUpdateEvent {
	return &a2a.TaskArtifactUpdateEvent{
		Artifact: &a2a.Artifact{
			ID:    a2a.ArtifactID(id),
			Parts: a2a.ContentParts{a2a.NewTextPart(text)},
		},
		Append:    appendChunk,
		LastChunk: last,
	}
}

// TestStreamCollectorKeepsSingleShotLastChunk is the regression test for
// discarding every LastChunk event: a server that sends the whole artifact in
// one frame marks it LastChunk, and that content is the entire reply.
func TestStreamCollectorKeepsSingleShotLastChunk(t *testing.T) {
	c := newStreamTextCollector()
	if c.appendStreamEvent(artifactEvent("a1", "the whole reply", false, true)) {
		t.Fatal("artifact event ended the stream")
	}
	if got := c.String(); got != "the whole reply" {
		t.Errorf("text = %q, want the single-shot artifact to be kept", got)
	}
}

// TestStreamCollectorSkipsReplacementSnapshot covers the kagent shape: deltas
// followed by a full-text replacement that must not be appended twice.
func TestStreamCollectorSkipsReplacementSnapshot(t *testing.T) {
	c := newStreamTextCollector()
	events := []*a2a.TaskArtifactUpdateEvent{
		artifactEvent("a1", "alpha", false, false),
		artifactEvent("a1", " beta", true, false),
		artifactEvent("a1", " gamma", true, false),
		artifactEvent("a1", "alpha beta gamma", false, true), // replacement
	}
	for _, e := range events {
		if c.appendStreamEvent(e) {
			t.Fatal("artifact event ended the stream")
		}
	}
	if got := c.String(); got != "alpha beta gamma" {
		t.Errorf("text = %q, want the deltas without the snapshot repeated", got)
	}
}

// TestStreamCollectorKeepsFinalDelta guards the other half of the rule: a
// LastChunk frame that is a delta carries new content, not a replacement.
func TestStreamCollectorKeepsFinalDelta(t *testing.T) {
	c := newStreamTextCollector()
	c.appendStreamEvent(artifactEvent("a1", "first", false, false))
	c.appendStreamEvent(artifactEvent("a1", " last", true, true))
	if got := c.String(); got != "first last" {
		t.Errorf("text = %q, want the final delta appended", got)
	}
}

// TestStreamCollectorTracksArtifactsSeparately checks that a replacement on
// one artifact does not suppress a first frame on another.
func TestStreamCollectorTracksArtifactsSeparately(t *testing.T) {
	c := newStreamTextCollector()
	c.appendStreamEvent(artifactEvent("a1", "one", false, false))
	c.appendStreamEvent(artifactEvent("a1", "one", false, true))
	c.appendStreamEvent(artifactEvent("a2", "two", false, true))
	if got := c.String(); got != "onetwo" {
		t.Errorf("text = %q, want a2's single-shot frame kept", got)
	}
}

// grpcWebFrame encodes one gRPC-Web frame.
func grpcWebFrame(flag byte, payload []byte) []byte {
	out := make([]byte, 5+len(payload))
	out[0] = flag
	binary.BigEndian.PutUint32(out[1:5], uint32(len(payload)))
	copy(out[5:], payload)
	return out
}

// TestCallPathRejectsMissingTrailer is the regression test for accepting a
// truncated unary response: without the trailer there is no grpc-status, so
// the payload cannot be called complete.
func TestCallPathRejectsMissingTrailer(t *testing.T) {
	body, err := proto.Marshal(&kagentapi.ListAgentInstancesResponse{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		// Data frame only — the connection ends before the trailer.
		_, _ = w.Write(grpcWebFrame(0, body))
	}))
	defer srv.Close()

	tr := newGRPCWebTransport(srv.URL, nil)
	var resp kagentapi.ListAgentInstancesResponse
	err = tr.callPath(t.Context(), "/kagent.api.v1alpha1.AgentInstanceService/ListAgentInstances",
		&kagentapi.ListAgentInstancesRequest{}, &resp)
	if !errors.Is(err, errMissingGRPCWebTrailer) {
		t.Fatalf("err = %v, want errMissingGRPCWebTrailer", err)
	}
}

// TestCallPathAcceptsTrailer is the positive case, so the check above cannot
// pass by rejecting everything.
func TestCallPathAcceptsTrailer(t *testing.T) {
	body, err := proto.Marshal(&kagentapi.ListAgentInstancesResponse{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(grpcWebFrame(0, body))
		_, _ = w.Write(grpcWebFrame(0x80, []byte("grpc-status:0\r\n")))
	}))
	defer srv.Close()

	tr := newGRPCWebTransport(srv.URL, nil)
	var resp kagentapi.ListAgentInstancesResponse
	if err := tr.callPath(t.Context(), "/kagent.api.v1alpha1.AgentInstanceService/ListAgentInstances",
		&kagentapi.ListAgentInstancesRequest{}, &resp); err != nil {
		t.Fatalf("callPath() = %v, want success", err)
	}
}

// TestCreateAgentInstancePathAndRequestID covers both kagent findings at once:
// the RPC must reach the AgentInstanceService path (not the A2A prefix) and
// must carry a request_id, which the service validates as 1-128 characters.
func TestCreateAgentInstancePathAndRequestID(t *testing.T) {
	var gotPath string
	var gotReq kagentapi.CreateAgentInstanceRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		raw := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(raw)
		if len(raw) > 5 {
			_ = proto.Unmarshal(raw[5:], &gotReq)
		}
		body, _ := proto.Marshal(&kagentapi.CreateAgentInstanceResponse{
			AgentInstance: &kagentapi.AgentInstance{Id: "inst-1"},
		})
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(grpcWebFrame(0, body))
		_, _ = w.Write(grpcWebFrame(0x80, []byte("grpc-status:0\r\n")))
	}))
	defer srv.Close()

	c := newKagentAPIClient(srv.URL)
	if _, err := c.createAgentInstance(t.Context(), "kagent", "pi-go"); err != nil {
		t.Fatalf("createAgentInstance() = %v", err)
	}

	want := "/kagent.api.v1alpha1.AgentInstanceService/CreateAgentInstance"
	if gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if strings.Contains(gotPath, "lf.a2a.v1.A2AService") {
		t.Errorf("path %q still carries the A2A service prefix", gotPath)
	}
	if n := len(gotReq.GetRequestId()); n < 1 || n > 128 {
		t.Errorf("request_id length = %d, want the service's 1-128 range", n)
	}
}

// TestNewKagentRequestIDIsUnique guards the idempotency key: reusing one would
// make a second create collapse into the first attempt's result.
func TestNewKagentRequestIDIsUnique(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := newKagentRequestID()
		if id == "" || len(id) > 128 {
			t.Fatalf("request id %q is outside the 1-128 range", id)
		}
		if seen[id] {
			t.Fatalf("request id %q was issued twice", id)
		}
		seen[id] = true
	}
}
