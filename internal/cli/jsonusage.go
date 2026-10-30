package cli

import (
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"

	"github.com/dimetron/pi-go/internal/provider"
)

// usageAccumulator sums provider-reported token usage across the responses of
// one --mode json run.
//
// It exists because the JSON stream previously carried no usage at all, so
// every consumer that wanted a token count had to approximate one from
// character counts. Terminal-Bench's agent context, hack/test/tb2/collect.py and
// the eval report all want the real number.
//
// Two properties matter:
//
//   - Only final responses are counted. A streaming run delivers partial events
//     whose usage block is absent, and the aggregate event arrives once the turn
//     completes; summing partials as well would double-count.
//   - Cost is priced per response, at report time, from the embedded pricing
//     snapshot. A model with no price contributes tokens but no cost, and
//     ResponsesPriced records how many responses could be priced so a zero cost
//     is never mistaken for a free run.
type usageAccumulator struct {
	model    string
	provider string

	input    int
	output   int
	cached   int
	thoughts int
	total    int
	costUSD  float64
	priced   int
	seen     bool
}

// add records one response's usage. Events without a usage block (pure tool
// responses, errors, providers that omit it) are ignored.
func (u *usageAccumulator) add(ev *session.Event) {
	if ev == nil || ev.UsageMetadata == nil || ev.Partial {
		return
	}
	m := ev.UsageMetadata

	u.seen = true
	u.input += int(m.PromptTokenCount)
	u.output += int(m.CandidatesTokenCount)
	u.cached += int(m.CachedContentTokenCount)
	u.thoughts += int(m.ThoughtsTokenCount)
	// Not every provider fills TotalTokenCount — the Ollama and Anthropic paths
	// leave it zero — so fall back to the sum rather than reporting a total of
	// zero alongside nonzero input and output.
	if total := int(m.TotalTokenCount); total > 0 {
		u.total += total
	} else {
		u.total += int(m.PromptTokenCount) + int(m.CandidatesTokenCount) + int(m.ThoughtsTokenCount)
	}

	if cost, ok := u.price(m); ok {
		u.costUSD += cost
		u.priced++
	}
}

// price resolves what one response cost, if its model is priced. Rates in the
// snapshot are per million tokens; CacheRead bills the cached prompt portion at
// the discounted rate when the model carries one.
func (u *usageAccumulator) price(m *genai.GenerateContentResponseUsageMetadata) (float64, bool) {
	if u.model == "" {
		return 0, false
	}
	pm, ok := provider.CostFor(u.provider, u.model)
	if !ok {
		return 0, false
	}
	prompt := float64(m.PromptTokenCount)
	cached := float64(m.CachedContentTokenCount)
	if pm.CacheRead > 0 && cached > 0 {
		// Cached prompt tokens bill at the cache rate instead of the full input
		// rate; the remainder bills at input.
		prompt -= cached
		if prompt < 0 {
			prompt = 0
		}
		return (prompt*pm.Input + cached*pm.CacheRead +
			float64(m.CandidatesTokenCount)*pm.Output) / 1e6, true
	}
	return (prompt*pm.Input + float64(m.CandidatesTokenCount)*pm.Output) / 1e6, true
}

// snapshot renders the accumulated usage for a message_end event, or nil when
// no response reported usage (in which case the field is omitted entirely
// rather than emitted as a misleading zero).
func (u *usageAccumulator) snapshot() *jsonUsage {
	if u == nil || !u.seen {
		return nil
	}
	return &jsonUsage{
		InputTokens:     u.input,
		OutputTokens:    u.output,
		CachedTokens:    u.cached,
		ThoughtTokens:   u.thoughts,
		TotalTokens:     u.total,
		CostUSD:         u.costUSD,
		ResponsesPriced: u.priced,
	}
}
