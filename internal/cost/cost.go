// Package cost turns provider token counts into a USD estimate.
//
// Rates are USD per million tokens. The default table is the models.dev
// snapshot in internal/provider (refresh it with /model-price-refresh). A
// config.json modelPrices entry replaces that rate for one model. Both
// sources go stale; callers say so when they show a figure.
//
// Cache-write tokens are not a separate input. Providers that pi-go records
// fold writes into the non-cached remainder, and writes bill above the input
// rate, so a freshly filled cache is a slight underestimate.
package cost

import (
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

const tokensPerMillion = 1_000_000

// Call is one model response. Input includes Cached. Output is candidate
// tokens and, on some providers, already includes reasoning. Thoughts is
// reasoning reported separately. Total is the provider's total, used only to
// decide whether Thoughts sit outside Output.
//
// Context is the prompt size used to pick a context-length tier. Zero means
// "use Input", which is correct for a single response. Negative means "do
// not apply a tier": the daily counter is a sum of many prompts, and a tier
// threshold is a property of one prompt, not of the sum.
type Call struct {
	Provider string
	Model    string
	Input    int64
	Output   int64
	Cached   int64
	Thoughts int64
	Total    int64
	Context  int64
}

// Result is one response priced, or left unpriced when no rate card exists.
type Result struct {
	Provider  string
	Model     string
	Fresh     int64
	Cached    int64
	Output    int64 // billed output, including reasoning that was not already in the candidate count
	Reasoning int64
	USD       float64
	Priced    bool
	Explicit  bool // rate came from config.json, not the catalog
	Tier      bool // a context-length tier replaced the base rate
	// CacheAtInput means the rate card has no cache_read price, so cached
	// tokens were billed at the input rate.
	CacheAtInput bool
}

// Summary is the session rollup. USD omits unpriced responses.
type Summary struct {
	USD      float64
	Requests int
	Unpriced int
}

// Group is one provider/model line in a /cost report.
type Group struct {
	Provider     string
	Model        string
	Requests     int
	Fresh        int64
	Cached       int64
	Output       int64
	Reasoning    int64
	USD          float64
	Priced       bool
	Explicit     bool
	Tier         bool
	CacheAtInput bool
}

// BilledOutput returns the output tokens to bill and how many of them are
// reasoning tokens that were not already inside the candidate count.
//
// OpenAI reports reasoning inside Output and a Total of Input+Output, so
// Thoughts must not be added again. Gemini reports Thoughts on top of Output
// and a Total that includes them. The gap between Total and Input+Output is
// that extra, capped at Thoughts so a total that counts something else does
// not invent tokens.
func BilledOutput(input, output, thoughts, total int64) (billed, extra int64) {
	if output < 0 {
		output = 0
	}
	if thoughts <= 0 || total <= 0 {
		return output, 0
	}
	if input < 0 {
		input = 0
	}
	gap := total - input - output
	if gap <= 0 {
		return output, 0
	}
	if gap > thoughts {
		gap = thoughts
	}
	return output + gap, gap
}

// SplitInput separates a prompt into fresh and cached tokens. Cached is a
// subset of Input; providers report the prompt count inclusive of cache reads.
func SplitInput(input, cached int64) (fresh, cache int64) {
	if input < 0 {
		input = 0
	}
	if cached < 0 {
		cached = 0
	}
	if cached > input {
		cached = input
	}
	return input - cached, cached
}

// EffectiveRates returns the rate card for a prompt of contextTokens.
// A tier applies when the prompt is strictly over its threshold. The highest
// such threshold wins. A zero tier field keeps the base rate.
func EffectiveRates(p provider.PricingModel, contextTokens int64) (provider.PricingModel, bool) {
	if contextTokens <= 0 || len(p.Tiers) == 0 {
		return p, false
	}
	best := -1
	for i, t := range p.Tiers {
		if t.ContextOver <= 0 || contextTokens <= t.ContextOver {
			continue
		}
		if best < 0 || t.ContextOver > p.Tiers[best].ContextOver {
			best = i
		}
	}
	if best < 0 {
		return p, false
	}
	t := p.Tiers[best]
	if t.Input != 0 {
		p.Input = t.Input
	}
	if t.Output != 0 {
		p.Output = t.Output
	}
	if t.CacheRead != 0 {
		p.CacheRead = t.CacheRead
	}
	if t.CacheWrite != 0 {
		p.CacheWrite = t.CacheWrite
	}
	return p, true
}

// PriceCall prices one response. found is false when neither the catalog nor
// an override knows the model. explicit marks a config.json override, which
// is applied even when every rate is zero ($0) and which does not pick up
// catalog tiers.
func PriceCall(c Call, price provider.PricingModel, found, explicit bool) Result {
	fresh, cached := SplitInput(c.Input, c.Cached)
	billed, extra := BilledOutput(c.Input, c.Output, c.Thoughts, c.Total)
	res := Result{
		Provider:  c.Provider,
		Model:     c.Model,
		Fresh:     fresh,
		Cached:    cached,
		Output:    billed,
		Reasoning: extra,
	}
	if !found || (!explicit && !hasPrice(price)) {
		return res
	}
	rates := price
	if explicit {
		res.Explicit = true
	} else {
		var tier bool
		rates, tier = EffectiveRates(price, tierContext(c))
		res.Tier = tier
	}
	cacheRate := rates.CacheRead
	if cached > 0 && cacheRate == 0 {
		cacheRate = rates.Input
		res.CacheAtInput = rates.Input > 0
	}
	inUSD := dollars(fresh, rates.Input)
	cacheUSD := dollars(cached, cacheRate)
	outUSD := dollars(billed, rates.Output)
	res.USD = inUSD + cacheUSD + outUSD
	res.Priced = true
	return res
}

// PriceAll prices each call with quote, which reports the rate card, whether
// a card was found, and whether it is a config override.
func PriceAll(calls []Call, quote func(providerName, modelName string) (provider.PricingModel, bool, bool)) []Result {
	out := make([]Result, 0, len(calls))
	for _, c := range calls {
		price, found, explicit := quote(c.Provider, c.Model)
		out = append(out, PriceCall(c, price, found, explicit))
	}
	return out
}

// Summarize totals priced responses. Unpriced ones stay visible to the caller
// via Results but do not add to USD.
func Summarize(results []Result) Summary {
	var s Summary
	s.Requests = len(results)
	for _, r := range results {
		if !r.Priced {
			s.Unpriced++
			continue
		}
		s.USD += r.USD
	}
	return s
}

// ByModel collapses responses that share a provider, model, and priced/explicit
// flag into one line. Order is provider, then model, priced lines first.
func ByModel(results []Result) []Group {
	type key struct {
		provider, model  string
		priced, explicit bool
	}
	order := make([]key, 0)
	by := map[key]*Group{}
	for _, r := range results {
		k := key{r.Provider, r.Model, r.Priced, r.Explicit}
		g, ok := by[k]
		if !ok {
			g = &Group{
				Provider: r.Provider,
				Model:    r.Model,
				Priced:   r.Priced,
				Explicit: r.Explicit,
			}
			by[k] = g
			order = append(order, k)
		}
		g.Requests++
		g.Fresh += r.Fresh
		g.Cached += r.Cached
		g.Output += r.Output
		g.Reasoning += r.Reasoning
		g.USD += r.USD
		g.Tier = g.Tier || r.Tier
		g.CacheAtInput = g.CacheAtInput || r.CacheAtInput
	}
	groups := make([]Group, len(order))
	for i, k := range order {
		groups[i] = *by[k]
	}
	// Insertion order is call order. Sort so the report does not depend on
	// which model happened to run first.
	for i := 1; i < len(groups); i++ {
		j := i
		for j > 0 && groupLess(groups[j], groups[j-1]) {
			groups[j], groups[j-1] = groups[j-1], groups[j]
			j--
		}
	}
	return groups
}

func groupLess(a, b Group) bool {
	if a.Provider != b.Provider {
		return a.Provider < b.Provider
	}
	if a.Model != b.Model {
		return a.Model < b.Model
	}
	if a.Priced != b.Priced {
		return a.Priced
	}
	return a.Explicit && !b.Explicit
}

// Lookup resolves a per-million-token rate card. An override wins, then an
// Ollama Cloud tag, then the models.dev snapshot. explicit is true only for
// an override. found is false when nothing matches.
func Lookup(providerName, modelName string, overrides map[string]provider.PricingModel) (provider.PricingModel, bool, bool) {
	if p, ok := overrideLookup(providerName, modelName, overrides); ok {
		return p, true, true
	}
	if strings.EqualFold(strings.TrimSpace(providerName), "ollama") && provider.IsOllamaCloudModel(modelName) {
		if p, ok := provider.OllamaCloudCost(ollamaCloudPriceKey(modelName)); ok {
			return p, true, false
		}
	}
	p, ok := provider.CostFor(providerName, modelName)
	return p, ok, false
}

// RatesFromConfig converts modelPrices entries into rate cards keyed
// "provider/model", lowercased. The map key's presence is the override;
// all-zero rates mean the model is free.
func RatesFromConfig(prices map[string]config.ModelPrice) map[string]provider.PricingModel {
	if len(prices) == 0 {
		return nil
	}
	out := make(map[string]provider.PricingModel, len(prices))
	for k, p := range prices {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			continue
		}
		out[k] = provider.PricingModel{
			Input:     p.Input,
			Output:    p.Output,
			CacheRead: p.CacheRead,
		}
	}
	return out
}

