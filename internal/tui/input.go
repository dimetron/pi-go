package tui

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dimetron/pi-go/internal/extension"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// charOffsetToByteOffset converts a UTF-8 character offset within a string
// to a byte offset. Returns 0 if pos is out of bounds.
func charOffsetToByteOffset(s string, charPos int) int {
	if charPos <= 0 {
		return 0
	}
	byteOffset := 0
	for i := 0; i < charPos && byteOffset < len(s); {
		_, size := utf8.DecodeRuneInString(s[byteOffset:])
		if size == 0 {
			break
		}
		byteOffset += size
		i++
	}
	return byteOffset
}

// terminalResponseRe matches common terminal response fragments that leak
// through as text: CSI params (digits, semicolons, question marks) ending
// with a letter, DECRPM ($y), OSC color payloads (rgb:/hex colons+slashes),
// and cursor position reports.
var terminalResponseRe = regexp.MustCompile(
	`\[\d+;\d+[A-Z]` + // CSI CPR like [38;4R
		`|\d+\$[A-Za-z]` + // DECRPM tails like ;2$y
		`|[0-9a-f]{4}/[0-9a-f]{4}/[0-9a-f]{4}` + // hex triplet XXXX/XXXX/XXXX
		`|rgb:` + // OSC color payload
		`|\]\d+;`, // OSC intro like ]11;
)

// InputSubmitMsg is emitted when the user presses Enter with non-empty input.
type InputSubmitMsg struct {
	Text     string
	Mentions []string // file paths referenced via @path
}

// InputModel manages the text input area: cursor, history, and completion.
type InputModel struct {
	Text      string
	CursorPos int // character position (not byte offset)
	Width     int // terminal width for rendering, 0 = unlimited

	History    []HistoryEntry
	HistoryIdx int

	// Ghost autocomplete suggestion.
	Completion string

	// Enhanced completion state.
	CompletionResult *CompleteResult
	CompletionMode   bool
	SelectedIndex    int

	// Command cycling state.
	CyclingIdx int

	// File @mention completion state.
	MentionMode          bool
	MentionStart         int // cursor position of the '@' character
	MentionResult        *CompleteResult
	MentionSelectedIndex int

	// Dependencies (set by root model).
	Skills    []extension.Skill
	SkillDirs []string
	WorkDir   string
}

// NewInputModel creates an InputModel with initial state.
func NewInputModel(history []HistoryEntry, skills []extension.Skill, skillDirs []string, workDir string) InputModel {
	return InputModel{
		History:    history,
		HistoryIdx: -1,
		CyclingIdx: -1,
		Skills:     skills,
		SkillDirs:  skillDirs,
		WorkDir:    workDir,
	}
}

// SetWidth sets the terminal width for rendering and invalidates the
// cached ghost autocomplete suggestion when width changes.
func (im *InputModel) SetWidth(width int) {
	if im.Width != width {
		im.Completion = ""
	}
	im.Width = width
}

