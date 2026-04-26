// Package tui implements the interactive terminal UI using Bubble Tea v2.
package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

	tea "charm.land/bubbletea/v2"

	"github.com/dimetron/pi-go/internal/auth"
	"github.com/dimetron/pi-go/internal/extension"
	"github.com/dimetron/pi-go/internal/otel"
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
	running bool
	mode    string        // "chat" or "plan" — shown in status bar
	agentCh chan agentMsg // channel for receiving agent events

	// Agent face renderer with mood expressions.
	face *FaceRenderer

	// Matrix rain animation state for sidebar.
	matrix matrixState

	// Deferred initialization state.
	loading      bool
	loadingItems map[string]bool // item name -> done?
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

	// Quit.
	quitting bool
	initErr  error // fatal init error → propagated from Run()

	// Ctrl+C handling: show warning on first press, quit on second.
	ctrlCCount int

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
	return tea.Batch(cmds...)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resizeAt = time.Now()
		m.width = msg.Width
		m.height = msg.Height
		m.applyResize()

	case tea.PasteMsg:
		if !m.running && !m.resizeDraining() && isUserPaste(msg.Content) {
			m.inputModel.InsertText(msg.Content)
		}

	case tea.KeyPressMsg:
		if m.resizeDraining() {
			return m, nil
		}
		return m.handleKey(msg)

	case tea.MouseMsg:
		switch msg := msg.(type) {
		case tea.MouseClickMsg:
			return m.handleMouseClick(msg)
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

// handleMouseClick processes mouse click events.
func (m *model) handleMouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	return m, nil
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
		if m.inputModel.InCompletionMode() {
			m.inputModel.DismissCompletion()
			return m, nil
		}
		if m.running {
			m.cancelAgent()
			return m, nil
		}
		return m, nil

	case key.Code == 'c' && key.Mod == tea.ModCtrl:
		if m.inputModel.InCompletionMode() {
			m.inputModel.DismissCompletion()
			return m, nil
		}
		if m.running {
			m.cancelAgent()
			return m, nil
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

	// Ctrl+B: toggle branch popup.
	if key.Code == 'b' && key.Mod == tea.ModCtrl {
		if m.statusModel.GitBranch != "" {
			if m.branchPopup == nil {
				m.newBranchPopup()
			} else {
				m.branchPopup = nil
			}
		}
		return m, nil
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

	// Delegate all other keys to InputModel.
	cmd := m.inputModel.HandleKey(msg)
	return m, cmd
}

func (m *model) View() tea.View {
	if m.quitting {
		return tea.NewView("Goodbye!\n")
	}

	if m.width == 0 {
		// Show matrix-style startup text before the first terminal size arrives.
		matrixLine := renderStartupMatrixLine(m.loadingDots)
		if m.loadingItems != nil {
			var lines []string
			lines = append(lines, matrixLine)
			for item, done := range m.loadingItems {
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

	// Layout: sidebar on the right, chat+status+input on the left.
	mainWidth := m.mainWidth()
	if m.statusModel.Width != mainWidth || m.chatModel.Width != mainWidth {
		m.applyResize()
		mainWidth = m.mainWidth()
	}
	sidebarWidth := m.width - mainWidth
	showSidebar := sidebarWidth > 0

	// Render components.
	messagesView := m.chatModel.RenderMessages(m.running)
	statusBar := m.statusModel.Render(m.statusRenderInput())
	inputArea := m.inputModel.View(m.running || m.loading)

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

	// Pad to fill available space, leaving 1 blank line between messages and status bar.
	visibleLineCount := strings.Count(visibleMessages, "\n") + 1
	for visibleLineCount < availableHeight-1 {
		visibleMessages += "\n"
		visibleLineCount++
	}

	// Note: width constraint is handled by glamour's WithWordWrap(contentWidth) in chatModel.UpdateRenderer.
	// lipgloss.Width() counts raw bytes including invisible ANSI codes, causing wrapping issues.

	// Render matrix rain as full-width top bar (when active).
	matrixBar := m.matrix.render()

	// Horizontal rule for separating sections.
	hrStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#585b70")) // Catppuccin Mocha surface2
	hr := hrStyle.Render(strings.Repeat("─", mainWidth))

	var b strings.Builder
	if matrixBar != "" {
		b.WriteString(hr)
		b.WriteString("\n")
		b.WriteString(matrixBar)
		b.WriteString("\n")
		b.WriteString(hr)
		b.WriteString("\n")
	} else {
		b.WriteString("\n") // blank line at top when no matrix
	}
	b.WriteString(visibleMessages)

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
	b.WriteString(inputArea)
	b.WriteString("\n")
	b.WriteString(hr)

	leftPanel := b.String()

	var final string
	if showSidebar {
		sidebarInput := SidebarRenderInput{
			Width:        sidebarWidth,
			Height:       m.height,
			Mascot:       m.mascot(),
			Mode:         m.mode,
			ProviderName: m.providerDisplayName(),
			ModelName:    m.cfg.ModelName,
			GitBranch:    m.statusModel.GitBranch,
			DiffAdded:    m.diffAdded,
			DiffRemoved:  m.diffRemoved,
			Running:      m.running,
			TokenTracker: m.cfg.TokenTracker,
			Messages:     m.chatModel.Messages,
			ActiveTool:   m.statusModel.ActiveTool,
			LoadingItems: m.loadingItems,
			MatrixLines:  "",
			StatusLine:   "",
			Orchestrator: m.cfg.Orchestrator,
			MCPTools:     extension.BuildMCPToolEntries(m.cfg.MCPToolsets),
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

	v := tea.NewView(final)
	v.AltScreen = true
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

func renderStartupMatrixLine(phase int) string {
	width := 48
	if width < 1 || len(matrixRunes) == 0 {
		return "Loading .."
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
	b.WriteString(accent.Render("Loading" + strings.Repeat(".", dotCount)))
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

func (m *model) applyResize() {
	mainWidth := m.mainWidth()
	m.statusModel.Width = mainWidth
	if m.chatModel.Width != mainWidth {
		m.chatModel.UpdateRenderer(mainWidth)
	}
	m.clampScroll()
	// Pre-render or reflow matrix bar so width changes are visible immediately.
	if !m.matrix.active {
		m.matrix.feed("pi-go", mainWidth)
	} else {
		m.matrix.tick(mainWidth)
	}
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
	mainWidth := m.mainWidth()
	if m.statusModel.Width != mainWidth {
		m.statusModel.Width = mainWidth
	}
	statusBar := m.statusModel.Render(m.statusRenderInput())
	inputArea := m.inputModel.View(m.running || m.loading)
	statusLines := strings.Count(statusBar, "\n") + 1
	inputLines := strings.Count(inputArea, "\n") + 1
	availableHeight := m.height - statusLines - inputLines - 4
	if m.matrix.render() != "" {
		availableHeight -= 2
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
		LoadingItems: m.loadingItems,
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
		m.loadingDots = 0
		m.initErr = ev.Err
		return m, tea.Quit
	}

	// Track item progress.
	if ev.Item != "" {
		m.loadingItems[ev.Item] = ev.Done
	}

	// Final result: apply all initialized subsystems.
	if ev.Result != nil {
		m.loading = false
		m.loadingItems = nil
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
		return m, tea.Batch(cmds...)
	}

	// Keep reading init events.
	return m, waitForInitEvent(msg.ch)
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
