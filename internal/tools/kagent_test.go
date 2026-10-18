package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dimetron/pi-go/internal/kagentapi"
)

// TestKagentEndpointFromEnv pins where the controller address comes from: the
// kagent compiler injects KAGENT_API_URL into harness pods, so a harness that
// ignored it could never call its own controller.
func TestKagentEndpointFromEnv(t *testing.T) {
	t.Run("from KAGENT_API_URL", func(t *testing.T) {
		t.Setenv(kagentAPIURLEnv, "http://kagent-controller.kagent:8083")
		t.Setenv(kagentNamespaceEnv, "")
		ep, ok := kagentEndpointFromEnv("")
		if !ok {
			t.Fatal("kagentEndpointFromEnv() reported no endpoint")
		}
		if ep.baseURL != "http://kagent-controller.kagent:8083" {
			t.Errorf("baseURL = %q", ep.baseURL)
		}
		if ep.namespace != kagentDefaultNamespace {
			t.Errorf("namespace = %q, want the default", ep.namespace)
		}
	})

	t.Run("explicit override wins", func(t *testing.T) {
		t.Setenv(kagentAPIURLEnv, "http://from-env:8083")
		ep, ok := kagentEndpointFromEnv("http://explicit:9000/")
		if !ok {
			t.Fatal("kagentEndpointFromEnv() reported no endpoint")
		}
		if ep.baseURL != "http://explicit:9000" {
			t.Errorf("baseURL = %q, want the override without its trailing slash", ep.baseURL)
		}
	})

	t.Run("namespace from env", func(t *testing.T) {
		t.Setenv(kagentAPIURLEnv, "http://c:8083")
		t.Setenv(kagentNamespaceEnv, "agents")
		ep, _ := kagentEndpointFromEnv("")
		if ep.namespace != "agents" {
			t.Errorf("namespace = %q, want the env value", ep.namespace)
		}
	})

	t.Run("unset means not configured", func(t *testing.T) {
		t.Setenv(kagentAPIURLEnv, "")
		if _, ok := kagentEndpointFromEnv(""); ok {
			t.Error("reported an endpoint with no URL anywhere")
		}
	})
}

// TestNewKagentToolsetNeedsController guards the gate that keeps a non-kagent
// process from being offered tools that cannot work.
func TestNewKagentToolsetNeedsController(t *testing.T) {
	t.Setenv(kagentAPIURLEnv, "")
	if ts := NewKagentToolset(); ts != nil {
		t.Errorf("NewKagentToolset() = %+v, want nil without a controller URL", ts)
	}
	if got := KagentTools(nil); got != nil {
		t.Errorf("KagentTools(nil) = %v, want nil", got)
	}
}

// TestKagentToolsRegistered checks the tool names a session ends up offering.
func TestKagentToolsRegistered(t *testing.T) {
	ts := NewKagentToolsetFor("http://example", "", "")
	got := map[string]bool{}
	for _, x := range KagentTools(ts) {
		got[x.Name()] = true
	}
	for _, want := range []string{"kagent_list_agents", "kagent_create_agent", "kagent_call_agent"} {
		if !got[want] {
			t.Errorf("tool %q not registered; got %v", want, got)
		}
	}
	if ts.Name() != "kagent" {
		t.Errorf("toolset name = %q", ts.Name())
	}
}

// TestKagentListAgentsFlattensResponse pins the fields the model needs: the
// instance id to call, and what the instance actually is.
func TestKagentListAgentsFlattensResponse(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{
		AgentInstances: []*kagentapi.AgentInstance{
			{
				Id:            "11111111-2222-3333-4444-555555555555",
				Name:          "pi-go example",
				Harness:       &kagentapi.ResourceReference{Name: "pi-go"},
				AgentTemplate: &kagentapi.ResourceReference{Name: "pi-go"},
				State:         kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
				Creator:       "admin@kagent.dev",
			},
			{
				Id:      "66666666-7777-8888-9999-000000000000",
				State:   kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED,
				Failure: &kagentapi.Failure{Reason: "ImagePull", Message: "no such image"},
			},
		},
	}))

	ts := NewKagentToolsetFor(srv.URL, "kagent", "")
	out := ts.listAgents(t.Context(), KagentListAgentsInput{})
	if out.Error != "" {
		t.Fatalf("listAgents() error = %q", out.Error)
	}
	if out.TotalAgents != 2 || len(out.Agents) != 2 {
		t.Fatalf("got %d agents (total %d), want 2", len(out.Agents), out.TotalAgents)
	}
	first := out.Agents[0]
	if first.InstanceID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("instance id = %q", first.InstanceID)
	}
	if first.Harness != "pi-go" || first.Template != "pi-go" {
		t.Errorf("harness/template = %q/%q", first.Harness, first.Template)
	}
	if !strings.Contains(first.State, "READY") {
		t.Errorf("state = %q, want READY", first.State)
	}
	// A failed instance must carry the controller's reason: without it the
	// model sees "FAILED" and has nothing to report.
	if !strings.Contains(out.Agents[1].Failure, "ImagePull") {
		t.Errorf("failure = %q, want the controller's reason", out.Agents[1].Failure)
	}

	// The namespace has to reach the controller, or the list would be empty
	// against a cluster that scopes by it.
	var sent kagentapi.ListAgentInstancesRequest
	requestPayload(t, (*reqs)[0], &sent)
	if sent.GetNamespace() != "kagent" {
		t.Errorf("namespace = %q", sent.GetNamespace())
	}
	if sent.GetAgentTemplate() != "" || sent.GetHarness() != "" {
		t.Errorf("unfiltered list sent filters: template=%q harness=%q",
			sent.GetAgentTemplate(), sent.GetHarness())
	}
}