// HandleKey processes a key press for the input area.
// Returns a tea.Cmd (InputSubmitMsg on submit, nil otherwise).
func (im *InputModel) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	key := msg.Key()

	switch {
	case key.Code == tea.KeyEnter:
		// Mention mode: apply file selection, don't submit.
		if im.MentionMode && im.MentionResult != nil && len(im.MentionResult.Candidates) > 0 {
			selected := im.MentionResult.Candidates[im.MentionSelectedIndex].Text
			// Replace @prefix with @selected-path
			beforeByte := charOffsetToByteOffset(im.Text, im.MentionStart)
			afterByte := charOffsetToByteOffset(im.Text, im.CursorPos)
			im.Text = im.Text[:beforeByte] + "@" + selected + im.Text[afterByte:]
			im.CursorPos = im.MentionStart + 1 + utf8.RuneCountInString(selected)
			im.dismissMention()
			return nil
		}
		// Cycling: place command, dismiss menu.
		if im.CyclingIdx >= 0 {
			im.CyclingIdx = -1
			im.CursorPos = utf8.RuneCountInString(im.Text)
			return nil
		}
		// Completion: apply selection.
		if im.CompletionMode && im.CompletionResult != nil && len(im.CompletionResult.Candidates) > 0 {
			im.Text = im.CompletionResult.ApplySelection(im.SelectedIndex)
			im.CursorPos = utf8.RuneCountInString(im.Text)
			im.CompletionMode = false
			im.CompletionResult = nil
			im.SelectedIndex = 0
			return nil
		}
		// Submit.
		text := strings.TrimSpace(im.Text)
		if text == "" {
			return nil
		}
		mentions := extractMentions(text)
		entry := HistoryEntry{Text: text, Mentions: mentions}
		if len(im.History) == 0 || im.History[len(im.History)-1].Text != text {
			im.History = append(im.History, entry)
			appendHistory(entry)
		}
		im.HistoryIdx = -1
		im.Text = ""
		im.CursorPos = 0
		return func() tea.Msg { return InputSubmitMsg{Text: text, Mentions: mentions} }

	case key.Code == tea.KeyTab && key.Mod == tea.ModShift:
		if im.MentionMode && im.MentionResult != nil && len(im.MentionResult.Candidates) > 0 {
			im.MentionResult.CycleSelection(-1)
			im.MentionSelectedIndex = im.MentionResult.Selected
			return nil
		}
		if im.CompletionMode && im.CompletionResult != nil && len(im.CompletionResult.Candidates) > 0 {
			im.CompletionResult.CycleSelection(-1)
			im.SelectedIndex = im.CompletionResult.Selected
		} else if im.Text == "/" || im.CyclingIdx >= 0 {
			im.cycleCommand(-1)
		}

	case key.Code == tea.KeyTab:
		if im.MentionMode && im.MentionResult != nil && len(im.MentionResult.Candidates) > 0 {
			im.MentionResult.CycleSelection(1)
			im.MentionSelectedIndex = im.MentionResult.Selected
			return nil
		}
		if im.CompletionMode && im.CompletionResult != nil && len(im.CompletionResult.Candidates) > 0 {
			im.CompletionResult.CycleSelection(1)
			im.SelectedIndex = im.CompletionResult.Selected
		} else if im.Text == "/" || im.CyclingIdx >= 0 {
			im.cycleCommand(1)
		} else {
			im.CompletionResult = Complete(im.Text, im.Skills, im.WorkDir)
			if len(im.CompletionResult.Candidates) == 1 {
				im.Text = im.CompletionResult.Candidates[0].Text
				im.CursorPos = utf8.RuneCountInString(im.Text)
				im.CompletionResult = nil
			} else if len(im.CompletionResult.Candidates) > 1 {
				im.CompletionMode = true
				im.SelectedIndex = 0
				im.CompletionResult.Selected = 0
			}
		}

	case key.Code == tea.KeyBackspace:
		if im.CursorPos > 0 {
			bytePos := charOffsetToByteOffset(im.Text, im.CursorPos)
			_, prevRuneSize := utf8.DecodeLastRuneInString(im.Text[:bytePos])
			im.Text = im.Text[:bytePos-prevRuneSize] + im.Text[bytePos:]
			im.CursorPos--
			if im.Text == "" {
				im.CyclingIdx = -1
			}
			// Update mention mode after backspace.
			if im.MentionMode {
				start, prefix := findMentionAtCursor(im.Text, im.CursorPos)
				if start >= 0 {
					im.MentionStart = start
					im.MentionResult = CompleteMention(prefix, im.WorkDir)
					im.MentionSelectedIndex = 0
				} else {
					im.dismissMention()
				}
			}
		}

	case key.Code == tea.KeyDelete:
		if im.CursorPos < utf8.RuneCountInString(im.Text) {
			bytePos := charOffsetToByteOffset(im.Text, im.CursorPos)
			_, nextRuneSize := utf8.DecodeRuneInString(im.Text[bytePos:])
			im.Text = im.Text[:bytePos] + im.Text[bytePos+nextRuneSize:]
		}

	case key.Code == tea.KeyLeft:
		if im.CyclingIdx >= 0 {
			im.CyclingIdx = -1
			im.CursorPos = 0
		} else if im.CursorPos > 0 {
			im.CursorPos--
		}

	case key.Code == tea.KeyRight:
		if im.CyclingIdx >= 0 {
			im.CyclingIdx = -1
			im.CursorPos = utf8.RuneCountInString(im.Text)
		} else if im.CursorPos < utf8.RuneCountInString(im.Text) {
			im.CursorPos++
		}
	case key.Code == tea.KeyHome || (key.Code == 'a' && key.Mod == tea.ModCtrl):
		im.CursorPos = 0

	case key.Code == tea.KeyEnd || (key.Code == 'e' && key.Mod == tea.ModCtrl):
		im.CursorPos = utf8.RuneCountInString(im.Text)

	case key.Code == tea.KeyUp:
		if im.CyclingIdx >= 0 {
			allCmds := im.AllCommandNames()
			if len(allCmds) > 0 {
				if im.CyclingIdx <= 0 {
					im.CyclingIdx = len(allCmds) - 1
				} else {
					im.CyclingIdx--
				}
				im.Text = allCmds[im.CyclingIdx]
				im.CursorPos = utf8.RuneCountInString(im.Text)
			}
		} else if len(im.History) > 0 {
			if im.HistoryIdx < 0 {
				im.HistoryIdx = len(im.History) - 1
			} else if im.HistoryIdx > 0 {
				im.HistoryIdx--
			}
			im.restoreHistoryEntry(im.HistoryIdx)
		}

	case key.Code == tea.KeyDown:
		if im.CyclingIdx >= 0 {
			allCmds := im.AllCommandNames()
			if len(allCmds) > 0 {
				im.CyclingIdx = (im.CyclingIdx + 1) % len(allCmds)
				im.Text = allCmds[im.CyclingIdx]
				im.CursorPos = utf8.RuneCountInString(im.Text)
			}
		} else if im.HistoryIdx >= 0 {
			im.HistoryIdx++
			if im.HistoryIdx >= len(im.History) {
				im.HistoryIdx = -1
				im.Text = ""
				im.CursorPos = 0
				im.dismissMention()
			} else {
				im.restoreHistoryEntry(im.HistoryIdx)
			}
		}

	case key.Code == tea.KeyEscape:
		if im.MentionMode {
			im.dismissMention()
			return nil
		}

	default:
		if key.Text != "" && isUserInput(key.Text) {
			if key.Text == "/" && im.Text == "" {
				im.ReloadSkills()
			}
			// Insert text at cursor position (properly handling UTF-8)
			beforeByte := charOffsetToByteOffset(im.Text, im.CursorPos)
			im.Text = im.Text[:beforeByte] + key.Text + im.Text[beforeByte:]
			im.CursorPos++
			im.CyclingIdx = -1

			// Enter mention mode when @ is typed.
			if key.Text == "@" {
				im.MentionMode = true
				im.MentionStart = im.CursorPos - 1
				im.MentionResult = CompleteMention("", im.WorkDir)
				im.MentionSelectedIndex = 0
				return nil
			}

			// Update mention completions while typing after @.
			if im.MentionMode {
				start, prefix := findMentionAtCursor(im.Text, im.CursorPos)
				if start >= 0 {
					im.MentionStart = start
					im.MentionResult = CompleteMention(prefix, im.WorkDir)
					im.MentionSelectedIndex = 0
				} else {
					im.dismissMention()
				}
			}
		}
	}

	// Update ghost autocomplete.
	if im.CursorPos == utf8.RuneCountInString(im.Text) {
		result := Complete(im.Text, im.Skills, im.WorkDir)
		if result != nil && len(result.Candidates) > 0 && len(result.Candidates) == 1 {
			im.Completion = result.Candidates[0].Text
		} else {
			im.Completion = ""
		}
	} else {
		im.Completion = ""
	}

	// Clear completion mode on non-Tab keys.
	if key.Code != tea.KeyTab {
		im.CompletionMode = false
		im.CompletionResult = nil
		im.SelectedIndex = 0
	}

	return nil
}

