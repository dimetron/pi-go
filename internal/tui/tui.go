// Package tui implements the interactive terminal UI using Bubble Tea v2.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/palace"
)

// model is the Bubble Tea model for the interactive TUI.
type model struct {
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc

	// UI state.
	width  int
	height int

	// Input sub-model.
	inputModel InputModel

	// Chat sub-model (messages, scroll, rendering).
	chatModel ChatModel

	// Status bar sub-model.
	statusModel StatusModel

	// Theme manager.
	themeManager *ThemeManager

	// palette is the resolved color palette for the active theme, recomputed
	// each frame in View so a /theme switch takes effect immediately.
	palette Palette

	// bgDetected records that the terminal background question is settled —
	// either the reply to RequestBackgroundColor arrived, or the user picked a
	// theme explicitly. Detection never overrides an explicit choice.
	bgDetected bool

	// Agent state.
	running     bool
	mode        string             // "chat" or "plan" — shown in status bar
	agentCh     chan agentMsg      // channel for receiving agent events
	agentCancel context.CancelFunc // cancels the active agent response without quitting the TUI

	// Agent face renderer with mood expressions.
	face *FaceRenderer

	// hookQueue serializes lifecycle-hook execution off the Update goroutine.
	// Created lazily by enqueueHook (see agent_loop.go), which is only ever
	// reached from Update, so the lazy init needs no lock.
	hookQueue chan func()

	// Matrix rain animation state for sidebar.
	matrix matrixState

	// Mouse text selection. lastFrame is the frame the selection's coordinates
	// refer to — pi selects text itself (see selection.go), so it needs the
	// pixels it drew in order to know what is under the cursor. frameRows is its
	// height, which is what converts a mouse row into a frame row.
	sel       selection
	lastFrame string
	frameRows int
	// sessionTitle is the title derived from the user's most recent prompt. It
	// is surfaced to the terminal via View().WindowTitle so Bubble Tea's
	// renderer emits the escape sequence in-band with the frame it draws.
	sessionTitle string
	// topSectionRows is the number of rows in the top section (messages + sidebar)
	// before the full-width status bar. The rail and sidebar only cover these rows.
	topSectionRowsVal int
	// The selectable rows: the message viewport, half-open. Everything else in
	// the panel is chrome — the matrix rain, the rules, the status bar, the
	// prompt — and selecting it yields nothing anyone wants on their clipboard.
	msgTop, msgBottom int

	// Transient status-bar notice. flashSeq invalidates the timer of a flash
	// that has already been superseded, so an old expiry cannot clear a newer
	// message.
	flash    string
	flashSeq int

	// Deferred initialization state.
	loading      bool
	loadingItems map[string]bool // item name -> done?
	loadingTotal int             // planned init item count when known
	initCh       <-chan InitEvent
	loadingDots  int // animation dots (0-3): ., .., ..., ....

	// Git diff stats (refreshed after tool completions).
	diffAdded   int
	diffRemoved int

	// Commit flow state.
	commit *commitState

	// Login flow state.
	login *loginState

	// Skill-create pending overwrite confirmation.
	pendingSkillCreate *pendingSkillCreate

	// Run flow state (/run command).
	run *runState

	// Branch popup state (shown on status bar click).
	branchPopup *branchPopupState

	// Unified search popup for slash commands and history.
	searchPopup *searchPopupState

	// Legacy selection index for slash commands (used in tests).
	slashCommandSelected int

	// Quit.
	quitting bool
	initErr  error // fatal init error → propagated from Run()

	// Ctrl+C handling: show warning on first press, quit on second.
	ctrlCCount int

	// Memory palace status for sidebar (nil if no palace DB).
	memoryStatus *palace.PalaceStatus

	// resizeAt records when the last WindowSizeMsg arrived. Key/paste input
	// is suppressed briefly after resize to let terminal response sequences
	// (OSC color replies, DECRPM, CPR) drain without leaking into the input.
	resizeAt time.Time
}

// branchPopupState manages the git branch list popup.
type branchPopupState struct {
	branches  []string // list of git branches
	selected  int      // currently selected index
	active    string   // the currently active branch
	height    int      // popup height (number of visible items)
	scrollOff int      // scroll offset when more branches than height
}

// moveUp selects the previous branch, scrolling the window when the selection
// would leave the top of it. It stops at the first branch.
func (p *branchPopupState) moveUp() {
	if p.selected <= 0 {
		return
	}
	p.selected--
	if p.selected < p.scrollOff {
		p.scrollOff--
	}
}

// moveDown selects the next branch, scrolling the window when the selection
// would leave the bottom of it. It stops at the last branch.
func (p *branchPopupState) moveDown() {
	if p.selected >= len(p.branches)-1 {
		return
	}
	p.selected++
	if p.selected >= p.scrollOff+p.height {
		p.scrollOff++
	}
}

// newBranchPopup creates a new branch popup with the list of git branches.
func (m *model) newBranchPopup() {
	branches := listGitBranches(m.cfg.WorkDir)
	if len(branches) == 0 {
		return
	}

	active := m.statusModel.GitBranch
	selected := 0
	for i, b := range branches {
		if b == active {
			selected = i
			break
		}
	}

	popupHeight := len(branches)
	if popupHeight > 8 {
		popupHeight = 8
	}

	m.branchPopup = &branchPopupState{
		branches:  branches,
		selected:  selected,
		active:    active,
		height:    popupHeight,
		scrollOff: 0,
	}
}

// listGitBranches returns a list of all local git branches, with the active one first.
func listGitBranches(workDir string) []string {
	cmd := exec.Command("git", "branch")
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var branches []string
	active := ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		// Active branch starts with '*'
		if strings.HasPrefix(line, "* ") {
			active = strings.TrimPrefix(line, "* ")
		} else {
			branches = append(branches, strings.TrimSpace(line))
		}
	}

	// Put active branch first
	if active != "" {
		result := []string{active}
		result = append(result, branches...)
		return result
	}
	return branches
}

// searchPopupState is a unified search window for slash commands and history.
// The mode determines what items are shown and how they are selected.
type searchPopupState struct {
	mode      searchMode   // "commands" or "history"
	entries   []SearchItem // all items (commands or history entries)
	filtered  []SearchItem // filtered by search query
	selected  int          // currently selected index in filtered list
	search    string       // current search query
	height    int          // popup height (number of visible items)
	scrollOff int          // scroll offset when more entries than height
}

// searchMode determines what the popup displays.
type searchMode string

const (
	searchModeCommands searchMode = "commands"
	searchModeHistory  searchMode = "history"
)

// SearchItem represents an item in the search popup (command or history entry).
type SearchItem struct {
	Text        string // the command or history text
	Description string // for commands: the description
}

// searchPopupChrome is the number of rows the search popup reserves for chrome
// — two border rows (top, bottom), a header row, and a search-prompt row.
// height = items.  The item list fills the remaining space, so the popup's
// total height is item count + searchPopupChrome.  The render loop and the
// Up/Down scroll math both treat height as the visible item count, so it has
// to reflect what actually fits, not the total number of items — otherwise
// scrolling never fires and the selected row renders off-screen.
const searchPopupChrome = 4

// searchPopupMaxItems caps the popup so a huge command list does not eat the
// whole message area.  25 mirrors the previous cap and is enough to scan at a
// glance.
const searchPopupMaxItems = 25

// searchPopupListHeight returns how many item rows the popup should expose.
// availableRows is the number of rows the message viewport can spare for the
// popup; the popup uses that many minus its own chrome.  The result is
// clamped to [1, max] when there are items so the popup is always usable,
// even on terminals too small for the chrome plus a margin.  An empty list
// returns 0 — the "no matches" path renders the chrome only.
func searchPopupListHeight(itemCount, availableRows int) int {
	if itemCount <= 0 {
		return 0
	}
	if availableRows <= searchPopupChrome {
		// Not enough room for the chrome plus any item; show one anyway
		// and let the overlay clip the bottom.  Picking something is more
		// useful than seeing an empty list.
		return 1
	}
	height := itemCount
	if max := availableRows - searchPopupChrome; height > max {
		height = max
	}
	if height > searchPopupMaxItems {
		height = searchPopupMaxItems
	}
	return height
}

// newSearchPopup creates a unified search popup with the given mode.
func (m *model) newSearchPopup(mode searchMode) {
	var items []SearchItem

	switch mode {
	case searchModeCommands:
		// Get all slash command candidates.
		allCandidates := m.allSearchCandidates()
		// Filter by current input text if it starts with /.
		inputText := m.inputModel.Text
		filter := ""
		showAll := inputText == "/" // Show all commands when just "/" is typed
		if strings.HasPrefix(inputText, "/") && !showAll {
			filter = strings.ToLower(inputText)
		}
		for _, c := range allCandidates {
			if filter == "" || strings.HasPrefix(strings.ToLower(c.Text), filter) {
				items = append(items, SearchItem{Text: c.Text, Description: c.Description})
			}
		}

	case searchModeHistory:
		entries := m.inputModel.History
		if len(entries) == 0 {
			return
		}
		// Show oldest first (last item in entries at top).
		items = make([]SearchItem, len(entries))
		for i, e := range entries {
			items[len(entries)-1-i] = SearchItem{Text: e.Text}
		}
	}

	availableRows := m.messageViewportHeight()
	// The overlay leaves a 1-row margin between the popup and the bottom of
	// the viewport when the popup fits.  Reserve that row too so the chrome
	// does not push the last item into that margin.
	if availableRows > 0 {
		availableRows--
	}
	popupHeight := searchPopupListHeight(len(items), availableRows)

	m.searchPopup = &searchPopupState{
		mode:      mode,
		entries:   items,
		filtered:  items,
		selected:  0,
		search:    "",
		height:    popupHeight,
		scrollOff: 0,
	}
}

