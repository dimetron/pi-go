package tools

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2aclient "github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/google/uuid"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/kagentapi"
)

const (
	kagentNamespaceHeader  = "x-kagent-agent-instance-namespace"
	kagentInstanceIDHeader = "x-kagent-agent-instance-id"
	kagentUserIDHeader     = "x-user-id"
	kagentDefaultUserID    = "admin@kagent.dev"
	kagentDefaultNamespace = "kagent"

	// Environment the kagent compiler injects into a harness pod, plus the
	// pi-go-specific overrides. See go/core/internal/translator/kagent/
	// compiler.go and go/core/pkg/env/kagent.go in the kagent repo.
	kagentAPIURLEnv    = "KAGENT_API_URL"
	kagentNamespaceEnv = "KAGENT_NAMESPACE"
	kagentUserIDEnv    = "PI_KAGENT_USER"
)

// kagentEndpoint describes a kagent controller reachable from a config URL of
// the form http://<host>:<port>/api/a2a/kagent/<agent>.
type kagentEndpoint struct {
	// baseURL is the controller origin, e.g. http://127.0.0.1:8083.
	baseURL string
	// namespace is the kagent namespace from the URL path (default "kagent").
	namespace string
	// agent is the AgentTemplate/Harness name from the URL path.
	agent string
}

// parseKagentURL parses a config A2A agent URL into a kagentEndpoint.
// Supported shapes:
//
//	http://127.0.0.1:8083/api/a2a/kagent/k8s-agent
//	http://127.0.0.1:8083/api/a2a/kagent/k8s-agent/
func parseKagentURL(raw string) (*kagentEndpoint, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse kagent URL %q: %w", raw, err)
	}
	ep := &kagentEndpoint{baseURL: strings.TrimSuffix(u.Scheme+"://"+u.Host, "/")}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	// Expect [api a2a kagent <agent>]; tolerate a missing /api prefix.
	if len(parts) >= 2 && parts[len(parts)-2] == "kagent" {
		ep.namespace = "kagent"
		ep.agent = parts[len(parts)-1]
	} else if len(parts) >= 1 {
		ep.namespace = "kagent"
		ep.agent = parts[len(parts)-1]
	} else {
		return nil, fmt.Errorf("kagent URL %q has no agent name", raw)
	}
	if ep.agent == "" {
		return nil, fmt.Errorf("kagent URL %q has no agent name", raw)
	}
	return ep, nil
}

// kagentAPIClient talks to the kagent controller's AgentInstanceService over
// gRPC-Web (the controller serves every registered gRPC service on its HTTP
// port when the request content-type is application/grpc-web+proto).
type kagentAPIClient struct {
	http *grpcWebTransport
}

func newKagentAPIClient(baseURL string) *kagentAPIClient {
	return &kagentAPIClient{
		http: newGRPCWebTransport(baseURL, map[string]string{
			kagentUserIDHeader: kagentUserID(),
		}),
	}
}

// listAgentInstances returns READY AgentInstances for the given agent template.
func (c *kagentAPIClient) listAgentInstances(ctx context.Context, namespace, template string) ([]*kagentapi.AgentInstance, error) {
	return c.listInstancesFiltered(ctx, namespace, template, "")
}

// listInstancesFiltered lists AgentInstances in a namespace, optionally
// narrowed to one agent template and/or one harness.
//
// The filters are the controller's own: passing an empty string omits that
// filter, which is what discovery needs when it should see every instance in
// the namespace rather than only those of one template.
func (c *kagentAPIClient) listInstancesFiltered(ctx context.Context, namespace, template, harness string) ([]*kagentapi.AgentInstance, error) {
	req := &kagentapi.ListAgentInstancesRequest{
		Namespace:     namespace,
		AgentTemplate: template,
		Harness:       harness,
		Page:          &kagentapi.PageRequest{Limit: 50},
	}
	var resp kagentapi.ListAgentInstancesResponse
	if err := c.http.callPath(ctx, kagentAPIMethod("ListAgentInstances"), req, &resp); err != nil {
		return nil, err
	}
	return resp.AgentInstances, nil
}

// createAgentInstance creates a new AgentInstance for the agent.
func (c *kagentAPIClient) createAgentInstance(ctx context.Context, namespace, agent string) (*kagentapi.AgentInstance, error) {
	return c.createNamedAgentInstance(ctx, namespace, agent, agent, "")
}

// createNamedAgentInstance creates an AgentInstance for an explicit
// (harness, template) pair with an optional display name.
//
// Harness and AgentTemplate are separate resources that kagent pairs at
// instance creation, so they cannot be assumed equal: pi-go runs under the
// harness named "pi-go", but a caller may want an instance of any template the
// harness admits. The one-argument form above is the common same-name case.
func (c *kagentAPIClient) createNamedAgentInstance(ctx context.Context, namespace, harness, template, name string) (*kagentapi.AgentInstance, error) {
	req := &kagentapi.CreateAgentInstanceRequest{
		Namespace:     namespace,
		Harness:       harness,
		AgentTemplate: template,
		// request_id is validated as a 1-128 character idempotency key, so
		// the service rejects a create with InvalidArgument when it is
		// empty. It must be unique per creation attempt: reusing one would
		// make a genuine second create collapse into the first's result.
		RequestId: newKagentRequestID(),
		Name:      name,
	}
	var resp kagentapi.CreateAgentInstanceResponse
	// callPath, not call: call prepends the A2A service prefix, which would
	// post to /lf.a2a.v1.A2AService/ + this already-complete method path.
	if err := c.http.callPath(ctx, kagentAPIMethod("CreateAgentInstance"), req, &resp); err != nil {
		return nil, err
	}
	return resp.AgentInstance, nil
}