// View renders the input area.
func (im *InputModel) View(running bool) string {
	prefix := lipgloss.NewStyle().
		Foreground(lipgloss.Color("39")).
		Bold(true).
		Render("> ")

	if running {
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		return prefix + dim.Render("(waiting for response...)")
	}

	// Convert character positions to byte offsets for proper UTF-8 handling
	beforeByte := charOffsetToByteOffset(im.Text, im.CursorPos)
	before := im.Text[:beforeByte]
	after := im.Text[beforeByte:]

	cursor := lipgloss.NewStyle().
		Background(lipgloss.Color("252")).
		Foreground(lipgloss.Color("0")).
		Render(" ")
	if im.CursorPos < utf8.RuneCountInString(im.Text) {
		_, runeSize := utf8.DecodeRuneInString(im.Text[beforeByte:])
		cursor = lipgloss.NewStyle().
			Background(lipgloss.Color("252")).
			Foreground(lipgloss.Color("0")).
			Render(im.Text[beforeByte : beforeByte+runeSize])
		afterByte := beforeByte + runeSize
		after = im.Text[afterByte:]
	}

	// Completion menu.
	if im.CompletionMode && im.CompletionResult != nil && len(im.CompletionResult.Candidates) > 0 {
		inputLine := prefix + before + cursor + after
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		sel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)

		var menu strings.Builder
		for i, c := range im.CompletionResult.Candidates {
			if i == im.SelectedIndex {
				menu.WriteString(sel.Render("  > " + c.Text))
			} else {
				menu.WriteString(dim.Render("    " + c.Text))
			}
			if c.Description != "" {
				menu.WriteString(dim.Render(" — " + c.Description))
			}
			menu.WriteString("\n")
		}
		return inputLine + "\n" + menu.String()
	}

	// Command cycling menu.
	if im.CyclingIdx >= 0 {
		inputLine := prefix + before + cursor + after
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		sel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
		descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

		cycleCmds := im.commandCycleList()
		var menu strings.Builder
		for i, cmd := range cycleCmds {
			desc := slashCommandDesc(cmd)
			if desc == "" {
				for _, skill := range im.Skills {
					if "/"+skill.Name == cmd {
						desc = skill.Description
						break
					}
				}
			}
			if i == im.CyclingIdx {
				menu.WriteString(sel.Render("  > " + cmd))
			} else {
				menu.WriteString(dim.Render("    " + cmd))
			}
			if desc != "" {
				menu.WriteString(descStyle.Render(" — " + desc))
			}
			menu.WriteString("\n")
		}
		return inputLine + "\n" + menu.String()
	}

	// File @mention completion menu.
	if im.MentionMode && im.MentionResult != nil && len(im.MentionResult.Candidates) > 0 {
		inputLine := prefix + before + cursor + after
		dim := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
		sel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
		fileStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

		var menu strings.Builder
		for i, c := range im.MentionResult.Candidates {
			if i == im.MentionSelectedIndex {
				menu.WriteString(sel.Render("  > @" + c.Text))
			} else {
				menu.WriteString(dim.Render("    @" + c.Text))
			}
			if c.Description != "" {
				menu.WriteString(fileStyle.Render(" — " + c.Description))
			}
			menu.WriteString("\n")
		}
		return inputLine + "\n" + menu.String()
	}

	// Ghost autocomplete.
	ghost := ""
	if im.Completion != "" && im.CursorPos == utf8.RuneCountInString(im.Text) {
		suffix := im.Completion[beforeByte:]
		ghost = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(suffix + " [tab]")
	}

	return prefix + before + cursor + after + ghost
}

