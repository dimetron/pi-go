package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"

	"github.com/dimetron/pi-go/internal/kagentapi"
)

// kagentToolTimeout bounds one control-plane call. Listing instances and
// creating one are quick controller operations; a call that takes longer than
// this is a controller that is not answering.
const kagentMaxInstances = 100

// KagentToolset exposes the kagent control plane to the agent as tools, so a
// harness running inside kagent can discover its sibling agents and talk to
// them over A2A rather than needing each one pre-declared in config.json.
//
// It is deliberately separate from A2AToolset: that one is config-driven and
// reaches agents by a name the user wrote down, whereas this one asks the
// controller what exists. The two compose — the A2A tool still works for the
// agents a config declares.
type KagentToolset struct {
	// baseURL and namespace come from kagentEndpointFromEnv; an empty baseURL
	// means kagent is not configured, and Tools returns nothing so the model
	// is never offered a tool that cannot work.
	baseURL   string
	namespace string
	// defaultAgent is the agent call_agent routes to when its agent argument
	// is empty. It is never inferred from the environment: KAGENT_NAME holds
	// "<template>-<harness>" (compiler.go:70), which is not a usable harness
	// or template name, so guessing it would produce a call that fails with a
	// confusing not-found rather than a clear "which agent?".
	defaultAgent string
}

// NewKagentToolset builds the toolset for the controller this process runs
// against, read from the environment the kagent compiler injects.
//
// It returns nil when no controller URL is known, so callers can append the
// result without checking first and a non-kagent process pays nothing.
func NewKagentToolset() *KagentToolset {
	ep, ok := kagentEndpointFromEnv("")
	if !ok {
		return nil
	}
	return &KagentToolset{baseURL: ep.baseURL, namespace: ep.namespace}
}

// NewKagentToolsetFor builds a toolset against an explicit controller, which
// is what tests and CLI flags need.
func NewKagentToolsetFor(baseURL, namespace, defaultAgent string) *KagentToolset {
	return &KagentToolset{
		baseURL:      strings.TrimSuffix(baseURL, "/"),
		namespace:    orDefault(namespace, kagentDefaultNamespace),
		defaultAgent: defaultAgent,
	}
}

// Name returns the name of the toolset.
func (t *KagentToolset) Name() string { return "kagent" }

// Tools returns the kagent tools.
func (t *KagentToolset) Tools(agent.ReadonlyContext) ([]tool.Tool, error) {
	return KagentTools(t), nil
}

// KagentTools returns the kagent tools for a toolset, or nil for a nil
// toolset so an unconfigured process registers nothing.
func KagentTools(t *KagentToolset) []tool.Tool {
	if t == nil || t.baseURL == "" {
		return nil
	}
	listT, err := t.newListAgentsTool()
	if err != nil {
		return nil
	}
	createT, err := t.newCreateAgentTool()
	if err != nil {
		return nil
	}
	callT, err := t.newCallAgentTool()
	if err != nil {
		return nil
	}
	return []tool.Tool{listT, createT, callT}
}

// --- list_agents ---

// KagentListAgentsInput defines the parameters for kagent_list_agents.
type KagentListAgentsInput struct {
	// AgentTemplate narrows the list to one agent template. Empty lists the
	// instances of every template in the namespace.
	AgentTemplate string `json:"agent_template,omitempty"`
	// Harness narrows the list to one harness. Empty applies no harness
	// filter.
	Harness string `json:"harness,omitempty"`
}

// KagentAgent is one discovered kagent agent instance, flattened to what a
// caller needs to decide whether to talk to it.
type KagentAgent struct {
	// InstanceID is the A2A routing key — pass it to kagent_call_agent.
	InstanceID string `json:"instance_id"`
	Name       string `json:"name,omitempty"`
	Harness    string `json:"harness,omitempty"`
	Template   string `json:"template,omitempty"`
	State      string `json:"state"`
	Creator    string `json:"creator,omitempty"`
	// Failure is the controller's reason when State is FAILED.
	Failure string `json:"failure,omitempty"`
}

