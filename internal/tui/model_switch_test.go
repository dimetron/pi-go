package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	llmmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/provider"
)

type switchCall struct{ model, provider string }

// modelSwitchModel builds a model whose lister serves listed[provider] and
// whose provider switcher records what it was asked for.
func modelSwitchModel(t *testing.T, current, currentProvider string, listed map[string][]provider.ModelInfo) (*model, *[]switchCall, *[]string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var calls []switchCall
	var listedFor []string
	m := &model{
		ctx:        ctx,
		cancel:     cancel,
		inputModel: NewInputModel(nil, nil, nil, ""),
		chatModel:  ChatModel{Messages: make([]message, 0)},
		width:      100,
		height:     30,
		cfg: Config{
			ModelName:    current,
			ProviderName: currentProvider,
			ModelLister: func(_ context.Context, p string) ([]provider.ModelInfo, error) {
				listedFor = append(listedFor, p)
				models, ok := listed[p]
				if !ok {
					return nil, errors.New("connection refused")
				}
				return models, nil
			},
			ProviderModelSwitcher: func(_ context.Context, name, p string) (llmmodel.LLM, string, string, error) {
				calls = append(calls, switchCall{name, p})
				return &stubLLM{name: name}, p + "/" + name, p, nil
			},
			ModelProviders: func() []string { return []string{"openai", "agentgateway", "ollama"} },
		},
	}
	return m, &calls, &listedFor
}

// runCmd runs a command and feeds its message back, the way the program loop does.
func runCmd(t *testing.T, m *model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	if _, _, ok := m.updateSession(cmd()); !ok {
		t.Fatal("message was not handled")
	}
}

func lastContent(m *model) string {
	return m.chatModel.Messages[len(m.chatModel.Messages)-1].content
}

func TestModelSwitchListsCurrentProviderAndSwitchesThere(t *testing.T) {
	m, calls, listedFor := modelSwitchModel(t, "agentgateway/openai/gpt-5", "agentgateway", map[string][]provider.ModelInfo{
		"agentgateway": {
			{ID: "anthropic/claude-opus-4-7", MaxInputTokens: 200000},
			{ID: "openai/gpt-5", DisplayName: "GPT-5"},
		},
	})

	_, cmd := m.handleModelCommand([]string{"switch"})
	if got := lastContent(m); !strings.Contains(got, "Fetching models from agentgateway") {
		t.Fatalf("placeholder = %q", got)
	}
	runCmd(t, m, cmd)

	if (*listedFor)[0] != "agentgateway" {
		t.Fatalf("listed %v, want the current provider", *listedFor)
	}
	sp := m.searchPopup
	if sp == nil || sp.mode != searchModeProviderModels {
		t.Fatalf("picker not open: %+v", sp)
	}
	if len(m.chatModel.Messages) != 0 {
		t.Errorf("placeholder left in transcript: %+v", m.chatModel.Messages)
	}
	// Opens on the active model even though the active name carries the
	// routing prefix and the listed ID does not.
	if got := sp.filtered[sp.selected]; got.Text != "openai/gpt-5" || !strings.Contains(got.Description, "active") {
		t.Errorf("selected %+v, want the active model", got)
	}
	if d := sp.entries[0].Description; !strings.Contains(d, "200.0k ctx") {
		t.Errorf("context window missing from %q", d)
	}

	sp.selected = 0
	m.acceptSearchPopupSelection()
	if len(*calls) != 1 || (*calls)[0] != (switchCall{"anthropic/claude-opus-4-7", "agentgateway"}) {
		t.Fatalf("switcher calls = %+v", *calls)
	}
	if m.cfg.ProviderName != "agentgateway" || m.cfg.ModelName != "agentgateway/anthropic/claude-opus-4-7" {
		t.Errorf("state = %s / %s", m.cfg.ProviderName, m.cfg.ModelName)
	}
	if !strings.Contains(lastContent(m), "Switched model") {
		t.Errorf("no switch report: %q", lastContent(m))
	}
}