// refreshSearchPopupHeight recomputes the popup's visible item count after
// the terminal has been resized.  The popup keeps its selection; height, and
// scrollOff are reconciled so the selection stays visible and the rendered
// window never overshoots len(filtered) — otherwise renderSearchPopup would
// index past the end and panic on a tall→short→tall resize cycle.
func (m *model) refreshSearchPopupHeight() {
	if m.searchPopup == nil {
		return
	}
	availableRows := m.messageViewportHeight()
	if availableRows > 0 {
		availableRows--
	}
	m.searchPopup.height = searchPopupListHeight(len(m.searchPopup.filtered), availableRows)
	n := len(m.searchPopup.filtered)
	if n == 0 {
		m.searchPopup.selected = 0
		m.searchPopup.scrollOff = 0
		return
	}
	if m.searchPopup.selected >= n {
		m.searchPopup.selected = n - 1
	}
	// Bring scrollOff in sync with the new height. A previous resize could
	// have left it pointing past the new bottom of the window (short→tall
	// means more items visible, so a stale scrollOff overshoots), or past the
	// end of the list entirely (tall→short can shrink the visible window
	// below the old scrollOff). The previous value is not safe to keep.
	if m.searchPopup.selected < m.searchPopup.scrollOff {
		m.searchPopup.scrollOff = m.searchPopup.selected
	} else if m.searchPopup.selected >= m.searchPopup.scrollOff+m.searchPopup.height {
		m.searchPopup.scrollOff = m.searchPopup.selected - m.searchPopup.height + 1
	}
	// Defense in depth: never overshoot the end of filtered, so the
	// i-based loop in renderSearchPopup cannot read past the slice. The
	// in-view clamps above keep scrollOff within selected±height; this is
	// the final tie-breaker for the case where a previous scrollOff is
	// still larger than the new visible window (e.g. n shrunk or height
	// grew). max() guards against a negative maxScroll, which can only
	// happen if some external code wrote a height larger than n between
	// when this function read it and when it ran this clamp.
	if maxScroll := n - m.searchPopup.height; m.searchPopup.scrollOff > maxScroll {
		m.searchPopup.scrollOff = max(0, maxScroll)
	}
}

// allSearchCandidates returns all slash command candidates for the search popup.
func (m *model) allSearchCandidates() []CompletionCandidate {
	skills := m.inputModel.Skills
	if len(skills) == 0 {
		skills = m.cfg.Skills
	}
	return allSlashCommandCandidates(skills)
}

// filterSearch filters items by search query (case-insensitive substring on Text).
func (sp *searchPopupState) filterSearch() {
	if sp.search == "" {
		sp.filtered = sp.entries
		sp.selected = 0
		sp.scrollOff = 0
		return
	}
	q := strings.ToLower(sp.search)
	var filtered []SearchItem
	for _, e := range sp.entries {
		if strings.Contains(strings.ToLower(e.Text), q) || strings.Contains(strings.ToLower(e.Description), q) {
			filtered = append(filtered, e)
		}
	}
	if filtered == nil {
		filtered = sp.entries // show all if no matches
	}
	sp.filtered = filtered
	sp.selected = 0
	sp.scrollOff = 0
}

// syncPalette resolves the active theme's palette and fans it out to every
// renderer that holds one. It runs once per frame from View, and again from
// applyResize and the theme-switch paths so the palette is never stale when the
// markdown renderer is rebuilt. A nil manager (tests) falls back to the dark
// palette, keeping existing output unchanged.
func (m *model) syncPalette() {
	m.palette = darkPalette
	if m.themeManager != nil {
		m.palette = paletteFor(m.themeManager.Current())
	}
	m.chatModel.Palette = m.palette
	m.chatModel.ToolDisplay.Palette = m.palette
	m.inputModel.Palette = m.palette
	m.matrix.palette = m.palette
}

// applyTheme fans out a theme change: it repaints the lipgloss chrome via
// syncPalette, then rebuilds the pieces that cache a palette internally — the
// glamour markdown renderer and the text input's prompt and cursor styles.
// Without this a /theme switch would leave the transcript in the old theme.
func (m *model) applyTheme() {
	m.syncPalette()
	m.chatModel.RefreshTheme()
	m.inputModel.RefreshTheme()
}

// Run starts the interactive TUI.
func Run(ctx context.Context, cfg Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Initialize the theme manager before the markdown renderer: the glamour
	// stylesheet is chosen from the palette, so the renderer cannot be built
	// until the theme is known.
	tm := NewThemeManager()
	if cfg.ThemeName != "" && cfg.ThemeName != "default" {
		_ = tm.SetTheme(cfg.ThemeName) // ignore error, falls back to tokyo-night
	}
	palette := paletteFor(tm.Current())

	renderer, _ := newMarkdownRenderer(100, palette)

	// Load persistent command history from ~/.pi-go/history.jsonl.
	history := loadHistory()
	if history == nil {
		history = make([]HistoryEntry, 0)
	}

	m := model{
		cfg:          cfg,
		ctx:          ctx,
		cancel:       cancel,
		inputModel:   NewInputModel(history, cfg.Skills, cfg.SkillDirs, cfg.WorkDir),
		chatModel:    NewChatModel(renderer),
		statusModel:  StatusModel{},
		themeManager: tm,
		palette:      palette,
		face:         NewFaceRenderer(),
	}
	m.syncPalette()
	// The renderer above was built from this palette, so record its key rather
	// than letting the first RefreshTheme rebuild it for nothing.
	m.chatModel.rendererPaletteKey = paletteKey(palette)

	if cfg.DeferredInit != nil {
		m.loading = true
		m.loadingItems = make(map[string]bool)
		m.initCh = cfg.DeferredInit
	} else {
		m.statusModel.GitBranch = detectBranch(cfg.WorkDir)
	}

	// Clear inherited terminal state before the first frame is drawn. pi renders
	// on the normal screen, so whatever the previous command left set is still
	// in effect until something resets it.
	prepareTerminal()

	p := tea.NewProgram(&m, tea.WithContext(ctx))
	_, err := p.Run()
	drainTerminalResponses()
	if m.initErr != nil {
		// Log the fatal init error so crash loops are diagnosable.
		if m.cfg.Logger != nil {
			m.cfg.Logger.Errorf("fatal init error: %v", m.initErr)
		}
		return m.initErr
	}
	return err
}

func (m *model) Init() tea.Cmd {
	// Ask the terminal for its background color so an unconfigured theme can
	// match it. The reply arrives as tea.BackgroundColorMsg; terminals that do
	// not answer simply leave the configured default in place. This is a
	// program-level command, deliberately not something the render path does —
	// see TestUpdateRendererDoesNotQueryTerminalBackground.
	requestBg := tea.RequestBackgroundColor

	if m.initCh != nil {
		// Deferred init: start listening for init events.
		// Heavy initialization runs in a background goroutine (started by cli).
		return tea.Batch(
			requestBg,
			waitForInitEvent(m.initCh),
			tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg { return loadingTickMsg{} }),
		)
	}

	// Synchronous init (non-deferred path, used by tests and non-interactive modes).
	m.refreshDiffStats()
	var cmds []tea.Cmd
	if m.cfg.AgentEventCh != nil {
		cmds = append(cmds, waitForSubEvent(m.cfg.AgentEventCh))
	}
	if m.cfg.SystemNoticeCh != nil {
		cmds = append(cmds, waitForSystemNotice(m.cfg.SystemNoticeCh))
	}
	cmds = append(cmds, memoryTickCmd(m.cwd()), requestBg)
	return tea.Batch(cmds...)
}

// defaultLightTheme is the theme used when the terminal reports a light
// background and the user has not configured one.
const defaultLightTheme = "catppuccin-latte"

// handleBackgroundColor applies the terminal's reported background color.
//
// It only ever supplies a *default*: a theme named in config, or picked with
// /theme, is a deliberate choice and is never overridden. Nothing is persisted
// — the detected theme is a property of the terminal pi happens to be running
// in, not of the user's configuration.
func (m *model) handleBackgroundColor(msg tea.BackgroundColorMsg) {
	if m.bgDetected {
		return
	}
	m.bgDetected = true

	if m.themeManager == nil {
		return
	}
	if m.cfg.ThemeName != "" && m.cfg.ThemeName != "default" {
		return
	}
	if msg.IsDark() {
		return // the built-in default is already a dark theme
	}
	if err := m.themeManager.SetTheme(defaultLightTheme); err != nil {
		return
	}
	m.applyTheme()
}

// msgHandler consumes a message, reporting whether it handled it. When handled
// is false the model and command returned are ignored.
type msgHandler func(tea.Msg) (_ tea.Model, _ tea.Cmd, handled bool)

// Update routes a message to the group that owns it — terminal input, the
// agent event stream, the /run workflow, or the session side-channels.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	for _, update := range []msgHandler{
		m.updateTerminal,
		m.updateAgentStream,
		m.updateRunWorkflow,
		m.updateSession,
	} {
		if model, cmd, handled := update(msg); handled {
			return model, cmd
		}
	}

	// Keep the agent listener alive for any unhandled message types.
	if m.running {
		return m, waitForAgent(m.agentCh)
	}
	return m, nil
}

