package tui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/provider"
)

// providerModelListTimeout bounds one provider's model listing. Listing is a
// single HTTP call, so anything slower is a provider that is not answering.
const providerModelListTimeout = 30 * time.Second

// providerModelsMsg carries the result of an async provider model listing.
type providerModelsMsg struct {
	provider string
	models   []provider.ModelInfo
	err      error
	// withRoles puts the configured roles ahead of the models, for bare
	// /model.
	withRoles bool
}

// takePopupCmd returns and clears the command a popup selection started.
func (m *model) takePopupCmd() tea.Cmd {
	cmd := m.popupCmd
	m.popupCmd = nil
	return cmd
}

// handleModelSwitchCommand handles /model switch [provider]: list the models
// the provider serves and pick one. Without a provider it lists the current
// one, which is the common case of trying a sibling model.
func (m *model) handleModelSwitchCommand(args []string) (tea.Model, tea.Cmd) {
	prov := m.cfg.ProviderName
	if len(args) > 0 {
		prov = strings.ToLower(strings.TrimSpace(args[0]))
	}
	return m, m.startProviderModelList(prov)
}

// handleModelProviderCommand handles /model provider: pick a configured
// provider, then one of its models. A second bare invocation closes the
// picker, like /model.
func (m *model) handleModelProviderCommand() (tea.Model, tea.Cmd) {
	if m.searchPopup != nil && m.searchPopup.mode == searchModeProviders {
		m.searchPopup = nil
		return m, nil
	}
	var provs []string
	if m.cfg.ModelProviders != nil {
		provs = m.cfg.ModelProviders()
	}
	items := providerSearchItems(provs, m.cfg.ProviderName)
	if len(items) == 0 {
		m.appendAssistant("No providers are configured. Set an API key (e.g. `OPENAI_API_KEY`) or a base URL first.")
		return m, nil
	}
	m.openSearchPopupItems(searchModeProviders, items)
	return m, nil
}

// providerSearchItems builds the /model provider rows: the current provider
// first, so Enter on an untouched picker is "stay here", then the rest sorted.
func providerSearchItems(provs []string, current string) []SearchItem {
	sorted := slices.Clone(provs)
	slices.Sort(sorted)
	sorted = slices.Compact(sorted)
	if current != "" && !slices.Contains(sorted, current) {
		sorted = append(sorted, current)
	}
	items := make([]SearchItem, 0, len(sorted))
	for _, p := range sorted {
		item := SearchItem{Text: p}
		if p == current {
			item.Description = "current"
			items = slices.Insert(items, 0, item)
			continue
		}
		items = append(items, item)
	}
	return items
}

// startProviderModelList validates that a listing can run and returns the
// command that runs it. Failures that need no network are reported now.
func (m *model) startProviderModelList(prov string) tea.Cmd {
	return m.startModelList(prov, false)
}

// canListProviderModels reports whether bare /model can fetch the current
// provider's models rather than only listing roles.
func (m *model) canListProviderModels() bool {
	return m.cfg.ProviderName != "" && m.cfg.ModelLister != nil && m.cfg.ProviderModelSwitcher != nil && !m.running
}

// startModelList is startProviderModelList with the roles optionally listed
// ahead of the models.
func (m *model) startModelList(prov string, withRoles bool) tea.Cmd {
	if prov == "" {
		m.appendAssistant("No current provider is known. Name one: `/model switch <provider>`, or pick one with `/model provider`.")
		return nil
	}
	if m.cfg.ModelLister == nil || m.cfg.ProviderModelSwitcher == nil {
		m.appendAssistant("Model switching is not available in this context.")
		return nil
	}
	if m.running {
		m.appendAssistant("Cannot switch model while a response is running. Wait for it to finish or cancel it first.")
		return nil
	}

	m.chatModel.Messages = append(m.chatModel.Messages, message{
		role:    "thinking",
		content: fmt.Sprintf("Fetching models from %s...", prov),
	})
	m.inputModel.Clear()

	ctx, lister := m.ctx, m.cfg.ModelLister
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, providerModelListTimeout)
		defer cancel()
		models, err := lister(ctx, prov)
		return providerModelsMsg{provider: prov, models: models, err: err, withRoles: withRoles}
	}
}

