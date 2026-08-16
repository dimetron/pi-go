package tui

import (
	"reflect"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestAddUsage(t *testing.T) {
	if got := addUsage(nil, nil); got != nil {
		t.Fatalf("addUsage(nil, nil) = %v, want nil", got)
	}

	src := &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        10,
		CandidatesTokenCount:    5,
		CachedContentTokenCount: 4,
		ThoughtsTokenCount:      2,
		TotalTokenCount:         15,
	}
	got := addUsage(nil, src)
	if got == src {
		t.Fatal("addUsage(nil, src) returned src itself; want a copy")
	}
	if !reflect.DeepEqual(got, src) {
		t.Fatalf("addUsage(nil, src) = %+v, want %+v", got, src)
	}
	// Mutating the result must not touch src.
	got.PromptTokenCount = 999
	if src.PromptTokenCount != 10 {
		t.Fatalf("mutating the copy changed src: PromptTokenCount = %d, want 10", src.PromptTokenCount)
	}

	// A nil src leaves dst untouched.
	dst := &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 7}
	if out := addUsage(dst, nil); out != dst || dst.PromptTokenCount != 7 {
		t.Fatalf("addUsage(dst, nil) = %+v (dst %+v), want dst unchanged", out, dst)
	}

	// Summation across responses (tool loops / stuck recoveries).
	a := &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 100, CandidatesTokenCount: 50, CachedContentTokenCount: 30, TotalTokenCount: 150,
	}
	b := &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 20, CandidatesTokenCount: 5, CachedContentTokenCount: 20, TotalTokenCount: 25,
	}
	sum := addUsage(a, b)
	want := genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 120, CandidatesTokenCount: 55, CachedContentTokenCount: 50, TotalTokenCount: 175,
	}
	if !reflect.DeepEqual(sum, &want) {
		t.Fatalf("addUsage(a, b) = %+v, want %+v", sum, want)
	}
}

func TestFormatTurnUsage(t *testing.T) {
	tests := []struct {
		name string
		u    *genai.GenerateContentResponseUsageMetadata
		want string
	}{
		{"nil", nil, ""},
		{"all zero", &genai.GenerateContentResponseUsageMetadata{}, ""},
		{
			"in and out only",
			&genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 50},
			"100 in · 50 out · 150 total",
		},
		{
			"with cache read",
			&genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 50, CachedContentTokenCount: 30},
			"100 in (30 cached) · 50 out · 150 total",
		},
		{
			"with reasoning and explicit total",
			&genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount: 2000, CandidatesTokenCount: 500, CachedContentTokenCount: 1000,
				ThoughtsTokenCount: 75, TotalTokenCount: 2500,
			},
			"2.0k in (1.0k cached) · 500 out · 75 reasoning · 2.5k total",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTurnUsage(tt.u); got != tt.want {
				t.Fatalf("formatTurnUsage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestChatModel_RenderMessages_MetaMessage(t *testing.T) {
	cm := NewChatModel(nil)
	cm.Messages = append(cm.Messages, message{
		role:    "assistant",
		content: "100 in · 50 out · 150 total",
		isMeta:  true,
	})

	result := cm.RenderMessages(false)
	if !strings.Contains(result, "100 in · 50 out · 150 total") {
		t.Error("expected meta content in output")
	}
	if !strings.Contains(result, "Σ") {
		t.Error("expected a Σ prefix to distinguish the meta line from the reply")
	}
	if strings.Contains(result, "◉") {
		t.Error("did not expect a bullet prefix on a meta line")
	}
}

func TestHandleAgentUsage_AppendsSummary(t *testing.T) {
	m := &model{chatModel: NewChatModel(nil)}
	m.handleAgentUsage(agentUsageMsg{usage: &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 100, CandidatesTokenCount: 50,
	}})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 meta message, got %d", len(m.chatModel.Messages))
	}
	msg := m.chatModel.Messages[0]
	if !msg.isMeta || msg.role != "assistant" {
		t.Fatalf("expected a meta assistant message, got %+v", msg)
	}
	if msg.content != "100 in · 50 out · 150 total" {
		t.Fatalf("unexpected summary content: %q", msg.content)
	}

	// A nil usage block appends nothing.
	m2 := &model{chatModel: NewChatModel(nil)}
	m2.handleAgentUsage(agentUsageMsg{})
	if len(m2.chatModel.Messages) != 0 {
		t.Fatalf("nil usage should not append a message, got %d", len(m2.chatModel.Messages))
	}
}