// updateTerminal handles messages that originate at the terminal — resize,
// keys, mouse, paste — plus the UI's own animation ticks.
func (m *model) updateTerminal(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.BackgroundColorMsg:
		m.handleBackgroundColor(msg)
		return m, nil, true

	case tea.PasteMsg:
		return m.handlePaste(msg)

	case tea.KeyPressMsg:
		// A resize can leave stray text fragments in the input queue.
		if m.resizeDraining() && isResizeTextFragment(msg) {
			return m, resizeDrainDoneCmd(m.resizeAt), true
		}
		model, cmd := m.handleKey(msg)
		return model, cmd, true

	case tea.MouseMsg:
		model, cmd := m.handleMouse(msg)
		return model, cmd, true

	case InputSubmitMsg:
		if strings.HasPrefix(msg.Text, "/") {
			model, cmd := m.handleSlashCommand(msg.Text)
			return model, cmd, true
		}
		model, cmd := m.submitPrompt(msg.Text, msg.Mentions)
		return model, cmd, true

	case resetCtrlCCountMsg:
		model, cmd := m.handleResetCtrlCCount()
		return model, cmd, true

	case resizeDrainDoneMsg:
		if msg.resizeAt.Equal(m.resizeAt) {
			m.resizeAt = time.Time{}
		}
		return m, nil, true

	case flashExpiredMsg:
		// Ignore a timer whose flash has already been replaced.
		if msg.seq == m.flashSeq {
			m.flash = ""
		}
		return m, nil, true

	case loadingTickMsg:
		// The loading-dots ticker was the engine behind TODO §30/§42: every
		// 300 ms fire it mutated m.loadingDots and re-armed unconditionally,
		// producing a 3.3 Hz full-history re-render for the life of the
		// process. The dots only animate the deferred-init splash
		// (m.loading is set at tui.go:455 and cleared at :1963/:1985), so
		// the re-arm belongs to that lifecycle, not to process uptime.
		// Re-arming only while m.loading is true stops the chain at init
		// completion and removes the idle 3.3 Hz floor.
		if !m.loading {
			return m, nil, true
		}
		m.loadingDots = (m.loadingDots + 1) % 4
		return m, tea.Tick(300*time.Millisecond, func(time.Time) tea.Msg { return loadingTickMsg{} }), true

	case matrixTickMsg:
		if m.running {
			m.matrix.tick(m.mainWidth())
			return m, matrixTickCmd(), true
		}
		return m, nil, true
	}
	return nil, nil, false
}

// handleWindowSize re-lays out the frame, and keeps the agent listener alive
// so a resize mid-turn does not drop the stream.
func (m *model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd, bool) {
	m.resizeAt = time.Now()
	m.width, m.height = msg.Width, msg.Height
	m.applyResize()

	cmd := resizeDrainDoneCmd(m.resizeAt)
	if m.running {
		return m, tea.Batch(cmd, waitForAgent(m.agentCh)), true
	}
	return m, cmd, true
}

// handlePaste inserts pasted text into the prompt, ignoring pastes that arrive
// while the agent runs or that are really resize noise. Outside a resize drain
// the message is left unhandled so the agent listener stays alive.
func (m *model) handlePaste(msg tea.PasteMsg) (tea.Model, tea.Cmd, bool) {
	if !m.running && !m.resizeDraining() && isUserPaste(msg.Content) {
		m.inputModel.InsertText(msg.Content)
	}
	if m.resizeDraining() {
		return m, resizeDrainDoneCmd(m.resizeAt), true
	}
	return nil, nil, false
}

// handleMouse dispatches to the click, drag, release and wheel handlers.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		return m.handleMouseClick(msg)
	case tea.MouseMotionMsg:
		return m.handleMouseMotion(msg)
	case tea.MouseReleaseMsg:
		return m.handleMouseRelease(msg)
	case tea.MouseWheelMsg:
		return m.handleMouseWheel(msg)
	}
	return m, nil
}

// updateAgentStream handles the events streamed by a running agent turn.
func (m *model) updateAgentStream(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case agentThinkingMsg:
		model, cmd := m.handleAgentThinking(msg)
		return model, cmd, true
	case agentTextMsg:
		model, cmd := m.handleAgentText(msg)
		return model, cmd, true
	case agentToolCallMsg:
		model, cmd := m.handleAgentToolCall(msg)
		return model, cmd, true
	case agentToolResultMsg:
		model, cmd := m.handleAgentToolResult(msg)
		return model, cmd, true
	case agentSubEventMsg:
		model, cmd := m.handleAgentSubEvent(msg)
		return model, cmd, true
	case agentWarningMsg:
		model, cmd := m.handleAgentWarning(msg)
		return model, cmd, true
	case systemNoticeMsg:
		m.chatModel.Messages = append(m.chatModel.Messages, message{
			role:    "assistant",
			content: msg.text,
		})
		return m, waitForSystemNotice(m.cfg.SystemNoticeCh), true
	case agentDoneMsg:
		model, cmd := m.handleAgentDone(msg)
		return model, cmd, true
	}
	return nil, nil, false
}

// updateRunWorkflow handles the /run spec workflow: agent progress, the
// quality gate, and the merge back.
func (m *model) updateRunWorkflow(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case runAgentEventMsg:
		model, cmd := m.handleRunAgentEvent(msg)
		return model, cmd, true
	case runAgentDoneMsg:
		model, cmd := m.handleRunAgentDone(msg)
		return model, cmd, true
	case runGateResultMsg:
		model, cmd := m.handleRunGateResult(msg)
		return model, cmd, true
	case runMergeResultMsg:
		model, cmd := m.handleRunMergeResult(msg)
		return model, cmd, true
	}
	return nil, nil, false
}

// updateSession handles the side-channels that outlive a single turn: startup,
// restart, login, commit, memory polling and ping.
func (m *model) updateSession(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case initEventMsg:
		model, cmd := m.handleInitEvent(msg)
		return model, cmd, true
	case restartMsg:
		execRestart()
		return m, tea.Quit, true
	case loginSSOResultMsg:
		model, cmd := m.handleLoginSSOResult(msg)
		return model, cmd, true
	case commitGeneratedMsg:
		model, cmd := m.handleCommitGenerated(msg)
		return model, cmd, true
	case commitDoneMsg:
		model, cmd := m.handleCommitDone(msg)
		return model, cmd, true
	case memoryTickMsg:
		model, cmd := m.handleMemoryTick(msg)
		return model, cmd, true
	case pingDoneMsg:
		model, cmd := m.handlePingDone(msg)
		return model, cmd, true
	}
	return nil, nil, false
}

// handleMemoryTick stores the latest memory palace status and reschedules the
// next poll.
func (m *model) handleMemoryTick(msg memoryTickMsg) (tea.Model, tea.Cmd) {
	m.memoryStatus = msg.status
	return m, tea.Tick(memoryTickInterval, func(time.Time) tea.Msg {
		return memoryTickCmd(m.cwd())()
	})
}

// handlePingDone replaces the "Pinging model..." placeholder with the ping
// result, and feeds the model's reply to the matrix rain.
func (m *model) handlePingDone(msg pingDoneMsg) (tea.Model, tea.Cmd) {
	content := msg.output
	if msg.err != nil {
		content += fmt.Sprintf("\n\n✗ Ping failed: %v", msg.err)
	}

	reply := message{role: "assistant", content: content}
	if n := len(m.chatModel.Messages); n > 0 && m.chatModel.Messages[n-1].role == "thinking" {
		m.chatModel.Messages[n-1] = reply
	} else {
		m.chatModel.Messages = append(m.chatModel.Messages, reply)
	}

	if msg.reply != "" {
		m.matrix.feed(msg.reply, m.mainWidth())
	}
	return m, nil
}

// handleMouseClick starts a text selection and clears any previous one.
//
// pi selects text itself: mouse reporting is on so the wheel can scroll the
// chat, and that takes click-drag away from the terminal. See selection.go.
func (m *model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return m, nil
	}
	// The gauge's [x] is a control, not text. It sits to the right of the chat
	// panel, so it has to be claimed before the off-panel branch below swallows
	// the click as "deselect".
	if m.hitContextClear(mouse.X, mouse.Y) {
		m.sel = selection{}
		m.clearConversation()
		return m, m.setFlash("Context cleared")
	}
	// Only the chat panel is selectable. A press on the rail or in the sidebar
	// starts nothing — and clears any highlight, the same as a click anywhere
	// else would.
	if mouse.X >= m.chatWidth() {
		m.sel = selection{}
		return m, nil
	}
	x, y := m.clampToChat(mouse.X, mouse.Y)

	// Clicking inside a highlight copies it again, rather than throwing it away.
	// The highlight is the affordance: what is lit up is what a click takes.
	if m.sel.contains(x, y, m.chatWidth()) {
		return m, m.copyAndFlash(selectedText(m.lastFrame, m.sel, m.chatWidth()))
	}

	m.sel = selection{dragging: true, anchorX: x, anchorY: y, cursorX: x, cursorY: y}
	return m, nil
}

// copyAndFlash puts text on the clipboard and says so in the status bar.
func (m *model) copyAndFlash(text string) tea.Cmd {
	cmd := copySelection(text)
	if cmd == nil {
		return nil
	}
	return tea.Batch(cmd, m.setFlash("Copied!"))
}

// flashDuration is how long a transient status notice stays up.
const flashDuration = 3 * time.Second

// flashExpiredMsg clears a flash. It carries the sequence number of the flash it
// belongs to, so a stale timer cannot wipe a message raised after it.
type flashExpiredMsg struct{ seq int }

func (m *model) setFlash(text string) tea.Cmd {
	m.flashSeq++
	m.flash = text
	seq := m.flashSeq
	return tea.Tick(flashDuration, func(time.Time) tea.Msg {
		return flashExpiredMsg{seq: seq}
	})
}

// handleMouseMotion extends an in-progress selection. Motion only arrives while
// a button is held, which is exactly a drag.
func (m *model) handleMouseMotion(msg tea.MouseMotionMsg) (tea.Model, tea.Cmd) {
	if !m.sel.dragging {
		return m, nil
	}
	mouse := msg.Mouse()
	m.sel.cursorX, m.sel.cursorY = m.clampToChat(mouse.X, mouse.Y)
	m.sel.present = true
	return m, nil
}

// handleMouseRelease ends the drag and copies the selection.
//
// Copy on release, with no extra keystroke: that is what the terminal used to do
// for us, and reproducing it is the whole point. The highlight stays up so it is
// obvious what landed on the clipboard.
func (m *model) handleMouseRelease(msg tea.MouseReleaseMsg) (tea.Model, tea.Cmd) {
	if !m.sel.dragging {
		return m, nil
	}
	m.sel.dragging = false

	if m.sel.empty() {
		m.sel = selection{} // a plain click, not a drag: just clear
		return m, nil
	}
	return m, m.copyAndFlash(selectedText(m.lastFrame, m.sel, m.chatWidth()))
}

