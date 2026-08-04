package guardrail

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNew_CreatesTracker(t *testing.T) {
	tr := New(100_000)
	if tr == nil {
		t.Fatal("expected non-nil tracker")
	}
	if tr.Limit() != 100_000 {
		t.Errorf("expected limit 100000, got %d", tr.Limit())
	}
}

func TestNew_Unlimited(t *testing.T) {
	tr := New(0)
	if tr.Limit() != 0 {
		t.Errorf("expected unlimited (0), got %d", tr.Limit())
	}
	if tr.Remaining() != -1 {
		t.Errorf("expected -1 for unlimited, got %d", tr.Remaining())
	}
	if tr.PercentUsed() != 0 {
		t.Errorf("expected 0%% for unlimited, got %.1f%%", tr.PercentUsed())
	}
}

func TestAdd_TracksUsage(t *testing.T) {
	tr := NewWithPath(0, "")
	if err := tr.Add(100, 50); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	u := tr.Current()
	if u.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", u.InputTokens)
	}
	if u.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", u.OutputTokens)
	}
	if u.Requests != 1 {
		t.Errorf("expected 1 request, got %d", u.Requests)
	}
	if u.TotalTokens() != 150 {
		t.Errorf("expected 150 total, got %d", u.TotalTokens())
	}
	if tr.TotalUsed() != 150 {
		t.Errorf("expected TotalUsed 150, got %d", tr.TotalUsed())
	}
}

func TestAdd_MultipleRequests(t *testing.T) {
	tr := NewWithPath(0, "")
	_ = tr.Add(100, 50)
	_ = tr.Add(200, 100)
	_ = tr.Add(300, 150)

	u := tr.Current()
	if u.InputTokens != 600 {
		t.Errorf("expected 600 input, got %d", u.InputTokens)
	}
	if u.OutputTokens != 300 {
		t.Errorf("expected 300 output, got %d", u.OutputTokens)
	}
	if u.Requests != 3 {
		t.Errorf("expected 3 requests, got %d", u.Requests)
	}
}

func TestAdd_EnforcesLimit(t *testing.T) {
	tr := NewWithPath(1000, "")

	// First call: 800 tokens — OK.
	if err := tr.Add(500, 300); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Second call: 300 more — would exceed 1000.
	err := tr.Add(200, 100)
	if err == nil {
		t.Fatal("expected limit exceeded error")
	}

	le := &LimitExceededError{}
	ok := errors.As(err, &le)
	if !ok {
		t.Fatalf("expected *LimitExceededError, got %T", err)
	}
	if le.Limit != 1000 {
		t.Errorf("expected limit 1000, got %d", le.Limit)
	}
	if le.Used != 800 {
		t.Errorf("expected used 800, got %d", le.Used)
	}
	if le.Asked != 300 {
		t.Errorf("expected asked 300, got %d", le.Asked)
	}
}

