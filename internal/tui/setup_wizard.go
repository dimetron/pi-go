package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// SetupProvider describes one provider the setup wizard can configure.
//
// The wizard deliberately carries its own small description of a provider
// rather than deriving it from internal/provider: that package answers "which
// backend serves this model", while this answers "what does a user have to
// paste, and from where". Keeping the credential URL next to the env var is
// what lets the wizard tell someone exactly which page to open.
type SetupProvider struct {
	Name     string // provider key as config.json spells it, e.g. "anthropic"
	Label    string // human name shown in the list
	EnvVar   string // environment variable / .env key
	NeedsKey bool   // false for a local daemon that authenticates nothing
	KeyURL   string // where the user gets a key, empty when NeedsKey is false
	// OptionalKey shows the key step without requiring an answer. It is for a
	// provider that runs without a credential by default but can be put
	// behind one — agentgateway with an apiKey policy — where skipping the
	// step would leave no way to configure the protected case, and requiring
	// it would block the common open one. Ignored when NeedsKey is set.
	OptionalKey bool
}

// asksForKey reports whether the wizard shows this provider a key step.
func (p SetupProvider) asksForKey() bool {
	return p.NeedsKey || p.OptionalKey
}

// SetupConfig is everything the wizard needs before it can draw a frame.
type SetupConfig struct {
	Providers []SetupProvider
	// Candidates maps a provider name to the model IDs offered for selection.
	// An empty or missing entry means free-text entry only, which is the
	// honest answer for a provider pi-go has no offline catalog for.
	Candidates map[string][]string

	// InitialProvider preselects the configured provider, so re-running setup
	// is an edit rather than a reset.
	InitialProvider string

	Theme string
}

// SetupResult is the choice the user confirmed, or Canceled.
//
// The wizard returns data instead of writing it: persistence lives in the CLI,
// so the UI stays free of I/O and a test can drive the whole flow in memory.
type SetupResult struct {
	Provider string
	APIKey   string
	Model    string
	Canceled bool
}

// setupStep is the wizard's position in its fixed three-step flow.
type setupStep int

const (
	setupStepProvider setupStep = iota
	setupStepKey
	setupStepModel
)

// setupMaxVisible caps how many provider/model rows are drawn at once. The
// window scrolls rather than growing to the length of the catalog: OpenRouter
// alone has 448 models, and a frame taller than the terminal cannot be read.
const setupMaxVisible = 12

// SetupWizard is a standalone Bubble Tea program for configuring a provider.
//
// It is separate from the main TUI on purpose. `pi setup` runs before any
// session exists — there is no agent, no logger and no config to reload — so
// hosting it inside the chat model would mean standing up that whole machine to
// collect three strings.
type SetupWizard struct {
	cfg SetupConfig

	step     setupStep
	provIdx  int
	keyInput textinput.Model
	// modelInput doubles as the type-ahead filter and, when nothing matches,
	// the custom model name. One field rather than a filter box plus a text
	// box: the two always hold the same string, so splitting them would only
	// create a way for them to disagree.
	modelInput textinput.Model

	candIdx int
	width   int
	height  int
	palette Palette
	// quitting stops the program; confirmed records that it stopped because
	// the user accepted the result. Kept separate rather than inferred from
	// the step, because Ctrl+C on the final step also stops the program from
	// that step and must not be read as an acceptance.
	quitting  bool
	confirmed bool
	// errMsg is shown under the step rather than returned, so a rejected key
	// does not tear down the program and lose what the user already chose.
	errMsg string
}

// NewSetupWizard builds the wizard with its first step ready.
func NewSetupWizard(cfg SetupConfig, p Palette) *SetupWizard {
	p = paletteOrDark(p)

	w := &SetupWizard{
		cfg:     cfg,
		palette: p,
	}

	key := textinput.New()
	key.Prompt = "  "
	key.Placeholder = "paste your API key"
	key.EchoMode = textinput.EchoPassword
	key.EchoCharacter = '•'

	model := textinput.New()
	model.Prompt = "  filter> "
	model.Placeholder = "type to filter, or enter a model name"

	w.keyInput = key
	w.modelInput = model

	w.provIdx = w.initialProviderIndex()
	return w
}