// KagentListAgentsOutput is the result of kagent_list_agents.
type KagentListAgentsOutput struct {
	Agents []KagentAgent `json:"agents"`
	// TotalAgents is the number found before the per-call cap, so a truncated
	// list is never read as the whole answer.
	TotalAgents int `json:"total_agents"`
	// Truncated reports that the cap dropped entries.
	Truncated bool   `json:"truncated,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (t *KagentToolset) newListAgentsTool() (tool.Tool, error) {
	desc := "List the kagent agent instances available to this agent, with the instance_id needed to call one. " +
		"Use it before kagent_call_agent to discover what exists. " +
		"Parameters: agent_template (optional, narrow to one template), harness (optional, narrow to one harness)."
	return newTool("kagent_list_agents", desc,
		func(ctx agent.Context, input KagentListAgentsInput) (KagentListAgentsOutput, error) {
			return t.listAgents(ctx, input), nil
		},
		map[string]string{"agent": "agent_template", "template": "agent_template"},
	)
}

// listAgents queries the controller and flattens the reply.
func (t *KagentToolset) listAgents(ctx context.Context, input KagentListAgentsInput) KagentListAgentsOutput {
	instances, err := newKagentAPIClient(t.baseURL).listInstancesFiltered(
		ctx, t.namespace, strings.TrimSpace(input.AgentTemplate), strings.TrimSpace(input.Harness))
	if err != nil {
		return KagentListAgentsOutput{Error: fmt.Sprintf("list agent instances: %v", err)}
	}

	out := KagentListAgentsOutput{TotalAgents: len(instances)}
	if len(instances) > kagentMaxInstances {
		instances = instances[:kagentMaxInstances]
		out.Truncated = true
	}
	out.Agents = make([]KagentAgent, 0, len(instances))
	for _, inst := range instances {
		out.Agents = append(out.Agents, kagentAgentFrom(inst))
	}
	return out
}

// kagentAgentFrom flattens one AgentInstance for the model.
func kagentAgentFrom(inst *kagentapi.AgentInstance) KagentAgent {
	a := KagentAgent{
		InstanceID: inst.GetId(),
		Name:       inst.GetName(),
		Harness:    inst.GetHarness().GetName(),
		Template:   inst.GetAgentTemplate().GetName(),
		State:      inst.GetState().String(),
		Creator:    inst.GetCreator(),
	}
	if f := inst.GetFailure(); f != nil {
		a.Failure = strings.TrimSpace(f.GetReason() + " " + f.GetMessage())
	}
	return a
}

// --- create_agent ---

// KagentCreateAgentInput defines the parameters for kagent_create_agent.
type KagentCreateAgentInput struct {
	// Harness names the harness (runtime adapter) to pair with the template —
	// kagent creates an instance from a (Harness, AgentTemplate) pair. The
	// harnesses admitting a template are the ones kagent_list_agents reports
	// for its existing instances.
	Harness string `json:"harness,omitempty"`
	// AgentTemplate names the template the instance is created from. Required.
	AgentTemplate string `json:"agent_template"`
	// Name is the optional display name for the new instance.
	Name string `json:"name,omitempty"`
}

// KagentCreateAgentOutput is the result of kagent_create_agent.
type KagentCreateAgentOutput struct {
	Agent KagentAgent `json:"agent"`
	// Ready reports whether the instance reached READY, which is the only
	// state in which an A2A call will be served.
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

func (t *KagentToolset) newCreateAgentTool() (tool.Tool, error) {
	desc := "Create a new kagent agent instance from an agent template, and return its instance_id. " +
		"Parameters: agent_template (required, the template to instantiate), " +
		"harness (the runtime adapter to pair with it), name (optional, display name). " +
		"The instance is only callable once it reports READY."
	return newTool("kagent_create_agent", desc,
		func(ctx agent.Context, input KagentCreateAgentInput) (KagentCreateAgentOutput, error) {
			return t.createAgent(ctx, input), nil
		},
		map[string]string{"template": "agent_template", "agent": "agent_template"},
	)
}

// createAgent creates one instance and reports whether it became READY.
func (t *KagentToolset) createAgent(ctx context.Context, input KagentCreateAgentInput) KagentCreateAgentOutput {
	template := strings.TrimSpace(input.AgentTemplate)
	if template == "" {
		return KagentCreateAgentOutput{Error: "agent_template is required"}
	}
	harness := orDefault(strings.TrimSpace(input.Harness), t.defaultAgent)
	if harness == "" {
		return KagentCreateAgentOutput{Error: "harness is required: kagent pairs a harness with the template to create an instance"}
	}

	inst, err := newKagentAPIClient(t.baseURL).createNamedAgentInstance(
		ctx, t.namespace, harness, template, strings.TrimSpace(input.Name))
	if err != nil {
		return KagentCreateAgentOutput{Error: fmt.Sprintf("create agent instance: %v", err)}
	}
	return KagentCreateAgentOutput{
		Agent: kagentAgentFrom(inst),
		Ready: inst.GetState() == kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY,
	}
}

// --- call_agent ---

// KagentCallAgentInput defines the parameters for kagent_call_agent.
type KagentCallAgentInput struct {
	// Agent is the instance id from kagent_list_agents, or the template name
	// of an agent to route to (an existing READY instance is reused, and one
	// is created when none is running).
	Agent string `json:"agent"`
	// Prompt is the message to send.
	Prompt string `json:"prompt"`
	// Stream requests the streaming transport.
	Stream bool `json:"stream,omitempty"`
}

// KagentCallAgentOutput is the result of kagent_call_agent.
type KagentCallAgentOutput struct {
	Agent string `json:"agent"`
	// InstanceID is the AgentInstance the call was routed to.
	InstanceID string `json:"instance_id,omitempty"`
	Status     string `json:"status"`
	Result     string `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (t *KagentToolset) newCallAgentTool() (tool.Tool, error) {
	desc := "Send a prompt to another kagent agent over A2A and return its reply. " +
		"Parameters: agent (instance_id from kagent_list_agents, or an agent template name to route to), " +
		"prompt (the message), stream (optional, use the streaming transport)."
	return newTool("kagent_call_agent", desc,
		func(ctx agent.Context, input KagentCallAgentInput) (KagentCallAgentOutput, error) {
			return t.callAgent(ctx, input), nil
		},
		map[string]string{"agent_name": "agent", "instance_id": "agent", "message": "prompt", "input": "prompt"},
	)
}

// callAgent routes one message to an agent and returns its reply.
//
// The agent argument accepts either an instance id or a template name. An
// instance id is used as-is; a template name goes through
// ensureAgentInstance, which reuses a READY instance or creates one — the same
// path a kagent-shaped entry in config.json already takes, so a name that
// works there works here.
func (t *KagentToolset) callAgent(ctx context.Context, input KagentCallAgentInput) KagentCallAgentOutput {
	target := strings.TrimSpace(input.Agent)
	if target == "" {
		target = t.defaultAgent
	}
	if target == "" {
		return KagentCallAgentOutput{Status: "failed", Error: "agent is required"}
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return KagentCallAgentOutput{Status: "failed", Agent: target, Error: "prompt is required"}
	}

	api := newKagentAPIClient(t.baseURL)
	instanceID, err := api.resolveInstance(ctx, t.namespace, target, t.defaultAgent)
	if err != nil {
		return KagentCallAgentOutput{Status: "failed", Agent: target, Error: err.Error()}
	}

	ep := &kagentEndpoint{baseURL: t.baseURL, namespace: t.namespace, agent: target}
	client, err := newKagentA2AClient(ctx, ep, instanceID)
	if err != nil {
		return KagentCallAgentOutput{
			Status: "failed", Agent: target, InstanceID: instanceID,
			Error: fmt.Sprintf("creating A2A client: %v", err),
		}
	}
	defer func() { _ = client.Destroy() }()

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(input.Prompt))
	out := KagentCallAgentOutput{Agent: target, InstanceID: instanceID}

	if input.Stream {
		out.Status = "streaming"
		out.Result, err = collectStreamText(ctx, client, msg)
	} else {
		var task a2a.SendMessageResult
		task, err = client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
		if err == nil {
			out.Status = "completed"
			out.Result = extractSendMessageResult(task)
		}
	}
	if err != nil {
		out.Status = "failed"
		out.Error = err.Error()
	}
	return out
}