// clampToChat converts a mouse position into a chat-panel cell.
//
// The Y translation matters: the UI runs on the normal screen, not the alternate
// one, so the frame is drawn *below* whatever the terminal already had on it —
// a shell prompt, a "pprof listening" line — and Bubble Tea reports the mouse in
// absolute terminal rows, not frame rows. Indexing the frame with a raw mouse Y
// therefore lands N rows too low, N being the height of that prior output.
//
// The frame is bottom-anchored (its last row is the screen's last row), so the
// offset is whatever is left over: height - frameRows. Deriving it beats
// hardcoding it, because it is right for any amount of prior output.
//
// X needs no such translation — the frame starts at column 0 — but it is clamped
// to the panel so a drag into the sidebar, or off the window entirely, pins to
// the edge instead of sweeping the rail and sidebar into the selection.
func (m *model) clampToChat(x, y int) (int, int) {
	x = min(max(x, 0), max(0, m.chatWidth()-1))

	// Rows are clamped to the message viewport, so a drag that strays into the
	// matrix rain above or the status bar and prompt below pins to the nearest
	// message row instead of copying chrome. Clamping rather than refusing keeps
	// a drag that overshoots by a row from doing nothing at all.
	top, bottom := m.msgTop, m.msgBottom
	if bottom <= top { // no viewport yet (first frame)
		return x, 0
	}
	y = min(max(m.frameRow(y), top), bottom-1)
	return x, y
}

// frameRow maps an absolute terminal row to a row of the rendered frame.
func (m *model) frameRow(y int) int {
	return y - m.frameTop()
}

// frameTop is the terminal row the frame's first line occupies.
func (m *model) frameTop() int {
	return max(0, m.height-m.frameRows)
}

// handleMouseWheel processes mouse wheel events for scrolling the chat viewport.
func (m *model) handleMouseWheel(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	switch mouse.Button {
	case tea.MouseWheelUp:
		m.chatModel.ScrollUp(3, m.height)
	case tea.MouseWheelDown:
		m.chatModel.ScrollDown(3)
	}
	return m, nil
}

// keyHandler consumes a key press, reporting whether it handled the key. When
// handled is false the model and command returned are ignored and the key
// falls through to the next handler.
type keyHandler func(tea.Key) (_ tea.Model, _ tea.Cmd, handled bool)

// handleKey routes a key press through the modal overlays in priority order,
// then the editing keys, and finally the prompt input. Each layer decides for
// itself whether it owns the key.
func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()

	// Overlays get first refusal, even while the agent runs.
	for _, handle := range []keyHandler{
		m.handleCommitKey,
		m.handleLoginKey,
		m.handleSkillCreateKey,
		m.handleBranchPopupKey,
		m.handleInterruptKey,
	} {
		if model, cmd, handled := handle(key); handled {
			return model, cmd
		}
	}

	// Everything below edits the prompt, which is read-only while busy.
	if m.running || m.loading {
		return m, nil
	}

	for _, handle := range []keyHandler{
		m.handleToggleKey,
		m.handleHistoryKey,
		m.handleScrollKey,
	} {
		if model, cmd, handled := handle(key); handled {
			return model, cmd
		}
	}

	return m.handleInputKey(msg)
}

// isCancelKey reports whether key is one of the two ways to back out of a
// modal prompt: Esc or Ctrl+C.
func isCancelKey(key tea.Key) bool {
	return key.Code == tea.KeyEsc || (key.Code == 'c' && key.Mod == tea.ModCtrl)
}

// handleCommitKey resolves the commit confirmation prompt, swallowing every
// other key so a stray press cannot commit.
func (m *model) handleCommitKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.running || m.commit == nil || m.commit.phase != "confirming" {
		return nil, nil, false
	}
	switch {
	case key.Code == tea.KeyEnter:
		model, cmd := m.handleCommitConfirm()
		return model, cmd, true
	case isCancelKey(key):
		model, cmd := m.handleCommitCancel()
		return model, cmd, true
	default:
		return m, nil, true
	}
}

// handleLoginKey drives the login overlay. In the phases that collect text the
// unhandled keys fall through to the prompt input; in every other phase the
// overlay swallows them.
func (m *model) handleLoginKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.running || m.login == nil {
		return nil, nil, false
	}
	if isCancelKey(key) {
		model, cmd := m.handleLoginCancel()
		return model, cmd, true
	}

	collecting := m.login.phase == "waiting" || m.login.phase == "manual-code"
	if key.Code == tea.KeyEnter && collecting {
		if m.login.phase == "waiting" {
			return m.submitLoginInput(m.handleLoginSave)
		}
		return m.submitLoginInput(m.handleLoginCodeSubmit)
	}
	return m, nil, !collecting
}

// submitLoginInput hands the trimmed prompt text to submit. Enter on a blank
// prompt does nothing rather than submitting an empty credential.
func (m *model) submitLoginInput(submit func(string) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd, bool) {
	text := strings.TrimSpace(m.inputModel.Text)
	if text == "" {
		return m, nil, true
	}
	m.inputModel.Clear()
	model, cmd := submit(text)
	return model, cmd, true
}

// handleSkillCreateKey resolves the skill-create overwrite confirmation.
func (m *model) handleSkillCreateKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.running || m.pendingSkillCreate == nil {
		return nil, nil, false
	}
	switch {
	case key.Code == tea.KeyEnter:
		model, cmd := m.handleSkillCreateConfirm()
		return model, cmd, true
	case isCancelKey(key):
		model, cmd := m.handleSkillCreateCancel()
		return model, cmd, true
	default:
		return m, nil, true
	}
}

// handleBranchPopupKey navigates the branch picker. Anything other than the
// arrows and Enter dismisses it.
func (m *model) handleBranchPopupKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.branchPopup == nil {
		return nil, nil, false
	}
	switch key.Code {
	case tea.KeyEnter:
		model, cmd := m.handleBranchSelect()
		return model, cmd, true
	case tea.KeyUp:
		m.branchPopup.moveUp()
	case tea.KeyDown:
		m.branchPopup.moveDown()
	default:
		m.branchPopup = nil
	}
	return m, nil, true
}

// handleInterruptKey handles the keys that stay live while the agent runs:
// Esc to dismiss or cancel, Ctrl+C to cancel then quit, and F12 as a no-op.
func (m *model) handleInterruptKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch {
	case key.Code == tea.KeyEsc:
		if m.searchPopup != nil {
			m.searchPopup = nil
			return m, nil, true
		}
		if m.running {
			m.cancelAgent()
		}
		return m, nil, true

	case key.Code == 'c' && key.Mod == tea.ModCtrl:
		if m.running {
			m.cancelAgent()
			m.ctrlCCount++
			m.chatModel.AppendWarning("\nCtrl+C again to quit (or wait 2s)...")
			return m, resetCtrlCCount(m), true
		}
		m.ctrlCCount++
		if m.ctrlCCount >= 2 {
			m.quitting = true
			return m, tea.Quit, true
		}
		// First press: warn, and reset the count after 2 seconds.
		m.chatModel.AppendWarning("\nCtrl+C again to quit (or wait 2s)...")
		return m, resetCtrlCCount(m), true

	case key.Code == tea.KeyF12:
		return m, nil, true
	}
	return nil, nil, false
}

// handleToggleKey handles the Ctrl-chord toggles and the search popup.
func (m *model) handleToggleKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	// Ctrl+O: toggle compact/expanded tool output.
	if key.Code == 'o' && key.Mod == tea.ModCtrl {
		m.chatModel.ToolDisplay.CompactTools = !m.chatModel.ToolDisplay.CompactTools
		return m, nil, true
	}

	// Ctrl+B toggles the branch popup only when the prompt is empty. The
	// standard text input uses Ctrl+B as backward cursor movement, and some
	// terminals emit it for left/back navigation.
	if key.Code == 'b' && key.Mod == tea.ModCtrl && m.inputModel.Text == "" {
		if m.statusModel.GitBranch != "" {
			if m.branchPopup == nil {
				m.newBranchPopup()
			} else {
				m.branchPopup = nil
			}
		}
		return m, nil, true
	}

	// Unified search popup keys (slash commands or history).
	if m.handleSearchPopupKey(key) {
		return m, nil, true
	}

	// Ctrl+R: open history search popup (reverse-i-search style). With no
	// history to search the key falls through to the input.
	if key.Code == 'r' && key.Mod == tea.ModCtrl && m.searchPopup == nil && len(m.inputModel.History) > 0 {
		m.newSearchPopup(searchModeHistory)
		return m, nil, true
	}
	return nil, nil, false
}

// handleHistoryKey maps Up to the prompt-history window and Down to the chat.
//
// Up used to cycle history inline, replacing whatever was typed. The window
// shows the whole history at once and leaves the prompt untouched until an
// entry is chosen, so a stray Up costs one Esc rather than your draft.
//
// The mouse wheel cannot land here: View enables mouse reporting, so a wheel
// tick is a MouseWheelMsg handled by handleMouseWheel, never a KeyUp. Up still
// scrolls the chat before any history exists, so the keyboard can scroll on a
// fresh session.
//
// A prompt starting with "/" is excluded: those arrows drive the slash-command
// popup instead.
func (m *model) handleHistoryKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	if m.searchPopup != nil || m.shouldShowSlashCommandPopup() {
		return nil, nil, false
	}
	switch key.Code {
	case tea.KeyUp:
		if len(m.inputModel.History) == 0 {
			m.chatModel.ScrollUp(3, m.height)
			return m, nil, true
		}
		m.newSearchPopup(searchModeHistory)
		return m, nil, true
	case tea.KeyDown:
		m.chatModel.ScrollDown(3)
		return m, nil, true
	}
	return nil, nil, false
}

// handleScrollKey pages the chat viewport.
func (m *model) handleScrollKey(key tea.Key) (tea.Model, tea.Cmd, bool) {
	switch key.Code {
	case tea.KeyPgUp:
		m.chatModel.ScrollUp(5, m.height)
		return m, nil, true
	case tea.KeyPgDown:
		m.chatModel.ScrollDown(5)
		return m, nil, true
	}
	return nil, nil, false
}

