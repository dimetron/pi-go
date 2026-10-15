package tui

import (
	"fmt"
	"strings"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/cost"
	"github.com/dimetron/pi-go/internal/guardrail"
	"github.com/dimetron/pi-go/internal/provider"
	pisession "github.com/dimetron/pi-go/internal/session"
)

// dailyUsageSource is the optional input/output split on the token tracker.
// *guardrail.Tracker implements it. The TokenTracker interface itself only
// exposes a combined daily total, which is not enough to price.
type dailyUsageSource interface {
	Current() guardrail.Usage
}

// showCostMessage appends the session's estimated USD spend.
func (m *model) showCostMessage() {
	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "assistant",
		content: m.formatCost(),
	})
}

// formatCost prices the session ledger and, when the tracker has it, today's
// combined token counter. Prices come from the models.dev snapshot unless
// config.json modelPrices replaces a model.
func (m *model) formatCost() string {
	overrides, configNote := m.priceOverrides()
	calls, sessionNote := m.sessionCalls()
	for i := range calls {
		if calls[i].Provider == "" {
			calls[i].Provider = m.cfg.ProviderName
		}
		if calls[i].Model == "" || calls[i].Model == pisession.UnknownModel {
			if m.cfg.ModelName != "" {
				calls[i].Model = m.cfg.ModelName
			}
		}
	}
	results := cost.PriceAll(toCostCalls(calls), func(providerName, modelName string) (provider.PricingModel, bool, bool) {
		return cost.Lookup(providerName, modelName, overrides)
	})
	return formatCostMessage(cost.ByModel(results), cost.Summarize(results), m.dailyCost(overrides), sessionNote, configNote, provider.PricingFetchedAt())
}

func (m *model) sessionCalls() ([]pisession.SpendCall, string) {
	if m.cfg.SessionService == nil {
		return nil, "Session token history is not available."
	}
	if m.cfg.SessionID == "" {
		return nil, "No session is active."
	}
	calls, err := m.cfg.SessionService.SessionSpend(m.cfg.SessionID)
	if err != nil {
		return nil, fmt.Sprintf("Could not read session token history: %v", err)
	}
	return calls, ""
}

func (m *model) priceOverrides() (map[string]provider.PricingModel, string) {
	cwd := m.cfg.WorkDir
	if cwd == "" {
		cwd = "."
	}
	cfg, err := config.LoadFrom(cwd)
	overrides := cost.RatesFromConfig(cfg.ModelPrices)
	if err != nil {
		return overrides, "Could not read config.json; catalog prices were used where the file did not load."
	}
	return overrides, ""
}

func (m *model) dailyCost(overrides map[string]provider.PricingModel) *cost.Result {
	src, ok := m.cfg.TokenTracker.(dailyUsageSource)
	if !ok {
		return nil
	}
	u := src.Current()
	if u.InputTokens == 0 && u.OutputTokens == 0 && u.CachedInputTokens == 0 {
		return nil
	}
	price, found, explicit := cost.Lookup(m.cfg.ProviderName, m.cfg.ModelName, overrides)
	res := cost.PriceCall(cost.Call{
		Provider: m.cfg.ProviderName,
		Model:    m.cfg.ModelName,
		Input:    u.InputTokens,
		Output:   u.OutputTokens,
		Cached:   u.CachedInputTokens,
		// The daily counter is a sum. A context tier applies to one prompt,
		// and this total is not one prompt.
		Context: -1,
	}, price, found, explicit)
	return &res
}

func toCostCalls(in []pisession.SpendCall) []cost.Call {
	out := make([]cost.Call, len(in))
	for i, c := range in {
		out[i] = cost.Call{
			Provider: c.Provider,
			Model:    c.Model,
			Input:    c.Input,
			Output:   c.Output,
			Cached:   c.Cached,
			Thoughts: c.Thoughts,
			Total:    c.Total,
		}
	}
	return out
}

