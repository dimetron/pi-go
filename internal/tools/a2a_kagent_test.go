package tools

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2apb/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/kagentapi"
)

func TestParseKagentURL(t *testing.T) {
	tests := []struct {
		name, raw, wantBase, wantAgent string
		wantErr                        bool
	}{
		{"full path", "http://127.0.0.1:8083/api/a2a/kagent/k8s-agent", "http://127.0.0.1:8083", "k8s-agent", false},
		{"trailing slash", "http://127.0.0.1:8083/api/a2a/kagent/k8s-agent/", "http://127.0.0.1:8083", "k8s-agent", false},
		{"no api prefix", "http://host:8083/a2a/kagent/pi-go", "http://host:8083", "pi-go", false},
		{"bare agent", "http://host:8083/pi-go", "http://host:8083", "pi-go", false},
		{"https scheme", "https://kagent.example/api/a2a/kagent/pi-go", "https://kagent.example", "pi-go", false},
		{"no agent", "http://host:8083/", "", "", true},
		{"no path at all", "http://host:8083", "", "", true},
		// url.Parse rejects an unterminated IPv6 literal, which is the only
		// realistic way a configured URL fails to parse at all.
		{"unparseable", "http://[::1", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep, err := parseKagentURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseKagentURL(%q) = %+v, want an error", tt.raw, ep)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseKagentURL(%q) = %v", tt.raw, err)
			}
			if ep.baseURL != tt.wantBase || ep.agent != tt.wantAgent || ep.namespace != "kagent" {
				t.Errorf("got base=%q agent=%q ns=%q", ep.baseURL, ep.agent, ep.namespace)
			}
		})
	}
}

func TestIsKagentURL(t *testing.T) {
	if !isKagentURL("http://h:8083/api/a2a/kagent/x") {
		t.Error("kagent URL not recognized")
	}
	if isKagentURL("http://h:9000/a2a") {
		t.Error("plain A2A URL treated as kagent")
	}
}

func TestKagentAPIMethod(t *testing.T) {
	want := "/kagent.api.v1alpha1.AgentInstanceService/ListAgentInstances"
	if got := kagentAPIMethod("ListAgentInstances"); got != want {
		t.Errorf("kagentAPIMethod() = %q, want %q", got, want)
	}
}

// TestNewKagentAPIClientSetsUserID checks the header the controller's
// AgentInstanceService authorizes on: without it every call is rejected.
func TestNewKagentAPIClientSetsUserID(t *testing.T) {
	c := newKagentAPIClient("http://example")
	if got := c.http.headers[kagentUserIDHeader]; got != kagentDefaultUserID {
		t.Errorf("%s = %q, want %q", kagentUserIDHeader, got, kagentDefaultUserID)
	}
}

// TestListAgentInstancesRequest pins the query the controller receives: the
// namespace and template scope the list, and the page limit bounds it.
func TestListAgentInstancesRequest(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, readyListResponse()))

	got, err := newKagentAPIClient(srv.URL).listAgentInstances(t.Context(), "kagent", "pi-go")
	if err != nil {
		t.Fatalf("listAgentInstances() = %v", err)
	}
	if len(got) != 1 || got[0].GetId() != "ready-1" {
		t.Errorf("instances = %+v, want the one from the response", got)
	}

	if path := (*reqs)[0].URL.Path; path != kagentAPIMethod("ListAgentInstances") {
		t.Errorf("path = %q", path)
	}
	var sent kagentapi.ListAgentInstancesRequest
	requestPayload(t, (*reqs)[0], &sent)
	if sent.GetNamespace() != "kagent" || sent.GetAgentTemplate() != "pi-go" {
		t.Errorf("sent = %+v, want the namespace and template scoped", &sent)
	}
	if sent.GetPage().GetLimit() == 0 {
		t.Error("sent no page limit, so the controller would apply its own default")
	}
}

func TestListAgentInstancesPropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := newKagentAPIClient(srv.URL).listAgentInstances(t.Context(), "kagent", "pi-go"); err == nil {
		t.Error("listAgentInstances() = nil, want the HTTP failure surfaced")
	}
}

// TestCreateAgentInstanceRequest checks the fields the controller validates:
// both harness and agent_template name the agent, and request_id is the
// idempotency key.
func TestCreateAgentInstanceRequest(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &kagentapi.CreateAgentInstanceResponse{
		AgentInstance: &kagentapi.AgentInstance{
			Id:    "inst-1",
			State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		},
	}))

	inst, err := newKagentAPIClient(srv.URL).createAgentInstance(t.Context(), "kagent", "pi-go")
	if err != nil {
		t.Fatalf("createAgentInstance() = %v", err)
	}
	if inst.GetId() != "inst-1" {
		t.Errorf("instance = %+v, want the created one", inst)
	}

	var sent kagentapi.CreateAgentInstanceRequest
	requestPayload(t, (*reqs)[0], &sent)
	if sent.GetNamespace() != "kagent" {
		t.Errorf("namespace = %q, want %q", sent.GetNamespace(), "kagent")
	}
	if sent.GetHarness() != "pi-go" || sent.GetAgentTemplate() != "pi-go" {
		t.Errorf("harness/template = %q/%q, want both to name the agent", sent.GetHarness(), sent.GetAgentTemplate())
	}
	if n := len(sent.GetRequestId()); n < 1 || n > 128 {
		t.Errorf("request_id length = %d, want the service's 1-128 range", n)
	}
}