// handleProviderModels opens the model picker over a finished listing, or
// reports why there is nothing to pick.
func (m *model) handleProviderModels(msg providerModelsMsg) (tea.Model, tea.Cmd) {
	// The "Fetching..." placeholder is only a placeholder; drop it so the
	// transcript keeps the outcome, not the wait.
	if n := len(m.chatModel.Messages); n > 0 && m.chatModel.Messages[n-1].role == "thinking" {
		m.chatModel.Messages = m.chatModel.Messages[:n-1]
	}
	var roles []SearchItem
	if msg.withRoles {
		roles = modelSearchItems(m.cfg)
		for i := range roles {
			roles[i].Role = true
			roles[i].Description = "role · " + roles[i].Description
		}
	}
	switch {
	case msg.err != nil:
		m.appendAssistant(fmt.Sprintf("Failed to list %s models: %s", msg.provider, msg.err))
		// Bare /model still offers the roles when the live list fails.
		if len(roles) > 0 {
			m.newSearchPopup(searchModeModels)
		}
		return m, nil
	case len(msg.models) == 0 && len(roles) == 0:
		m.appendAssistant(fmt.Sprintf("Provider %s reported no models.", msg.provider))
		return m, nil
	}
	current := ""
	if msg.provider == m.cfg.ProviderName {
		// The active name can carry the routing prefix the listing does not
		// ("agentgateway/openai/gpt-5" vs "openai/gpt-5").
		current = strings.TrimPrefix(m.cfg.ModelName, msg.provider+"/")
	}
	m.providerModelsFor = msg.provider
	items := append(roles, providerModelSearchItems(msg.provider, msg.models, current)...)
	m.openSearchPopupItems(searchModeProviderModels, items)
	// Open on the model in use so the picker reads as "you are here".
	for i, item := range m.searchPopup.entries {
		if !item.Role && item.Text == current {
			m.searchPopup.selected = i
			break
		}
	}
	return m, nil
}

// providerModelSearchItems builds one row per model. The description carries
// what helps choose between siblings: display name, context window, and
// whether it is the model in use.
func providerModelSearchItems(prov string, models []provider.ModelInfo, current string) []SearchItem {
	items := make([]SearchItem, 0, len(models))
	for _, mi := range models {
		if mi.ID == "" {
			continue
		}
		var parts []string
		if mi.DisplayName != "" && mi.DisplayName != mi.ID {
			parts = append(parts, mi.DisplayName)
		}
		if ctxWin := mi.MaxInputTokens; ctxWin > 0 || mi.ContextWindow > 0 {
			if ctxWin == 0 {
				ctxWin = mi.ContextWindow
			}
			parts = append(parts, formatTokenCount(ctxWin)+" ctx")
		}
		if mi.ID == current {
			parts = append(parts, "active")
		}
		items = append(items, SearchItem{Text: mi.ID, Description: strings.Join(parts, " · "), Provider: prov})
	}
	return items
}

// switchModelOnProvider switches to modelName served by prov. It shares the
// rebuild-and-report tail with switchModelTarget; only the LLM construction
// differs, because the provider is known rather than detected.
func (m *model) switchModelOnProvider(modelName, prov string) {
	if m.cfg.ProviderModelSwitcher == nil {
		m.appendAssistant("Model switching is not available in this context.")
		return
	}
	if m.running {
		m.appendAssistant("Cannot switch model while a response is running. Wait for it to finish or cancel it first.")
		return
	}
	newLLM, newName, newProvider, err := m.cfg.ProviderModelSwitcher(m.ctx, modelName, prov)
	if err != nil {
		m.appendAssistant(fmt.Sprintf("Failed to switch model: %s", err))
		return
	}
	m.applySwitchedModel(newLLM, newName, newProvider, "")
}
