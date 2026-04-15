package tools

import (
	"context"
	"iter"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	a2aclient "github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/adk/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/config"
)

type mockExecutor struct {
	responseText string
}

func (e *mockExecutor) Execute(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(e.responseText)), nil)
	}
}

func (e *mockExecutor) Cancel(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(nil, a2a.TaskStateCanceled, nil), nil)
	}
}

func createTestServer(responseText string) *httptest.Server {
	executor := &mockExecutor{responseText: responseText}
	handler := a2asrv.NewHandler(executor)
	jsonrpcHandler := a2asrv.NewJSONRPCHandler(handler)
	return httptest.NewServer(jsonrpcHandler)
}

func TestClientCacheGetClient(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: "http://localhost:8080"},
		},
	}

	cache := NewClientCache(cfg)

	client, err := cache.GetClient(context.Background(), "helper")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if client == nil {
		t.Fatal("GetClient() returned nil client")
	}

	client2, err := cache.GetClient(context.Background(), "helper")
	if err != nil {
		t.Fatalf("GetClient() second call error = %v", err)
	}
	if client != client2 {
		t.Error("GetClient() should return cached client")
	}
}

func TestClientCacheUpdateAgents(t *testing.T) {
	// Create cache with one agent
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "agent1", URL: "http://localhost:8080"},
			{Name: "agent2", URL: "http://localhost:8081"},
		},
	}
	cache := NewClientCache(cfg)

	// Create clients for both agents
	_, err := cache.GetClient(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("GetClient(agent1) error = %v", err)
	}
	_, err = cache.GetClient(context.Background(), "agent2")
	if err != nil {
		t.Fatalf("GetClient(agent2) error = %v", err)
	}

	// Update agents: remove agent1, keep agent2, add agent3
	newCfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "agent2", URL: "http://localhost:8081"}, // kept, same URL
			{Name: "agent3", URL: "http://localhost:8082"}, // new
		},
	}
	cache.UpdateAgents(newCfg)

	// agent1 should be gone (client evicted)
	_, err = cache.GetClient(context.Background(), "agent1")
	if err == nil {
		t.Error("GetClient(agent1) should fail after UpdateAgents removed it")
	}

	// agent2 should still work
	_, err = cache.GetClient(context.Background(), "agent2")
	if err != nil {
		t.Errorf("GetClient(agent2) error = %v", err)
	}

	// agent3 should work
	_, err = cache.GetClient(context.Background(), "agent3")
	if err != nil {
		t.Errorf("GetClient(agent3) error = %v", err)
	}
}

func TestClientCacheUpdateAgents_URLChanged(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "agent1", URL: "http://localhost:8080"},
		},
	}
	cache := NewClientCache(cfg)

	// Create client
	client1, err := cache.GetClient(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}

	// Update with changed URL - client should be evicted
	newCfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "agent1", URL: "http://localhost:9090"}, // changed URL
		},
	}
	cache.UpdateAgents(newCfg)

	// New client should be created (not cached)
	client2, err := cache.GetClient(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if client1 == client2 {
		t.Error("GetClient() should return new client after URL change")
	}
}

func TestClientCacheClose(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "agent1", URL: "http://localhost:8080"},
			{Name: "agent2", URL: "http://localhost:8081"},
		},
	}
	cache := NewClientCache(cfg)

	// Create clients
	_, err := cache.GetClient(context.Background(), "agent1")
	if err != nil {
		t.Fatalf("GetClient(agent1) error = %v", err)
	}
	_, err = cache.GetClient(context.Background(), "agent2")
	if err != nil {
		t.Fatalf("GetClient(agent2) error = %v", err)
	}

	// Close the cache
	cache.Close()

	// Clients should be evicted (but GetClient should still work for new clients)
	_, err = cache.GetClient(context.Background(), "agent1")
	if err != nil {
		t.Errorf("GetClient(agent1) error after Close = %v", err)
	}
}

func TestClientCacheGetClient_UnknownAgent(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: "http://localhost:8080"},
		},
	}

	cache := NewClientCache(cfg)

	_, err := cache.GetClient(context.Background(), "unknown")
	if err == nil {
		t.Fatal("GetClient() expected error for unknown agent")
	}
}

func TestClientCacheGetClient_NilConfig(t *testing.T) {
	cache := NewClientCache(nil)

	_, err := cache.GetClient(context.Background(), "helper")
	if err == nil {
		t.Fatal("GetClient() expected error with nil config")
	}
}