// FormatUSD renders an estimate. Amounts under $100 keep enough decimals to
// show a fraction of a cent; larger amounts use cents. Trailing zeros stop
// at two decimal places, so a round dollar is "$4.00".
func FormatUSD(v float64) string {
	if v <= 0 {
		return "$0.00"
	}
	prec := 4
	if v < 0.0001 {
		prec = 6
	} else if v >= 100 {
		prec = 2
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		for len(s) > i+3 && strings.HasSuffix(s, "0") {
			s = s[:len(s)-1]
		}
	}
	return "$" + s
}

func overrideLookup(providerName, modelName string, overrides map[string]provider.PricingModel) (provider.PricingModel, bool) {
	if len(overrides) == 0 {
		return provider.PricingModel{}, false
	}
	prov := strings.ToLower(strings.TrimSpace(providerName))
	model := strings.ToLower(strings.TrimSpace(modelName))
	if prov == "" || model == "" {
		return provider.PricingModel{}, false
	}
	if p, ok := overrides[prov+"/"+model]; ok {
		return p, true
	}
	prefix := prov + "/"
	best := ""
	var got provider.PricingModel
	for k, p := range overrides {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		id := strings.TrimPrefix(k, prefix)
		if id == "" || !strings.HasPrefix(model, id) || len(id) <= len(best) {
			continue
		}
		best = id
		got = p
	}
	if best == "" {
		return provider.PricingModel{}, false
	}
	return got, true
}

// ollamaCloudPriceKey strips the cloud tag so the name matches the base IDs
// in the Ollama Cloud snapshot. A dated cloud ID keeps the date so the
// prefix lookup still resolves the right base entry.
func ollamaCloudPriceKey(modelID string) string {
	if i := strings.LastIndex(modelID, "-cloud"); i >= 0 {
		return modelID[:i]
	}
	return strings.TrimSuffix(modelID, ":cloud")
}

func hasPrice(p provider.PricingModel) bool {
	return p.Input != 0 || p.Output != 0 || p.CacheRead != 0 || p.CacheWrite != 0 || len(p.Tiers) > 0
}

func dollars(tokens int64, perMillion float64) float64 {
	if tokens <= 0 || perMillion <= 0 {
		return 0
	}
	return float64(tokens) * perMillion / tokensPerMillion
}

// tierContext is the prompt size a tier is chosen against. A negative
// Context skips tiers. Zero falls back to this response's input count.
func tierContext(c Call) int64 {
	if c.Context < 0 {
		return 0
	}
	if c.Context > 0 {
		return c.Context
	}
	if c.Input < 0 {
		return 0
	}
	return c.Input
}