// handleInputKey delegates to the prompt input, keeping the slash-command
// popup in sync with the text as it changes.
func (m *model) handleInputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()

	// Show the commands popup when the input starts with "/".
	if m.shouldShowSlashCommandPopup() {
		if m.searchPopup == nil || m.searchPopup.mode != searchModeCommands {
			m.newSearchPopup(searchModeCommands)
		}
		// Immediately handle Tab/Up/Down to navigate the popup.
		switch key.Code {
		case tea.KeyTab, tea.KeyUp, tea.KeyDown:
			if m.handleSearchPopupKey(key) {
				return m, nil
			}
		}
	}

	prevText := m.inputModel.Text
	cmd := m.inputModel.HandleKey(msg)
	if m.inputModel.Text == prevText {
		return m, cmd
	}

	// The text changed: open or close the popup to match the new prompt.
	if m.searchPopup != nil && m.searchPopup.mode == searchModeCommands && !m.shouldShowSlashCommandPopup() {
		m.searchPopup = nil
	}
	if m.searchPopup == nil && m.shouldShowSlashCommandPopup() {
		m.newSearchPopup(searchModeCommands)
	}
	return m, cmd
}

func (m *model) handleSearchPopupKey(key tea.Key) bool {
	if m.searchPopup == nil {
		return false
	}

	sp := m.searchPopup

	switch key.Code {
	case tea.KeyUp:
		if sp.selected > 0 {
			sp.selected--
		} else if len(sp.filtered) > 1 {
			// Wrap to last item on Up from first.
			sp.selected = len(sp.filtered) - 1
		}
		sp.scrollOff = max(0, sp.selected-sp.height+1)
		return true
	case tea.KeyDown:
		if sp.selected < len(sp.filtered)-1 {
			sp.selected++
		} else if len(sp.filtered) > 1 {
			// Wrap to first item on Down from last.
			sp.selected = 0
		}
		if sp.selected >= sp.scrollOff+sp.height {
			sp.scrollOff = sp.selected - sp.height + 1
		}
		return true
	case tea.KeyTab:
		if len(sp.filtered) == 0 {
			return true
		}
		if key.Mod == tea.ModShift {
			if sp.selected > 0 {
				sp.selected--
			} else {
				// Wrap to last item on Shift+Tab from first.
				sp.selected = len(sp.filtered) - 1
			}
		} else {
			// Tab advances; stays at last item (no wrap).
			if sp.selected < len(sp.filtered)-1 {
				sp.selected++
			}
		}
		sp.scrollOff = max(0, sp.selected-sp.height+1)
		return true
	case tea.KeyEnter:
		if len(sp.filtered) > 0 && sp.selected < len(sp.filtered) {
			item := sp.filtered[sp.selected]
			switch sp.mode {
			case searchModeCommands:
				m.inputModel.SetText(item.Text + " ")
				m.searchPopup = nil
			case searchModeHistory:
				m.inputModel.SetText(item.Text)
				m.searchPopup = nil
			}
		}
		return true
	case tea.KeyEsc:
		m.searchPopup = nil
		return true
	case tea.KeyBackspace:
		if len(sp.search) > 0 {
			sp.search = sp.search[:len(sp.search)-1]
			sp.filterSearch()
		} else {
			// If search is empty, close popup on backspace
			m.searchPopup = nil
		}
		return true
	default:
		// Type to search (only for printable single characters).
		if key.Text != "" && len(key.Text) == 1 && key.Mod == 0 {
			sp.search += key.Text
			sp.filterSearch()
			return true
		}
		return false
	}
}

func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("Goodbye!\n")
	}

	m.syncPalette()

	if m.width == 0 {
		// Show matrix-style startup text before the first terminal size arrives.
		matrixLine := renderStartupMatrixLine(m.loadingDots, m.cfg.AppVersion, m.loadingItems, m.loadingTotal, m.palette)
		if m.loadingItems != nil {
			var lines []string
			lines = append(lines, matrixLine)
			for _, item := range sortedKeys(m.loadingItems) {
				done := m.loadingItems[item]
				mark := " "
				if done {
					mark = "✓"
				}
				lines = append(lines, "  "+mark+" "+item)
			}
			return tea.NewView(strings.Join(lines, "\n") + "\n")
		}
		return tea.NewView(matrixLine + "\n")
	}

	// Layout: messages + sidebar on top, status bar + input spanning the full
	// width below. The sidebar sits beside the messages only; the status bar
	// extends to the right edge, giving it more room for tools and clock info.
	mainWidth := m.mainWidth()
	if m.statusModel.Width != m.width || m.chatModel.Width != m.chatWidth() {
		m.applyResize()
		mainWidth = m.mainWidth()
	}
	bodyWidth := m.chatWidth()
	sidebarWidth := m.width - mainWidth
	showSidebar := sidebarWidth > 0

	// Render components.
	m.inputModel.SetWidth(max(0, m.width-3))
	messagesView, lineKinds := m.chatModel.renderMessages(m.running)
	statusBar := m.statusModel.Render(m.statusRenderInput())
	inputArea := m.inputModel.View(m.running || m.loading)
	var inputCursor *tea.Cursor
	if !m.running && !m.loading {
		inputCursor = m.inputModel.Cursor()
	}

	// Calculate available height for messages.
	availableHeight := m.messageViewportHeight()

	// Truncate messages to fit viewport.
	msgLines := strings.Split(messagesView, "\n")
	totalLines := len(msgLines)

	startLine := totalLines - availableHeight - m.chatModel.Scroll
	if startLine < 0 {
		startLine = 0
	}
	endLine := startLine + availableHeight
	if endLine > totalLines {
		endLine = totalLines
	}

	visibleMessages := strings.Join(msgLines[startLine:endLine], "\n")

	// Pad to fill the viewport. availableHeight is message rows only — the blank
	// rows that inset the block from the rules are budgeted separately.
	visibleLineCount := strings.Count(visibleMessages, "\n") + 1
	for visibleLineCount < availableHeight {
		// Keep the final padding row materialized. A trailing newline only
		// terminates the preceding row; treating its trailing empty string as a
		// row made the composed frame one line shorter than the terminal.
		visibleMessages += "\n "
		visibleLineCount++
	}
	visibleMessages = m.overlaySearchPopup(visibleMessages, bodyWidth)

	// Note: width constraint is handled by glamour's WithWordWrap(contentWidth) in chatModel.UpdateRenderer.
	// lipgloss.Width() counts raw bytes including invisible ANSI codes, causing wrapping issues.

	// Render matrix rain as full-width top bar (when active).
	matrixBar := m.matrix.render()

	// Horizontal rule for separating sections. The panel hr is bodyWidth; the
	// full-width hr spans the entire terminal (used below the sidebar).
	hrStyle := lipgloss.NewStyle().Foreground(m.palette.Surface)
	hr := hrStyle.Render(strings.Repeat("─", bodyWidth))
	fullHr := hrStyle.Render(strings.Repeat("─", m.width))

	var b strings.Builder
	if matrixBar != "" {
		b.WriteString(hr)
		b.WriteString("\n")
		b.WriteString(matrixBar)
		b.WriteString("\n")
		b.WriteString(hr)
		b.WriteString("\n")
	}
	// One blank row above the messages, matched by one below, so the block is
	// inset evenly between the rules whether or not the matrix bar is showing.
	b.WriteString("\n")

	// The rows the message viewport occupies: where the rail switches from a
	// plain divider to the minimap, and how many cells it needs. A block that
	// ends in a newline is already terminated, so its trailing "" is not a row —
	// counting it as one pushed a minimap bar onto the rule below.
	msgStart := strings.Count(b.String(), "\n")
	msgRows := strings.Count(visibleMessages, "\n") + 1
	if strings.HasSuffix(visibleMessages, "\n") {
		msgRows--
	}

	b.WriteString(visibleMessages)
	// Terminate the message block. When the viewport is exactly full nothing
	// pads it, and the blank row below would otherwise be appended to the last
	// message line.
	if !strings.HasSuffix(visibleMessages, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n") // the matching blank row below

	// Render branch popup if open.
	if m.branchPopup != nil {
		popupView := m.renderBranchPopup()
		b.WriteString(popupView)
		b.WriteString("\n")
	}

	// The rule closes the panel — no newline after it. A trailing newline made
	// the panel one row taller than its content: an empty row below the rule,
	// which the sidebar filled with its own last row, so its filler dots ran on
	// beside the status bar and the two rules never met.
	b.WriteString(hr)

	// The top section: body | rail | sidebar. The sidebar only covers the
	// messages area, not the status bar below — the status bar extends the
	// full terminal width underneath.
	body := padLinesTo(b.String(), bodyWidth)
	panelRows := strings.Count(body, "\n") + 1
	rail := railColumn(panelRows, msgStart, renderMinimap(lineKinds, startLine, endLine, msgRows, m.palette), m.palette)
	leftPanel := lipgloss.JoinHorizontal(lipgloss.Top, body, rail)

	var topSection string
	if showSidebar {
		hostName := cachedHostname()
		sidebarInput := SidebarRenderInput{
			Width: sidebarWidth,
			// Exactly as tall as the panel beside it. Sized to the terminal
			// instead, the sidebar outran the panel — JoinHorizontal padded the
			// panel with blank rows, leaving a gap below the prompt while the
			// sidebar's filler dots carried on past it.
			Height:       panelRows,
			Mascot:       m.mascot(),
			Mode:         m.mode,
			ProviderName: m.providerDisplayName(),
			ModelName:    m.cfg.ModelName,
			GitBranch:    m.statusModel.GitBranch,
			DiffAdded:    m.diffAdded,
			DiffRemoved:  m.diffRemoved,
			Running:      m.running,
			TokenTracker: m.cfg.TokenTracker,
			AppVersion:   m.cfg.AppVersion,
			HostName:     hostName,
			FolderName:   sidebarFolderName(m.cwd()),
			Messages:     m.chatModel.Messages,
			ActiveTool:   m.statusModel.ActiveTool,
			LoadingItems: m.loadingItems,
			MatrixLines:  "",
			StatusLine:   "",
			Orchestrator: m.cfg.Orchestrator,
			MCPTools:     extension.BuildMCPToolEntries(m.cfg.MCPToolsets),
			MemoryStatus: m.memoryStatus,
			Artifacts:    m.artifactList(),
			Palette:      m.palette,
		}
		if m.run != nil && m.run.phase != "" {
			sidebarInput.RunChecklist = m.run.checklist
			sidebarInput.RunPhase = m.run.phase
			sidebarInput.RunSpec = m.run.specName
			sidebarInput.RunCycle = m.run.retries + 1
			sidebarInput.RunMaxCycle = m.run.maxRetries
		}
		sidebar := RenderSidebar(sidebarInput)
		topSection = joinPanelSidebar(leftPanel, sidebar, mainWidth, sidebarWidth)
	} else {
		topSection = padLinesTo(leftPanel, m.width)
	}

	// Bottom section: full-width status bar + input. These span the entire
	// terminal width — the sidebar ends at the hr above, so the status bar has
	// room for all its segments (tools, clock, context) without competing with
	// the sidebar for columns.
	var bottom strings.Builder
	bottom.WriteString(statusBar)
	bottom.WriteString("\n")
	bottom.WriteString(fullHr)
	bottom.WriteString("\n")
	inputCursorY := strings.Count(topSection, "\n") + 1 + strings.Count(bottom.String(), "\n")
	bottom.WriteString(inputArea)
	bottom.WriteString("\n")
	// The closing rule doubles as the session context gauge — same row, same
	// width, now carrying a reading instead of only closing the frame.
	bottom.WriteString(renderContextRule(m.contextRuleFor(m.width), m.palette))

	// Pad the bottom section to the full terminal width so no row is wider or
	// narrower than m.width. The status bar's Width style handles it for that
	// line, but the input area and rules need explicit padding.
	bottomStr := padLinesTo(bottom.String(), m.width)
	final := topSection + "\n" + bottomStr

	// Remember the frame the mouse is pointing at, then draw the selection over
	// it. The selection is in screen coordinates, so it has to be applied to the
	// composed frame — and the copy on release reads back from this same string,
	// which is why it is kept rather than re-derived.
	m.lastFrame = final
	m.frameRows = strings.Count(final, "\n") + 1
	m.topSectionRowsVal = panelRows
	m.msgTop, m.msgBottom = msgStart, msgStart+msgRows
	final = highlight(final, m.sel, m.chatWidth(), m.width)

	v := tea.NewView(final)
	// The renderer diffs WindowTitle against the last frame and only emits the
	// escape sequence when it changes, then clears it again on teardown. Going
	// through the View is what keeps the sequence ordered against the frame
	// writes — writing it to os.Stdout directly races the renderer and lands
	// mid-frame, which corrupts the drawn output.
	v.WindowTitle = formatTerminalTitleWithCWD(m.sessionTitle, m.cfg.WorkDir)
	if inputCursor != nil {
		inputCursor.Y += inputCursorY
		v.Cursor = inputCursor
	}
	// Stay on the normal terminal screen, but report mouse events so the wheel
	// scrolls the chat viewport (handleMouseWheel) instead of the terminal's
	// scrollback, which only ever showed stale frames of a redrawing UI.
	//
	// This is also what keeps the wheel off the history window: with reporting
	// on, a wheel tick arrives as a MouseWheelMsg and can never be delivered as
	// an Up key, so scrolling and Up stay structurally separate rather than
	// racing over the same event.
	//
	// Reporting takes click-drag away from the terminal, so the terminal can no
	// longer select text for us. Rather than make the user hold a bypass modifier,
	// pi does the selection itself — drag to select, release to copy. See
	// selection.go. CellMotion is the narrowest mode that carries wheel events;
	// Bubble Tea has no wheel-only mode.
	v.AltScreen = false
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// drainTerminalResponses discards any pending terminal response sequences
// (e.g. cursor position reports, DECRQM replies) that may arrive after the
// TUI exits. Without this, late responses leak into the shell prompt as garbage
// like "[14;1R[?2026;2$y".
func drainTerminalResponses() {
	f := os.Stdin
	// Switch stdin to non-blocking so we can read without waiting.
	if err := setNonBlock(f); err != nil {
		return
	}
	defer setBlock(f) //nolint:errcheck

	buf := make([]byte, 256)
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		n, _ := f.Read(buf)
		if n == 0 {
			break
		}
	}
}