func TestSendMessageNonStreaming(t *testing.T) {
	server := createTestServer("hello from agent")
	defer server.Close()

	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: server.URL},
		},
	}

	cache := NewClientCache(cfg)
	result := cache.SendMessage(context.Background(), "helper", "hello", false)

	if result.Agent != "helper" {
		t.Errorf("Agent = %q, want 'helper'", result.Agent)
	}
	if result.Status != "completed" {
		t.Errorf("Status = %q, want 'completed'", result.Status)
	}
	if result.Result != "hello from agent" {
		t.Errorf("Result = %q, want 'hello from agent'", result.Result)
	}
	if result.Error != "" {
		t.Errorf("Error = %q, want empty", result.Error)
	}
}

func TestSendMessageUnknownAgent(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: "http://localhost:8080"},
		},
	}

	cache := NewClientCache(cfg)
	result := cache.SendMessage(context.Background(), "unknown", "hello", false)

	if result.Agent != "unknown" {
		t.Errorf("Agent = %q, want 'unknown'", result.Agent)
	}
	if result.Status != "failed" {
		t.Errorf("Status = %q, want 'failed'", result.Status)
	}
	if result.Error == "" {
		t.Error("Error should not be empty for unknown agent")
	}
}

func TestSendMessageUnreachable(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: "http://localhost:19999"},
		},
	}

	cache := NewClientCache(cfg)
	result := cache.SendMessage(context.Background(), "helper", "hello", false)

	if result.Status != "failed" {
		t.Errorf("Status = %q, want 'failed' for unreachable agent", result.Status)
	}
	if result.Error == "" {
		t.Error("Error should not be empty for unreachable agent")
	}
}

func TestSendMessageStreaming(t *testing.T) {
	server := createTestServer("streaming response")
	defer server.Close()

	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: server.URL},
		},
	}

	cache := NewClientCache(cfg)
	result := cache.SendMessage(context.Background(), "helper", "hello", true)

	if result.Status != "streaming" {
		t.Errorf("Status = %q, want 'streaming'", result.Status)
	}
	if result.Result != "streaming response" {
		t.Errorf("Result = %q, want 'streaming response'", result.Result)
	}
}

func TestNewA2ATool(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: "http://localhost:8080"},
			{Name: "assistant", URL: "http://localhost:8081"},
		},
	}

	cache := NewClientCache(cfg)
	tool, err := NewA2ATool(cache)
	if err != nil {
		t.Fatalf("NewA2ATool() error = %v", err)
	}
	if tool == nil {
		t.Fatal("NewA2ATool() returned nil tool")
	}

	if tool.Name() != "a2a" {
		t.Errorf("Name() = %q, want 'a2a'", tool.Name())
	}
}

func TestNewA2ATool_EmptyConfig(t *testing.T) {
	cache := NewClientCache(nil)
	tool, err := NewA2ATool(cache)
	if err != nil {
		t.Fatalf("NewA2ATool() error = %v", err)
	}
	if tool == nil {
		t.Fatal("NewA2ATool() returned nil tool")
	}
}

func TestA2ATools(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: "http://localhost:8080"},
		},
	}

	tools := A2ATools(NewClientCache(cfg))
	if len(tools) != 1 {
		t.Fatalf("A2ATools() returned %d tools, want 1", len(tools))
	}
	if tools[0].Name() != "a2a" {
		t.Errorf("tools[0].Name() = %q, want 'a2a'", tools[0].Name())
	}
}

type mockReadonlyContext struct {
	context.Context
}

func (m *mockReadonlyContext) UserContent() *genai.Content          { return nil }
func (m *mockReadonlyContext) InvocationID() string                 { return "" }
func (m *mockReadonlyContext) AgentName() string                    { return "test" }
func (m *mockReadonlyContext) ReadonlyState() session.ReadonlyState { return nil }
func (m *mockReadonlyContext) UserID() string                       { return "" }
func (m *mockReadonlyContext) AppName() string                      { return "" }
func (m *mockReadonlyContext) SessionID() string                    { return "" }
func (m *mockReadonlyContext) Branch() string                       { return "" }

func TestA2AToolset(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "helper", URL: "http://localhost:8080"},
		},
	}

	ts := NewA2AToolset(cfg)
	if ts.Name() != "a2a" {
		t.Errorf("Name() = %q, want 'a2a'", ts.Name())
	}

	ctx := &mockReadonlyContext{Context: context.Background()}
	tools, err := ts.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools() error = %v", err)
	}
	if len(tools) != 1 {
		t.Errorf("Tools() returned %d tools, want 1", len(tools))
	}
}

func TestA2AInputAliases(t *testing.T) {
	input := A2AInput{
		AgentName: "helper",
		Prompt:    "hello",
		Stream:    false,
	}

	if input.AgentName != "helper" {
		t.Errorf("AgentName = %q, want 'helper'", input.AgentName)
	}
	if input.Prompt != "hello" {
		t.Errorf("Prompt = %q, want 'hello'", input.Prompt)
	}
}