func TestCheck_LimitNotExceeded(t *testing.T) {
	tr := NewWithPath(1000, "")
	tr.Add(500, 200)

	if err := tr.Check(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCheck_LimitExceeded(t *testing.T) {
	tr := NewWithPath(1000, "")
	// Force usage past limit by using internal state.
	tr.mu.Lock()
	tr.usage.Date = time.Now().Format("2006-01-02")
	tr.usage.InputTokens = 600
	tr.usage.OutputTokens = 500
	tr.mu.Unlock()

	err := tr.Check()
	if err == nil {
		t.Fatal("expected limit exceeded error")
	}
	if !strings.Contains(err.Error(), "daily token limit exceeded") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCheck_Unlimited(t *testing.T) {
	tr := NewWithPath(0, "")
	tr.Add(1_000_000, 1_000_000)
	if err := tr.Check(); err != nil {
		t.Errorf("unlimited should never error: %v", err)
	}
}

func TestRemaining(t *testing.T) {
	tr := NewWithPath(1000, "")
	tr.Add(300, 200)

	rem := tr.Remaining()
	if rem != 500 {
		t.Errorf("expected 500 remaining, got %d", rem)
	}
}

func TestRemaining_Exceeded(t *testing.T) {
	tr := NewWithPath(100, "")
	tr.mu.Lock()
	tr.usage.Date = time.Now().Format("2006-01-02")
	tr.usage.InputTokens = 150
	tr.mu.Unlock()

	if tr.Remaining() != 0 {
		t.Errorf("expected 0 when exceeded, got %d", tr.Remaining())
	}
}

func TestPercentUsed(t *testing.T) {
	tr := NewWithPath(1000, "")
	tr.Add(250, 250)

	pct := tr.PercentUsed()
	if pct != 50.0 {
		t.Errorf("expected 50%%, got %.1f%%", pct)
	}
}

func TestSetLimit(t *testing.T) {
	tr := NewWithPath(0, "")
	tr.SetLimit(50_000)

	if tr.Limit() != 50_000 {
		t.Errorf("expected 50000, got %d", tr.Limit())
	}
}

func TestPersistence_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	// Create tracker and add usage.
	tr1 := NewWithPath(100_000, path)
	_ = tr1.Add(500, 200)
	_ = tr1.Add(300, 100)

	// Create new tracker from same file.
	tr2 := NewWithPath(100_000, path)
	u := tr2.Current()

	if u.InputTokens != 800 {
		t.Errorf("expected 800 input, got %d", u.InputTokens)
	}
	if u.OutputTokens != 300 {
		t.Errorf("expected 300 output, got %d", u.OutputTokens)
	}
	if u.Requests != 2 {
		t.Errorf("expected 2 requests, got %d", u.Requests)
	}
}

func TestPersistence_DayRollover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	// Write yesterday's data.
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	data, _ := json.Marshal(Usage{
		Date:         yesterday,
		InputTokens:  999_999,
		OutputTokens: 999_999,
		Requests:     100,
	})
	os.WriteFile(path, data, 0600)

	// Load — should reset to today.
	tr := NewWithPath(100_000, path)
	u := tr.Current()

	if u.Date != time.Now().Format("2006-01-02") {
		t.Errorf("expected today's date, got %q", u.Date)
	}
	if u.TotalTokens() != 0 {
		t.Errorf("expected 0 tokens after rollover, got %d", u.TotalTokens())
	}
}

func TestPersistence_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "usage.json")

	os.WriteFile(path, []byte("not json"), 0600)

	// Should not crash, just start fresh.
	tr := NewWithPath(100_000, path)
	u := tr.Current()
	if u.TotalTokens() != 0 {
		t.Errorf("expected 0 tokens from corrupted file, got %d", u.TotalTokens())
	}
}

func TestPersistence_MissingFile(t *testing.T) {
	tr := NewWithPath(100_000, "/nonexistent/path/usage.json")
	u := tr.Current()
	if u.TotalTokens() != 0 {
		t.Errorf("expected 0, got %d", u.TotalTokens())
	}
}

func TestPersistence_NoPath(t *testing.T) {
	tr := NewWithPath(100_000, "")
	tr.Add(500, 200)
	u := tr.Current()
	if u.TotalTokens() != 700 {
		t.Errorf("expected 700, got %d", u.TotalTokens())
	}
	// No file written — verify no crash.
}

func TestLimitExceededError_Message(t *testing.T) {
	e1 := &LimitExceededError{Limit: 1000, Used: 800, Asked: 300}
	if !strings.Contains(e1.Error(), "800/1000") {
		t.Errorf("expected 800/1000 in message, got: %s", e1.Error())
	}
	if !strings.Contains(e1.Error(), "300") {
		t.Errorf("expected 300 in message, got: %s", e1.Error())
	}

	e2 := &LimitExceededError{Limit: 1000, Used: 1000}
	if !strings.Contains(e2.Error(), "1000/1000") {
		t.Errorf("expected 1000/1000, got: %s", e2.Error())
	}
}

func TestFormatUsage_WithLimit(t *testing.T) {
	u := Usage{
		Date:         "2026-03-17",
		InputTokens:  50_000,
		OutputTokens: 25_000,
		Requests:     10,
	}
	s := FormatUsage(u, 100_000)
	if !strings.Contains(s, "75.0k") {
		t.Errorf("expected 75.0k total, got: %s", s)
	}
	if !strings.Contains(s, "75%") {
		t.Errorf("expected 75%%, got: %s", s)
	}
	if !strings.Contains(s, "10 requests") {
		t.Errorf("expected 10 requests, got: %s", s)
	}
}