const startupProgressBarWidth = 8

func renderStartupMatrixLine(phase int, appVersion string, loadingItems map[string]bool, loadingTotal int, p Palette) string {
	versionSuffix := ""
	if appVersion != "" {
		versionSuffix = " " + appVersion
	}
	progress := renderStartupProgress(loadingItems, loadingTotal)
	detail := renderStartupDetail(loadingItems)
	width := 48
	if width < 1 || len(matrixRunes) == 0 {
		return "Loading Pi" + versionSuffix + progress + detail + " .."
	}
	bright := lipgloss.NewStyle().Foreground(p.Teal).Bold(true)
	mid := lipgloss.NewStyle().Foreground(p.Blue)
	dim := lipgloss.NewStyle().Foreground(p.Surface1)
	accent := lipgloss.NewStyle().Foreground(p.Mauve).Bold(true)

	dotCount := 2 + phase%3
	wave := phase % (2 * (width - 1))
	if wave >= width {
		wave = 2*(width-1) - wave
	}

	var b strings.Builder
	b.WriteString(accent.Render("Loading Pi" + versionSuffix + progress + detail + strings.Repeat(".", dotCount)))
	b.WriteString(" ")
	for i := 0; i < width; i++ {
		r := matrixRunes[(i+phase*7)%len(matrixRunes)]
		delta := i - wave
		if delta < 0 {
			delta = -delta
		}
		switch {
		case delta == 0:
			b.WriteString(bright.Render(string(r)))
		case delta <= 2:
			b.WriteString(mid.Render(string(r)))
		default:
			b.WriteString(dim.Render(string(r)))
		}
	}
	return b.String()
}

func renderStartupProgress(loadingItems map[string]bool, loadingTotal int) string {
	if loadingItems == nil {
		return ""
	}

	done := 0
	for _, itemDone := range loadingItems {
		if itemDone {
			done++
		}
	}

	total := loadingTotal
	if total < len(loadingItems) {
		total = len(loadingItems)
	}
	if total < 1 {
		return fmt.Sprintf(" [%s 0%%]", strings.Repeat("░", startupProgressBarWidth))
	}

	pct := done * 100 / total
	if pct > 100 {
		pct = 100
	}

	filled := done * startupProgressBarWidth / total
	if done > 0 && filled == 0 {
		filled = 1
	}
	if filled > startupProgressBarWidth {
		filled = startupProgressBarWidth
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", startupProgressBarWidth-filled)
	return fmt.Sprintf(" [%s %d%% %d/%d]", bar, pct, done, total)
}

func renderStartupDetail(loadingItems map[string]bool) string {
	if loadingItems == nil {
		return ""
	}
	if len(loadingItems) == 0 {
		return " starting init pipeline"
	}

	var pending []string
	for _, item := range sortedKeys(loadingItems) {
		if !loadingItems[item] {
			pending = append(pending, item)
		}
	}
	if len(pending) == 0 {
		return " finalizing init"
	}
	return " working: " + strings.Join(pending, ", ")
}

func (m *model) applyResize() {
	// Resolve the palette first: UpdateRenderer picks the glamour stylesheet
	// from ChatModel.Palette, and on the first resize View has not run yet, so
	// without this the renderer would be built from the zero palette.
	m.syncPalette()

	// The chat viewport is sized to the panel minus the rail, which owns the
	// last column. The status bar spans the full terminal width — it sits below
	// the sidebar, not beside it — so it gets m.width, not chatWidth.
	chatWidth := m.chatWidth()
	m.statusModel.Width = m.width
	if m.chatModel.Width != chatWidth {
		m.chatModel.UpdateRenderer(chatWidth)
	}
	m.clampScroll()
	// Pre-render or reflow matrix bar so width changes are visible immediately.
	// It sits inside the panel body, so it is sized like everything else there.
	if !m.matrix.active {
		m.matrix.feed("pi-go", chatWidth)
	} else {
		m.matrix.tick(chatWidth)
	}
	// Matrix height can affect the message viewport, so clamp again after it updates.
	m.clampScroll()
	// The search popup exposes a fixed number of item rows.  Recompute that
	// here so a resize keeps the highlighted row inside the new window —
	// otherwise the Up/Down math still trusts the old budget and the
	// selection can land off-screen.
	m.refreshSearchPopupHeight()
}

func (m *model) clampScroll() {
	maxScroll := m.chatModel.MaxScroll(m.messageViewportHeight())
	if m.chatModel.Scroll > maxScroll {
		m.chatModel.Scroll = maxScroll
	}
	if m.chatModel.Scroll < 0 {
		m.chatModel.Scroll = 0
	}
}

func (m *model) messageViewportHeight() int {
	if m.statusModel.Width != m.width {
		m.statusModel.Width = m.width
	}
	statusBar := m.statusModel.Render(m.statusRenderInput())
	inputArea := m.inputModel.View(m.running || m.loading)
	statusLines := strings.Count(statusBar, "\n") + 1
	inputLines := strings.Count(inputArea, "\n") + 1
	// The chrome around the messages: the panel's closing rule plus the two rules
	// that frame the input below it. The two blank rows that inset the messages
	// from those rules are not message rows either. Counting them as such made
	// the panel one row taller than the terminal, so the terminal scrolled the
	// frame and tore the panel away from the sidebar.
	availableHeight := m.height - statusLines - inputLines - 3 - 2
	if m.matrix.render() != "" {
		// The matrix bar adds three rows above the messages (rule, bar, rule).
		availableHeight -= 3
	}
	if m.branchPopup != nil {
		availableHeight -= m.branchPopup.height + 6
	}
	if availableHeight < 1 {
		return 1
	}
	return availableHeight
}

// resizeDraining returns true for a short window after a terminal resize,
// during which key and paste input is suppressed to let terminal response
// sequences (OSC color replies, DECRPM, cursor position reports) drain.
func (m *model) resizeDraining() bool {
	return !m.resizeAt.IsZero() && time.Since(m.resizeAt) < 150*time.Millisecond
}

type resizeDrainDoneMsg struct {
	resizeAt time.Time
}

func resizeDrainDoneCmd(resizeAt time.Time) tea.Cmd {
	if resizeAt.IsZero() {
		return nil
	}
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg {
		return resizeDrainDoneMsg{resizeAt: resizeAt}
	})
}

