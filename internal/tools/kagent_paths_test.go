package tools

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/dimetron/pi-go/internal/kagentapi"
)

// methodServer answers each controller method with its own body, so a flow that
// lists and then creates sees a different reply for each step. A method with no
// entry gets a 500.
func methodServer(t *testing.T, replies map[string]proto.Message) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for method, msg := range replies {
			if r.URL.Path == kagentAPIMethod(method) {
				w.Header().Set("Content-Type", "application/grpc-web+proto")
				_, _ = w.Write(grpcWebBody(t, msg))
				return
			}
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func createReply(state kagentapi.AgentInstanceState) *kagentapi.CreateAgentInstanceResponse {
	return &kagentapi.CreateAgentInstanceResponse{
		AgentInstance: &kagentapi.AgentInstance{Id: "created-1", State: state},
	}
}

// TestNewKagentToolsetReadsEnv covers the production constructor, which the
// compiler-injected environment drives.
func TestNewKagentToolsetReadsEnv(t *testing.T) {
	t.Setenv(kagentAPIURLEnv, "http://kagent-controller.kagent:8083")
	t.Setenv(kagentNamespaceEnv, "agents")
	ts := NewKagentToolset()
	if ts == nil {
		t.Fatal("NewKagentToolset() = nil with a controller URL set")
	}
	if ts.baseURL != "http://kagent-controller.kagent:8083" || ts.namespace != "agents" {
		t.Errorf("toolset = %q/%q, want the environment's values", ts.baseURL, ts.namespace)
	}
	got, err := ts.Tools(nil)
	if err != nil || len(got) != 3 {
		t.Errorf("Tools() = %d tools, %v; want 3", len(got), err)
	}
}

func TestNewKagentToolsetForNormalizes(t *testing.T) {
	ts := NewKagentToolsetFor("http://c:8083/", "", "pi-go")
	if ts.baseURL != "http://c:8083" {
		t.Errorf("baseURL = %q, want the trailing slash trimmed", ts.baseURL)
	}
	if ts.namespace != kagentDefaultNamespace {
		t.Errorf("namespace = %q, want the default", ts.namespace)
	}
}

// TestKagentToolsRun invokes each tool through Run, the way the agent does, so
// the handler closures that decode arguments are exercised too.
func TestKagentToolsRun(t *testing.T) {
	srv := methodServer(t, map[string]proto.Message{
		"ListAgentInstances":  readyListResponse(),
		"CreateAgentInstance": createReply(kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY),
	})
	byName := map[string]any{}
	for _, x := range KagentTools(NewKagentToolsetFor(srv.URL, "kagent", "")) {
		byName[x.Name()] = x
	}

	if out := runTool(t, byName["kagent_list_agents"], map[string]any{}); out["total_agents"] != float64(1) {
		t.Errorf("list_agents total_agents = %v, want 1", out["total_agents"])
	}
	if out := runTool(t, byName["kagent_create_agent"], map[string]any{
		"agent_template": "k8s-agent", "harness": "pi-go",
	}); out["ready"] != true {
		t.Errorf("create_agent ready = %v, want true", out["ready"])
	}
	if out := runTool(t, byName["kagent_call_agent"], map[string]any{}); out["error"] != "agent is required" {
		t.Errorf("call_agent error = %v, want the missing-agent error", out["error"])
	}
}

// TestKagentListAgentsTruncates pins that the cap keeps the true total, so a
// cut list is not read as the whole answer.
func TestKagentListAgentsTruncates(t *testing.T) {
	resp := &kagentapi.ListAgentInstancesResponse{}
	for i := range kagentMaxInstances + 5 {
		resp.AgentInstances = append(resp.AgentInstances, &kagentapi.AgentInstance{
			Id: fmt.Sprintf("inst-%d", i), State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		})
	}
	srv, _ := recordingServer(t, grpcWebBody(t, resp))

	out := NewKagentToolsetFor(srv.URL, "kagent", "").listAgents(t.Context(), KagentListAgentsInput{})
	if len(out.Agents) != kagentMaxInstances || !out.Truncated {
		t.Errorf("got %d agents, truncated=%v; want %d and true", len(out.Agents), out.Truncated, kagentMaxInstances)
	}
	if out.TotalAgents != kagentMaxInstances+5 {
		t.Errorf("TotalAgents = %d, want the pre-cap count %d", out.TotalAgents, kagentMaxInstances+5)
	}
}

func TestKagentCreateAgentReportsError(t *testing.T) {
	srv := methodServer(t, nil)
	out := NewKagentToolsetFor(srv.URL, "kagent", "").createAgent(t.Context(), KagentCreateAgentInput{
		AgentTemplate: "k8s-agent", Harness: "pi-go",
	})
	if !strings.Contains(out.Error, "create agent instance") {
		t.Errorf("error = %q, want the create failure surfaced", out.Error)
	}
}

// TestKagentCallAgentResolveFailure keeps a controller outage on the template
// lookup from reading as a successful call.
func TestKagentCallAgentResolveFailure(t *testing.T) {
	srv := methodServer(t, nil)
	out := NewKagentToolsetFor(srv.URL, "kagent", "").callAgent(t.Context(), KagentCallAgentInput{
		Agent: "k8s-agent", Prompt: "hi",
	})
	if out.Status != "failed" || out.Error == "" {
		t.Errorf("status=%q error=%q, want a failed call with a reason", out.Status, out.Error)
	}
}

// TestKagentCallAgentStream takes the streaming branch. The stub answers with a
// unary body, so the stream itself fails — what matters is that the branch ran
// and the failure is reported rather than swallowed.
func TestKagentCallAgentStream(t *testing.T) {
	srv := kagentCallStub(t, readyListResponse())
	out := NewKagentToolsetFor(srv.URL, "kagent", "").callAgent(t.Context(), KagentCallAgentInput{
		Agent: "11111111-2222-3333-4444-555555555555", Prompt: "hi", Stream: true,
	})
	if out.Status != "failed" || out.Error == "" {
		t.Errorf("status=%q error=%q, want the stream failure reported", out.Status, out.Error)
	}
}

// TestKagentResolveInstanceForHarness covers the pair path: a harness distinct
// from the template resolves a (harness, template) instance rather than a bare
// template one.
func TestKagentResolveInstanceForHarness(t *testing.T) {
	ready := kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY
	tests := []struct {
		name    string
		replies map[string]proto.Message
		want    string
		wantErr string
	}{
		{
			name:    "reuses a READY instance",
			replies: map[string]proto.Message{"ListAgentInstances": readyListResponse()},
			want:    "ready-1",
		},
		{
			name: "creates when none exists",
			replies: map[string]proto.Message{
				"ListAgentInstances":  &kagentapi.ListAgentInstancesResponse{},
				"CreateAgentInstance": createReply(ready),
			},
			want: "created-1",
		},
		{
			name: "rejects a created instance that is not READY",
			replies: map[string]proto.Message{
				"ListAgentInstances":  &kagentapi.ListAgentInstancesResponse{},
				"CreateAgentInstance": createReply(kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED),
			},
			wantErr: "not READY",
		},
		{
			name:    "surfaces a list failure",
			replies: nil,
			wantErr: "list AgentInstances",
		},
		{
			name:    "surfaces a create failure",
			replies: map[string]proto.Message{"ListAgentInstances": &kagentapi.ListAgentInstancesResponse{}},
			wantErr: "create AgentInstance",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := methodServer(t, tt.replies)
			got, err := newKagentAPIClient(srv.URL).resolveInstance(t.Context(), "kagent", "k8s-agent", "pi-go")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("resolveInstance() = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
}

// TestIsAgentInstanceIDRejectsMalformed covers the two ways a 36-character
// string can still not be a UUID: a misplaced separator and a non-hex digit.
func TestIsAgentInstanceIDRejectsMalformed(t *testing.T) {
	for _, s := range []string{
		"11111111x2222-3333-4444-555555555555",
		"1111111g-2222-3333-4444-555555555555",
	} {
		if isAgentInstanceID(s) {
			t.Errorf("isAgentInstanceID(%q) = true, want false", s)
		}
	}
}