func TestModelSwitchNamedProvider(t *testing.T) {
	m, calls, listedFor := modelSwitchModel(t, "gpt-5", "openai", map[string][]provider.ModelInfo{
		"ollama": {{ID: "llama3"}},
	})
	_, cmd := m.handleModelCommand([]string{"switch", "Ollama"})
	runCmd(t, m, cmd)
	if (*listedFor)[0] != "ollama" {
		t.Fatalf("listed %v", *listedFor)
	}
	m.acceptSearchPopupSelection()
	if (*calls)[0] != (switchCall{"llama3", "ollama"}) {
		t.Fatalf("calls = %+v", *calls)
	}
}

func TestModelProviderPickerChainsIntoModelList(t *testing.T) {
	m, calls, _ := modelSwitchModel(t, "gpt-5", "openai", map[string][]provider.ModelInfo{
		"agentgateway": {{ID: "openai/gpt-5"}},
	})
	m.handleModelCommand([]string{"provider"})
	sp := m.searchPopup
	if sp == nil || sp.mode != searchModeProviders {
		t.Fatalf("provider picker not open: %+v", sp)
	}
	if sp.entries[0].Text != "openai" || sp.entries[0].Description != "current" {
		t.Errorf("current provider not first: %+v", sp.entries)
	}
	for i, e := range sp.entries {
		if e.Text == "agentgateway" {
			sp.selected = i
		}
	}
	// Enter goes through the key path, which has to return the fetch command.
	_, cmd := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	runCmd(t, m, cmd)
	if m.searchPopup == nil || m.searchPopup.mode != searchModeProviderModels {
		t.Fatalf("model picker not open after provider pick")
	}
	m.acceptSearchPopupSelection()
	if (*calls)[0] != (switchCall{"openai/gpt-5", "agentgateway"}) {
		t.Fatalf("calls = %+v", *calls)
	}

	// A second bare /model provider closes the picker.
	m.handleModelCommand([]string{"provider"})
	m.handleModelCommand([]string{"provider"})
	if m.searchPopup != nil {
		t.Error("second /model provider did not close the picker")
	}
}

