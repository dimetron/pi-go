package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	llmmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/config"
)

// modelPickerModel builds a model with the model picker open and a switcher
// that records the model it was asked for.
func modelPickerModel(t *testing.T, roles map[string]config.RoleConfig, active string) (*model, *[]string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var asked []string
	m := &model{
		ctx:        ctx,
		cancel:     cancel,
		inputModel: NewInputModel(nil, nil, nil, ""),
		chatModel:  ChatModel{Messages: make([]message, 0)},
		width:      100,
		height:     30,
		cfg: Config{
			ModelName:  "current-model",
			ActiveRole: active,
			Roles:      roles,
			ModelSwitcher: func(_ context.Context, name string) (llmmodel.LLM, string, string, error) {
				asked = append(asked, name)
				return &stubLLM{name: name}, name, "test-provider", nil
			},
		},
	}
	m.newSearchPopup(searchModeModels)
	return m, &asked
}

func TestModelPickerListsRoles(t *testing.T) {
	m, _ := modelPickerModel(t, map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6"},
		"smol":    {Model: "gemini-2.5-flash"},
		"slow":    {Model: "claude-opus-4-7", Provider: "anthropic"},
	}, "default")

	if m.searchPopup == nil {
		t.Fatal("model picker did not open")
	}
	if got, want := len(m.searchPopup.entries), 3; got != want {
		t.Fatalf("picker has %d rows, want %d: %+v", got, want, m.searchPopup.entries)
	}
	// Sorted by role name, so the order is deterministic rather than Go map order.
	want := []string{"default", "slow", "smol"}
	for i, item := range m.searchPopup.entries {
		if item.Text != want[i] {
			t.Errorf("row %d = %q, want %q", i, item.Text, want[i])
		}
		if item.Description == "" {
			t.Errorf("row %q has no description naming its model", item.Text)
		}
	}
}

func TestModelPickerSkipsRoleWithNoModel(t *testing.T) {
	// A role with no model is a dead end: switchModelTarget would only report
	// "role has no model configured". Offering it invites a failed selection.
	m, _ := modelPickerModel(t, map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6"},
		"empty":   {},
	}, "default")

	for _, item := range m.searchPopup.entries {
		if item.Text == "empty" {
			t.Error("picker offered a role with no model configured")
		}
	}
}

func TestModelPickerMarksActiveRole(t *testing.T) {
	m, _ := modelPickerModel(t, map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6"},
		"smol":    {Model: "gemini-2.5-flash"},
	}, "smol")

	var marked []string
	for _, item := range m.searchPopup.entries {
		if containsFold(item.Description, "active") {
			marked = append(marked, item.Text)
		}
	}
	if len(marked) != 1 || marked[0] != "smol" {
		t.Errorf("active roles marked = %v, want [smol]", marked)
	}
}

func TestModelPickerEnterSwitchesThroughTheSamePath(t *testing.T) {
	m, asked := modelPickerModel(t, map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6"},
		"smol":    {Model: "gemini-2.5-flash"},
	}, "default")

	// Select the smol role and accept it.
	for i, item := range m.searchPopup.filtered {
		if item.Text == "smol" {
			m.searchPopup.selected = i
			break
		}
	}
	if !m.handleSearchPopupKey(tea.Key{Code: tea.KeyEnter}) {
		t.Fatal("Enter was not handled by the model picker")
	}

	if len(*asked) != 1 || (*asked)[0] != "gemini-2.5-flash" {
		t.Fatalf("switcher asked for %v, want [gemini-2.5-flash]", *asked)
	}
	if m.searchPopup != nil {
		t.Error("Enter left the picker open")
	}
	if got := m.cfg.ModelName; got != "gemini-2.5-flash" {
		t.Errorf("cfg.ModelName = %q, want the switched model", got)
	}
	if got := m.cfg.ActiveRole; got != "smol" {
		t.Errorf("cfg.ActiveRole = %q, want smol", got)
	}
}

func TestModelPickerSecondCommandCloses(t *testing.T) {
	m, _ := modelPickerModel(t, map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6"},
	}, "default")
	if m.searchPopup == nil {
		t.Fatal("model picker did not open")
	}
	_, _ = m.handleModelCommand(nil)
	if m.searchPopup != nil {
		t.Error("second bare /model left the picker open")
	}
}

func TestModelPickerFallsBackToTextWithNoRoles(t *testing.T) {
	// Nothing to pick from is not an empty popup; it is the informational text
	// the command has always shown.
	m := &model{
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{ModelName: "solo-model"},
	}
	newM, _ := m.handleModelCommand(nil)
	mm := newM.(*model)

	if mm.searchPopup != nil {
		t.Error("opened a picker with no roles to offer")
	}
	if len(mm.chatModel.Messages) != 1 {
		t.Fatalf("got %d messages, want the model info fallback", len(mm.chatModel.Messages))
	}
	if !containsFold(mm.chatModel.Messages[0].content, "solo-model") {
		t.Errorf("fallback named no model: %q", mm.chatModel.Messages[0].content)
	}
}

func TestModelPickerFilterNarrows(t *testing.T) {
	m, _ := modelPickerModel(t, map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6"},
		"smol":    {Model: "gemini-2.5-flash"},
		"slow":    {Model: "claude-opus-4-7"},
	}, "default")

	m.searchPopup.search = "smo"
	m.searchPopup.filterSearch()
	if len(m.searchPopup.filtered) != 1 || m.searchPopup.filtered[0].Text != "smol" {
		t.Errorf("filter for \"smo\" gave %+v, want just smol", m.searchPopup.filtered)
	}

	m.searchPopup.search = "zzzz"
	m.searchPopup.filterSearch()
	if len(m.searchPopup.filtered) != 0 {
		t.Errorf("no-match filter returned %d rows, want 0", len(m.searchPopup.filtered))
	}
}
