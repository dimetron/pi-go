package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2aclient "github.com/a2aproject/a2a-go/v2/a2aclient"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"

	"github.com/dimetron/pi-go/internal/config"
)

// A2AInput defines the parameters for the a2a tool.
type A2AInput struct {
	// AgentName is the name of the configured A2A agent to call.
	AgentName string `json:"agent_name,omitempty"`
	// Prompt is the message to send to the agent.
	Prompt string `json:"prompt,omitempty"`
	// Stream enables streaming response mode.
	Stream bool `json:"stream,omitempty"`
}

// A2AOutput is the result from an A2A agent call.
type A2AOutput struct {
	Agent  string `json:"agent"`
	Status string `json:"status"` // "completed", "streaming", "failed"
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

// ClientCache manages A2A client instances for each configured agent.
// It automatically evicts stale clients when agent configurations change.
type ClientCache struct {
	agents    map[string]config.A2AAgentConfig
	clients   map[string]*a2aclient.Client
	clientsMu sync.RWMutex
}

// NewClientCache creates a new ClientCache for the given A2A configuration.
func NewClientCache(cfg *config.A2AConfig) *ClientCache {
	cache := &ClientCache{
		agents:  make(map[string]config.A2AAgentConfig),
		clients: make(map[string]*a2aclient.Client),
	}
	if cfg != nil {
		for _, agent := range cfg.Agents {
			cache.agents[agent.Name] = agent
		}
	}
	return cache
}

// UpdateAgents replaces the agent configuration and evicts stale clients.
// Clients for removed agents are closed and removed from the cache.
func (c *ClientCache) UpdateAgents(cfg *config.A2AConfig) {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Build new agent map
	newAgents := make(map[string]config.A2AAgentConfig)
	if cfg != nil {
		for _, agent := range cfg.Agents {
			newAgents[agent.Name] = agent
		}
	}

	// Evict clients for agents that no longer exist or have changed config
	for name, client := range c.clients {
		if _, exists := newAgents[name]; !exists {
			// Agent removed - destroy and evict client
			_ = client.Destroy()
			delete(c.clients, name)
		} else if oldCfg, wasConfigured := c.agents[name]; wasConfigured {
			// Check if URL changed
			if newAgents[name].URL != oldCfg.URL {
				_ = client.Destroy()
				delete(c.clients, name)
			}
		}
	}

	c.agents = newAgents
}

// Close destroys all cached clients and clears the cache.
func (c *ClientCache) Close() {
	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	for _, client := range c.clients {
		_ = client.Destroy()
	}
	c.clients = make(map[string]*a2aclient.Client)
}

// GetClient returns or creates an A2A client for the given agent name.
func (c *ClientCache) GetClient(ctx context.Context, agentName string) (*a2aclient.Client, error) {
	c.clientsMu.RLock()
	client, ok := c.clients[agentName]
	c.clientsMu.RUnlock()
	if ok {
		return client, nil
	}

	// Look up agent config
	agent, ok := c.agents[agentName]
	if !ok {
		return nil, fmt.Errorf("unknown agent: %q (available: %v)", agentName, c.availableAgents())
	}

	c.clientsMu.Lock()
	defer c.clientsMu.Unlock()

	// Double-check after acquiring write lock
	if client, ok := c.clients[agentName]; ok {
		return client, nil
	}

	// Create new client using AgentInterface
	agentInterface := a2a.NewAgentInterface(agent.URL, a2a.TransportProtocolJSONRPC)
	client, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{agentInterface})
	if err != nil {
		return nil, fmt.Errorf("creating A2A client for %q: %w", agentName, err)
	}

	c.clients[agentName] = client
	return client, nil
}

// availableAgents returns a sorted list of available agent names.
func (c *ClientCache) availableAgents() string {
	names := make([]string, 0, len(c.agents))
	for name := range c.agents {
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// SendMessage sends a message to an A2A agent and returns the result.
// It handles both streaming and non-streaming modes.
func (c *ClientCache) SendMessage(ctx context.Context, agentName string, prompt string, stream bool) A2AOutput {
	result := A2AOutput{Agent: agentName}

	client, err := c.GetClient(ctx, agentName)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}

	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(prompt))

	if stream {
		result.Status = "streaming"
		result.Result, err = c.sendStreamingMessage(ctx, client, msg)
	} else {
		task, err := client.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			return result
		}
		result.Status = "completed"
		result.Result = extractSendMessageResult(task)
	}

	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
	}
	return result
}

