package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/dimetron/pi-go/internal/subagent"
)

func TestBuildCompressionPrompt(t *testing.T) {
	raw := RawObservation{
		SessionID: "sess-1",
		Project:   "/proj",
		ToolName:  "Read",
		ToolInput: map[string]any{
			"file_path": "/proj/main.go",
		},
		ToolOutput: map[string]any{
			"content": "package main",
		},
		Timestamp: time.Now(),
	}

	prompt := buildCompressionPrompt(raw)
	if prompt == "" {
		t.Fatal("expected non-empty prompt")
	}
	if !contains(prompt, "Read") {
		t.Errorf("prompt should contain tool name, got: %s", prompt)
	}
	if !contains(prompt, "main.go") {
		t.Errorf("prompt should contain file path, got: %s", prompt)
	}
}

func TestBuildCompressionPrompt_TruncatesLargeOutput(t *testing.T) {
	// Create output larger than maxPromptOutput.
	largeOutput := make([]byte, maxPromptOutput+1000)
	for i := range largeOutput {
		largeOutput[i] = 'x'
	}

	raw := RawObservation{
		ToolName: "Read",
		ToolInput: map[string]any{
			"file_path": "/big.go",
		},
		ToolOutput: map[string]any{
			"content": string(largeOutput),
		},
		Timestamp: time.Now(),
	}

	prompt := buildCompressionPrompt(raw)
	if !contains(prompt, "truncated") {
		t.Error("expected truncation marker in prompt for large output")
	}
}

func TestParseCompressedResponse_Valid(t *testing.T) {
	raw := RawObservation{
		SessionID: "sess-1",
		Project:   "/proj",
		ToolName:  "Edit",
		Timestamp: time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC),
	}

	text := `{"title": "Updated main.go handler", "type": "change", "text": "Modified the HTTP handler to support POST requests.", "source_files": ["/proj/main.go"]}`

	obs, err := parseCompressedResponse(text, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.Title != "Updated main.go handler" {
		t.Errorf("title = %q, want %q", obs.Title, "Updated main.go handler")
	}
	if obs.Type != TypeChange {
		t.Errorf("type = %q, want %q", obs.Type, TypeChange)
	}
	if obs.SessionID != "sess-1" {
		t.Errorf("sessionID = %q, want %q", obs.SessionID, "sess-1")
	}
	if len(obs.SourceFiles) != 1 || obs.SourceFiles[0] != "/proj/main.go" {
		t.Errorf("source_files = %v, want [/proj/main.go]", obs.SourceFiles)
	}
}

