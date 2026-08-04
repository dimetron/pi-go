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

// DefaultMaxDailyTokens is the default daily token limit.
const DefaultMaxDailyTokens = 50000000

// Usage tracks token consumption for the current day.
type Usage struct {
	Date         string `json:"date"`          // YYYY-MM-DD
	InputTokens  int64  `json:"input_tokens"`  // total prompt tokens (includes cached)
	OutputTokens int64  `json:"output_tokens"` // total completion tokens
	Requests     int64  `json:"requests"`      // number of LLM calls

	// CachedInputTokens is the portion of InputTokens served from a provider
	// prompt cache. Providers bill these at a steep discount (Anthropic: 0.1x),
	// so InputTokens alone overstates cost whenever caching is active.
	//
	// Cache *write* tokens are not broken out separately: the genai usage
	// metadata has no field for them, so they remain folded into the
	// non-cached remainder. Writes bill at 1.25x, so the fresh-token figure
	// is a slight underestimate of true cost when a cache is first populated.
	CachedInputTokens int64 `json:"cached_input_tokens"`
}

// TotalTokens returns the combined input + output token count.
func (u *Usage) TotalTokens() int64 {
	return u.InputTokens + u.OutputTokens
}

// FreshInputTokens returns prompt tokens that were not served from cache.
func (u *Usage) FreshInputTokens() int64 {
	fresh := u.InputTokens - u.CachedInputTokens
	if fresh < 0 {
		return 0
	}
	return fresh
}

// CacheHitRate returns the share of prompt tokens served from cache (0-100).
// Returns 0 when no prompt tokens have been recorded.
func (u *Usage) CacheHitRate() float64 {
	if u.InputTokens <= 0 {
		return 0
	}
	return float64(u.CachedInputTokens) / float64(u.InputTokens) * 100
}

// Tracker tracks daily token usage and enforces a configurable limit.
type Tracker struct {
	mu       sync.Mutex
	usage    Usage
	limit    int64  // max total tokens per day (0 = unlimited)
	filePath string // persistence path

	// Session context window tracking.
	lastPromptTokens  int64 // most recent PromptTokenCount from LLM response
	lastCachedTokens  int64 // most recent CachedContentTokenCount from LLM response
	contextWindowSize int64 // model's context window size (0 = unknown)

	// cachePrefixTokens is the stable, already-cached prefix of the current
	// session — system prompt, tool declarations, and initial context. It is
	// the baseline subtracted under the BodyAfterPrefix compaction scope.
	cachePrefixTokens int64
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
	return t.AddWithCache(inputTokens, outputTokens, 0)
}

// AddWithCache records token usage from an LLM response, separating the
// portion of inputTokens that the provider served from its prompt cache.
// cachedTokens must be a subset of inputTokens; providers report the prompt
// count inclusive of cache reads.
//
// Returns an error if the daily limit would be exceeded.
func (t *Tracker) AddWithCache(inputTokens, outputTokens, cachedTokens int32) error {
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
	if cachedTokens > 0 {
		t.usage.CachedInputTokens += int64(cachedTokens)
	}
	t.usage.Requests++
	// Track the latest prompt token count for context window display.
	if inputTokens > 0 {
		t.lastPromptTokens = int64(inputTokens)
		t.lastCachedTokens = int64(cachedTokens)
		// The first request of a context window establishes the stable prefix
		// baseline — system prompt, tool declarations, initial context.
		if t.cachePrefixTokens == 0 {
			t.cachePrefixTokens = int64(inputTokens)
		}
	}
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

// CachedTokensToday returns prompt tokens served from cache today.
func (t *Tracker) CachedTokensToday() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureToday()
	return t.usage.CachedInputTokens
}

// CacheHitRateToday returns the share of today's prompt tokens served from
// cache (0-100). Returns 0 when no prompt tokens have been recorded.
func (t *Tracker) CacheHitRateToday() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ensureToday()
	return t.usage.CacheHitRate()
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

	_ = os.MkdirAll(filepath.Dir(t.filePath), 0700)
	_ = os.WriteFile(t.filePath, data, 0600)
}

// SetContextWindowSize sets the model's context window size for display.
func (t *Tracker) SetContextWindowSize(size int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.contextWindowSize = size
}

// ContextWindowSize returns the model's context window size.
func (t *Tracker) ContextWindowSize() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.contextWindowSize
}

// LastPromptTokens returns the most recent prompt token count from an LLM response.
// This represents how much of the context window is currently used.
func (t *Tracker) LastPromptTokens() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastPromptTokens
}

// ContextPercentUsed returns the percentage of the context window used (0-100+).
// Returns 0 if context window size is unknown.
func (t *Tracker) ContextPercentUsed() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.contextWindowSize <= 0 || t.lastPromptTokens <= 0 {
		return 0
	}
	return float64(t.lastPromptTokens) / float64(t.contextWindowSize) * 100
}

// LastCachedTokens returns the cache-read token count of the most recent
// LLM response. Zero means the last request missed the prompt cache entirely.
func (t *Tracker) LastCachedTokens() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastCachedTokens
}

// CachePrefixTokens returns the stable prefix baseline for the current context
// window — the prompt size of the window's first request.
func (t *Tracker) CachePrefixTokens() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cachePrefixTokens
}

// BodyTokens returns the tokens accumulated after the stable cached prefix.
// This is the figure the BodyAfterPrefix compaction scope measures, so a large
// but fully-cached system prompt never pushes a session toward compaction.
func (t *Tracker) BodyTokens() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	body := t.lastPromptTokens - t.cachePrefixTokens
	if body < 0 {
		return 0
	}
	return body
}

// ResetContextWindow clears the per-window baselines. Call this after
// compaction installs a fresh context window, so the next request re-establishes
// the prefix baseline.
func (t *Tracker) ResetContextWindow() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cachePrefixTokens = 0
	t.lastPromptTokens = 0
	t.lastCachedTokens = 0
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
		return fmt.Sprintf("Today: %s tokens (%s in, %s out) · %d requests · unlimited%s",
			formatTokenCount(total), formatTokenCount(u.InputTokens), formatTokenCount(u.OutputTokens),
			u.Requests, formatCacheSuffix(u))
	}
	pct := float64(total) / float64(limit) * 100
	remaining := limit - total
	if remaining < 0 {
		remaining = 0
	}
	return fmt.Sprintf("Today: %s / %s tokens (%.0f%%) · %s in, %s out · %d requests · %s remaining%s",
		formatTokenCount(total), formatTokenCount(limit), pct,
		formatTokenCount(u.InputTokens), formatTokenCount(u.OutputTokens),
		u.Requests, formatTokenCount(remaining), formatCacheSuffix(u))
}

// formatCacheSuffix renders the cache-hit breakdown, or an explicit
// "no cache hits" marker once enough traffic has flowed to make the absence
// meaningful. Silence would be indistinguishable from caching working.
func formatCacheSuffix(u Usage) string {
	if u.InputTokens <= 0 {
		return ""
	}
	if u.CachedInputTokens <= 0 {
		if u.Requests < 2 {
			return ""
		}
		return " · cache: no hits"
	}
	return fmt.Sprintf(" · cache: %s read (%.0f%% of input), %s fresh",
		formatTokenCount(u.CachedInputTokens), u.CacheHitRate(),
		formatTokenCount(u.FreshInputTokens()))
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
