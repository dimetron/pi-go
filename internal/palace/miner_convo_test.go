package palace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseJSONL_PiGoFormat(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "session.jsonl")

	content := `{"type":"user","content":"How do I configure JWT?"}
{"type":"assistant","content":"You can configure JWT by setting the secret key in your config file."}
{"type":"user","content":"What about refresh tokens?"}
{"type":"assistant","content":"Refresh tokens should be stored securely in the database."}
`
	writeTestFile(t, path, content)

	exchanges, err := parseJSONL(path)
	if err != nil {
		t.Fatalf("parseJSONL: %v", err)
	}

	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges, got %d", len(exchanges))
	}

	if !containsStr(exchanges[0].content, "JWT") {
		t.Error("first exchange should mention JWT")
	}
	if !containsStr(exchanges[1].content, "refresh tokens") {
		t.Error("second exchange should mention refresh tokens")
	}
}

func TestParseJSONL_ClaudeCodeFormat(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "convo.jsonl")

	content := `{"role":"user","content":"Fix the authentication bug"}
{"role":"assistant","content":"I found the issue in the token validation logic."}
`
	writeTestFile(t, path, content)

	exchanges, err := parseJSONL(path)
	if err != nil {
		t.Fatalf("parseJSONL: %v", err)
	}

	if len(exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(exchanges))
	}
}

func TestParseJSONL_MalformedLines(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "messy.jsonl")

	content := `not json at all
{"role":"user","content":"valid question"}
{bad json
{"role":"assistant","content":"valid answer"}
`
	writeTestFile(t, path, content)

	exchanges, err := parseJSONL(path)
	if err != nil {
		t.Fatalf("parseJSONL: %v", err)
	}

	if len(exchanges) != 1 {
		t.Fatalf("expected 1 exchange (skipping malformed), got %d", len(exchanges))
	}
}

func TestParsePlainText(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "chat.txt")

	content := `> How do I set up the database?
Assistant: First, run the migration script.
Then configure the connection string.

> What about indexes?
Assistant: Add indexes for frequently queried columns.
`
	writeTestFile(t, path, content)

	exchanges, err := parsePlainText(path)
	if err != nil {
		t.Fatalf("parsePlainText: %v", err)
	}

	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges, got %d", len(exchanges))
	}

	if !containsStr(exchanges[0].content, "database") {
		t.Error("first exchange should mention database")
	}
}

func TestParsePlainText_UserPrefix(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "chat.txt")

	content := `User: What is Go?
AI: Go is a programming language.
`
	writeTestFile(t, path, content)

	exchanges, err := parsePlainText(path)
	if err != nil {
		t.Fatalf("parsePlainText: %v", err)
	}

	if len(exchanges) != 1 {
		t.Fatalf("expected 1 exchange, got %d", len(exchanges))
	}
}

func TestMineConversations_JSONL(t *testing.T) {
	tmpDir := t.TempDir()

	content := `{"role":"user","content":"How do I add a new API endpoint?"}
{"role":"assistant","content":"Create a handler in the api package and register the route."}
{"role":"user","content":"What about authentication?"}
{"role":"assistant","content":"Use the auth middleware to protect the endpoint."}
`
	writeTestFile(t, filepath.Join(tmpDir, "session.jsonl"), content)

	p := newTestPalace(t)
	ctx := context.Background()

	cfg := &MineConfig{
		Wing: "sessions",
		Rooms: []RoomDef{
			{Name: "api", Keywords: []string{"endpoint", "route"}},
			{Name: "auth", Keywords: []string{"authentication", "middleware"}},
		},
	}

	result, err := MineConversations(ctx, p, tmpDir, cfg)
	if err != nil {
		t.Fatalf("MineConversations: %v", err)
	}

	if result.Added == 0 {
		t.Error("expected at least 1 added drawer")
	}
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}

	drawers, _ := p.ListDrawers(ctx, DrawerFilter{Wing: "sessions"})
	if len(drawers) == 0 {
		t.Error("expected drawers in palace")
	}
}

func TestMineConversations_PlainText(t *testing.T) {
	tmpDir := t.TempDir()

	content := `> How does the build system work?
Assistant: The build uses Make targets for common tasks.

> What about testing?
Assistant: Run 'go test ./...' for all tests.
`
	writeTestFile(t, filepath.Join(tmpDir, "chat.txt"), content)

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineConversations(ctx, p, tmpDir, &MineConfig{Wing: "chats"})
	if err != nil {
		t.Fatalf("MineConversations: %v", err)
	}

	if result.Added != 2 {
		t.Errorf("expected 2 added, got %d", result.Added)
	}
}

func TestMineConversations_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineConversations(ctx, p, tmpDir, &MineConfig{Wing: "empty"})
	if err != nil {
		t.Fatalf("MineConversations: %v", err)
	}
	if result.Added != 0 {
		t.Errorf("expected 0 added, got %d", result.Added)
	}
}

func TestMineConversations_SkipsUnsupported(t *testing.T) {
	tmpDir := t.TempDir()

	// Write a .go file — should be skipped in convo mining.
	writeTestFile(t, filepath.Join(tmpDir, "code.go"), "package main\nfunc main() {}")

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineConversations(ctx, p, tmpDir, &MineConfig{Wing: "test"})
	if err != nil {
		t.Fatalf("MineConversations: %v", err)
	}
	if result.Processed != 0 {
		t.Errorf("expected 0 processed (only .go files), got %d", result.Processed)
	}
}

func TestPairExchanges_ConsecutiveAssistant(t *testing.T) {
	messages := []jsonlMessage{
		{Role: "user", Content: "question one"},
		{Role: "assistant", Content: "answer part one"},
		{Role: "assistant", Content: "answer part two"},
		{Role: "user", Content: "question two"},
		{Role: "assistant", Content: "answer two"},
	}

	exchanges := pairExchanges(messages)
	if len(exchanges) != 2 {
		t.Fatalf("expected 2 exchanges, got %d", len(exchanges))
	}

	// First exchange should include both assistant parts.
	if !containsStr(exchanges[0].content, "part one") || !containsStr(exchanges[0].content, "part two") {
		t.Error("first exchange should contain both assistant parts")
	}
}

// --- helpers ---

func containsStr(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) > len(substr) && findSubstr(s, substr))
}

func findSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func init() {
	// Ensure test temp dirs don't conflict.
	_ = os.Setenv("TMPDIR", os.TempDir())
}
