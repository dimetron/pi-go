package tools

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2aclient "github.com/a2aproject/a2a-go/v2/a2aclient"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/kagentapi"
)

const (
	kagentNamespaceHeader  = "x-kagent-agent-instance-namespace"
	kagentInstanceIDHeader = "x-kagent-agent-instance-id"
	kagentUserIDHeader     = "x-user-id"
	kagentDefaultUserID    = "admin@kagent.dev"
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
			kagentUserIDHeader: kagentDefaultUserID,
		}),
	}
}

// listAgentInstances returns READY AgentInstances for the given agent template.
func (c *kagentAPIClient) listAgentInstances(ctx context.Context, namespace, template string) ([]*kagentapi.AgentInstance, error) {
	req := &kagentapi.ListAgentInstancesRequest{
		Namespace:     namespace,
		AgentTemplate: template,
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
	req := &kagentapi.CreateAgentInstanceRequest{
		Namespace:     namespace,
		Harness:       agent,
		AgentTemplate: agent,
	}
	var resp kagentapi.CreateAgentInstanceResponse
	if err := c.http.call(ctx, kagentAPIMethod("CreateAgentInstance"), req, &resp); err != nil {
		return nil, err
	}
	return resp.AgentInstance, nil
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

// newKagentA2AClient builds an a2aclient.Client that routes A2A calls for one
// kagent AgentInstance through the controller's gRPC-Web endpoint.
func newKagentA2AClient(ctx context.Context, ep *kagentEndpoint, instanceID string) (*a2aclient.Client, error) {
	headers := map[string]string{
		kagentNamespaceHeader:  ep.namespace,
		kagentInstanceIDHeader: instanceID,
		kagentUserIDHeader:     kagentDefaultUserID,
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