func TestModelSwitchReportsFailures(t *testing.T) {
	t.Run("list error", func(t *testing.T) {
		m, _, _ := modelSwitchModel(t, "gpt-5", "openai", nil)
		_, cmd := m.handleModelCommand([]string{"switch"})
		runCmd(t, m, cmd)
		if m.searchPopup != nil || !strings.Contains(lastContent(m), "Failed to list openai models: connection refused") {
			t.Errorf("popup=%v msg=%q", m.searchPopup, lastContent(m))
		}
		if len(m.chatModel.Messages) != 1 {
			t.Errorf("placeholder not replaced: %+v", m.chatModel.Messages)
		}
	})
	t.Run("empty list", func(t *testing.T) {
		m, _, _ := modelSwitchModel(t, "gpt-5", "openai", map[string][]provider.ModelInfo{"openai": {}})
		_, cmd := m.handleModelCommand([]string{"switch"})
		runCmd(t, m, cmd)
		if !strings.Contains(lastContent(m), "reported no models") {
			t.Errorf("msg = %q", lastContent(m))
		}
	})
	t.Run("no current provider", func(t *testing.T) {
		m, _, _ := modelSwitchModel(t, "x", "", nil)
		if _, cmd := m.handleModelCommand([]string{"switch"}); cmd != nil {
			t.Error("fetch started without a provider")
		}
		if !strings.Contains(lastContent(m), "/model provider") {
			t.Errorf("msg = %q", lastContent(m))
		}
	})
	t.Run("running", func(t *testing.T) {
		m, _, _ := modelSwitchModel(t, "gpt-5", "openai", nil)
		m.running = true
		if _, cmd := m.handleModelCommand([]string{"switch"}); cmd != nil {
			t.Error("fetch started while running")
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		m, _, _ := modelSwitchModel(t, "gpt-5", "openai", nil)
		m.cfg.ModelLister = nil
		if _, cmd := m.handleModelCommand([]string{"switch"}); cmd != nil {
			t.Error("fetch started without a lister")
		}
		if !strings.Contains(lastContent(m), "not available") {
			t.Errorf("msg = %q", lastContent(m))
		}
	})
	t.Run("switch error", func(t *testing.T) {
		m, _, _ := modelSwitchModel(t, "gpt-5", "openai", nil)
		m.cfg.ProviderModelSwitcher = func(context.Context, string, string) (llmmodel.LLM, string, string, error) {
			return nil, "", "", errors.New("bad key")
		}
		m.switchModelOnProvider("gpt-4o", "openai")
		if !strings.Contains(lastContent(m), "Failed to switch model: bad key") || m.cfg.ModelName != "gpt-5" {
			t.Errorf("msg=%q model=%q", lastContent(m), m.cfg.ModelName)
		}
	})
	t.Run("no providers", func(t *testing.T) {
		m, _, _ := modelSwitchModel(t, "gpt-5", "", nil)
		m.cfg.ModelProviders = nil
		m.handleModelCommand([]string{"provider"})
		if m.searchPopup != nil || !strings.Contains(lastContent(m), "No providers") {
			t.Errorf("popup=%v msg=%q", m.searchPopup, lastContent(m))
		}
	})
}

// Bare /model with one role used to be a one-row picker; it now lists the
// roles first and then the provider's live models.
func TestBareModelListsRolesThenLiveModels(t *testing.T) {
	m, calls, _ := modelSwitchModel(t, "agentgateway/ollama-deepseek", "agentgateway", map[string][]provider.ModelInfo{
		"agentgateway": {{ID: "ollama-deepseek"}, {ID: "openai/gpt-5"}, {ID: "anthropic/claude-opus-4-7"}},
	})
	m.cfg.Roles = map[string]config.RoleConfig{"default": {Model: "agentgateway/ollama-deepseek", Provider: "agentgateway"}}
	m.cfg.ActiveRole = "default"

	_, cmd := m.handleModelCommand(nil)
	runCmd(t, m, cmd)
	sp := m.searchPopup
	if sp == nil || sp.mode != searchModeProviderModels {
		t.Fatalf("picker not open: %+v", sp)
	}
	if got := len(sp.entries); got != 4 {
		t.Fatalf("rows = %d, want 1 role + 3 models: %+v", got, sp.entries)
	}
	if !sp.entries[0].Role || sp.entries[0].Text != "default" || !strings.HasPrefix(sp.entries[0].Description, "role · ") {
		t.Errorf("first row = %+v, want the default role", sp.entries[0])
	}
	if got := sp.filtered[sp.selected]; got.Role || got.Text != "ollama-deepseek" {
		t.Errorf("selected %+v, want the active model row", got)
	}

	// Typing filters the live list.
	for _, r := range "opus" {
		m.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(sp.filtered) != 1 || sp.filtered[0].Text != "anthropic/claude-opus-4-7" {
		t.Fatalf("filtered = %+v", sp.filtered)
	}
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(*calls) != 1 || (*calls)[0] != (switchCall{"anthropic/claude-opus-4-7", "agentgateway"}) {
		t.Fatalf("calls = %+v", *calls)
	}

	// A second bare /model closes the combined picker.
	_, cmd = m.handleModelCommand(nil)
	runCmd(t, m, cmd)
	m.handleModelCommand(nil)
	if m.searchPopup != nil {
		t.Error("second bare /model did not close the picker")
	}
}

func TestBareModelRoleRowSwitchesRole(t *testing.T) {
	m, calls, _ := modelSwitchModel(t, "gpt-5", "openai", map[string][]provider.ModelInfo{"openai": {{ID: "gpt-5"}}})
	m.cfg.Roles = map[string]config.RoleConfig{"smol": {Model: "gpt-5-mini"}}
	var roleSwitch []string
	m.cfg.ModelSwitcher = func(_ context.Context, name string) (llmmodel.LLM, string, string, error) {
		roleSwitch = append(roleSwitch, name)
		return &stubLLM{name: name}, name, "openai", nil
	}
	_, cmd := m.handleModelCommand(nil)
	runCmd(t, m, cmd)
	m.searchPopup.selected = 0
	m.acceptSearchPopupSelection()
	if len(roleSwitch) != 1 || roleSwitch[0] != "gpt-5-mini" || len(*calls) != 0 || m.cfg.ActiveRole != "smol" {
		t.Fatalf("role=%v provider=%v active=%q", roleSwitch, *calls, m.cfg.ActiveRole)
	}
}

// When the live list fails, bare /model still offers the roles.
func TestBareModelFallsBackToRolesOnListError(t *testing.T) {
	m, _, _ := modelSwitchModel(t, "gpt-5", "openai", nil)
	m.cfg.Roles = map[string]config.RoleConfig{"default": {Model: "gpt-5"}}
	_, cmd := m.handleModelCommand(nil)
	runCmd(t, m, cmd)
	if m.searchPopup == nil || m.searchPopup.mode != searchModeModels {
		t.Fatalf("roles picker not open: %+v", m.searchPopup)
	}
	if !strings.Contains(lastContent(m), "Failed to list openai models") {
		t.Errorf("msg = %q", lastContent(m))
	}
}

func TestProviderSearchItemsAddsUnlistedCurrentFirst(t *testing.T) {
	items := providerSearchItems([]string{"openai", "ollama", "openai"}, "fm")
	got := make([]string, len(items))
	for i, it := range items {
		got[i] = it.Text
	}
	if strings.Join(got, ",") != "fm,ollama,openai" || items[0].Description != "current" {
		t.Fatalf("rows = %v (%+v)", got, items[0])
	}
}

func TestProviderModelSearchItemsDescriptions(t *testing.T) {
	items := providerModelSearchItems("p", []provider.ModelInfo{
		{ID: ""},
		{ID: "a", DisplayName: "a"},
		{ID: "b", ContextWindow: 8000},
		{ID: "c", MaxInputTokens: 4000, ContextWindow: 8000, DisplayName: "C"},
	}, "b")
	if len(items) != 3 {
		t.Fatalf("empty ID not skipped: %+v", items)
	}
	want := []string{"", "8.0k ctx · active", "C · 4.0k ctx"}
	for i, it := range items {
		if it.Description != want[i] || it.Provider != "p" {
			t.Errorf("row %d = %+v, want description %q", i, it, want[i])
		}
	}
}

func TestSwitchModelOnProviderGuards(t *testing.T) {
	m, calls, _ := modelSwitchModel(t, "gpt-5", "openai", nil)
	m.running = true
	m.switchModelOnProvider("gpt-4o", "openai")
	if len(*calls) != 0 || !strings.Contains(lastContent(m), "while a response is running") {
		t.Errorf("running: calls=%v msg=%q", *calls, lastContent(m))
	}
	m.running = false
	m.cfg.ProviderModelSwitcher = nil
	m.switchModelOnProvider("gpt-4o", "openai")
	if !strings.Contains(lastContent(m), "not available") {
		t.Errorf("nil switcher: msg=%q", lastContent(m))
	}
}

func TestProviderPopupsRender(t *testing.T) {
	m, _, _ := modelSwitchModel(t, "gpt-5", "openai", map[string][]provider.ModelInfo{"openai": {{ID: "gpt-5"}}})
	m.handleModelCommand([]string{"provider"})
	if out := m.renderSearchPopup(60); !strings.Contains(out, "Providers") || !strings.Contains(out, "openai") {
		t.Errorf("providers popup:\n%s", out)
	}
	m.searchPopup.search = "zzz"
	m.searchPopup.filterSearch()
	if out := m.renderSearchPopup(60); !strings.Contains(out, "No matching providers") {
		t.Errorf("providers no-match:\n%s", out)
	}

	m.searchPopup = nil
	_, cmd := m.handleModelCommand([]string{"switch"})
	runCmd(t, m, cmd)
	if out := m.renderSearchPopup(60); !strings.Contains(out, "Models · openai") || !strings.Contains(out, "gpt-5") {
		t.Errorf("models popup:\n%s", out)
	}
	m.searchPopup.search = "zzz"
	m.searchPopup.filterSearch()
	if out := m.renderSearchPopup(60); !strings.Contains(out, "No matching models") {
		t.Errorf("models no-match:\n%s", out)
	}
}