func (im *InputModel) commandCycleList() []string {
	allCmds := im.AllCommandNames()
	if len(allCmds) == 0 {
		return nil
	}
	if im.Text == "/" {
		return allCmds
	}
	prefix := strings.ToLower(im.Text)
	var filtered []string
	for _, cmd := range allCmds {
		if strings.HasPrefix(strings.ToLower(cmd), prefix) {
			filtered = append(filtered, cmd)
		}
	}
	if len(filtered) <= 1 {
		return allCmds
	}
	return filtered
}

func (im *InputModel) cycleCommand(delta int) {
	cmds := im.commandCycleList()
	if len(cmds) == 0 {
		return
	}
	if im.CyclingIdx < 0 || im.CyclingIdx >= len(cmds) {
		if delta < 0 {
			im.CyclingIdx = len(cmds) - 1
		} else {
			im.CyclingIdx = 0
		}
	} else {
		im.CyclingIdx = (im.CyclingIdx + delta + len(cmds)) % len(cmds)
	}
	im.Text = cmds[im.CyclingIdx]
	im.CursorPos = utf8.RuneCountInString(im.Text)
}

// InsertText inserts pasted or programmatic text at cursor position.
func (im *InputModel) InsertText(text string) {
	beforeByte := charOffsetToByteOffset(im.Text, im.CursorPos)
	im.Text = im.Text[:beforeByte] + text + im.Text[beforeByte:]
	im.CursorPos += utf8.RuneCountInString(text)
}

// Clear resets the input text and cursor.
func (im *InputModel) Clear() {
	im.Text = ""
	im.CursorPos = 0
}

// InCompletionMode returns true if the input is showing a completion, cycling, or mention menu.
func (im *InputModel) InCompletionMode() bool {
	return im.CompletionMode || im.CyclingIdx >= 0 || im.MentionMode
}

// DismissCompletion clears completion/cycling/mention state and input.
func (im *InputModel) DismissCompletion() {
	im.CompletionMode = false
	im.CompletionResult = nil
	im.SelectedIndex = 0
	im.CyclingIdx = -1
	im.dismissMention()
	im.Text = ""
	im.CursorPos = 0
}

// restoreHistoryEntry restores full input state from a history entry.
func (im *InputModel) restoreHistoryEntry(idx int) {
	entry := im.History[idx]
	im.Text = entry.Text
	im.CursorPos = utf8.RuneCountInString(im.Text)
}

