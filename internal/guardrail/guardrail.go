// Package guardrail tracks daily token usage and enforces limits.
package guardrail

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultMaxDailyTokens is the default daily token limit (0 = unlimited).
const DefaultMaxDailyTokens = 0

// Usage tracks token consumption for the current day.
type Usage struct {
	Date         string `json:"date"`          // YYYY-MM-DD
	InputTokens  int64  `json:"input_tokens"`  // total prompt tokens
	OutputTokens int64  `json:"output_tokens"` // total completion tokens
	Requests     int64  `json:"requests"`      // number of LLM calls
}

// TotalTokens returns the combined input + output token count.
func (u *Usage) TotalTokens() int64 {
	return u.InputTokens + u.OutputTokens
}

// Tracker tracks daily token usage and enforces a configurable limit.
type Tracker struct {
	mu       sync.Mutex
	usage    Usage
	limit    int64  // max total tokens per day (0 = unlimited)
	filePath string // persistence path
}

// New creates a tracker with the given daily token limit.
// It loads any existing usage for today from ~/.pi-go/usage.json.
func New(maxDailyTokens int64) *Tracker {
	t := &Tracker{
		limit: maxDailyTokens,
	}

	home, err := os.UserHomeDir()
	if err == nil {
		t.filePath = filepath.Join(home, ".pi-go", "usage.json")
	}

	t.load()
	return t
}

// NewWithPath creates a tracker with a custom file path (for testing).
func NewWithPath(maxDailyTokens int64, path string) *Tracker {
	t := &Tracker{
		limit:    maxDailyTokens,
		filePath: path,
	}
	t.load()
	return t
}

// Add records token usage from an LLM response.
// Returns an error if the daily limit would be exceeded.
func (t *Tracker) Add(inputTokens, outputTokens int32) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ensureToday()

	if t.limit > 0 {
		projected := t.usage.TotalTokens() + int64(inputTokens) + int64(outputTokens)
		if projected > t.limit {
			return &LimitExceededError{
				Limit: t.limit,
				Used:  t.usage.TotalTokens(),
				Asked: int64(inputTokens) + int64(outputTokens),
			}
		}
	}

	t.usage.InputTokens += int64(inputTokens)
	t.usage.OutputTokens += int64(outputTokens)
	t.usage.Requests++
	t.save()
	return nil
}

// Check returns an error if the daily limit is already exceeded.
func (t *Tracker) Check() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.ensureToday()

	if t.limit > 0 && t.usage.TotalTokens() >= t.limit {
		return &LimitExceededError{
			Limit: t.limit,
			Used:  t.usage.TotalTokens(),
		}
	}
	return nil
}

// Current returns the current usage snapshot.
func (t *Tracker) Current() Usage {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureToday()
	return t.usage
}

// Limit returns the configured daily token limit.
func (t *Tracker) Limit() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.limit
}

// SetLimit updates the daily token limit.
func (t *Tracker) SetLimit(limit int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.limit = limit
}

// Remaining returns how many tokens are left today.
// Returns -1 if unlimited.
func (t *Tracker) Remaining() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureToday()

	if t.limit <= 0 {
		return -1
	}
	rem := t.limit - t.usage.TotalTokens()
	if rem < 0 {
		return 0
	}
	return rem
}

// TotalUsed returns total tokens consumed today.
func (t *Tracker) TotalUsed() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureToday()
	return t.usage.TotalTokens()
}

// PercentUsed returns the percentage of daily limit consumed (0-100+).
// Returns 0 if unlimited.
func (t *Tracker) PercentUsed() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureToday()

	if t.limit <= 0 {
		return 0
	}
	return float64(t.usage.TotalTokens()) / float64(t.limit) * 100
}

// ensureToday resets the counter if the date has changed. Must hold mu.
func (t *Tracker) ensureToday() {
	today := time.Now().Format("2006-01-02")
	if t.usage.Date != today {
		t.usage = Usage{Date: today}
		t.save()
	}
}

func (t *Tracker) load() {
	if t.filePath == "" {
		return
	}

	data, err := os.ReadFile(t.filePath)
	if err != nil {
		return
	}

	var u Usage
	if json.Unmarshal(data, &u) == nil {
		today := time.Now().Format("2006-01-02")
		if u.Date == today {
			t.usage = u
		} else {
			// Different day — start fresh.
			t.usage = Usage{Date: today}
		}
	}
}

func (t *Tracker) save() {
	if t.filePath == "" {
		return
	}

	data, err := json.MarshalIndent(t.usage, "", "  ")
	if err != nil {
		return
	}

	os.MkdirAll(filepath.Dir(t.filePath), 0700)
	os.WriteFile(t.filePath, data, 0600)
}

// LimitExceededError is returned when the daily token limit is reached.
type LimitExceededError struct {
	Limit int64
	Used  int64
	Asked int64
}

func (e *LimitExceededError) Error() string {
	if e.Asked > 0 {
		return fmt.Sprintf("daily token limit exceeded: used %d/%d, request needs ~%d more tokens",
			e.Used, e.Limit, e.Asked)
	}
	return fmt.Sprintf("daily token limit exceeded: used %d/%d tokens", e.Used, e.Limit)
}

// FormatUsage returns a human-readable usage summary.
func FormatUsage(u Usage, limit int64) string {
	total := u.TotalTokens()
	if limit <= 0 {
		return fmt.Sprintf("Today: %s tokens (%s in, %s out) · %d requests · unlimited",
			formatTokenCount(total), formatTokenCount(u.InputTokens), formatTokenCount(u.OutputTokens), u.Requests)
	}
	pct := float64(total) / float64(limit) * 100
	remaining := limit - total
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("Today: %s / %s tokens (%.0f%%) · %s in, %s out · %d requests · %s remaining",
		formatTokenCount(total), formatTokenCount(limit), pct,
		formatTokenCount(u.InputTokens), formatTokenCount(u.OutputTokens),
		u.Requests, formatTokenCount(remaining))
}

func formatTokenCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