// sendStreamingMessage sends a streaming message and accumulates all text parts.
func (c *ClientCache) sendStreamingMessage(ctx context.Context, client *a2aclient.Client, msg *a2a.Message) (string, error) {
	var sb strings.Builder
	req := &a2a.SendMessageRequest{Message: msg}

	for event, err := range client.SendStreamingMessage(ctx, req) {
		if err != nil {
			return sb.String(), fmt.Errorf("streaming error: %w", err)
		}

		switch e := event.(type) {
		case *a2a.Message:
			// Terminal message with result
			for _, part := range e.Parts {
				if text := part.Text(); text != "" {
					sb.WriteString(text)
				}
			}
			return sb.String(), nil

		case *a2a.Task:
			// Terminal task (non-streaming completion)
			result := extractTaskResult(e)
			sb.WriteString(result)
			return sb.String(), nil

		case *a2a.TaskStatusUpdateEvent:
			// State change updates - check for terminal state
			if e.Status.State.Terminal() {
				return sb.String(), nil
			}

		case *a2a.TaskArtifactUpdateEvent:
			// Extract text from artifact parts
			for _, part := range e.Artifact.Parts {
				if text := part.Text(); text != "" {
					sb.WriteString(text)
				}
			}
		}
	}

	return sb.String(), nil
}

// extractSendMessageResult extracts text from a SendMessageResult (either *a2a.Task or *a2a.Message).
func extractSendMessageResult(result a2a.SendMessageResult) string {
	switch r := result.(type) {
	case *a2a.Message:
		return extractMessageText(r)
	case *a2a.Task:
		return extractTaskResult(r)
	default:
		return ""
	}
}

// extractMessageText extracts text from all parts of a message.
func extractMessageText(msg *a2a.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Parts {
		if text := part.Text(); text != "" {
			sb.WriteString(text)
		}
	}
	return sb.String()
}

// extractTaskResult extracts text content from a Task.
func extractTaskResult(task *a2a.Task) string {
	if task == nil {
		return ""
	}

	// Check History for messages
	for _, msg := range task.History {
		if text := extractMessageText(msg); text != "" {
			return text
		}
	}

	// Check Artifacts for content
	for _, artifact := range task.Artifacts {
		for _, part := range artifact.Parts {
			if text := part.Text(); text != "" {
				return text
			}
		}
	}

	return ""
}

// A2AToolset implements tool.Toolset for A2A tools.
type A2AToolset struct {
	cache *ClientCache
}

// NewA2AToolset creates a new A2A toolsets from configuration.
func NewA2AToolset(cfg *config.A2AConfig) *A2AToolset {
	return &A2AToolset{
		cache: NewClientCache(cfg),
	}
}

// Name returns the name of the toolset.
func (t *A2AToolset) Name() string {
	return "a2a"
}

// Tools returns the A2A tools available.
func (t *A2AToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return A2ATools(t.cache), nil
}

// A2ATools returns the A2A tools built from the client cache.
func A2ATools(cache *ClientCache) []tool.Tool {
	t, err := NewA2ATool(cache)
	if err != nil {
		return nil
	}
	return []tool.Tool{t}
}

// NewA2ATool creates the a2a ADK tool using the provided client cache.
func NewA2ATool(cache *ClientCache) (tool.Tool, error) {
	desc := buildA2ADescription(cache)

	return newTool("a2a", desc,
		func(ctx tool.Context, input A2AInput) (A2AOutput, error) {
			// Send the message
			result := cache.SendMessage(ctx, input.AgentName, input.Prompt, input.Stream)
			return result, nil
		},
		// Common LLM parameter name aliases
		map[string]string{
			"agent":   "agent_name",
			"message": "prompt",
			"input":   "prompt",
		},
	)
}

// buildA2ADescription generates a dynamic tool description listing available agents.
func buildA2ADescription(cache *ClientCache) string {
	if cache == nil || len(cache.agents) == 0 {
		return "Call a remote A2A-capable agent. Parameters: agent_name (name of configured agent), prompt (message to send), stream (optional, enable streaming). No A2A agents configured."
	}

	var sb strings.Builder
	sb.WriteString("Call a remote A2A-capable agent. Parameters: agent_name (name of configured agent), prompt (message to send), stream (optional, enable streaming). ")
	sb.WriteString("Available agents: ")
	names := make([]string, 0, len(cache.agents))
	for name := range cache.agents {
		names = append(names, name)
	}
	sb.WriteString(strings.Join(names, ", "))
	sb.WriteString(".")

	return sb.String()
}