// dismissMention exits mention completion mode.
func (im *InputModel) dismissMention() {
	im.MentionMode = false
	im.MentionResult = nil
	im.MentionSelectedIndex = 0
	im.MentionStart = 0
}

// ReloadSkills re-scans skill directories from disk and updates the cached list.
func (im *InputModel) ReloadSkills() {
	if len(im.SkillDirs) > 0 {
		if fresh, err := extension.LoadSkills(im.SkillDirs...); err == nil {
			im.Skills = fresh
		}
	}
}

// AllCommandNames returns a sorted list of all command names: built-in + skills.
func (im *InputModel) AllCommandNames() []string {
	seen := make(map[string]bool)
	var cmds []string
	for _, cmd := range slashCommands {
		if !seen[cmd] {
			seen[cmd] = true
			cmds = append(cmds, cmd)
		}
	}
	for _, skill := range im.Skills {
		name := "/" + skill.Name
		if !seen[name] {
			seen[name] = true
			cmds = append(cmds, name)
		}
	}
	sort.Strings(cmds)
	return cmds
}

// slashCommands is the list of available slash commands for autocomplete.
var slashCommands = []string{
	"/help",
	"/clear",
	"/model",
	"/session",
	"/context",
	"/branch",
	"/compact",
	"/subagents",
	"/history",
	"/login",
	"/commit",
	"/plan",
	"/run",
	"/skills",
	"/skill-list",
	"/skill-load",
	"/skill-create",
	"/theme",
	"/ping",
	"/rtk",
	"/mcp",
	"/restart",
	"/exit",
	"/quit",
}

// slashCommandDesc returns the description for a slash command.
func slashCommandDesc(cmd string) string {
	switch cmd {
	case "/help":
		return "Show help"
	case "/clear":
		return "Clear conversation"
	case "/model":
		return "Show current model"
	case "/session":
		return "Show session info"
	case "/context":
		return "Show context usage"
	case "/branch":
		return "Manage branches"
	case "/compact":
		return "Compact context"
	case "/subagents":
		return "Show subagents"
	case "/rtk":
		return "Output compaction stats"
	case "/mcp":
		return "List MCP servers and tool status"
	case "/history":
		return "Command history"
	case "/login":
		return "Configure API keys (codex, openai, anthropic, gemini)"
	case "/commit":
		return "Create commit from staged changes"
	case "/plan":
		return "Start PDD planning session"
	case "/run":
		return "Execute a spec with task agent"
	case "/theme":
		return "Switch theme or list themes"
	case "/skills":
		return "List skills (create, load)"
	case "/skill-list":
		return "List all loaded skills"
	case "/skill-load":
		return "Reload skills from disk"
	case "/skill-create":
		return "Create a new skill"
	case "/ping":
		return "Test LLM connectivity"
	case "/restart":
		return "Restart pi process"
	case "/exit", "/quit":
		return "Exit"
	default:
		return ""
	}
}

// completeSlashCommand returns the best matching slash command for the current input.
// Only suggests completions when at least 2 characters have been typed after '/'.
func completeSlashCommand(input string) string {
	// Require at least 2 chars after '/' before suggesting completions
	if !strings.HasPrefix(input, "/") || len(input) < 3 {
		return ""
	}
	prefix := strings.ToLower(input)
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, prefix) && cmd != prefix {
			return cmd
		}
	}
	return ""
}

// matchingSlashCommands returns all slash commands matching the given prefix.
func matchingSlashCommands(input string) []string {
	prefix := strings.ToLower(input)
	var matches []string
	for _, cmd := range slashCommands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

// isUserInput returns true if the string represents genuine user keyboard input.
// Real keyboard input via KeyPressMsg is always a single rune. Multi-character
// text values are terminal response fragments (CSI, OSC, DECRPM) that leaked
// through Bubble Tea's parser when escape sequences get split at arbitrary
// byte boundaries during resize or color queries.
func isUserInput(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	// Real keystrokes produce exactly one rune. Multi-char text in a
	// KeyPressMsg is always terminal response garbage. Actual multi-char
	// input (paste) arrives via PasteMsg which is filtered separately.
	if utf8.RuneCountInString(s) > 1 {
		return false
	}
	return true
}

// isUserPaste returns true if a PasteMsg contains real pasted text rather than
// terminal response sequences that were misidentified as bracketed paste.
func isUserPaste(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return !terminalResponseRe.MatchString(s)
}
