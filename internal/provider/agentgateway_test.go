package provider

import (
	"context"
	"testing"
)

func TestNewAgentGateway(t *testing.T) {
	llm, err := NewAgentGateway(context.Background(), "deepseek-v4-flash:0731-cloud", "", "", nil)
	if err != nil {
		t.Fatalf("NewAgentGateway error: %v", err)
	}
	if llm == nil {
		t.Fatal("NewAgentGateway returned nil")
	}
	if llm.Name() != "deepseek-v4-flash:0731-cloud" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "deepseek-v4-flash:0731-cloud")
	}
	// agentgateway delegates to the OpenAI-compatible client.
	if _, ok := llm.(*openaiModel); !ok {
		t.Errorf("expected *openaiModel, got %T", llm)
	}
}

func TestNewAgentGateway_CustomBaseURL(t *testing.T) {
	llm, err := NewAgentGateway(context.Background(), "deepseek-v4-flash:0731-cloud", "", "http://localhost:9999", nil)
	if err != nil {
		t.Fatalf("NewAgentGateway with custom baseURL: %v", err)
	}
	if llm.Name() != "deepseek-v4-flash:0731-cloud" {
		t.Errorf("Name() = %q, want %q", llm.Name(), "deepseek-v4-flash:0731-cloud")
	}
}

func TestResolveAgentGateway(t *testing.T) {
	info, err := Resolve("agentgateway/deepseek-v4-flash:0731-cloud")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "agentgateway" || info.Model != "deepseek-v4-flash:0731-cloud" {
		t.Fatalf("info = %+v, want agentgateway/deepseek-v4-flash:0731-cloud", info)
	}
	if info.Ollama {
		t.Fatalf("info = %+v, expected Ollama to be false (the -cloud tag must not route to Ollama)", info)
	}
}

func TestResolveAgentGatewayCaseInsensitive(t *testing.T) {
	info, err := Resolve("AGENTGATEWAY/deepseek-v4-flash:0731-cloud")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Provider != "agentgateway" || info.Model != "deepseek-v4-flash:0731-cloud" {
		t.Fatalf("info = %+v, want agentgateway/deepseek-v4-flash:0731-cloud", info)
	}
}

func TestContextWindowSizeForAgentGateway(t *testing.T) {
	if got := ContextWindowSizeFor("agentgateway", "deepseek-v4-flash:0731-cloud"); got != 1_000_000 {
		t.Errorf("ContextWindowSizeFor(agentgateway, deepseek-v4-flash:0731-cloud) = %d, want 1000000", got)
	}
}
