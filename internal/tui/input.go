package tui

import (
	"sort"
	"strings"
	"unicode"

	"github.com/dimetron/pi-go/internal/extension"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// InputSubmitMsg is emitted when the user presses Enter with non-empty input.
type InputSubmitMsg struct {
	Text     string
	Mentions []string // file paths referenced via @path
}

// InputModel manages the text input area: cursor, history, and completion.
type InputModel struct {
	Text       string
	CursorPos  int
	History    []string
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
func NewInputModel(history []string, skills []extension.Skill, skillDirs []string, workDir string) InputModel {
	return InputModel{
		History:    history,
		HistoryIdx: -1,
		CyclingIdx: -1,
		Skills:     skills,
		SkillDirs:  skillDirs,
		WorkDir:    workDir,
	}
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
			before := im.Text[:im.MentionStart]
			after := im.Text[im.CursorPos:]
			im.Text = before + "@" + selected + after
			im.CursorPos = im.MentionStart + 1 + len(selected)
			im.dismissMention()
			return nil
		}
		// Cycling: place command, dismiss menu.
		if im.CyclingIdx >= 0 {
			im.CyclingIdx = -1
			im.CursorPos = len(im.Text)
			return nil
		}
		// Completion: apply selection.
		if im.CompletionMode && im.CompletionResult != nil && len(im.CompletionResult.Candidates) > 0 {
			im.Text = im.CompletionResult.ApplySelection(im.SelectedIndex)
			im.CursorPos = len(im.Text)
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
		if len(im.History) == 0 || im.History[len(im.History)-1] != text {
			im.History = append(im.History, text)
			appendHistory(text)
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
			allCmds := im.AllCommandNames()
			if len(allCmds) > 0 {
				if im.CyclingIdx <= 0 {
					im.CyclingIdx = len(allCmds) - 1
				} else {
					im.CyclingIdx--
				}
				im.Text = allCmds[im.CyclingIdx]
				im.CursorPos = len(im.Text)
			}
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
			allCmds := im.AllCommandNames()
			if len(allCmds) > 0 {
				im.CyclingIdx = (im.CyclingIdx + 1) % len(allCmds)
				im.Text = allCmds[im.CyclingIdx]
				im.CursorPos = len(im.Text)
			}
		} else {
			im.CompletionResult = Complete(im.Text, im.Skills, im.WorkDir)
			if len(im.CompletionResult.Candidates) == 1 {
				im.Text = im.CompletionResult.Candidates[0].Text
				im.CursorPos = len(im.Text)
				im.CompletionResult = nil
			} else if len(im.CompletionResult.Candidates) > 1 {
				im.CompletionMode = true
				im.SelectedIndex = 0
				im.CompletionResult.Selected = 0
			}
		}

	case key.Code == tea.KeyBackspace:
		if im.CursorPos > 0 {
			im.Text = im.Text[:im.CursorPos-1] + im.Text[im.CursorPos:]
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
		if im.CursorPos < len(im.Text) {
			im.Text = im.Text[:im.CursorPos] + im.Text[im.CursorPos+1:]
		}

	case key.Code == tea.KeyLeft:
		if im.CursorPos > 0 {
			im.CursorPos--
		}

	case key.Code == tea.KeyRight:
		if im.CursorPos < len(im.Text) {
			im.CursorPos++
		}

	case key.Code == tea.KeyHome || (key.Code == 'a' && key.Mod == tea.ModCtrl):
		im.CursorPos = 0

	case key.Code == tea.KeyEnd || (key.Code == 'e' && key.Mod == tea.ModCtrl):
		im.CursorPos = len(im.Text)

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
				im.CursorPos = len(im.Text)
			}
		} else if len(im.History) > 0 {
			if im.HistoryIdx < 0 {
				im.HistoryIdx = len(im.History) - 1
			} else if im.HistoryIdx > 0 {
				im.HistoryIdx--
			}
			im.Text = im.History[im.HistoryIdx]
			im.CursorPos = len(im.Text)
		}

	case key.Code == tea.KeyDown:
		if im.CyclingIdx >= 0 {
			allCmds := im.AllCommandNames()
			if len(allCmds) > 0 {
				im.CyclingIdx = (im.CyclingIdx + 1) % len(allCmds)
				im.Text = allCmds[im.CyclingIdx]
				im.CursorPos = len(im.Text)
			}
		} else if im.HistoryIdx >= 0 {
			im.HistoryIdx++
			if im.HistoryIdx >= len(im.History) {
				im.HistoryIdx = -1
				im.Text = ""
			} else {
				im.Text = im.History[im.HistoryIdx]
			}
			im.CursorPos = len(im.Text)
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
				im.Text = "/"
				im.CursorPos = 1
				im.CyclingIdx = 0
				allCmds := im.AllCommandNames()
				if len(allCmds) > 0 {
					im.Text = allCmds[0]
					im.CursorPos = len(im.Text)
				}
				return nil
			}
			im.Text = im.Text[:im.CursorPos] + key.Text + im.Text[im.CursorPos:]
			im.CursorPos += len(key.Text)
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
	if im.CursorPos == len(im.Text) {
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

	before := im.Text[:im.CursorPos]
	after := im.Text[im.CursorPos:]
	cursor := lipgloss.NewStyle().
		Background(lipgloss.Color("252")).
		Foreground(lipgloss.Color("0")).
		Render(" ")
	if im.CursorPos < len(im.Text) {
		cursor = lipgloss.NewStyle().
			Background(lipgloss.Color("252")).
			Foreground(lipgloss.Color("0")).
			Render(string(im.Text[im.CursorPos]))
		after = im.Text[im.CursorPos+1:]
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

		allCmds := im.AllCommandNames()
		var menu strings.Builder
		for i, cmd := range allCmds {
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
	if im.Completion != "" && im.CursorPos == len(im.Text) {
		suffix := im.Completion[len(im.Text):]
		ghost = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(suffix + " [tab]")
	}

	return prefix + before + cursor + after + ghost
}

// InsertText inserts pasted or programmatic text at cursor position.
func (im *InputModel) InsertText(text string) {
	im.Text = im.Text[:im.CursorPos] + text + im.Text[im.CursorPos:]
	im.CursorPos += len(text)
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
	"/agents",
	"/history",
	"/login",
	"/commit",
	"/plan",
	"/run",
	"/skills",
	"/theme",
	"/ping",
	"/rtk",
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
	case "/agents":
		return "Show subagents"
	case "/rtk":
		return "Output compaction stats"
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
func completeSlashCommand(input string) string {
	if !strings.HasPrefix(input, "/") || len(input) < 2 {
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
func isUserInput(s string) bool {
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	if len(s) > 2 && s[0] == ']' && s[1] >= '0' && s[1] <= '9' {
		return false
	}
	if len(s) > 2 && s[0] == '[' && (s[len(s)-1] >= 'A' && s[len(s)-1] <= 'Z') {
		return false
	}
	if strings.Contains(s, ";rgb:") || strings.Contains(s, "rgb:") {
		return false
	}
	return true
}