// initialProviderIndex finds the configured provider in the list so a re-run
// starts where the user already is. Falls back to the first entry.
func (w *SetupWizard) initialProviderIndex() int {
	for i, p := range w.cfg.Providers {
		if strings.EqualFold(p.Name, w.cfg.InitialProvider) {
			return i
		}
	}
	return 0
}

// Result reports the confirmed choice. It is only meaningful once the program
// has exited; anything other than an explicit confirmation returns Canceled.
func (w *SetupWizard) Result() SetupResult {
	if !w.confirmed {
		return SetupResult{Canceled: true}
	}
	return SetupResult{
		Provider: w.provider().Name,
		APIKey:   strings.TrimSpace(w.keyInput.Value()),
		Model:    w.model(),
	}
}

func (w *SetupWizard) provider() SetupProvider {
	if w.provIdx < 0 || w.provIdx >= len(w.cfg.Providers) {
		return SetupProvider{}
	}
	return w.cfg.Providers[w.provIdx]
}

// candidates returns the model IDs offered for the selected provider.
func (w *SetupWizard) candidates() []string {
	return w.cfg.Candidates[w.provider().Name]
}

// filtered narrows the candidate list by the typed filter.
//
// Matching is case-insensitive substring rather than fuzzy: model IDs are
// structured ("claude-sonnet-4-5", "anthropic/claude-3-haiku"), so a substring
// finds "sonnet-4" reliably, while a fuzzy match would also surface unrelated
// names and make the highlight unpredictable.
func (w *SetupWizard) filtered() []string {
	all := w.candidates()
	needle := strings.ToLower(strings.TrimSpace(w.modelInput.Value()))
	if needle == "" {
		return all
	}
	out := make([]string, 0, len(all))
	for _, id := range all {
		if strings.Contains(strings.ToLower(id), needle) {
			out = append(out, id)
		}
	}
	return out
}

// model returns the model the user would confirm right now: the highlighted
// candidate, or the typed text when it matches nothing.
func (w *SetupWizard) model() string {
	list := w.filtered()
	if len(list) > 0 {
		if w.candIdx >= 0 && w.candIdx < len(list) {
			return list[w.candIdx]
		}
		return list[0]
	}
	return strings.TrimSpace(w.modelInput.Value())
}

// visibleWindow returns the candidate slice to draw and the index within it
// that is selected, keeping the selection on screen.
func (w *SetupWizard) visibleWindow() (rows []string, selected int) {
	list := w.filtered()
	if len(list) == 0 {
		return nil, 0
	}
	start, end := windowAround(w.candIdx, len(list), w.listHeight())
	rows = list[start:end]
	return rows, w.candIdx - start
}

// listHeight is the number of candidate rows that fit, leaving room for the
// header, the filter box, the hint line and the model preview.
func (w *SetupWizard) listHeight() int {
	if w.height <= 0 {
		return setupMaxVisible
	}
	return clampInt(w.height-8, 3, setupMaxVisible)
}

// windowAround returns the half-open range of a list to render so that idx is
// visible, centered where the list allows.
func windowAround(idx, total, height int) (start, end int) {
	if total <= height {
		return 0, total
	}
	start = idx - height/2
	if start < 0 {
		start = 0
	}
	if start+height > total {
		start = total - height
	}
	return start, start + height
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Init starts the wizard with the first step's input focused.
func (w *SetupWizard) Init() tea.Cmd {
	return nil
}

// Update drives the state machine.
func (w *SetupWizard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		w.width = m.Width
		w.height = m.Height
		w.modelInput.SetWidth(clampInt(m.Width-16, 10, 70))
		w.keyInput.SetWidth(clampInt(m.Width-16, 10, 70))
		return w, nil
	case tea.KeyPressMsg:
		return w.handleKey(m)
	}
	return w, nil
}