func TestA2AOutput(t *testing.T) {
	output := A2AOutput{
		Agent:  "helper",
		Status: "completed",
		Result: "response text",
		Error:  "",
	}

	if output.Agent != "helper" {
		t.Errorf("Agent = %q, want 'helper'", output.Agent)
	}
	if output.Status != "completed" {
		t.Errorf("Status = %q, want 'completed'", output.Status)
	}
	if output.Result != "response text" {
		t.Errorf("Result = %q, want 'response text'", output.Result)
	}
}

func TestA2AOutputError(t *testing.T) {
	output := A2AOutput{
		Agent:  "helper",
		Status: "failed",
		Result: "",
		Error:  "connection refused",
	}

	if output.Status != "failed" {
		t.Errorf("Status = %q, want 'failed'", output.Status)
	}
	if output.Error != "connection refused" {
		t.Errorf("Error = %q, want 'connection refused'", output.Error)
	}
}

var _ *a2aclient.Client = (*a2aclient.Client)(nil)

// --- extractSendMessageResult tests ---

func TestExtractSendMessageResult_Message(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("hello from message"))
	result := extractSendMessageResult(msg)
	if result != "hello from message" {
		t.Errorf("got %q, want 'hello from message'", result)
	}
}

func TestExtractSendMessageResult_Task(t *testing.T) {
	task := &a2a.Task{
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("task result")),
		},
	}
	result := extractSendMessageResult(task)
	if result != "task result" {
		t.Errorf("got %q, want 'task result'", result)
	}
}

// --- extractMessageText tests ---

func TestExtractMessageText_Nil(t *testing.T) {
	result := extractMessageText(nil)
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}
}

func TestExtractMessageText_MultipleParts(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleAgent,
		a2a.NewTextPart("part1 "),
		a2a.NewTextPart("part2"),
	)
	result := extractMessageText(msg)
	if result != "part1 part2" {
		t.Errorf("got %q, want 'part1 part2'", result)
	}
}

func TestExtractMessageText_EmptyParts(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart(""))
	result := extractMessageText(msg)
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}
}

// --- extractTaskResult tests ---

func TestExtractTaskResult_Nil(t *testing.T) {
	result := extractTaskResult(nil)
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}
}

func TestExtractTaskResult_Empty(t *testing.T) {
	task := &a2a.Task{}
	result := extractTaskResult(task)
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}
}

func TestExtractTaskResult_FromHistory(t *testing.T) {
	task := &a2a.Task{
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("from history")),
		},
	}
	result := extractTaskResult(task)
	if result != "from history" {
		t.Errorf("got %q, want 'from history'", result)
	}
}

func TestExtractTaskResult_EmptyMessage(t *testing.T) {
	task := &a2a.Task{
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("")),
		},
	}
	result := extractTaskResult(task)
	if result != "" {
		t.Errorf("got %q, want empty", result)
	}
}

func TestBuildA2ADescription_NilCache(t *testing.T) {
	result := buildA2ADescription(nil)
	if !strings.Contains(result, "No A2A agents configured") {
		t.Errorf("should mention no agents configured, got %q", result)
	}
}

func TestBuildA2ADescription_EmptyAgents(t *testing.T) {
	cache := NewClientCache(nil)
	result := buildA2ADescription(cache)
	if !strings.Contains(result, "No A2A agents configured") {
		t.Errorf("should mention no agents configured, got %q", result)
	}
}

func TestBuildA2ADescription_WithAgents(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "agent1", URL: "http://localhost:8080"},
			{Name: "agent2", URL: "http://localhost:8081"},
		},
	}
	cache := NewClientCache(cfg)
	result := buildA2ADescription(cache)

	if !strings.Contains(result, "agent1") {
		t.Errorf("should list agent1, got %q", result)
	}
	if !strings.Contains(result, "agent2") {
		t.Errorf("should list agent2, got %q", result)
	}
}

// --- ClientCache availableAgents tests ---

func TestClientCache_AvailableAgents(t *testing.T) {
	cfg := &config.A2AConfig{
		Agents: []config.A2AAgentConfig{
			{Name: "alpha", URL: "http://localhost:8080"},
			{Name: "beta", URL: "http://localhost:8081"},
			{Name: "gamma", URL: "http://localhost:8082"},
		},
	}
	cache := NewClientCache(cfg)

	// availableAgents is private, but we can test via error message
	_, err := cache.GetClient(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent agent")
	}
	// Error should contain available agents
	if !strings.Contains(err.Error(), "alpha") && !strings.Contains(err.Error(), "beta") && !strings.Contains(err.Error(), "gamma") {
		t.Errorf("error should list available agents, got %q", err.Error())
	}
}
