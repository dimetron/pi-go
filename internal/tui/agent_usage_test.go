package tui

import (
	"reflect"
	"strings"
	"testing"
	"time"

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
		name    string
		u       *genai.GenerateContentResponseUsageMetadata
		elapsed time.Duration
		want    string
	}{
		{"nil", nil, 0, ""},
		{"all zero", &genai.GenerateContentResponseUsageMetadata{}, 0, ""},
		{"elapsed without usage", nil, 3 * time.Second, ""},
		{
			"in and out only",
			&genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 50},
			0,
			"100 in · 50 out · 150 total",
		},
		{
			"with cache read",
			&genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 50, CachedContentTokenCount: 30},
			0,
			"100 in (30 cached) · 50 out · 150 total",
		},
		{
			"with reasoning and explicit total",
			&genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount: 2000, CandidatesTokenCount: 500, CachedContentTokenCount: 1000,
				ThoughtsTokenCount: 75, TotalTokenCount: 2500,
			},
			0,
			"2.0k in (1.0k cached) · 500 out · 75 reasoning · 2.5k total",
		},
		{
			"reasoning only, no total",
			&genai.GenerateContentResponseUsageMetadata{ThoughtsTokenCount: 75},
			0,
			"0 in · 0 out · 75 reasoning · 75 total",
		},
		{
			"with elapsed time",
			&genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 100, CandidatesTokenCount: 50},
			12345 * time.Millisecond,
			"100 in · 50 out · 150 total · took 12.3s",
		},
		{
			"with elapsed time over a minute",
			&genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 323600, CandidatesTokenCount: 2900, TotalTokenCount: 326500},
			92 * time.Second,
			"323.6k in · 2.9k out · 326.5k total · took 1m 32s",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTurnUsage(tt.u, tt.elapsed); got != tt.want {
				t.Fatalf("formatTurnUsage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatTurnDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{250 * time.Millisecond, "250ms"},
		{999 * time.Millisecond, "999ms"},
		{time.Second, "1.0s"},
		{12345 * time.Millisecond, "12.3s"},
		{59500 * time.Millisecond, "59.5s"},
		{time.Minute, "1m 00s"},
		{92 * time.Second, "1m 32s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{time.Hour, "1h 00m"},
		{2*time.Hour + 5*time.Minute, "2h 05m"},
	}
	for _, tt := range tests {
		if got := formatTurnDuration(tt.d); got != tt.want {
			t.Errorf("formatTurnDuration(%s) = %q, want %q", tt.d, got, tt.want)
		}
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
	m.handleAgentUsage(agentUsageMsg{
		usage: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount: 100, CandidatesTokenCount: 50,
		},
		elapsed: 2500 * time.Millisecond,
	})
	if len(m.chatModel.Messages) != 1 {
		t.Fatalf("expected 1 meta message, got %d", len(m.chatModel.Messages))
	}
	msg := m.chatModel.Messages[0]
	if !msg.isMeta || msg.role != "assistant" {
		t.Fatalf("expected a meta assistant message, got %+v", msg)
	}
	if msg.content != "100 in · 50 out · 150 total · took 2.5s" {
		t.Fatalf("unexpected summary content: %q", msg.content)
	}

	// A nil usage block appends nothing.
	m2 := &model{chatModel: NewChatModel(nil)}
	m2.handleAgentUsage(agentUsageMsg{})
	if len(m2.chatModel.Messages) != 0 {
		t.Fatalf("nil usage should not append a message, got %d", len(m2.chatModel.Messages))
	}
}