func (w *SetupWizard) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()
	w.errMsg = ""

	// Ctrl+C always aborts; Esc steps back, and cancels from the first step.
	if key.Code == 'c' && key.Mod == tea.ModCtrl {
		w.quitting = true
		return w, tea.Quit
	}
	if key.Code == tea.KeyEsc {
		if w.step == setupStepProvider {
			w.quitting = true
			return w, tea.Quit
		}
		w.step--
		// Back from the model step lands on the provider list when the key
		// step was never shown, rather than on a key box the provider skipped.
		if w.step == setupStepKey && !w.provider().asksForKey() {
			w.step = setupStepProvider
		}
		w.focusStep()
		return w, nil
	}

	switch w.step {
	case setupStepProvider:
		return w.handleProviderKey(key)
	case setupStepKey:
		return w.handleKeyStep(msg, key)
	case setupStepModel:
		return w.handleModelKey(msg, key)
	}
	return w, nil
}

// handleProviderKey moves the radio selection. Enter advances, skipping the key
// step for a provider that needs no credential.
func (w *SetupWizard) handleProviderKey(key tea.Key) (tea.Model, tea.Cmd) {
	switch key.Code {
	case tea.KeyUp, 'k':
		w.provIdx = wrapIndex(w.provIdx-1, len(w.cfg.Providers))
	case tea.KeyDown, 'j':
		w.provIdx = wrapIndex(w.provIdx+1, len(w.cfg.Providers))
	case tea.KeyEnter:
		w.advanceFromProvider()
	}
	return w, nil
}

func (w *SetupWizard) advanceFromProvider() {
	w.candIdx = 0
	w.errMsg = ""
	if w.provider().asksForKey() {
		w.step = setupStepKey
	} else {
		// Nothing to authenticate: going straight on is the whole reason
		// NeedsKey exists, rather than showing a key box a local daemon
		// would ignore.
		w.step = setupStepModel
	}
	w.focusStep()
}

// handleKeyStep collects the credential. The value is not validated here —
// there is no offline way to tell a valid key from a typo, and a wrong guess
// would block a user whose key is simply new.
func (w *SetupWizard) handleKeyStep(msg tea.KeyPressMsg, key tea.Key) (tea.Model, tea.Cmd) {
	if key.Code == tea.KeyEnter {
		if strings.TrimSpace(w.keyInput.Value()) == "" && w.provider().NeedsKey {
			w.errMsg = "a key is required for " + w.provider().Label
			return w, nil
		}
		w.step = setupStepModel
		w.focusStep()
		return w, nil
	}
	var cmd tea.Cmd
	w.keyInput, cmd = w.keyInput.Update(msg)
	return w, cmd
}

// handleModelKey moves the model selection and accepts either a candidate or a
// typed custom name.
func (w *SetupWizard) handleModelKey(msg tea.KeyPressMsg, key tea.Key) (tea.Model, tea.Cmd) {
	switch key.Code {
	case tea.KeyUp:
		w.candIdx = wrapIndex(w.candIdx-1, len(w.filtered()))
		return w, nil
	case tea.KeyDown:
		w.candIdx = wrapIndex(w.candIdx+1, len(w.filtered()))
		return w, nil
	case tea.KeyEnter:
		if w.model() == "" {
			w.errMsg = "choose a model or type one"
			return w, nil
		}
		w.confirmed = true
		w.quitting = true
		return w, tea.Quit
	}

	// Any other key edits the filter, so the selection must be re-clamped:
	// typing narrows the list under the highlight.
	var cmd tea.Cmd
	w.modelInput, cmd = w.modelInput.Update(msg)
	if list := w.filtered(); len(list) > 0 && w.candIdx >= len(list) {
		w.candIdx = len(list) - 1
	}
	return w, cmd
}

// focusStep moves the cursor to the input the current step uses.
func (w *SetupWizard) focusStep() {
	w.keyInput.Blur()
	w.modelInput.Blur()
	switch w.step {
	case setupStepKey:
		_ = w.keyInput.Focus()
	case setupStepModel:
		_ = w.modelInput.Focus()
	}
}

// wrapIndex moves an index within [0,n) and wraps, so holding Up at the top of
// a list lands on the last entry rather than stopping.
func wrapIndex(i, n int) int {
	if n <= 0 {
		return 0
	}
	for i < 0 {
		i += n
	}
	return i % n
}