// TestKagentListAgentsPassesFilters checks the narrowing arguments reach the
// request rather than being dropped.
func TestKagentListAgentsPassesFilters(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{}))

	NewKagentToolsetFor(srv.URL, "kagent", "").listAgents(t.Context(),
		KagentListAgentsInput{AgentTemplate: "k8s-agent", Harness: "pi-go"})

	var sent kagentapi.ListAgentInstancesRequest
	requestPayload(t, (*reqs)[0], &sent)
	if sent.GetAgentTemplate() != "k8s-agent" || sent.GetHarness() != "pi-go" {
		t.Errorf("filters = %q/%q, want both passed through", sent.GetAgentTemplate(), sent.GetHarness())
	}
}

// TestKagentListAgentsReportsError keeps a controller failure visible in the
// result instead of surfacing as an empty list, which reads as "no agents".
func TestKagentListAgentsReportsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	out := NewKagentToolsetFor(srv.URL, "kagent", "").listAgents(t.Context(), KagentListAgentsInput{})
	if out.Error == "" {
		t.Error("Error is empty; an unreachable controller would read as zero agents")
	}
	if len(out.Agents) > 0 {
		t.Errorf("reported %d agents alongside an error", len(out.Agents))
	}
}

// TestKagentCreateAgentSendsPair pins the two names that make a create valid:
// kagent pairs a harness with a template, and request_id is the idempotency
// key the service requires to be non-empty.
func TestKagentCreateAgentSendsPair(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.CreateAgentInstanceResponse{
		AgentInstance: &kagentapi.AgentInstance{
			Id:    "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		},
	}))

	out := NewKagentToolsetFor(srv.URL, "kagent", "").createAgent(t.Context(), KagentCreateAgentInput{
		AgentTemplate: "k8s-agent",
		Harness:       "pi-go",
		Name:          "troubleshoot prod",
	})
	if out.Error != "" {
		t.Fatalf("createAgent() error = %q", out.Error)
	}
	if !out.Ready {
		t.Error("Ready = false for a READY instance")
	}

	var sent kagentapi.CreateAgentInstanceRequest
	requestPayload(t, (*reqs)[0], &sent)
	if sent.GetHarness() != "pi-go" || sent.GetAgentTemplate() != "k8s-agent" {
		t.Errorf("harness/template = %q/%q, want the pair as given (not the template twice)",
			sent.GetHarness(), sent.GetAgentTemplate())
	}
	if sent.GetName() != "troubleshoot prod" {
		t.Errorf("name = %q, want it forwarded", sent.GetName())
	}
	if n := len(sent.GetRequestId()); n < 1 || n > 128 {
		t.Errorf("request_id length = %d, want the service's 1-128 range", n)
	}
}

// TestKagentCreateAgentRequiresTemplate checks the early rejection, so the
// model gets a usable message rather than a validation failure from the
// controller.
func TestKagentCreateAgentRequiresTemplate(t *testing.T) {
	out := NewKagentToolsetFor("http://example", "kagent", "pi-go").
		createAgent(t.Context(), KagentCreateAgentInput{Harness: "pi-go"})
	if !strings.Contains(out.Error, "agent_template is required") {
		t.Errorf("error = %q, want a missing-template rejection", out.Error)
	}
}

// TestKagentCreateAgentRequiresHarness covers the other half of the pair: a
// create with no harness cannot be satisfied by a guess.
func TestKagentCreateAgentRequiresHarness(t *testing.T) {
	out := NewKagentToolsetFor("http://example", "kagent", "").
		createAgent(t.Context(), KagentCreateAgentInput{AgentTemplate: "k8s-agent"})
	if !strings.Contains(out.Error, "harness is required") {
		t.Errorf("error = %q, want a missing-harness rejection", out.Error)
	}
}