func isResizeTextFragment(msg tea.KeyPressMsg) bool {
	return msg.Key().Text != ""
}

// mainWidth returns the width of the main panel (excluding sidebar).
func (m *model) mainWidth() int {
	if m.width <= 0 {
		return 1
	}
	if m.width > 80 {
		w := m.width - SidebarWidth
		if w > 0 {
			return w
		}
	}
	return m.width
}

// chatWidth is the width of the left panel's content — everything in it, not
// just messages: the panel minus the rail on its right edge. Content plus rail
// always add up to mainWidth.
func (m *model) chatWidth() int {
	return max(1, m.mainWidth()-railWidth)
}

// topSectionRows returns the number of rows occupied by the top section — the
// messages area plus the sidebar beside it, ending at the horizontal rule above
// the status bar. The status bar and input below span the full width without a
// rail or sidebar. The value is set during View(); call it after a render.
func (m *model) topSectionRows() int {
	return m.topSectionRowsVal
}

func (m *model) eyes() string {
	if m.face != nil {
		return m.face.Eyes()
	}
	return MoodIdle.Eyes()
}

func (m *model) mascot() string {
	if m.face != nil {
		return m.face.Mascot()
	}
	return MoodIdle.Mascot()
}

// artifactList returns the artifacts attached to the current session,
// formatted for the sidebar. Returns nil when no artifact service is
// wired — the sidebar treats nil/empty identically and renders nothing.
//
// This is a stub: the artifact service is plumbed through agent.Config
// in the image-paste feature; once that lands, this method reads the
// service off the runner and translates List+Load responses into
// []ArtifactEntry.
func (m *model) artifactList() []ArtifactEntry {
	return nil
}

// refreshDiffStats updates the git diff line counts.
func (m *model) refreshDiffStats() {
	cwd := m.cwd()
	if cwd == "" {
		return
	}
	cmd := exec.Command("git", "diff", "--numstat", "HEAD")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return
	}
	var added, removed int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var a, r int
		if _, err := fmt.Sscanf(line, "%d\t%d\t", &a, &r); err == nil {
			added += a
			removed += r
		}
	}
	added += countUntrackedLines(cwd)
	m.diffAdded = added
	m.diffRemoved = removed
}

// countUntrackedLines counts total lines across untracked files.
func countUntrackedLines(cwd string) int {
	cmd := exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	total := 0
	for _, file := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if file == "" {
			continue
		}
		wc := exec.Command("wc", "-l", file)
		wc.Dir = cwd
		wcOut, err := wc.Output()
		if err != nil {
			continue
		}
		var lines int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(wcOut)), "%d", &lines); err == nil {
			total += lines
		}
	}
	return total
}

// statusRenderInput builds the StatusRenderInput from the current model state.
func (m *model) statusRenderInput() StatusRenderInput {
	var rc *runCycleInfo
	if m.run != nil && m.run.phase != "done" && m.run.phase != "failed" {
		rc = &runCycleInfo{
			SpecName:   m.run.specName,
			Cycle:      m.run.retries + 1,
			MaxRetries: m.run.maxRetries,
		}
	}
	mode := m.mode
	if mode == "" {
		mode = "chat"
	}
	hostName := cachedHostname()
	return StatusRenderInput{
		ProviderName: m.providerDisplayName(),
		ModelName:    m.cfg.ModelName,
		Running:      m.running,
		Mode:         mode,
		Eyes:         m.eyes(),
		Messages:     m.chatModel.Messages,
		TokenTracker: m.cfg.TokenTracker,
		DiffAdded:    m.diffAdded,
		DiffRemoved:  m.diffRemoved,
		RunCycle:     rc,
		FolderName:   sidebarFolderName(m.cwd()),
		HostName:     hostName,
		LoadingItems: m.loadingItems,
		Flash:        m.flash,
		Palette:      m.palette,
	}
}

// providerDisplayName returns the provider label shown in the status bar
// and sidebar. When the configured provider is "openai" but the loaded
// OPENAI_API_KEY is actually a codex ChatGPT OAuth token (a JWT with the
// OpenAI auth claim), "codex" is shown instead so the user knows which
// credential is active.
func (m *model) providerDisplayName() string {
	name := m.cfg.ProviderName
	if strings.EqualFold(name, "openai") && auth.IsCodexOAuthToken(os.Getenv("OPENAI_API_KEY")) {
		return "codex"
	}
	return name
}

// detectBranch returns the current git branch name, or empty string.
func detectBranch(workDir string) string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	if workDir != "" {
		cmd.Dir = workDir
	}
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// handleBranchSelect switches to the selected branch.
func (m *model) handleBranchSelect() (tea.Model, tea.Cmd) {
	if m.branchPopup == nil || len(m.branchPopup.branches) == 0 {
		m.branchPopup = nil
		return m, nil
	}

	selectedBranch := m.branchPopup.branches[m.branchPopup.selected]

	// Don't switch if already on this branch
	if selectedBranch == m.branchPopup.active {
		m.branchPopup = nil
		return m, nil
	}

	cwd := m.cwd()

	// Run git checkout in the background
	cmd := exec.Command("git", "checkout", selectedBranch)
	if cwd != "" {
		cmd.Dir = cwd
	}

	err := cmd.Run()
	if err != nil {
		m.chatModel.AppendWarning(fmt.Sprintf("Failed to switch branch: %v", err))
	} else {
		m.statusModel.GitBranch = selectedBranch
		m.refreshDiffStats()
	}

	m.branchPopup = nil
	return m, nil
}

// resetCtrlCCount is a tea.Cmd that resets the Ctrl+C counter after a delay.
func resetCtrlCCount(m *model) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(2 * time.Second)
		return resetCtrlCCountMsg{}
	}
}

// msgResetCtrlCCount resets the Ctrl+C counter.
type resetCtrlCCountMsg struct{}

// loadingTickMsg advances the loading dots animation.
type loadingTickMsg struct{}

func (m *model) handleResetCtrlCCount() (tea.Model, tea.Cmd) {
	m.ctrlCCount = 0
	return m, nil
}

// --- Deferred initialization ---

// initEventMsg wraps an InitEvent received from the deferred init channel.
type initEventMsg struct {
	event InitEvent
	ch    <-chan InitEvent
}

// waitForInitEvent returns a Cmd that reads the next event from the init channel.
func waitForInitEvent(ch <-chan InitEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return initEventMsg{event: InitEvent{Err: fmt.Errorf("init channel closed unexpectedly")}, ch: ch}
		}
		return initEventMsg{event: ev, ch: ch}
	}
}

// handleInitEvent processes deferred initialization progress.
func (m *model) handleInitEvent(msg initEventMsg) (tea.Model, tea.Cmd) {
	ev := msg.event

	if ev.Err != nil {
		m.loading = false
		m.loadingItems = nil
		m.loadingTotal = 0
		m.loadingDots = 0
		m.initErr = ev.Err
		return m, tea.Quit
	}

	if ev.Total > 0 {
		m.loadingTotal = ev.Total
	}

	// Track item progress.
	if ev.Item != "" {
		if m.loadingItems == nil {
			m.loadingItems = make(map[string]bool)
		}
		m.loadingItems[ev.Item] = ev.Done
	}

	// Final result: apply all initialized subsystems.
	if ev.Result != nil {
		m.loading = false
		m.loadingItems = nil
		m.loadingTotal = 0
		m.loadingDots = 0

		r := ev.Result
		m.cfg.Agent = r.Agent
		m.cfg.SessionID = r.SessionID
		// Seed the terminal window/tab title with the default (git repo / CWD
		// basename) so the OSC 0 sequence on the next frame already carries a
		// sensible label — before the user types the first prompt. The prompt
		// later overwrites it via applySessionTitle, so this only matters for
		// the pre-prompt frames.
		if m.sessionTitle == "" && r.SessionTitle != "" {
			m.sessionTitle = r.SessionTitle
		}
		m.cfg.SessionService = r.SessionService
		m.cfg.Orchestrator = r.Orchestrator
		m.cfg.Logger = r.Logger
		if r.Logger != nil {
			auth.SetDebugLogger(func(msg string) { r.Logger.Info("auth: " + msg) })
		} else {
			auth.SetDebugLogger(nil)
		}
		// Hook failures must never reach stderr while the TUI holds the
		// alternate screen. Installed unconditionally: with no logger the sink
		// swallows the message, which still beats corrupting the UI.
		extension.SetHookLogger(func(msg string) { r.Logger.Error("hook: " + msg) })
		m.cfg.Skills = r.Skills
		m.cfg.SkillDirs = r.SkillDirs
		m.cfg.GenerateCommitMsg = r.GenerateCommitMsg
		m.cfg.AgentEventCh = r.AgentEventCh
		m.cfg.SystemNoticeCh = r.SystemNoticeCh
		m.cfg.ContextBreakdown = r.ContextBreakdown
		m.cfg.TokenTracker = r.TokenTracker
		m.cfg.CompactMetrics = r.CompactMetrics
		m.statusModel.GitBranch = r.GitBranch
		m.diffAdded = r.DiffAdded
		m.diffRemoved = r.DiffRemoved
		m.cfg.MCPToolsets = r.MCPToolsets
		m.cfg.MCPServers = r.MCPServers

		// Update input model with loaded skills.
		m.inputModel.Skills = r.Skills
		m.inputModel.SkillDirs = r.SkillDirs

		// Rebuild the transcript last: it reads the session service and token
		// tracker that were just installed above.
		if r.Resumed {
			m.restoreSession()
		}

		var cmds []tea.Cmd
		if r.AgentEventCh != nil {
			cmds = append(cmds, waitForSubEvent(r.AgentEventCh))
		}
		if r.SystemNoticeCh != nil {
			cmds = append(cmds, waitForSystemNotice(r.SystemNoticeCh))
		}
		cmds = append(cmds, memoryTickCmd(m.cwd()))
		return m, tea.Batch(cmds...)
	}

	// Keep reading init events.
	return m, waitForInitEvent(msg.ch)
}

