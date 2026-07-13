package palace

import (
	"strings"
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

// The chunker must never emit a chunk larger than the embedder will accept.
//
// Embed() truncates every input to maxCharLength, so any chunk above that limit
// is silently cut down before embedding: the drawer keeps the full text but its
// vector only describes the head of it, leaving the tail stored yet unfindable
// by semantic search. When these two constants drifted apart (chunk 1500 vs
// limit 512), 98% of chunks were truncated and ~60% of all mined text never
// reached the model.
func TestChunkSizeFitsEmbedderLimit(t *testing.T) {
	if defaultChunkSize > maxCharLength {
		t.Fatalf("defaultChunkSize (%d) exceeds maxCharLength (%d): every chunk would be "+
			"truncated before embedding, and the tail would be unsearchable",
			defaultChunkSize, maxCharLength)
	}
}

// No chunk the chunker produces may exceed the embedder's limit, for any input.
func TestChunkTextNeverExceedsEmbedderLimit(t *testing.T) {
	inputs := []string{
		strings.Repeat("package main\nfunc main() { println(\"x\") }\n", 200), // dense code
		strings.Repeat("word ", 5000),                                         // prose
		strings.Repeat("a", 10000),                                            // no break points at all
		strings.Repeat("para\n\n", 1000),                                      // paragraph breaks
	}

	for i, in := range inputs {
		for j, chunk := range chunkText(in, defaultChunkSize, defaultChunkOverlap) {
			if len(chunk) > maxCharLength {
				t.Errorf("input %d chunk %d is %d chars, over the embedder's %d-char limit — "+
					"it would be truncated and its tail lost", i, j, len(chunk), maxCharLength)
			}
		}
	}
}
