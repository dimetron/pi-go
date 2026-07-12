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
	"github.com/charmbracelet/glamour"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/otel"
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

	// Agent state.
	running     bool
	mode        string             // "chat" or "plan" — shown in status bar
	agentCh     chan agentMsg      // channel for receiving agent events
	agentCancel context.CancelFunc // cancels the active agent response without quitting the TUI

	// Agent face renderer with mood expressions.
	face *FaceRenderer

	// Matrix rain animation state for sidebar.
	matrix matrixState

	// Mouse text selection. lastFrame is the frame the selection's coordinates
	// refer to — pi selects text itself (see selection.go), so it needs the
	// pixels it drew in order to know what is under the cursor. frameRows is its
	// height, which is what converts a mouse row into a frame row.
	sel       selection
	lastFrame string
	frameRows int
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

// newSearchPopup creates a unified search popup with the given mode.
func (m *model) newSearchPopup(mode searchMode) {
	var items []SearchItem
	var popupHeight int

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
		popupHeight = searchPopupListHeight(len(items))

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
		popupHeight = searchPopupListHeight(len(items))
	}

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

func searchPopupListHeight(itemCount int) int {
	if itemCount <= 0 {
		return 0
	}
	height := itemCount
	if height > 25 {
		height = 25
	}
	if height < 3 {
		height = 3
	}
	return height
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

// Run starts the interactive TUI.
func Run(ctx context.Context, cfg Config) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
		glamour.WithEmoji(),
	)

	// Load persistent command history from ~/.pi-go/history.jsonl.
	history := loadHistory()
	if history == nil {
		history = make([]HistoryEntry, 0)
	}

	// Initialize theme manager.
	tm := NewThemeManager()
	if cfg.ThemeName != "" && cfg.ThemeName != "default" {
		_ = tm.SetTheme(cfg.ThemeName) // ignore error, falls back to tokyo-night
	}

	m := model{
		cfg:          cfg,
		ctx:          ctx,
		cancel:       cancel,
		inputModel:   NewInputModel(history, cfg.Skills, cfg.SkillDirs, cfg.WorkDir),
		chatModel:    NewChatModel(renderer),
		statusModel:  StatusModel{},
		themeManager: tm,
		face:         NewFaceRenderer(),
	}

	if cfg.DeferredInit != nil {
		m.loading = true
		m.loadingItems = make(map[string]bool)
		m.initCh = cfg.DeferredInit
	} else {
		m.statusModel.GitBranch = detectBranch(cfg.WorkDir)
	}

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
	if m.initCh != nil {
		// Deferred init: start listening for init events.
		// Heavy initialization runs in a background goroutine (started by cli).
		return tea.Batch(
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
	cmds = append(cmds, memoryTickCmd(m.cwd()))
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resizeAt = time.Now()
		m.width = msg.Width
		m.height = msg.Height
		m.applyResize()
		cmd := resizeDrainDoneCmd(m.resizeAt)
		if m.running {
			return m, tea.Batch(cmd, waitForAgent(m.agentCh))
		}
		return m, cmd

	case tea.PasteMsg:
		if !m.running && !m.resizeDraining() && isUserPaste(msg.Content) {
			m.inputModel.InsertText(msg.Content)
		}
		if m.resizeDraining() {
			return m, resizeDrainDoneCmd(m.resizeAt)
		}

	case tea.KeyPressMsg:
		if m.resizeDraining() && isResizeTextFragment(msg) {
			return m, resizeDrainDoneCmd(m.resizeAt)
		}
		return m.handleKey(msg)

	case flashExpiredMsg:
		// Ignore a timer whose flash has already been replaced.
		if msg.seq == m.flashSeq {
			m.flash = ""
		}
		return m, nil

	case tea.MouseMsg:
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

	case InputSubmitMsg:
		if strings.HasPrefix(msg.Text, "/") {
			return m.handleSlashCommand(msg.Text)
		}
		return m.submitPrompt(msg.Text, msg.Mentions)

	case initEventMsg:
		return m.handleInitEvent(msg)

	case restartMsg:
		execRestart()
		return m, tea.Quit

	case agentThinkingMsg:
		return m.handleAgentThinking(msg)

	case resetCtrlCCountMsg:
		return m.handleResetCtrlCCount()

	case resizeDrainDoneMsg:
		if msg.resizeAt.Equal(m.resizeAt) {
			m.resizeAt = time.Time{}
		}
		return m, nil

	case loadingTickMsg:
		m.loadingDots = (m.loadingDots + 1) % 4
		return m, tea.Tick(300*time.Millisecond, func(t time.Time) tea.Msg { return loadingTickMsg{} })

	case agentTextMsg:
		return m.handleAgentText(msg)

	case agentToolCallMsg:
		return m.handleAgentToolCall(msg)

	case agentToolResultMsg:
		return m.handleAgentToolResult(msg)

	case agentSubEventMsg:
		return m.handleAgentSubEvent(msg)

	case agentDoneMsg:
		return m.handleAgentDone(msg)

	case runAgentEventMsg:
		return m.handleRunAgentEvent(msg)

	case runAgentDoneMsg:
		return m.handleRunAgentDone(msg)

	case runGateResultMsg:
		return m.handleRunGateResult(msg)

	case runMergeResultMsg:
		return m.handleRunMergeResult(msg)

	case loginSSOResultMsg:
		return m.handleLoginSSOResult(msg)

	case commitGeneratedMsg:
		return m.handleCommitGenerated(msg)

	case matrixTickMsg:
		if m.running {
			m.matrix.tick(m.mainWidth())
			return m, matrixTickCmd()
		}
		return m, nil

	case commitDoneMsg:
		return m.handleCommitDone(msg)

	case memoryTickMsg:
		m.memoryStatus = msg.status
		return m, tea.Tick(memoryTickInterval, func(t time.Time) tea.Msg {
			return memoryTickCmd(m.cwd())()
		})

	case pingDoneMsg:
		content := msg.output
		if msg.err != nil {
			content += fmt.Sprintf("\n\n✗ Ping failed: %v", msg.err)
		}
		// Replace the "Pinging model..." thinking placeholder with assistant response.
		if len(m.chatModel.Messages) > 0 && m.chatModel.Messages[len(m.chatModel.Messages)-1].role == "thinking" {
			m.chatModel.Messages[len(m.chatModel.Messages)-1] = message{role: "assistant", content: content}
		} else {
			m.chatModel.Messages = append(m.chatModel.Messages, message{role: "assistant", content: content})
		}
		// Update matrix rain with the model's reply.
		if msg.reply != "" {
			m.matrix.feed(msg.reply, m.mainWidth())
		}
		return m, nil
	}

	// Keep the agent listener alive for any unhandled message types.
	if m.running {
		return m, waitForAgent(m.agentCh)
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

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.Key()

	// Handle commit confirmation mode first.
	if !m.running && m.commit != nil && m.commit.phase == "confirming" {
		switch {
		case key.Code == tea.KeyEnter:
			return m.handleCommitConfirm()
		case key.Code == tea.KeyEsc:
			return m.handleCommitCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handleCommitCancel()
		default:
			return m, nil
		}
	}

	// Handle login flow.
	if !m.running && m.login != nil {
		switch {
		case key.Code == tea.KeyEsc:
			return m.handleLoginCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handleLoginCancel()
		case key.Code == tea.KeyEnter && m.login.phase == "waiting":
			apiKey := strings.TrimSpace(m.inputModel.Text)
			if apiKey == "" {
				return m, nil
			}
			m.inputModel.Clear()
			return m.handleLoginSave(apiKey)
		case key.Code == tea.KeyEnter && m.login.phase == "manual-code":
			code := strings.TrimSpace(m.inputModel.Text)
			if code == "" {
				return m, nil
			}
			m.inputModel.Clear()
			return m.handleLoginCodeSubmit(code)
		}
		if m.login.phase != "waiting" && m.login.phase != "manual-code" {
			return m, nil
		}
	}

	// Handle skill-create overwrite confirmation.
	if !m.running && m.pendingSkillCreate != nil {
		switch {
		case key.Code == tea.KeyEnter:
			return m.handleSkillCreateConfirm()
		case key.Code == tea.KeyEsc:
			return m.handleSkillCreateCancel()
		case key.Code == 'c' && key.Mod == tea.ModCtrl:
			return m.handleSkillCreateCancel()
		default:
			return m, nil
		}
	}

	// Handle branch popup.
	if m.branchPopup != nil {
		switch key.Code {
		case tea.KeyEsc:
			m.branchPopup = nil
			return m, nil
		case tea.KeyEnter:
			return m.handleBranchSelect()
		case tea.KeyUp:
			if m.branchPopup.selected > 0 {
				m.branchPopup.selected--
				if m.branchPopup.selected < m.branchPopup.scrollOff {
					m.branchPopup.scrollOff--
				}
			}
			return m, nil
		case tea.KeyDown:
			if m.branchPopup.selected < len(m.branchPopup.branches)-1 {
				m.branchPopup.selected++
				if m.branchPopup.selected >= m.branchPopup.scrollOff+m.branchPopup.height {
					m.branchPopup.scrollOff++
				}
			}
			return m, nil
		default:
			// Any other key dismisses the popup
			m.branchPopup = nil
			return m, nil
		}
	}

	// Esc / Ctrl+C: dismiss completion, cancel agent, or quit.
	switch {
	case key.Code == tea.KeyEsc:
		if m.searchPopup != nil {
			m.searchPopup = nil
			return m, nil
		}
		if m.running {
			m.cancelAgent()
			return m, nil
		}
		return m, nil

	case key.Code == 'c' && key.Mod == tea.ModCtrl:
		if m.running {
			m.cancelAgent()
			m.ctrlCCount++
			m.chatModel.AppendWarning("\nCtrl+C again to quit (or wait 2s)...")
			return m, resetCtrlCCount(m)
		}
		m.ctrlCCount++
		if m.ctrlCCount >= 2 {
			m.quitting = true
			return m, tea.Quit
		}
		// First press: show warning and reset count after 2 seconds
		m.chatModel.AppendWarning("\nCtrl+C again to quit (or wait 2s)...")
		return m, resetCtrlCCount(m)

	case key.Code == tea.KeyF12:
		return m, nil
	}

	if m.running || m.loading {
		return m, nil
	}

	// Ctrl+O: toggle compact/expanded tool output.
	if key.Code == 'o' && key.Mod == tea.ModCtrl {
		m.chatModel.ToolDisplay.CompactTools = !m.chatModel.ToolDisplay.CompactTools
		return m, nil
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
		return m, nil
	}

	// Handle unified search popup keys (slash commands or history).
	if m.handleSearchPopupKey(key) {
		return m, nil
	}

	// Ctrl+R: open history search popup (reverse-i-search style).
	if key.Code == 'r' && key.Mod == tea.ModCtrl && m.searchPopup == nil {
		if len(m.inputModel.History) > 0 {
			m.newSearchPopup(searchModeHistory)
			return m, nil
		}
	}

	// Up opens the prompt-history window; Down scrolls the chat.
	//
	// Up used to cycle history inline, replacing whatever was typed. The window
	// shows the whole history at once and leaves the prompt untouched until an
	// entry is chosen, so a stray Up costs one Esc rather than your draft.
	//
	// The mouse wheel cannot land here: View enables mouse reporting, so a wheel
	// tick is a MouseWheelMsg handled by handleMouseWheel, never a KeyUp. Up
	// still scrolls the chat before any history exists, so the keyboard can
	// scroll on a fresh session.
	//
	// A prompt starting with "/" is excluded: those arrows drive the slash-command
	// popup handled further down.
	if m.searchPopup == nil && !m.shouldShowSlashCommandPopup() {
		switch key.Code {
		case tea.KeyUp:
			if len(m.inputModel.History) == 0 {
				m.chatModel.ScrollUp(3, m.height)
				return m, nil
			}
			m.newSearchPopup(searchModeHistory)
			return m, nil
		case tea.KeyDown:
			m.chatModel.ScrollDown(3)
			return m, nil
		}
	}

	// Scroll keys stay in root model.
	switch key.Code {
	case tea.KeyPgUp:
		m.chatModel.ScrollUp(5, m.height)
		return m, nil

	case tea.KeyPgDown:
		m.chatModel.ScrollDown(5)
		return m, nil
	}

	// Slash command: show commands popup when input starts with /.
	if m.shouldShowSlashCommandPopup() {
		if m.searchPopup == nil || m.searchPopup.mode != searchModeCommands {
			m.newSearchPopup(searchModeCommands)
		}
		// Immediately handle Tab/Up/Down to navigate the popup.
		if key.Code == tea.KeyTab || key.Code == tea.KeyUp || key.Code == tea.KeyDown {
			if m.handleSearchPopupKey(key) {
				return m, nil
			}
		}
	}

	// Delegate all other keys to InputModel.
	prevText := m.inputModel.Text
	cmd := m.inputModel.HandleKey(msg)
	// Reset search popup when input text changes (slash commands mode).
	if m.inputModel.Text != prevText {
		if m.searchPopup != nil && m.searchPopup.mode == searchModeCommands {
			if !m.shouldShowSlashCommandPopup() {
				m.searchPopup = nil
			}
		}
		if m.searchPopup == nil && m.shouldShowSlashCommandPopup() {
			m.newSearchPopup(searchModeCommands)
		}
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

	if m.width == 0 {
		// Show matrix-style startup text before the first terminal size arrives.
		matrixLine := renderStartupMatrixLine(m.loadingDots, m.cfg.AppVersion, m.loadingItems, m.loadingTotal)
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

	// Layout: sidebar on the right, chat+status+input on the left. The chat is
	// panel body is one column narrower than mainWidth; the rail on its right
	// edge is both the minimap and the divider from the sidebar.
	mainWidth := m.mainWidth()
	if m.statusModel.Width != m.chatWidth() || m.chatModel.Width != m.chatWidth() {
		m.applyResize()
		mainWidth = m.mainWidth()
	}
	bodyWidth := m.chatWidth()
	sidebarWidth := m.width - mainWidth
	showSidebar := sidebarWidth > 0

	// Render components.
	m.inputModel.SetWidth(max(0, bodyWidth-2))
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
		visibleMessages += "\n"
		visibleLineCount++
	}
	visibleMessages = m.overlaySearchPopup(visibleMessages, bodyWidth)

	// Note: width constraint is handled by glamour's WithWordWrap(contentWidth) in chatModel.UpdateRenderer.
	// lipgloss.Width() counts raw bytes including invisible ANSI codes, causing wrapping issues.

	// Render matrix rain as full-width top bar (when active).
	matrixBar := m.matrix.render()

	// Horizontal rule for separating sections.
	hrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70")) // Catppuccin Mocha surface2
	hr := hrStyle.Render(strings.Repeat("─", bodyWidth))

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

	b.WriteString(hr)
	b.WriteString("\n")
	b.WriteString(statusBar)
	b.WriteString("\n")
	b.WriteString(hr)
	b.WriteString("\n")
	inputCursorY := strings.Count(b.String(), "\n")
	b.WriteString(inputArea)
	b.WriteString("\n")
	b.WriteString(hr)

	// Three columns, composed like columns: body | rail | sidebar.
	//
	// The body is pinned to exactly bodyWidth. Without that, JoinHorizontal sizes
	// it to its widest *visible* line, so the rail and the sidebar behind it slid
	// left and right as scrolling changed which lines were on screen.
	//
	// The rail carries the minimap across the message rows and a plain divider
	// elsewhere, so it doubles as the border the sidebar used to draw itself.
	body := padLinesTo(b.String(), bodyWidth)
	panelRows := strings.Count(body, "\n") + 1
	rail := railColumn(panelRows, msgStart, renderMinimap(lineKinds, startLine, endLine, msgRows))
	leftPanel := lipgloss.JoinHorizontal(lipgloss.Top, body, rail)

	var final string
	if showSidebar {
		hostName, _ := os.Hostname()
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
			OTELEnabled:  otel.IsEnabled(),
		}
		if m.run != nil && m.run.phase != "" {
			sidebarInput.RunChecklist = m.run.checklist
			sidebarInput.RunPhase = m.run.phase
			sidebarInput.RunSpec = m.run.specName
			sidebarInput.RunCycle = m.run.retries + 1
			sidebarInput.RunMaxCycle = m.run.maxRetries
		}
		sidebar := RenderSidebar(sidebarInput)
		final = lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, sidebar)
	} else {
		final = leftPanel
	}

	// Remember the frame the mouse is pointing at, then draw the selection over
	// it. The selection is in screen coordinates, so it has to be applied to the
	// composed frame — and the copy on release reads back from this same string,
	// which is why it is kept rather than re-derived.
	m.lastFrame = final
	m.frameRows = strings.Count(final, "\n") + 1
	m.msgTop, m.msgBottom = msgStart, msgStart+msgRows
	final = highlight(final, m.sel, m.chatWidth(), m.width)

	v := tea.NewView(final)
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

func renderStartupMatrixLine(phase int, appVersion string, loadingItems map[string]bool, loadingTotal int) string {
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
	bright := lipgloss.NewStyle().Foreground(lipgloss.Color("#94e2d5")).Bold(true)
	mid := lipgloss.NewStyle().Foreground(lipgloss.Color("#89b4fa"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#45475a"))
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#cba6f7")).Bold(true)

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
	// Everything in the left panel — messages and status bar alike — is sized to
	// the panel minus the rail, which owns the last column.
	chatWidth := m.chatWidth()
	m.statusModel.Width = chatWidth
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
	if chatWidth := m.chatWidth(); m.statusModel.Width != chatWidth {
		m.statusModel.Width = chatWidth
	}
	statusBar := m.statusModel.Render(m.statusRenderInput())
	inputArea := m.inputModel.View(m.running || m.loading)
	statusLines := strings.Count(statusBar, "\n") + 1
	inputLines := strings.Count(inputArea, "\n") + 1
	// The two blank rows that inset the messages from the rules above and below
	// are not message rows. Counting them as such made the panel one row taller
	// than the terminal, so the terminal scrolled the frame and tore the panel
	// away from the sidebar.
	availableHeight := m.height - statusLines - inputLines - 4 - 2
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
	hostName, _ := os.Hostname()
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
		m.cfg.SessionService = r.SessionService
		m.cfg.Orchestrator = r.Orchestrator
		m.cfg.Logger = r.Logger
		if r.Logger != nil {
			auth.SetDebugLogger(func(msg string) { r.Logger.Info("auth: " + msg) })
		} else {
			auth.SetDebugLogger(nil)
		}
		m.cfg.Skills = r.Skills
		m.cfg.SkillDirs = r.SkillDirs
		m.cfg.GenerateCommitMsg = r.GenerateCommitMsg
		m.cfg.AgentEventCh = r.AgentEventCh
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

		var cmds []tea.Cmd
		if r.AgentEventCh != nil {
			cmds = append(cmds, waitForSubEvent(r.AgentEventCh))
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
	bg := lipgloss.Color("236")

	// Get colors based on mode.
	popupStyle := lipgloss.NewStyle().Background(bg)
	headerStyle := lipgloss.NewStyle().Background(bg).Bold(true)
	searchStyle := lipgloss.NewStyle().Background(bg)
	itemStyle := lipgloss.NewStyle().Background(bg)
	selectedItemStyle := lipgloss.NewStyle().Background(lipgloss.Color("15"))

	var header string

	switch sp.mode {
	case searchModeCommands:
		border := lipgloss.Color("33") // cyan for commands
		popupStyle = popupStyle.
			Foreground(lipgloss.Color("252")).
			Border(lipgloss.RoundedBorder(), true, true, true, true).
			BorderForeground(border).
			Width(width)
		headerStyle = headerStyle.Foreground(lipgloss.Color("252")).Width(width)
		searchStyle = searchStyle.Foreground(lipgloss.Color("245"))
		itemStyle = itemStyle.Foreground(lipgloss.Color("81")) // teal
		selectedItemStyle = selectedItemStyle.Background(lipgloss.Color("33"))
		header = "Commands"
	case searchModeHistory:
		border := lipgloss.Color("208") // orange for history
		popupStyle = popupStyle.
			Foreground(lipgloss.Color("252")).
			Border(lipgloss.RoundedBorder(), true, true, true, true).
			BorderForeground(border).
			Width(width)
		headerStyle = headerStyle.Foreground(lipgloss.Color("252")).Width(width)
		searchStyle = searchStyle.Foreground(lipgloss.Color("245"))
		itemStyle = itemStyle.Foreground(lipgloss.Color("208")) // orange
		selectedItemStyle = selectedItemStyle.Background(lipgloss.Color("208"))
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

	// Item list.
	for i := 0; i < sp.height && i < len(sp.filtered); i++ {
		idx := sp.scrollOff + i
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
	bg := lipgloss.Color("236")
	border := lipgloss.Color("240")
	selected := lipgloss.Color("33")
	activeFg := lipgloss.Color("35")
	dimFg := lipgloss.Color("243")

	style := lipgloss.NewStyle().
		Background(bg).
		Foreground(lipgloss.Color("252")).
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
		Foreground(lipgloss.Color("252")).
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
			line = fmt.Sprintf("  ● %s (current)", branch)
		} else {
			line = fmt.Sprintf("    %s", branch)
		}

		if isSelected {
			line = "> " + line[2:] // Replace leading spaces with ">"
		}

		var lineStyle lipgloss.Style
		switch {
		case isSelected:
			lineStyle = lipgloss.NewStyle().Background(selected).Foreground(lipgloss.Color("15"))
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
