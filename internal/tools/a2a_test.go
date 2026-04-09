package tools

import (
	"context"
	"iter"
	"net/http/httptest"
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