// TestKagentCallAgentByInstanceID checks that a UUID routes straight to A2A
// with no instance lookup: an id from list_agents is already the routing key.
func TestKagentCallAgentByInstanceID(t *testing.T) {
	const id = "11111111-2222-3333-4444-555555555555"
	srv := kagentCallStub(t, &a2apb.SendMessageResponse{
		Payload: &a2apb.SendMessageResponse_Task{Task: &a2apb.Task{
			Id:     "task-1",
			Status: &a2apb.TaskStatus{State: a2apb.TaskState_TASK_STATE_COMPLETED},
			Artifacts: []*a2apb.Artifact{{
				ArtifactId: "a1",
				Parts:      []*a2apb.Part{{Content: &a2apb.Part_Text{Text: "hello from the agent"}}},
			}},
		}},
	})

	out := NewKagentToolsetFor(srv.URL, "kagent", "").callAgent(t.Context(), KagentCallAgentInput{
		Agent:  id,
		Prompt: "hi",
	})
	if out.Error != "" {
		t.Fatalf("callAgent() error = %q", out.Error)
	}
	if out.Status != "completed" {
		t.Errorf("status = %q, want completed", out.Status)
	}
	if out.Result != "hello from the agent" {
		t.Errorf("result = %q, want the agent's reply", out.Result)
	}
	if out.InstanceID != id {
		t.Errorf("instance id = %q, want the given one", out.InstanceID)
	}
}

// TestKagentCallAgentByTemplateReusesReady checks the name path: a template
// name is resolved through the list-or-create flow, so a name works the same
// way it does in a configured A2A URL.
func TestKagentCallAgentByTemplateReusesReady(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, readyListResponse()))

	out := NewKagentToolsetFor(srv.URL, "kagent", "").callAgent(t.Context(), KagentCallAgentInput{
		Agent:  "k8s-agent",
		Prompt: "hi",
	})
	// The stub answers the instance lookup and then the A2A send with the
	// same body, which is not a valid task — the call fails, but the routing
	// decision is what this test is about.
	if out.InstanceID != "ready-1" {
		t.Errorf("instance id = %q, want the READY instance from the lookup", out.InstanceID)
	}
	paths := make([]string, 0, len(*reqs))
	for _, r := range *reqs {
		paths = append(paths, r.URL.Path)
	}
	if len(paths) == 0 || paths[0] != kagentAPIMethod("ListAgentInstances") {
		t.Errorf("paths = %v, want the instance lookup first", paths)
	}
	for _, p := range paths {
		if strings.Contains(p, "CreateAgentInstance") {
			t.Errorf("created an instance despite a READY one existing: %v", paths)
		}
	}
}

// TestKagentCallAgentRequiresPrompt keeps an empty send from reaching the
// controller.
func TestKagentCallAgentRequiresPrompt(t *testing.T) {
	out := NewKagentToolsetFor("http://example", "kagent", "").
		callAgent(t.Context(), KagentCallAgentInput{Agent: "k8s-agent"})
	if !strings.Contains(out.Error, "prompt is required") {
		t.Errorf("error = %q", out.Error)
	}
}

// TestKagentCallAgentDefaultAgent covers the fallback used when the model
// omits the agent, which is the one case an explicit default is for.
func TestKagentCallAgentDefaultAgent(t *testing.T) {
	out := NewKagentToolsetFor("http://example", "kagent", "").
		callAgent(t.Context(), KagentCallAgentInput{Prompt: "hi"})
	if !strings.Contains(out.Error, "agent is required") {
		t.Errorf("error = %q, want an agent requirement", out.Error)
	}
}

func TestIsAgentInstanceID(t *testing.T) {
	tests := map[string]bool{
		"11111111-2222-3333-4444-555555555555": true,
		"AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE": true,
		"pi-go":                                false,
		"k8s-agent":                            false,
		"111111112222333344445555555555555555": false, // no dashes
		"11111111-2222-3333-4444-55555555555g": false, // not hex
		"":                                     false,
	}
	for in, want := range tests {
		if got := isAgentInstanceID(in); got != want {
			t.Errorf("isAgentInstanceID(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestKagentUserIDFromEnv pins the identity override and its default.
func TestKagentUserIDFromEnv(t *testing.T) {
	t.Setenv(kagentUserIDEnv, "")
	if got := kagentUserID(); got != kagentDefaultUserID {
		t.Errorf("user = %q, want the default %q", got, kagentDefaultUserID)
	}
	t.Setenv(kagentUserIDEnv, "alice@example.com")
	if got := kagentUserID(); got != "alice@example.com" {
		t.Errorf("user = %q, want the override", got)
	}
}

// kagentCallStub serves the A2A SendMessage for any path, so a call that needs
// no instance lookup can be driven end to end.
func kagentCallStub(t *testing.T, resp proto.Message) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(grpcWebBody(t, resp))
	}))
	t.Cleanup(srv.Close)
	return srv
}
