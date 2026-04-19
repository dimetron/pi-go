package acp

import (
	"strings"
	"sync"
	"testing"
)

func TestToolCallTitleFilter_UpdateReplacesPending(t *testing.T) {
	var (
		mu      sync.Mutex
		emitted []string
		filter  = NewToolCallTitleFilter(func(title string) {
			mu.Lock()
			emitted = append(emitted, title)
			mu.Unlock()
		})
	)

	filter.OnToolCall("id-1", "Terminal")
	filter.OnToolCallUpdate("id-1", "`go test ./...`")
	filter.Flush()

	if got := strings.Join(emitted, "|"); got != "`go test ./...`" {
		t.Errorf("emitted = %q, want only the update title", got)
	}
}

func TestToolCallTitleFilter_FlushEmitsPending(t *testing.T) {
	var emitted []string
	filter := NewToolCallTitleFilter(func(title string) {
		emitted = append(emitted, title)
	})

	filter.OnToolCall("id-1", "Search")
	filter.Flush()

	if len(emitted) != 1 || emitted[0] != "Search" {
		t.Errorf("emitted = %v, want [Search]", emitted)
	}
}

func TestToolCallTitleFilter_EmptyIdEmitsImmediately(t *testing.T) {
	var emitted []string
	filter := NewToolCallTitleFilter(func(title string) {
		emitted = append(emitted, title)
	})

	filter.OnToolCall("", "Inline")
	if len(emitted) != 1 || emitted[0] != "Inline" {
		t.Errorf("emitted = %v, want [Inline]", emitted)
	}
}

func TestEnrichToolCallTitle_AppendsCommand(t *testing.T) {
	got := EnrichToolCallTitle("Terminal", map[string]any{"command": "go test ./..."})
	want := "Terminal: go test ./..."
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEnrichToolCallTitle_PrefersDescriptionOverPrompt(t *testing.T) {
	got := EnrichToolCallTitle("TaskOutput", map[string]any{
		"description": "Review session and provider code",
		"prompt":      "lots of text...",
	})
	want := "TaskOutput: Review session and provider code"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEnrichToolCallTitle_FallsBackToTitleWhenRawInputUnusable(t *testing.T) {
	got := EnrichToolCallTitle("Read", map[string]any{"unrelated": 42.0})
	if got != "Read" {
		t.Errorf("got %q, want Read", got)
	}
}

func TestEnrichToolCallTitle_NilRawInput(t *testing.T) {
	got := EnrichToolCallTitle("Edit", nil)
	if got != "Edit" {
		t.Errorf("got %q, want Edit", got)
	}
}

func TestEnrichToolCallTitle_EmptyTitleUsesSnippet(t *testing.T) {
	got := EnrichToolCallTitle("", map[string]any{"file_path": "/tmp/x.go"})
	if got != "/tmp/x.go" {
		t.Errorf("got %q, want /tmp/x.go", got)
	}
}

func TestEnrichToolCallTitle_TruncatesLongSnippet(t *testing.T) {
	long := strings.Repeat("a", 500)
	got := EnrichToolCallTitle("Bash", map[string]any{"command": long})
	if !strings.HasPrefix(got, "Bash: ") {
		t.Fatalf("missing prefix: %q", got)
	}
	tail := strings.TrimPrefix(got, "Bash: ")
	if len(tail) > 120 {
		t.Errorf("snippet length = %d, want <= 120", len(tail))
	}
	if !strings.HasSuffix(tail, "...") {
		t.Errorf("expected truncation marker, got %q", tail)
	}
}