// View renders the current step.
func (w *SetupWizard) View() tea.View {
	p := w.palette
	title := lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
	dim := lipgloss.NewStyle().Foreground(p.Dim)
	muted := lipgloss.NewStyle().Foreground(p.Subtext)
	sel := lipgloss.NewStyle().Foreground(p.Success).Bold(true)
	label := lipgloss.NewStyle().Foreground(p.Text)

	var b strings.Builder
	b.WriteString(title.Render("pi setup"))
	b.WriteString(dim.Render("  — configure a provider"))
	b.WriteString("\n")
	b.WriteString(dim.Render(w.stepIndicator()))
	b.WriteString("\n\n")

	switch w.step {
	case setupStepProvider:
		b.WriteString(label.Render("Which provider do you want to use?"))
		b.WriteString("\n\n")
		for i, sp := range w.cfg.Providers {
			marker := "○ "
			style := muted
			if i == w.provIdx {
				marker = "◉ "
				style = sel
			}
			line := marker + sp.Label
			switch {
			case sp.NeedsKey:
			case sp.OptionalKey:
				line += dim.Render("  (key optional)")
			default:
				line += dim.Render("  (no key needed)")
			}
			b.WriteString(style.Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(dim.Render("↑/↓ select · enter continue · esc quit"))

	case setupStepKey:
		sp := w.provider()
		b.WriteString(label.Render(fmt.Sprintf("Paste your %s API key", sp.Label)))
		b.WriteString("\n")
		b.WriteString(dim.Render("stored in ~/.pi-go/.env as " + sp.EnvVar))
		b.WriteString("\n\n")
		b.WriteString(w.keyInput.View())
		b.WriteString("\n\n")
		if !sp.NeedsKey {
			b.WriteString(muted.Render("optional — leave blank if it needs no key, or to keep the one already set"))
			b.WriteString("\n\n")
		}
		if sp.KeyURL != "" {
			b.WriteString(muted.Render("Get a key at: " + sp.KeyURL))
			b.WriteString("\n\n")
		}
		b.WriteString(dim.Render("enter continue · esc back · ctrl+c quit"))

	case setupStepModel:
		b.WriteString(label.Render("Which model should be the default?"))
		b.WriteString("\n\n")
		b.WriteString(w.modelInput.View())
		b.WriteString("\n\n")

		rows, selected := w.visibleWindow()
		if len(rows) == 0 {
			if strings.TrimSpace(w.modelInput.Value()) == "" {
				b.WriteString(muted.Render("no models listed for " + w.provider().Name + " — type one above"))
				b.WriteString("\n")
			} else {
				b.WriteString(sel.Render("◉ " + strings.TrimSpace(w.modelInput.Value())))
				b.WriteString(dim.Render("  (custom)"))
				b.WriteString("\n")
			}
		} else {
			for i, id := range rows {
				if i == selected {
					b.WriteString(sel.Render("◉ " + id))
				} else {
					b.WriteString(muted.Render("○ " + id))
				}
				b.WriteString("\n")
			}
			if len(w.filtered()) > len(rows) {
				b.WriteString(dim.Render(fmt.Sprintf("  … %d more", len(w.filtered())-len(rows))))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
		b.WriteString(dim.Render("↑/↓ select · type to filter · enter confirm · esc back"))

	default:
		// The step enum has no other members; a frame reaching here would
		// otherwise render an empty screen with no way forward.
		b.WriteString(muted.Render("unexpected step"))
		b.WriteString("\n")
	}

	if w.errMsg != "" {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(p.Error).Render("✗ " + w.errMsg))
		b.WriteString("\n")
	}

	return tea.NewView(b.String())
}

// stepIndicator renders "1/3"-style progress, skipping the key step for a
// provider that does not use one so the count never promises a step that will
// not appear.
func (w *SetupWizard) stepIndicator() string {
	total, current := 3, int(w.step)+1
	if !w.provider().asksForKey() {
		total, current = 2, int(w.step)
		if w.step == setupStepModel {
			current = 2
		}
	}
	return fmt.Sprintf("step %d/%d", current, total)
}
