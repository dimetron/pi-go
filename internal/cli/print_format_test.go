package cli

import (
	"strings"
	"testing"
)

func TestToolArgsPreviewAndPrintFormatting(t *testing.T) {
	if got := toolArgsPreview(nil); got != "" {
		t.Fatalf("empty preview = %q", got)
	}
	preview := toolArgsPreview(map[string]any{"file_path": "main.go"})
	if !strings.Contains(preview, "main.go") {
		t.Fatalf("preview = %q", preview)
	}
	long := toolArgsPreview(map[string]any{"text": strings.Repeat("x", 200)})
	if len([]rune(long)) != 100 {
		t.Fatalf("long preview length = %d, want 100", len([]rune(long)))
	}
	callWithArgs := formatPrintToolCall("read", map[string]any{"file_path": "main.go"})
	if !strings.Contains(callWithArgs, "read") || !strings.Contains(callWithArgs, "main.go") {
		t.Fatalf("callWithArgs = %q", callWithArgs)
	}
	callNoArgs := formatPrintToolCall("bash", nil)
	if !strings.Contains(callNoArgs, "bash") || strings.Contains(callNoArgs, "file_path") {
		t.Fatalf("callNoArgs = %q", callNoArgs)
	}
	if done := formatPrintToolDone("read"); !strings.Contains(done, "read done") {
		t.Fatalf("done = %q", done)
	}
}