func (m *model) shouldShowSlashCommandPopup() bool {
	if m.running || m.loading || m.login != nil || m.commit != nil || m.pendingSkillCreate != nil {
		return false
	}
	text := m.inputModel.Text
	return strings.HasPrefix(text, "/") && !strings.ContainsAny(text, " \t\n\r")
}

func (m *model) overlaySearchPopup(messages string, mainWidth int) string {
	popup := m.renderSearchPopup(max(0, mainWidth-4))
	if popup == "" {
		return messages
	}

	lines := strings.Split(messages, "\n")
	viewportHeight := len(lines)

	popupLines := strings.Split(popup, "\n")
	if viewportHeight > 0 && len(popupLines) > viewportHeight {
		popupLines = popupLines[:viewportHeight]
	}
	if len(popupLines) == 0 {
		return strings.Join(lines, "\n")
	}

	popupWidth := maxLineWidth(popupLines)
	left := 0
	if mainWidth > popupWidth {
		left = (mainWidth - popupWidth) / 2
	}
	start := 0
	if viewportHeight > len(popupLines) {
		start = viewportHeight - len(popupLines) - 1
	}
	if start < 0 {
		start = 0
	}

	for i, line := range popupLines {
		idx := start + i
		if idx >= len(lines) {
			break
		}
		lines[idx] = overlayPopupLine(line, left, mainWidth)
	}
	return strings.Join(lines, "\n")
}

func maxLineWidth(lines []string) int {
	width := 0
	for _, line := range lines {
		width = max(width, lipgloss.Width(line))
	}
	return width
}

func overlayPopupLine(line string, left int, totalWidth int) string {
	if totalWidth <= 0 {
		return line
	}
	if left < 0 {
		left = 0
	}
	prefix := strings.Repeat(" ", left)
	rendered := prefix + line
	if width := lipgloss.Width(rendered); width < totalWidth {
		rendered += strings.Repeat(" ", totalWidth-width)
	}
	return rendered
}

func (m *model) renderSearchPopup(width int) string {
	if m.searchPopup == nil {
		return ""
	}
	if width < 24 {
		width = 24
	}

	sp := m.searchPopup
	bg := m.palette.Surface0

	// Get colors based on mode.
	popupStyle := lipgloss.NewStyle().Background(bg)
	headerStyle := lipgloss.NewStyle().Background(bg).Bold(true)
	searchStyle := lipgloss.NewStyle().Background(bg)
	itemStyle := lipgloss.NewStyle().Background(bg)
	// Each mode below sets its own selection background; this is just the
	// starting point.
	selectedItemStyle := lipgloss.NewStyle().Background(m.palette.Surface0)

	var header string

	switch sp.mode {
	case searchModeCommands:
		border := m.palette.Cyan // cyan for commands
		popupStyle = popupStyle.
			Foreground(m.palette.Subtext).
			Border(lipgloss.RoundedBorder(), true, true, true, true).
			BorderForeground(border).
			Width(width)
		headerStyle = headerStyle.Foreground(m.palette.Subtext).Width(width)
		searchStyle = searchStyle.Foreground(m.palette.Dim)
		itemStyle = itemStyle.Foreground(m.palette.Teal) // teal
		selectedItemStyle = selectedItemStyle.Background(m.palette.Cyan)
		header = "Commands"
	case searchModeHistory:
		border := m.palette.Peach // orange for history
		popupStyle = popupStyle.
			Foreground(m.palette.Subtext).
			Border(lipgloss.RoundedBorder(), true, true, true, true).
			BorderForeground(border).
			Width(width)
		headerStyle = headerStyle.Foreground(m.palette.Subtext).Width(width)
		searchStyle = searchStyle.Foreground(m.palette.Dim)
		itemStyle = itemStyle.Foreground(m.palette.Peach) // orange
		selectedItemStyle = selectedItemStyle.Background(m.palette.Peach)
		header = "History"
	}

	var b strings.Builder

	// Header with count.
	if len(sp.filtered) > 0 {
		header = fmt.Sprintf("%s (%d)", header, len(sp.filtered))
	}
	b.WriteString(headerStyle.Render(header))

	// Search prompt line.
	if sp.search != "" {
		b.WriteString("\n")
		searchLine := fmt.Sprintf("  Search: %s", sp.search)
		b.WriteString(searchStyle.Width(width).Render(clipRunes(searchLine, width)))
	} else {
		b.WriteString("\n")
		b.WriteString(searchStyle.Width(width).Render("  Search... (type to filter)"))
	}

	if len(sp.filtered) == 0 {
		b.WriteString("\n")
		if sp.mode == searchModeCommands {
			b.WriteString(searchStyle.Width(width).Render("  No matching commands"))
		} else {
			b.WriteString(searchStyle.Width(width).Render("  No matching history"))
		}
		return popupStyle.Render(b.String())
	}

	// Item list. Both i and scrollOff+i are bounded by len(filtered) so a
	// stale scrollOff from before a resize cannot index past the end and
	// panic — refreshSearchPopupHeight is the primary guard, this is the
	// belt to its braces.
	for i := 0; i < sp.height; i++ {
		idx := sp.scrollOff + i
		if idx < 0 || idx >= len(sp.filtered) {
			break
		}
		item := sp.filtered[idx]
		prefix := "  "
		currentItemStyle := itemStyle
		if idx == sp.selected {
			// Always highlight the selected item.
			prefix = "> "
			currentItemStyle = selectedItemStyle
		}

		line := prefix + item.Text
		// Add description for commands.
		if item.Description != "" && sp.mode == searchModeCommands {
			desc := clipRunes(item.Description, width*50/100)
			if desc != "" {
				line += "  " + desc
			}
		}

		b.WriteString("\n")
		b.WriteString(currentItemStyle.Width(width).Render(clipRunes(line, width)))
	}

	return popupStyle.Render(b.String())
}

func (m *model) slashCommandCandidates(prefix string) []CompletionCandidate {
	skills := m.inputModel.Skills
	if len(skills) == 0 {
		skills = m.cfg.Skills
	}

	if prefix == "/" {
		return allSlashCommandCandidates(skills)
	}

	workDir := m.inputModel.WorkDir
	if workDir == "" {
		workDir = m.cfg.WorkDir
	}
	result := Complete(prefix, skills, workDir)
	if result == nil || len(result.Candidates) == 0 {
		return nil
	}

	candidates := make([]CompletionCandidate, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		if candidate.Type == CompletionTypeCommand || candidate.Type == CompletionTypeSkill {
			candidates = append(candidates, candidate)
		}
	}
	return candidates
}

func allSlashCommandCandidates(skills []extension.Skill) []CompletionCandidate {
	seen := make(map[string]bool)
	candidates := make([]CompletionCandidate, 0, len(slashCommands)+len(skills))
	for _, cmd := range slashCommands {
		if seen[cmd] {
			continue
		}
		seen[cmd] = true
		candidates = append(candidates, CompletionCandidate{
			Text:        cmd,
			Description: slashCommandDesc(cmd),
			Type:        CompletionTypeCommand,
		})
	}
	for _, skill := range skills {
		cmd := "/" + skill.Name
		if seen[cmd] {
			continue
		}
		seen[cmd] = true
		candidates = append(candidates, CompletionCandidate{
			Text:        cmd,
			Description: skill.Description,
			Type:        CompletionTypeSkill,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return strings.ToLower(candidates[i].Text) < strings.ToLower(candidates[j].Text)
	})
	return candidates
}

func clipRunes(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

// renderBranchPopup renders the branch list popup.
func (m *model) renderBranchPopup() string {
	if m.branchPopup == nil {
		return ""
	}

	popup := m.branchPopup
	bg := m.palette.Surface0
	border := m.palette.Surface
	selected := m.palette.Primary
	activeFg := m.palette.Green
	dimFg := m.palette.Faint

	style := lipgloss.NewStyle().
		Background(bg).
		Foreground(m.palette.Subtext).
		Border(lipgloss.ThickBorder(), true, true, true, true).
		BorderForeground(border).
		Width(m.width - 10)

	// Calculate popup position (centered horizontally, near the bottom)
	popupWidth := m.width - 10

	var b strings.Builder
	b.WriteString("\n")

	// Header
	header := lipgloss.NewStyle().
		Background(bg).
		Foreground(m.palette.Subtext).
		Bold(true).
		Width(popupWidth).
		Align(lipgloss.Center).
		Render("Git Branches (Enter to switch, Esc to close)")
	b.WriteString(header)
	b.WriteString("\n")

	// Render visible branches
	branches := popup.branches
	height := popup.height
	scrollOff := popup.scrollOff

	if len(branches) > height {
		branches = branches[scrollOff : scrollOff+height]
	}

	for i, branch := range branches {
		actualIndex := i + scrollOff
		isSelected := actualIndex == popup.selected
		isActive := branch == popup.active

		var line string
		if isActive {
			line = fmt.Sprintf("  ◉ %s (current)", branch)
		} else {
			line = fmt.Sprintf("    %s", branch)
		}

		if isSelected {
			line = "> " + line[2:] // Replace leading spaces with ">"
		}

		var lineStyle lipgloss.Style
		switch {
		case isSelected:
			lineStyle = lipgloss.NewStyle().Background(selected).Foreground(m.palette.White)
		case isActive:
			lineStyle = lipgloss.NewStyle().Background(bg).Foreground(activeFg)
		default:
			lineStyle = lipgloss.NewStyle().Background(bg).Foreground(dimFg)
		}

		b.WriteString(lineStyle.Width(popupWidth).Render(line))
		b.WriteString("\n")
	}

	// Show scroll indicator if needed
	if len(popup.branches) > popup.height {
		scrollStyle := lipgloss.NewStyle().Background(bg).Foreground(dimFg)
		b.WriteString(scrollStyle.Render("  ↑↓ scroll"))
	}

	return style.Render(b.String())
}
