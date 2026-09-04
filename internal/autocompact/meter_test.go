package autocompact

import (
	"sync"
	"testing"

	"google.golang.org/genai"

	adkmodel "google.golang.org/adk/v2/model"
)

// TestMeterBodyTokens covers the prefix rule. The failure it guards against is
// silent: a meter that counted the cached system prompt as body would compact
// a session that had barely started, and one that never established a prefix
// would never compact at all.
func TestMeterBodyTokens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		observe  []int64
		wantBody int64
	}{
		{desc: "no observations means no body", observe: nil, wantBody: 0},
		{desc: "first observation is all prefix", observe: []int64{11_045}, wantBody: 0},
		{desc: "growth after the prefix is body", observe: []int64{11_045, 40_000}, wantBody: 28_955},
		{desc: "a shrinking prompt never reports negative body", observe: []int64{40_000, 10_000}, wantBody: 0},
		{desc: "non-positive counts are ignored", observe: []int64{11_045, 0, -5, 20_000}, wantBody: 8_955},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()
			m := NewMeter()
			for _, n := range tt.observe {
				m.Observe(n)
			}
			if got := m.BodyTokens(); got != tt.wantBody {
				t.Errorf("BodyTokens() = %d, want %d", got, tt.wantBody)
			}
		})
	}
}

// TestMeterContextWindow pins the contract BuildHook relies on: an unset
// window reads as 0, which Decide treats as "never compact".
func TestMeterContextWindow(t *testing.T) {
	t.Parallel()

	m := NewMeter()
	if got := m.ContextWindowSize(); got != 0 {
		t.Fatalf("fresh meter ContextWindowSize() = %d, want 0", got)
	}
	m.SetContextWindowSize(1_048_576)
	if got := m.ContextWindowSize(); got != 1_048_576 {
		t.Errorf("ContextWindowSize() = %d, want 1048576", got)
	}
}

// TestMeterSetLastPromptTokens covers the post-compaction reset: the rewritten
// window has a new prefix, so the old one must not carry over and make the
// fresh window look empty.
func TestMeterSetLastPromptTokens(t *testing.T) {
	t.Parallel()

	m := NewMeter()
	m.Observe(50_000)
	m.Observe(120_000)

	m.SetLastPromptTokens(18_000)
	if got := m.BodyTokens(); got != 18_000 {
		t.Fatalf("BodyTokens() after reset = %d, want 18000", got)
	}

	// The first request after compaction re-establishes the prefix, exactly as
	// the first request of any window does, so the fresh window reads as empty
	// until it grows again. guardrail.Tracker does the same; see
	// TestTrackerAndMeterAgreeOnBodyTokens.
	m.Observe(25_000)
	if got := m.BodyTokens(); got != 0 {
		t.Errorf("BodyTokens() = %d, want 0 — the post-compaction request is the new prefix", got)
	}
	m.Observe(33_000)
	if got := m.BodyTokens(); got != 8_000 {
		t.Errorf("BodyTokens() = %d, want 8000 — growth past the new prefix", got)
	}
}

// TestMeterCallback covers the wiring, including the shapes that arrive when a
// response carries no usage at all. A callback that panicked on one of these
// would take the turn down with it.
func TestMeterCallback(t *testing.T) {
	t.Parallel()

	m := NewMeter()
	cb := MeterCallback(m)

	for _, resp := range []*adkmodel.LLMResponse{
		nil,
		{},
		{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 9_000}},
		{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 31_000}},
	} {
		got, err := cb(nil, resp, nil)
		if err != nil {
			t.Fatalf("callback returned error: %v", err)
		}
		if got != nil {
			t.Errorf("callback rewrote the response (%v); it must observe only", got)
		}
	}

	if got := m.BodyTokens(); got != 22_000 {
		t.Errorf("BodyTokens() = %d, want 22000", got)
	}

	// A nil meter is the shape a caller gets from an un-wired Deps; the
	// callback must tolerate it rather than panic mid-turn.
	if _, err := MeterCallback(nil)(nil, &adkmodel.LLMResponse{
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 1},
	}, nil); err != nil {
		t.Errorf("nil-meter callback returned error: %v", err)
	}
}

// TestMeterConcurrentAccess exists for the race detector: a streaming turn
// writes from the model goroutine while the pre-turn hook reads.
func TestMeterConcurrentAccess(t *testing.T) {
	t.Parallel()

	m := NewMeter()
	m.SetContextWindowSize(200_000)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() { defer wg.Done(); m.Observe(int64(1_000 + i)) }()
		go func() { defer wg.Done(); _ = m.BodyTokens(); _ = m.ContextWindowSize() }()
	}
	wg.Wait()
}
