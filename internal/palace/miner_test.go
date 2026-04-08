package palace

import (
	"testing"
)

func TestChunkText_ShortText(t *testing.T) {
	text := "Hello world, this is a short text."
	chunks := chunkText(text, 100, 20)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0] != text {
		t.Errorf("chunk mismatch: got %q", chunks[0])
	}
}

func TestChunkText_SplitsOnParagraph(t *testing.T) {
	text := "First paragraph with enough content to be meaningful.\n\nSecond paragraph also with substantial content here.\n\nThird paragraph to ensure we have multiple chunks."
	chunks := chunkText(text, 80, 10)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestChunkText_MinimumSize(t *testing.T) {
	text := "A\n\nB\n\nThis is a longer paragraph that should survive the minimum size filter because it has enough content."
	chunks := chunkText(text, 60, 10)

	// Very short chunks (< 50 chars) should be filtered out.
	for _, c := range chunks {
		if len(c) < 50 {
			t.Errorf("chunk below minimum size (50): len=%d, content=%q", len(c), c)
		}
	}
}

func TestChunkText_Overlap(t *testing.T) {
	// Generate enough text for multiple chunks with overlap.
	text := ""
	for i := 0; i < 20; i++ {
		text += "Line number " + string(rune('A'+i)) + " with enough padding to be relevant to chunking.\n"
	}
	chunks := chunkText(text, 200, 50)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
}

func TestDetectRoom_PatternMatch(t *testing.T) {
	rooms := []RoomDef{
		{Name: "auth", Patterns: []string{"internal/auth/*"}},
		{Name: "api", Patterns: []string{"cmd/api/*"}},
	}

	room := detectRoom("internal/auth/handler.go", rooms)
	if room != "auth" {
		t.Errorf("expected auth, got %s", room)
	}
}

func TestDetectRoom_DirectoryMatch(t *testing.T) {
	rooms := []RoomDef{
		{Name: "auth"},
		{Name: "database"},
	}

	room := detectRoom("internal/auth/handler.go", rooms)
	if room != "auth" {
		t.Errorf("expected auth, got %s", room)
	}
}

func TestDetectRoom_FirstDirFallback(t *testing.T) {
	rooms := []RoomDef{
		{Name: "unrelated"},
	}

	room := detectRoom("cmd/server/main.go", rooms)
	if room != "cmd" {
		t.Errorf("expected cmd, got %s", room)
	}
}

func TestDetectRoom_General(t *testing.T) {
	rooms := []RoomDef{
		{Name: "unrelated"},
	}

	room := detectRoom("main.go", rooms)
	if room != "general" {
		t.Errorf("expected general, got %s", room)
	}
}

func TestIsGitignored(t *testing.T) {
	patterns := []string{"*.log", "dist", "coverage"}

	tests := []struct {
		path     string
		expected bool
	}{
		{"app.log", true},
		{"dist/bundle.js", true},
		{"coverage/report.html", true},
		{"src/main.go", false},
		{"internal/auth/handler.go", false},
	}

	for _, tt := range tests {
		got := isGitignored(tt.path, patterns)
		if got != tt.expected {
			t.Errorf("isGitignored(%q) = %v, want %v", tt.path, got, tt.expected)
		}
	}
}

func TestDetectRoomFromContent(t *testing.T) {
	rooms := []RoomDef{
		{Name: "auth", Keywords: []string{"jwt", "login", "session"}},
		{Name: "database", Keywords: []string{"sql", "query", "migration"}},
	}

	tests := []struct {
		content  string
		expected string
	}{
		{"JWT token validation for user login", "auth"},
		{"SQL migration to add users table", "database"},
		{"General utility functions", "general"},
	}

	for _, tt := range tests {
		got := detectRoomFromContent(tt.content, rooms)
		if got != tt.expected {
			t.Errorf("detectRoomFromContent(%q) = %s, want %s", tt.content[:20], got, tt.expected)
		}
	}
}