// formatCostMessage renders the priced session. sessionNote replaces the
// per-model lines when the ledger could not be read. daily is today's
// counter priced at the active model, or nil.
func formatCostMessage(groups []cost.Group, sum cost.Summary, daily *cost.Result, sessionNote, configNote, fetchedAt string) string {
	var b strings.Builder
	b.WriteString("**Session cost** (estimate)\n\n")
	if sessionNote != "" {
		b.WriteString(sessionNote)
		b.WriteByte('\n')
	} else if len(groups) == 0 {
		b.WriteString("No token usage recorded for this session yet.\n")
	} else {
		for _, g := range groups {
			writeCostGroup(&b, g)
		}
		fmt.Fprintf(&b, "\n**Total**: %s", cost.FormatUSD(sum.USD))
		if sum.Requests > 0 {
			fmt.Fprintf(&b, " · %d responses", sum.Requests)
		}
		b.WriteByte('\n')
		if sum.Unpriced > 0 {
			fmt.Fprintf(&b, "%d response(s) have no catalog price and are omitted from the total.\n", sum.Unpriced)
		}
	}

	if daily != nil {
		b.WriteString("\n*Today*\n")
		label := daily.Provider
		if daily.Model != "" {
			if label != "" {
				label += " / "
			}
			label += daily.Model
		}
		if label == "" {
			label = "the active model"
		}
		fmt.Fprintf(&b, "All sessions on this machine, priced at %s: %s in", label, formatTokenCount(daily.Fresh+daily.Cached))
		if daily.Cached > 0 {
			fmt.Fprintf(&b, " (%s cached)", formatTokenCount(daily.Cached))
		}
		fmt.Fprintf(&b, " · %s out · ", formatTokenCount(daily.Output))
		if daily.Priced {
			b.WriteString(cost.FormatUSD(daily.USD))
		} else {
			b.WriteString("no price")
		}
		b.WriteString("\nThe daily figure uses the active model's rates for every token counted today, so it is only a guide when the model stayed the same.\n")
	}

	if configNote != "" {
		b.WriteByte('\n')
		b.WriteString(configNote)
		b.WriteByte('\n')
	}
	writeCostFooter(&b, groups, fetchedAt)
	return b.String()
}

func writeCostGroup(b *strings.Builder, g cost.Group) {
	name := g.Model
	if g.Provider != "" {
		if name != "" {
			name = g.Provider + " / " + name
		} else {
			name = g.Provider
		}
	}
	if name == "" {
		name = "unknown model"
	}
	fmt.Fprintf(b, "- **%s** — %d", name, g.Requests)
	if g.Requests == 1 {
		b.WriteString(" response")
	} else {
		b.WriteString(" responses")
	}
	in := g.Fresh + g.Cached
	fmt.Fprintf(b, " · %s in", formatTokenCount(in))
	if g.Cached > 0 {
		fmt.Fprintf(b, " (%s cached)", formatTokenCount(g.Cached))
	}
	fmt.Fprintf(b, " · %s out", formatTokenCount(g.Output))
	if g.Reasoning > 0 {
		fmt.Fprintf(b, " (%s reasoning)", formatTokenCount(g.Reasoning))
	}
	b.WriteString(" · ")
	if g.Priced {
		b.WriteString("**" + cost.FormatUSD(g.USD) + "**")
	} else {
		b.WriteString("**no price**")
	}
	if g.Explicit {
		b.WriteString(" · config rate")
	}
	if g.Tier {
		b.WriteString(" · context tier")
	}
	b.WriteByte('\n')
}

func writeCostFooter(b *strings.Builder, groups []cost.Group, fetchedAt string) {
	b.WriteString("\n*Prices are estimates and can be stale.* ")
	if fetchedAt != "" {
		fmt.Fprintf(b, "Rates are USD per million tokens from the models.dev snapshot (as of %s). ", fetchedAt)
	} else {
		b.WriteString("Rates are USD per million tokens from the models.dev snapshot. ")
	}
	b.WriteString("Refresh them with `/model-price-refresh`, or set `modelPrices` in config.json (`\"openai/gpt-5.6-sol\": {\"input\": 4, \"output\": 20, \"cache_read\": 0.4}`). ")
	b.WriteString("Cache writes are not reported separately, so they are billed as fresh input and a new cache costs a little less here than on the invoice. ")
	b.WriteString("A context-length tier, when the catalog has one, follows that response's prompt size. ")
	b.WriteString("Subagent sessions keep their own totals.\n")
	for _, g := range groups {
		if g.CacheAtInput {
			b.WriteString("A model with no cache-read rate had its cached tokens billed at the input price.\n")
			return
		}
	}
}