func TestFormatUsage_Unlimited(t *testing.T) {
	u := Usage{InputTokens: 1_500_000, OutputTokens: 500_000, Requests: 50}
	s := FormatUsage(u, 0)
	if !strings.Contains(s, "2.0M") {
		t.Errorf("expected 2.0M total, got: %s", s)
	}
	if !strings.Contains(s, "unlimited") {
		t.Errorf("expected 'unlimited', got: %s", s)
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{500, "500"},
		{1_000, "1.0k"},
		{1_500, "1.5k"},
		{50_000, "50.0k"},
		{1_000_000, "1.0M"},
		{2_500_000, "2.5M"},
	}
	for _, tt := range tests {
		got := formatTokenCount(tt.n)
		if got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestUsage_TotalTokens(t *testing.T) {
	u := Usage{InputTokens: 100, OutputTokens: 50}
	if u.TotalTokens() != 150 {
		t.Errorf("expected 150, got %d", u.TotalTokens())
	}
}

func TestFormatUsage_OverLimit(t *testing.T) {
	u := Usage{
		Date:         "2026-03-17",
		InputTokens:  80_000,
		OutputTokens: 30_000,
		Requests:     20,
	}
	s := FormatUsage(u, 100_000)
	if !strings.Contains(s, "110%") || !strings.Contains(s, "0 remaining") {
		t.Errorf("expected over-limit formatting, got: %s", s)
	}
}

func TestContextWindow_GetSet(t *testing.T) {
	tr := New(0)

	// Initially unknown.
	if got := tr.ContextWindowSize(); got != 0 {
		t.Errorf("expected default ContextWindowSize 0, got %d", got)
	}
	if got := tr.LastPromptTokens(); got != 0 {
		t.Errorf("expected default LastPromptTokens 0, got %d", got)
	}
	if got := tr.ContextPercentUsed(); got != 0 {
		t.Errorf("expected 0%% when unknown, got %f", got)
	}

	// Set a context window.
	tr.SetContextWindowSize(200_000)
	if got := tr.ContextWindowSize(); got != 200_000 {
		t.Errorf("expected 200000, got %d", got)
	}

	// Still 0 when no prompt tokens have been recorded.
	if got := tr.ContextPercentUsed(); got != 0 {
		t.Errorf("expected 0%% with no prompt tokens, got %f", got)
	}

	// Record prompt tokens via Add (sets lastPromptTokens).
	if err := tr.Add(50_000, 1_000); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if got := tr.LastPromptTokens(); got != 50_000 {
		t.Errorf("expected LastPromptTokens 50000, got %d", got)
	}
	got := tr.ContextPercentUsed()
	if got < 24.9 || got > 25.1 {
		t.Errorf("expected ContextPercentUsed ~25, got %f", got)
	}
}

// SetLastPromptTokens is the path compaction uses to refresh the gauge
// without waiting for the next LLM response: it must overwrite the prompt
// count and reset the cached-prefix baseline (the new window has a new
// prefix by definition).
func TestSetLastPromptTokens_OverwritesBaseline(t *testing.T) {
	tr := New(0)
	tr.SetContextWindowSize(200_000)
	// Prime the prefix baseline by recording one LLM response — this is what
	// the first request of a window normally does.
	if err := tr.Add(80_000, 1_000); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if got := tr.CachePrefixTokens(); got != 80_000 {
		t.Fatalf("expected prefix baseline 80000 after first Add, got %d", got)
	}

	tr.SetLastPromptTokens(30_000)
	if got := tr.LastPromptTokens(); got != 30_000 {
		t.Errorf("expected LastPromptTokens 30000 after SetLastPromptTokens, got %d", got)
	}
	if got := tr.CachePrefixTokens(); got != 0 {
		t.Errorf("expected CachePrefixTokens reset to 0 after SetLastPromptTokens, got %d", got)
	}
	if got := tr.LastCachedTokens(); got != 0 {
		t.Errorf("expected LastCachedTokens reset to 0 after SetLastPromptTokens, got %d", got)
	}

	// Zero / negative values leave the prompt count as-is but still reset
	// the prefix — useful when a compaction produces an empty window.
	tr.SetLastPromptTokens(15_000)
	if got := tr.LastPromptTokens(); got != 15_000 {
		t.Errorf("expected LastPromptTokens 15000 after second SetLastPromptTokens, got %d", got)
	}
	tr.SetLastPromptTokens(0)
	if got := tr.LastPromptTokens(); got != 15_000 {
		t.Errorf("SetLastPromptTokens(0) must not clobber a non-zero prompt count, got %d", got)
	}
}

func TestContextPercentUsed_ZeroWindow(t *testing.T) {
	tr := New(0)
	// No context window set, but add some tokens.
	if err := tr.Add(10, 1); err != nil {
		t.Fatalf("Add returned error: %v", err)
	}
	if got := tr.ContextPercentUsed(); got != 0 {
		t.Errorf("expected 0%% with unknown context window, got %f", got)
	}
}
