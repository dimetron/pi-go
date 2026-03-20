package memory

import (
	"testing"
	"time"
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