func TestParseCompressedResponse_WithCodeFences(t *testing.T) {
	raw := RawObservation{
		SessionID: "sess-1",
		Project:   "/proj",
		ToolName:  "Read",
		Timestamp: time.Now(),
	}

	text := "```json\n{\"title\": \"Read config file\", \"type\": \"discovery\", \"text\": \"Explored config.\", \"source_files\": []}\n```"

	obs, err := parseCompressedResponse(text, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.Title != "Read config file" {
		t.Errorf("title = %q, want %q", obs.Title, "Read config file")
	}
	if obs.Type != TypeDiscovery {
		t.Errorf("type = %q, want %q", obs.Type, TypeDiscovery)
	}
}

func TestParseCompressedResponse_InvalidType(t *testing.T) {
	raw := RawObservation{
		SessionID: "sess-1",
		Project:   "/proj",
		ToolName:  "Bash",
		Timestamp: time.Now(),
	}

	text := `{"title": "Ran tests", "type": "unknown_type", "text": "Ran unit tests.", "source_files": []}`

	obs, err := parseCompressedResponse(text, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid type should default to "change".
	if obs.Type != TypeChange {
		t.Errorf("type = %q, want %q (default)", obs.Type, TypeChange)
	}
}

func TestParseCompressedResponse_EmptyTitle(t *testing.T) {
	raw := RawObservation{ToolName: "Read", Timestamp: time.Now()}
	text := `{"title": "", "type": "change", "text": "some text", "source_files": []}`

	_, err := parseCompressedResponse(text, raw)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestParseCompressedResponse_MalformedJSON(t *testing.T) {
	raw := RawObservation{ToolName: "Read", Timestamp: time.Now()}
	text := `not valid json at all`

	_, err := parseCompressedResponse(text, raw)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !contains(err.Error(), "invalid JSON") {
		t.Errorf("error should mention invalid JSON: %v", err)
	}
}

func TestParseCompressedResponse_EmptyResponse(t *testing.T) {
	raw := RawObservation{ToolName: "Read", Timestamp: time.Now()}

	_, err := parseCompressedResponse("", raw)
	if err == nil {
		t.Fatal("expected error for empty response")
	}
	if !contains(err.Error(), "empty response") {
		t.Errorf("error should mention empty response: %v", err)
	}
}

func TestParseCompressedResponse_NilSourceFiles(t *testing.T) {
	raw := RawObservation{
		SessionID: "sess-1",
		Project:   "/proj",
		ToolName:  "Bash",
		Timestamp: time.Now(),
	}

	text := `{"title": "Ran command", "type": "change", "text": "Executed build."}`

	obs, err := parseCompressedResponse(text, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.SourceFiles == nil {
		t.Error("source_files should be empty slice, not nil")
	}
	if len(obs.SourceFiles) != 0 {
		t.Errorf("source_files len = %d, want 0", len(obs.SourceFiles))
	}
}

func TestStripCodeFences(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no fences", `{"title": "x"}`, `{"title": "x"}`},
		{"json fences", "```json\n{\"title\": \"x\"}\n```", `{"title": "x"}`},
		{"plain fences", "```\n{\"title\": \"x\"}\n```", `{"title": "x"}`},
		{"with whitespace", "  ```json\n{\"title\": \"x\"}\n```  ", `{"title": "x"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripCodeFences(tt.in)
			if got != tt.want {
				t.Errorf("stripCodeFences(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTruncateForError(t *testing.T) {
	short := "short string"
	if truncateForError(short) != short {
		t.Errorf("short string should not be truncated")
	}

	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	result := truncateForError(string(long))
	if len(result) > 210 { // 200 + "..."
		t.Errorf("long string should be truncated, got len=%d", len(result))
	}
	if !contains(result, "...") {
		t.Error("truncated string should end with ...")
	}
}

func TestBuildSummaryPrompt(t *testing.T) {
	observations := []*Observation{
		{
			Title:       "Read main.go",
			Type:        TypeDiscovery,
			Text:        "Explored the main entry point.",
			SourceFiles: []string{"/proj/main.go"},
		},
		{
			Title:       "Fixed handler bug",
			Type:        TypeBugfix,
			Text:        "Corrected nil pointer in handler.",
			SourceFiles: []string{"/proj/handler.go"},
		},
	}

	prompt := buildSummaryPrompt(observations)
	if !contains(prompt, "Read main.go") {
		t.Error("summary prompt should contain observation titles")
	}
	if !contains(prompt, "handler.go") {
		t.Error("summary prompt should contain source files")
	}
	if !contains(prompt, "request") {
		t.Error("summary prompt should contain response format instructions")
	}
}

func TestParseSummaryResponse_Valid(t *testing.T) {
	text := `{"request": "Fix the handler", "investigated": "Read handler.go", "learned": "nil check was missing", "completed": "Added nil guard", "next_steps": "Add tests"}`

	summary, err := parseSummaryResponse(text, "sess-1", "/proj")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Request != "Fix the handler" {
		t.Errorf("request = %q, want %q", summary.Request, "Fix the handler")
	}
	if summary.SessionID != "sess-1" {
		t.Errorf("sessionID = %q, want %q", summary.SessionID, "sess-1")
	}
	if summary.Project != "/proj" {
		t.Errorf("project = %q, want %q", summary.Project, "/proj")
	}
	if summary.NextSteps != "Add tests" {
		t.Errorf("next_steps = %q, want %q", summary.NextSteps, "Add tests")
	}
}

func TestParseSummaryResponse_MalformedJSON(t *testing.T) {
	_, err := parseSummaryResponse("not json", "sess-1", "/proj")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestParseSummaryResponse_Empty(t *testing.T) {
	_, err := parseSummaryResponse("", "sess-1", "/proj")
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

// mockOrchestrator implements the minimal subagentOrchestrator interface for testing.
type mockOrchestrator struct {
	mu      sync.Mutex
	lookups []string
	spawns  []subagent.SpawnInput
	events  []subagent.Event
	failAt  string // "LookupAgent" or "Spawn"
	failErr error
}

func newMockOrchestrator(events []subagent.Event) *mockOrchestrator {
	return &mockOrchestrator{events: events}
}

func (m *mockOrchestrator) LookupAgent(name string) (subagent.AgentConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lookups = append(m.lookups, name)
	if m.failAt == "LookupAgent" {
		return subagent.AgentConfig{}, m.failErr
	}
	return subagent.AgentConfig{Name: name, Role: "smol"}, nil
}

func (m *mockOrchestrator) Spawn(_ context.Context, input subagent.SpawnInput) (
	<-chan subagent.Event, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spawns = append(m.spawns, input)
	if m.failAt == "Spawn" {
		return nil, "", m.failErr
	}

	ch := make(chan subagent.Event, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, "agent-123", nil
}

func (m *mockOrchestrator) getLookups() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.lookups))
	copy(out, m.lookups)
	return out
}

func (m *mockOrchestrator) getSpawns() []subagent.SpawnInput {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]subagent.SpawnInput, len(m.spawns))
	copy(out, m.spawns)
	return out
}

func TestNewSubagentCompressor(t *testing.T) {
	orch := newMockOrchestrator(nil)
	c := NewSubagentCompressor(orch)
	if c == nil {
		t.Fatal("expected non-nil compressor")
	}
}

func TestSubagentCompressor_CompressObservation_Success(t *testing.T) {
	events := []subagent.Event{
		{Type: "text_delta", Content: `{"title": "Read main.go", "type": "discovery", "text": "Explored main entry.", "source_files": ["/proj/main.go"]}`},
	}
	orch := newMockOrchestrator(events)
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	raw := RawObservation{
		SessionID: "sess-1",
		Project:   "/proj",
		ToolName:  "Read",
		ToolInput: map[string]any{"file_path": "/proj/main.go"},
		Timestamp: time.Now(),
	}

	obs, err := c.CompressObservation(ctx, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs.Title != "Read main.go" {
		t.Errorf("title = %q, want %q", obs.Title, "Read main.go")
	}
	if obs.Type != TypeDiscovery {
		t.Errorf("type = %q, want %q", obs.Type, TypeDiscovery)
	}

	lookups := orch.getLookups()
	if len(lookups) != 1 || lookups[0] != "memory-compressor" {
		t.Errorf("lookups = %v, want [memory-compressor]", lookups)
	}

	spawns := orch.getSpawns()
	if len(spawns) != 1 {
		t.Fatalf("spawns = %d, want 1", len(spawns))
	}
	if spawns[0].Agent.Name != "memory-compressor" {
		t.Errorf("spawn agent name = %q, want %q", spawns[0].Agent.Name, "memory-compressor")
	}
}

func TestSubagentCompressor_CompressObservation_LookupAgentError(t *testing.T) {
	orch := newMockOrchestrator(nil)
	orch.failAt = "LookupAgent"
	orch.failErr = fmt.Errorf("agent not found")
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	raw := RawObservation{ToolName: "Read", Timestamp: time.Now()}

	_, err := c.CompressObservation(ctx, raw)
	if err == nil {
		t.Fatal("expected error when LookupAgent fails")
	}
	if !contains(err.Error(), "finding memory-compressor agent") {
		t.Errorf("error = %v, want finding memory-compressor agent", err)
	}
}

func TestSubagentCompressor_CompressObservation_SpawnError(t *testing.T) {
	orch := newMockOrchestrator(nil)
	orch.failAt = "Spawn"
	orch.failErr = fmt.Errorf("spawn refused")
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	raw := RawObservation{ToolName: "Read", Timestamp: time.Now()}

	_, err := c.CompressObservation(ctx, raw)
	if err == nil {
		t.Fatal("expected error when Spawn fails")
	}
	if !contains(err.Error(), "spawning memory-compressor") {
		t.Errorf("error = %v, want spawning memory-compressor", err)
	}
}

func TestSubagentCompressor_CompressObservation_ErrorEvent(t *testing.T) {
	events := []subagent.Event{
		{Type: "text_delta", Content: `some partial`},
		{Type: "error", Error: "model overloaded"},
	}
	orch := newMockOrchestrator(events)
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	raw := RawObservation{ToolName: "Read", Timestamp: time.Now()}

	_, err := c.CompressObservation(ctx, raw)
	if err == nil {
		t.Fatal("expected error when error event received")
	}
	if !contains(err.Error(), "memory-compressor error") {
		t.Errorf("error = %v, want memory-compressor error", err)
	}
}

func TestSubagentCompressor_SummarizeSession_Success(t *testing.T) {
	events := []subagent.Event{
		{Type: "text_delta", Content: `{"request": "Fix handler", "investigated": "handler.go", "learned": "nil missing", "completed": "Added guard", "next_steps": "Add tests"}`},
	}
	orch := newMockOrchestrator(events)
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	observations := []*Observation{
		{Title: "Read handler", Type: TypeDiscovery, Text: "Found bug", SourceFiles: []string{"/proj/handler.go"}},
	}

	summary, err := c.SummarizeSession(ctx, "sess-1", "/proj", observations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.SessionID != "sess-1" {
		t.Errorf("sessionID = %q, want %q", summary.SessionID, "sess-1")
	}
	if summary.Request != "Fix handler" {
		t.Errorf("request = %q, want %q", summary.Request, "Fix handler")
	}
	if summary.NextSteps != "Add tests" {
		t.Errorf("next_steps = %q, want %q", summary.NextSteps, "Add tests")
	}

	lookups := orch.getLookups()
	if len(lookups) != 1 || lookups[0] != "memory-compressor" {
		t.Errorf("lookups = %v, want [memory-compressor]", lookups)
	}
}

func TestSubagentCompressor_SummarizeSession_LookupAgentError(t *testing.T) {
	orch := newMockOrchestrator(nil)
	orch.failAt = "LookupAgent"
	orch.failErr = fmt.Errorf("agent missing")
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	_, err := c.SummarizeSession(ctx, "sess-1", "/proj", nil)
	if err == nil {
		t.Fatal("expected error when LookupAgent fails")
	}
	if !contains(err.Error(), "finding memory-compressor agent") {
		t.Errorf("error = %v, want finding memory-compressor agent", err)
	}
}

func TestSubagentCompressor_SummarizeSession_SpawnError(t *testing.T) {
	orch := newMockOrchestrator(nil)
	orch.failAt = "Spawn"
	orch.failErr = fmt.Errorf("spawn failed")
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	_, err := c.SummarizeSession(ctx, "sess-1", "/proj", nil)
	if err == nil {
		t.Fatal("expected error when Spawn fails")
	}
	if !contains(err.Error(), "spawning memory-compressor for summary") {
		t.Errorf("error = %v, want spawning memory-compressor for summary", err)
	}
}

func TestSubagentCompressor_SummarizeSession_ErrorEvent(t *testing.T) {
	events := []subagent.Event{
		{Type: "error", Error: "model crashed"},
	}
	orch := newMockOrchestrator(events)
	c := NewSubagentCompressor(orch)

	ctx := context.Background()
	_, err := c.SummarizeSession(ctx, "sess-1", "/proj", nil)
	if err == nil {
		t.Fatal("expected error when error event received")
	}
	if !contains(err.Error(), "memory-compressor summary error") {
		t.Errorf("error = %v, want memory-compressor summary error", err)
	}
}

// contains is a test helper.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