func TestCreateAgentInstancePropagatesError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(grpcWebFrame(0, nil))
		_, _ = w.Write(grpcWebFrame(0x80, []byte("grpc-status:3\r\ngrpc-message:invalid\r\n")))
	}))
	defer srv.Close()

	if _, err := newKagentAPIClient(srv.URL).createAgentInstance(t.Context(), "kagent", "pi-go"); err == nil {
		t.Error("createAgentInstance() = nil, want the grpc-status surfaced")
	}
}

// TestEnsureAgentInstanceReusesReady checks the happy path that avoids a
// create entirely.
func TestEnsureAgentInstanceReusesReady(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, readyListResponse()))

	id, err := newKagentAPIClient(srv.URL).ensureAgentInstance(t.Context(), "kagent", "pi-go")
	if err != nil {
		t.Fatalf("ensureAgentInstance() = %v", err)
	}
	if id != "ready-1" {
		t.Errorf("id = %q, want the READY instance", id)
	}
	for _, r := range *reqs {
		if strings.Contains(r.URL.Path, "CreateAgentInstance") {
			t.Errorf("created an instance despite a READY one existing: %s", r.URL.Path)
		}
	}
}

// TestEnsureAgentInstanceSkipsNotReady covers the selection rule: a listed
// instance that is still starting is not routable, so it must not be picked.
func TestEnsureAgentInstanceSkipsNotReady(t *testing.T) {
	srv := kagentControllerStub(t, &kagentapi.ListAgentInstancesResponse{
		AgentInstances: []*kagentapi.AgentInstance{
			{Id: "creating-1", State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING},
			{Id: "failed-1", State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_FAILED},
			{Id: "ready-2", State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY},
		},
	}, &kagentapi.CreateAgentInstanceResponse{})

	id, err := newKagentAPIClient(srv.URL).ensureAgentInstance(t.Context(), "kagent", "pi-go")
	if err != nil {
		t.Fatalf("ensureAgentInstance() = %v", err)
	}
	if id != "ready-2" {
		t.Errorf("id = %q, want the READY instance rather than the pending one", id)
	}
}

// TestEnsureAgentInstanceCreatesWhenNoneReady covers the fallback: an empty
// list means the agent has no running instance yet.
func TestEnsureAgentInstanceCreatesWhenNoneReady(t *testing.T) {
	srv := kagentControllerStub(t,
		&kagentapi.ListAgentInstancesResponse{},
		&kagentapi.CreateAgentInstanceResponse{AgentInstance: &kagentapi.AgentInstance{
			Id:    "created-1",
			State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		}},
	)

	id, err := newKagentAPIClient(srv.URL).ensureAgentInstance(t.Context(), "kagent", "pi-go")
	if err != nil {
		t.Fatalf("ensureAgentInstance() = %v", err)
	}
	if id != "created-1" {
		t.Errorf("id = %q, want the newly created instance", id)
	}
}

// TestEnsureAgentInstanceRejectsNotReadyCreate guards against handing back an
// instance id that cannot serve traffic yet: routing A2A calls at it would
// fail later with a much less obvious error.
func TestEnsureAgentInstanceRejectsNotReadyCreate(t *testing.T) {
	srv := kagentControllerStub(t,
		&kagentapi.ListAgentInstancesResponse{},
		&kagentapi.CreateAgentInstanceResponse{AgentInstance: &kagentapi.AgentInstance{
			Id:    "created-1",
			State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_CREATING,
		}},
	)

	_, err := newKagentAPIClient(srv.URL).ensureAgentInstance(t.Context(), "kagent", "pi-go")
	if err == nil || !strings.Contains(err.Error(), "not READY") {
		t.Errorf("err = %v, want a not-READY rejection", err)
	}
}

func TestEnsureAgentInstanceListError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := newKagentAPIClient(srv.URL).ensureAgentInstance(t.Context(), "kagent", "pi-go")
	if err == nil || !strings.Contains(err.Error(), "list AgentInstances") {
		t.Errorf("err = %v, want a list failure", err)
	}
}

// TestEnsureAgentInstanceCreateError covers the second failure point, which
// carries a different message than the list failure so the two are
// distinguishable in a log.
func TestEnsureAgentInstanceCreateError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "CreateAgentInstance") {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(grpcWebBody(t, &kagentapi.ListAgentInstancesResponse{}))
	}))
	defer srv.Close()

	_, err := newKagentAPIClient(srv.URL).ensureAgentInstance(t.Context(), "kagent", "pi-go")
	if err == nil || !strings.Contains(err.Error(), "create AgentInstance") {
		t.Errorf("err = %v, want a create failure", err)
	}
}