// resolveInstance turns the caller's target into an AgentInstance id.
//
// A UUID is taken to be an instance id and used directly. Anything else is a
// template name, resolved through the same reuse-or-create path a configured
// kagent URL uses.
func (c *kagentAPIClient) resolveInstance(ctx context.Context, namespace, target, harness string) (string, error) {
	if isAgentInstanceID(target) {
		return target, nil
	}
	if harness != "" && harness != target {
		return c.ensureInstanceFor(ctx, namespace, harness, target)
	}
	return c.ensureAgentInstance(ctx, namespace, target)
}

// ensureInstanceFor finds a READY instance of one (harness, template) pair,
// creating one if none exists.
func (c *kagentAPIClient) ensureInstanceFor(ctx context.Context, namespace, harness, template string) (string, error) {
	instances, err := c.listInstancesFiltered(ctx, namespace, template, harness)
	if err != nil {
		return "", fmt.Errorf("list AgentInstances for %q: %w", template, err)
	}
	for _, inst := range instances {
		if inst.GetState() == kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY {
			return inst.GetId(), nil
		}
	}
	inst, err := c.createNamedAgentInstance(ctx, namespace, harness, template, "")
	if err != nil {
		return "", fmt.Errorf("create AgentInstance for %q: %w", template, err)
	}
	if inst.GetState() != kagentapi.AgentInstanceState_AGENT_INSTANCE_STATE_READY {
		return "", fmt.Errorf("AgentInstance %q is %s, not READY", inst.GetId(), inst.GetState())
	}
	return inst.GetId(), nil
}

// isAgentInstanceID reports whether s looks like an AgentInstance id rather
// than a resource name. The controller mints ids as UUIDs
// (agent_instances.proto validates agent_instance_id as one).
func isAgentInstanceID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if !isHexDigit(c) {
			return false
		}
	}
	return true
}

func isHexDigit(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// orDefault returns v when it is non-empty, otherwise fallback.
func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
