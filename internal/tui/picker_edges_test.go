package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	llmmodel "google.golang.org/adk/v2/model"

	"github.com/dimetron/pi-go/internal/config"
)

// These cover branches the first round of picker tests left dark. They are all
// error or degenerate paths, which is exactly where untested code hides: the
// happy path is exercised by using the feature, so only the refusals need a test.

func TestModelPicker_SwitchWithNoSwitcher(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{Roles: map[string]config.RoleConfig{"default": {Model: "x"}}},
	}
	// ModelSwitcher is nil: /model must say so rather than panic or do nothing.
	m.switchModelTarget("x")
	assertMessageContains(t, m, "not available in this context")
	if m.cfg.ModelName != "" {
		t.Errorf("ModelName changed to %q on a failed switch", m.cfg.ModelName)
	}
}

func TestModelPicker_SwitchWhileRunning(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		running:   true,
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			Roles: map[string]config.RoleConfig{"default": {Model: "x"}},
			ModelSwitcher: func(context.Context, string) (llmmodel.LLM, string, string, error) {
				t.Error("switcher was called while a turn was running")
				return nil, "", "", nil
			},
		},
	}
	m.switchModelTarget("x")
	assertMessageContains(t, m, "Cannot switch model while a response is running")
}

func TestModelPicker_SwitchRoleWithNoModel(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg:       Config{Roles: map[string]config.RoleConfig{"empty": {}}},
	}
	m.switchModelTarget("empty")
	assertMessageContains(t, m, "has no model configured")
}

func TestModelPicker_SwitchFailureReported(t *testing.T) {
	m := &model{
		ctx:       context.Background(),
		chatModel: ChatModel{Messages: make([]message, 0)},
		cfg: Config{
			ModelSwitcher: func(context.Context, string) (llmmodel.LLM, string, string, error) {
				return nil, "", "", errors.New("provider rejected the model")
			},
		},
	}
	m.switchModelTarget("bad-model")
	assertMessageContains(t, m, "Failed to switch model")
	// A failed switch must not leave the old name pointing at a dead LLM.
	if m.cfg.ModelName != "" {
		t.Errorf("ModelName = %q after a failed switch, want unchanged", m.cfg.ModelName)
	}
}

func TestModelPicker_SecondCommandOnEmptyListClosesWithoutText(t *testing.T) {
	// With roles configured the second bare /model closes the picker; it must not
	// append the informational text as a side effect of closing.
	m, _ := modelPickerModel(t, map[string]config.RoleConfig{
		"default": {Model: "claude-sonnet-4-6"},
	}, "default")
	before := len(m.chatModel.Messages)
	_, _ = m.handleModelCommand(nil)
	if m.searchPopup != nil {
		t.Error("second bare /model left the picker open")
	}
	if got := len(m.chatModel.Messages); got != before {
		t.Errorf("closing the picker appended %d messages", got-before)
	}
}

func TestThemePicker_TabPreviewsAndBackspaceRepreviews(t *testing.T) {
	m := themePickerModel(t)
	sp := m.searchPopup
	if len(sp.filtered) < 3 {
		t.Skip("need at least three themes")
	}

	// Tab advances and previews, like Down.
	start := sp.selected
	if !m.handleSearchPopupKey(tea.Key{Code: tea.KeyTab}) {
		t.Fatal("Tab was not handled by the theme picker")
	}
	if sp.selected == start {
		t.Error("Tab did not move the selection")
	}
	if got, want := m.themeManager.CurrentName(), sp.filtered[sp.selected].Text; got != want {
		t.Errorf("Tab previewed %q, want %q", got, want)
	}

	// Typing narrows, then Backspace widens again — each keystroke re-previews the
	// row that is now selected.
	m.handleSearchPopupKey(tea.Key{Text: "t", Code: 't'})
	if sp.search != "t" {
		t.Fatalf("search = %q, want \"t\"", sp.search)
	}
	narrowed := len(sp.filtered)
	m.handleSearchPopupKey(tea.Key{Code: tea.KeyBackspace})
	if sp.search != "" {
		t.Errorf("search = %q, want empty after Backspace", sp.search)
	}
	if len(sp.filtered) < narrowed {
		t.Error("Backspace did not widen the list back out")
	}
}

func TestThemePicker_BackspaceOnEmptySearchCloses(t *testing.T) {
	m := themePickerModel(t)
	if !m.handleSearchPopupKey(tea.Key{Code: tea.KeyBackspace}) {
		t.Fatal("Backspace on an empty query was not handled")
	}
	if m.searchPopup != nil {
		t.Error("Backspace on an empty query left the picker open")
	}
}

func TestTimelinePicker_BackspaceOnEmptySearchCloses(t *testing.T) {
	m := buildTranscript(t, 100)
	m.handleTimelineCommand()
	if m.searchPopup == nil {
		t.Fatal("/timeline did not open the picker")
	}
	m.handleSearchPopupKey(tea.Key{Code: tea.KeyBackspace})
	if m.searchPopup != nil {
		t.Error("Backspace on an empty query left the timeline open")
	}
}

func TestThemePicker_SearchFieldFiltersOnThemeName(t *testing.T) {
	m := themePickerModel(t)
	// The description carries " · current" for the active theme, so a search for
	// "current" should find exactly that row — filtering looks at both fields.
	m.searchPopup.search = "current"
	m.searchPopup.filterSearch()
	if len(m.searchPopup.filtered) != 1 {
		t.Fatalf("filter for \"current\" gave %d rows, want 1", len(m.searchPopup.filtered))
	}
	if !strings.Contains(m.searchPopup.filtered[0].Description, "current") {
		t.Errorf("row %q does not carry the description the filter matched",
			m.searchPopup.filtered[0].Text)
	}
}

func TestThemePicker_RevertWithNoPopupIsSafe(t *testing.T) {
	m := newTestModelFull(t)
	m.themeManager = NewThemeManager()
	// No popup open: revert must not panic on a nil payload.
	m.revertThemeSelection()
	if m.searchPopup != nil {
		t.Error("revert created a popup")
	}
}

func TestThemePicker_CommitWithNoSelectionIsSafe(t *testing.T) {
	m := newTestModelFull(t)
	m.themeManager = NewThemeManager()
	before := m.themeManager.CurrentName()
	// No popup: commit must be a no-op rather than a nil dereference.
	m.commitThemeSelection()
	if got := m.themeManager.CurrentName(); got != before {
		t.Errorf("commit without a popup changed the theme to %q", got)
	}
}

func TestExpandAgentCard_EmptyIDIgnored(t *testing.T) {
	// A row with no AgentID (a non-agent picker row reaching this path, or a
	// hand-built item) must not expand some other card or clear the flash.
	m := newTestModel(t)
	m.chatModel.Messages = []message{
		{role: "tool", tool: "subagent", agentID: "aaaa1111"},
	}
	m.expandAgentCard("")
	if m.expandedAgentID() != "" {
		t.Errorf("expandedAgent = %q after an empty ID", m.expandedAgentID())
	}
}

// assertMessageContains checks the last chat message mentions want.
func assertMessageContains(t *testing.T, m *model, want string) {
	t.Helper()
	if len(m.chatModel.Messages) == 0 {
		t.Fatalf("expected a message containing %q, got none", want)
	}
	last := m.chatModel.Messages[len(m.chatModel.Messages)-1].content
	if !strings.Contains(last, want) {
		t.Errorf("last message = %q, want it to contain %q", last, want)
	}
}