// TestNewKagentA2AClientBindsInstance checks that the A2A calls a built client
// makes carry the namespace, instance, and user headers — routing to the wrong
// AgentInstance is otherwise silent.
func TestNewKagentA2AClientBindsInstance(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, &a2apb.Task{
		Id:     "task-1",
		Status: &a2apb.TaskStatus{State: a2apb.TaskState_TASK_STATE_COMPLETED},
	}))

	ep := &kagentEndpoint{baseURL: srv.URL, namespace: "kagent", agent: "pi-go"}
	client, err := newKagentA2AClient(t.Context(), ep, "inst-42")
	if err != nil {
		t.Fatalf("newKagentA2AClient() = %v", err)
	}
	t.Cleanup(func() { _ = client.Destroy() })

	if _, err := client.GetTask(t.Context(), &a2a.GetTaskRequest{ID: "task-1"}); err != nil {
		t.Fatalf("GetTask() = %v", err)
	}
	if len(*reqs) == 0 {
		t.Fatal("the client made no request")
	}
	h := (*reqs)[0].Header
	for k, want := range map[string]string{
		kagentNamespaceHeader:  "kagent",
		kagentInstanceIDHeader: "inst-42",
		kagentUserIDHeader:     kagentDefaultUserID,
	} {
		if h.Get(k) != want {
			t.Errorf("header %s = %q, want %q", k, h.Get(k), want)
		}
	}
}

// TestKagentClientForConfigPropagatesURLError covers the early return before
// any network call is attempted.
func TestKagentClientForConfigPropagatesURLError(t *testing.T) {
	_, err := kagentClientForConfig(t.Context(), config.A2AAgentConfig{URL: "http://host/"})
	if err == nil {
		t.Error("kagentClientForConfig() = nil, want an error for a URL with no agent")
	}
}

// TestKagentClientForConfigPropagatesInstanceError covers the second failure
// point: a reachable controller that cannot produce an instance.
func TestKagentClientForConfigPropagatesInstanceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := kagentClientForConfig(t.Context(), config.A2AAgentConfig{
		URL: srv.URL + "/api/a2a/kagent/pi-go",
	})
	if err == nil || !strings.Contains(err.Error(), "list AgentInstances") {
		t.Errorf("err = %v, want the instance lookup failure", err)
	}
}

// TestKagentClientForConfigEndToEnd drives the whole path: parse, reuse a
// READY instance, and build a client whose A2A calls route to it.
func TestKagentClientForConfigEndToEnd(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, readyListResponse()))

	client, err := kagentClientForConfig(t.Context(), config.A2AAgentConfig{
		URL: srv.URL + "/api/a2a/kagent/pi-go",
	})
	if err != nil {
		t.Fatalf("kagentClientForConfig() = %v", err)
	}
	if client == nil {
		t.Fatal("kagentClientForConfig() returned a nil client")
	}
	t.Cleanup(func() { _ = client.Destroy() })

	if len(*reqs) != 1 {
		t.Fatalf("made %d requests, want just the instance lookup", len(*reqs))
	}
	if path := (*reqs)[0].URL.Path; path != kagentAPIMethod("ListAgentInstances") {
		t.Errorf("path = %q, want the instance lookup", path)
	}
}

// TestGetClientRoutesKagentURLs is the seam between the generic A2A tool and
// the kagent transport: a kagent-shaped URL must take the AgentInstance path,
// which a plain JSON-RPC client would not.
func TestGetClientRoutesKagentURLs(t *testing.T) {
	srv, reqs := recordingServer(t, grpcWebBody(t, readyListResponse()))

	cache := NewClientCache(&config.A2AConfig{Agents: []config.A2AAgentConfig{{
		Name: "k8s",
		URL:  srv.URL + "/api/a2a/kagent/k8s-agent",
	}}})
	t.Cleanup(cache.Close)

	client, err := cache.GetClient(t.Context(), "k8s")
	if err != nil {
		t.Fatalf("GetClient() = %v", err)
	}
	if client == nil {
		t.Fatal("GetClient() returned a nil client")
	}
	if len(*reqs) != 1 || (*reqs)[0].URL.Path != kagentAPIMethod("ListAgentInstances") {
		t.Errorf("requests = %v, want the kagent AgentInstance lookup", *reqs)
	}
}

// kagentControllerStub serves the two AgentInstanceService methods
// ensureAgentInstance calls, dispatching on the gRPC path.
func kagentControllerStub(t *testing.T, list *kagentapi.ListAgentInstancesResponse, create *kagentapi.CreateAgentInstanceResponse) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg proto.Message = list
		if strings.Contains(r.URL.Path, "CreateAgentInstance") {
			msg = create
		}
		w.Header().Set("Content-Type", "application/grpc-web+proto")
		_, _ = w.Write(grpcWebBody(t, msg))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// readyListResponse returns a list containing one READY AgentInstance.
func readyListResponse() *kagentapi.ListAgentInstancesResponse {
	return &kagentapi.ListAgentInstancesResponse{
		AgentInstances: []*kagentapi.AgentInstance{{
			Id:    "ready-1",
			State: kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
		}},
	}
}
