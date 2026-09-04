package autocompact

import (
	"sync"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	adkmodel "google.golang.org/adk/v2/model"
)

// Meter is a standalone [ContextMeter] for callers that have no guardrail
// tracker to read — piagent, which is barred from importing internal/guardrail
// and in any case wants none of the daily-budget half of it.
//
// It measures and never enforces: nothing here can refuse a request. Feed it
// with [MeterCallback].
type Meter struct {
	mu                sync.Mutex
	contextWindowSize int64
	lastPromptTokens  int64

	// cachePrefixTokens is the stable, already-cached prefix of the current
	// window — system prompt, tool declarations, initial context. The first
	// request of a window establishes it, and BodyTokens subtracts it. It
	// mirrors guardrail.Tracker's rule deliberately: the two must agree, or
	// compaction would fire at different points depending on which entry
	// point the session ran through.
	cachePrefixTokens int64
}

// NewMeter returns a meter with an unknown context window. Compaction stays
// off until [Meter.SetContextWindowSize] is given a positive size, which is
// the same thing an unresolvable window means everywhere else.
func NewMeter() *Meter { return &Meter{} }

// SetContextWindowSize records the model's context window, in tokens.
func (m *Meter) SetContextWindowSize(size int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contextWindowSize = size
}

// ContextWindowSize reports the model's context window, or 0 if unknown.
func (m *Meter) ContextWindowSize() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.contextWindowSize
}

// BodyTokens reports the tokens accumulated after the stable cached prefix.
func (m *Meter) BodyTokens() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	body := m.lastPromptTokens - m.cachePrefixTokens
	if body < 0 {
		return 0
	}
	return body
}

// SetLastPromptTokens overwrites the prompt-token baseline, and clears the
// prefix baseline with it. A compaction pass calls this because it knows the
// post-pass count exactly; the rewritten window has a new stable prefix by
// definition, so the old one no longer applies.
//
// A non-positive n clears the prefix without setting a new count.
func (m *Meter) SetLastPromptTokens(n int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n > 0 {
		m.lastPromptTokens = n
	}
	m.cachePrefixTokens = 0
}

// Observe records the prompt-token count of one LLM response. promptTokens is
// inclusive of cache reads, as every provider reports it.
func (m *Meter) Observe(promptTokens int64) {
	if promptTokens <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastPromptTokens = promptTokens
	if m.cachePrefixTokens == 0 {
		m.cachePrefixTokens = promptTokens
	}
}

// MeterCallback returns an ADK after-model callback that feeds m from each
// response's usage metadata. It never rewrites the response.
//
// A streaming turn yields many responses and the callback sees each one; the
// last prompt count wins, which is the one describing the whole request.
func MeterCallback(m *Meter) llmagent.AfterModelCallback {
	return func(_ agent.Context, resp *adkmodel.LLMResponse, _ error) (*adkmodel.LLMResponse, error) {
		if m != nil && resp != nil && resp.UsageMetadata != nil {
			m.Observe(int64(resp.UsageMetadata.PromptTokenCount))
		}
		return nil, nil
	}
}