// newKagentRequestID returns an idempotency key for one create attempt. The
// service caps request_id at 128 characters; a UUID is far inside that.
func newKagentRequestID() string {
	return uuid.NewString()
}

// ensureAgentInstance finds a READY AgentInstance for the agent, creating one
// if none exists. The instance id is the A2A routing key.
func (c *kagentAPIClient) ensureAgentInstance(ctx context.Context, namespace, agent string) (string, error) {
	instances, err := c.listAgentInstances(ctx, namespace, agent)
	if err != nil {
		return "", fmt.Errorf("list AgentInstances for %q: %w", agent, err)
	}
	for _, inst := range instances {
		if inst.GetState() == kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY {
			return inst.GetId(), nil
		}
	}
	inst, err := c.createAgentInstance(ctx, namespace, agent)
	if err != nil {
		return "", fmt.Errorf("create AgentInstance for %q: %w", agent, err)
	}
	if inst.GetState() != kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY {
		return "", fmt.Errorf("AgentInstance %q is %s, not READY", inst.GetId(), inst.GetState())
	}
	return inst.GetId(), nil
}

// kagentAPIMethod builds the gRPC-Web path for an AgentInstanceService method.
func kagentAPIMethod(method string) string {
	return "/kagent.api.v1alpha1.AgentInstanceService/" + method
}

// kagentEndpointFromEnv describes the controller this process is running
// inside, from the environment the kagent compiler injects into harness pods.
//
// KAGENT_API_URL is the control-plane base URL (the kagent compiler sets it to
// http://<controller>.<namespace>:8083); KAGENT_NAMESPACE and KAGENT_NAME scope
// the resources. Empty is returned when neither an explicit url override nor
// KAGENT_API_URL is set, which tells the caller kagent is not configured here
// rather than inventing a default endpoint to fail against.
func kagentEndpointFromEnv(urlOverride string) (*kagentEndpoint, bool) {
	base := strings.TrimSpace(urlOverride)
	if base == "" {
		base = strings.TrimSpace(os.Getenv(kagentAPIURLEnv))
	}
	if base == "" {
		return nil, false
	}
	namespace := strings.TrimSpace(os.Getenv(kagentNamespaceEnv))
	if namespace == "" {
		namespace = kagentDefaultNamespace
	}
	return &kagentEndpoint{baseURL: strings.TrimSuffix(base, "/"), namespace: namespace}, true
}

// kagentUserID resolves the identity the controller authorizes on. The
// compiler-injected default is the controller's own admin, which is what a
// harness running with no external identity uses; PI_KAGENT_USER overrides it
// for deployments behind an identity provider.
func kagentUserID() string {
	if user := strings.TrimSpace(os.Getenv(kagentUserIDEnv)); user != "" {
		return user
	}
	return kagentDefaultUserID
}

// newKagentA2AClient builds an a2aclient.Client that routes A2A calls for one
// kagent AgentInstance through the controller's gRPC-Web endpoint.
func newKagentA2AClient(ctx context.Context, ep *kagentEndpoint, instanceID string) (*a2aclient.Client, error) {
	headers := map[string]string{
		kagentNamespaceHeader:  ep.namespace,
		kagentInstanceIDHeader: instanceID,
		kagentUserIDHeader:     kagentUserID(),
	}
	factory := &grpcWebTransportFactory{baseURL: ep.baseURL, headers: headers}
	return a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{{
		URL:             ep.baseURL,
		ProtocolBinding: a2a.TransportProtocolGRPC,
		ProtocolVersion: a2a.Version,
	}},
		a2aclient.WithDefaultsDisabled(),
		a2aclient.WithTransport(a2a.TransportProtocolGRPC, factory),
	)
}

// kagentClientForConfig returns an a2aclient.Client for a kagent-style config
// URL, creating/reusing an AgentInstance as needed.
func kagentClientForConfig(ctx context.Context, agent config.A2AAgentConfig) (*a2aclient.Client, error) {
	ep, err := parseKagentURL(agent.URL)
	if err != nil {
		return nil, err
	}

	api := newKagentAPIClient(ep.baseURL)
	instanceID, err := api.ensureAgentInstance(ctx, ep.namespace, ep.agent)
	if err != nil {
		return nil, err
	}

	return newKagentA2AClient(ctx, ep, instanceID)
}

// isKagentURL reports whether a config URL targets a kagent controller A2A
// endpoint (path contains /api/a2a/kagent/).
func isKagentURL(raw string) bool {
	return strings.Contains(raw, "/api/a2a/kagent/")
}
