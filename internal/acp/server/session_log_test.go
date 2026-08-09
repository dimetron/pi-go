package server

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"google.golang.org/adk/v2/model"
	adksession "google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/logger"
)

// ACP wrote no session log at all until this was added, which left its runs
// invisible to every tool that reads ~/.pi-go/log. These tests pin the entry
// shape so ACP logs stay readable by the same analysis as every other mode.

func acpEvent(role string, parts ...*genai.Part) *adksession.Event {
	return &adksession.Event{
		Author:      "pi",
		LLMResponse: model.LLMResponse{Content: &genai.Content{Role: role, Parts: parts}},
	}
}

// readEntries runs fn against a logger rooted at a temp HOME and returns the
// decoded log lines.
func readEntries(t *testing.T, fn func(l *logger.Logger)) []map[string]any {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	l, err := logger.New()
	if err != nil {
		t.Fatalf("logger.New: %v", err)
	}
	fn(l)
	path := l.Path()
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func TestLogEventParts_WritesEveryPartType(t *testing.T) {
	entries := readEntries(t, func(l *logger.Logger) {
		logEventParts(l, acpEvent("thinking", &genai.Part{Text: "pondering"}))
		logEventParts(l, acpEvent("model", &genai.Part{Text: "the answer"}))
		logEventParts(l, acpEvent("model", &genai.Part{
			FunctionCall: &genai.FunctionCall{Name: "read", Args: map[string]any{"path": "a.go"}},
		}))
		logEventParts(l, acpEvent("user", &genai.Part{
			FunctionResponse: &genai.FunctionResponse{Name: "read", Response: map[string]any{"content": "pkg"}},
		}))
	})

	var types []string
	for _, e := range entries {
		types = append(types, e["type"].(string))
	}
	want := []string{"thinking", "llm_text", "tool_call", "tool_result"}
	if len(types) != len(want) {
		t.Fatalf("got entry types %v, want %v", types, want)
	}
	for i, w := range want {
		if types[i] != w {
			t.Errorf("entry %d = %q, want %q", i, types[i], w)
		}
	}
	// The tool result must be JSON, so log readers can parse it like the
	// other modes' output rather than a Go %v dump.
	if c, _ := entries[3]["content"].(string); !strings.Contains(c, `"content":"pkg"`) {
		t.Errorf("tool_result content should be JSON, got %q", c)
	}
}

func TestLogEventParts_NilLoggerAndEmptyEventAreNoops(t *testing.T) {
	// ACP must still serve when the log could not be created.
	logEventParts(nil, acpEvent("model", &genai.Part{Text: "x"}))

	entries := readEntries(t, func(l *logger.Logger) {
		logEventParts(l, nil)
		logEventParts(l, &adksession.Event{})
	})
	if len(entries) != 0 {
		t.Errorf("nil/empty events must write nothing, got %v", entries)
	}
}
