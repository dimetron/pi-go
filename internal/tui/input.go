package tui

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dimetron/pi-go/internal/extension"

	"charm.land/bubbles/v2/textinput"
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

// InputModel wraps Bubble Tea's standard textinput component with history
// and slash-command support. All completion/mention state has been removed;
// the textinput library handles cursor movement and editing directly.
//
// History is recorded here but not navigated here: the root model binds Up to
// the history window (see handleKey), so the input never sees arrow keys.
type InputModel struct {
	Text      string
	CursorPos int // character position (not byte offset)
	History   []HistoryEntry

	// Dependencies (set by root model).
	Skills    []extension.Skill
	SkillDirs []string
	WorkDir   string

	// Palette is the resolved theme palette, set each frame by the model before
	// rendering. Zero means the dark default.
	Palette Palette

	input textinput.Model

	// stylePaletteKey fingerprints the palette `input`'s prompt and cursor
	// styles were built from, so RefreshTheme can rebuild them on a theme
	// switch. textinput bakes its styles in at construction, so unlike the
	// lipgloss chrome they do not follow Palette on their own.
	stylePaletteKey uint64
}

// NewInputModel creates an InputModel with initial state.
func NewInputModel(history []HistoryEntry, skills []extension.Skill, skillDirs []string, workDir string) InputModel {
	im := InputModel{
		History:   history,
		Skills:    skills,
		SkillDirs: skillDirs,
		WorkDir:   workDir,
	}
	im.ensureInput()
	return im
}

// HandleKey processes a key press for the input area.
// Returns a tea.Cmd (InputSubmitMsg on submit, nil otherwise).
func (im *InputModel) HandleKey(msg tea.KeyPressMsg) tea.Cmd {
	im.ensureInput()
	key := msg.Key()

	switch {
	case isLineStartKey(key):
		im.input.CursorStart()
		im.syncFromInput()
		return nil
	case isLineEndKey(key):
		im.input.CursorEnd()
		im.syncFromInput()
		return nil
	}

	switch key.Code {
	case tea.KeyEnter:
		text := strings.TrimSpace(im.input.Value())
		if text == "" {
			return nil
		}
		mentions := extractMentions(text)
		entry := HistoryEntry{Text: text, Mentions: mentions}
		if len(im.History) == 0 || im.History[len(im.History)-1].Text != text {
			im.History = append(im.History, entry)
			appendHistory(entry)
		}
		im.setValue("")
		return func() tea.Msg { return InputSubmitMsg{Text: text, Mentions: mentions} }
	}

	if key.Text != "" && !isUserInput(key.Text) {
		if isUserPaste(key.Text) {
			im.InsertText(key.Text)
		}
		return nil
	}

	var cmd tea.Cmd
	im.input, cmd = im.input.Update(msg)
	im.syncFromInput()
	return cmd
}

// SetWidth sets the visible width of the editable input text, excluding the prompt.
func (im *InputModel) SetWidth(width int) {
	im.ensureInput()
	if width < 0 {
		width = 0
	}
	pos := im.CursorPos
	im.input.SetWidth(width)
	// The textinput viewport only recalculates when the cursor moves outside
	// the current bounds. After a width change (especially from the initial
	// width=0 to a real value), the old viewport covers the full text and the
	// cursor stays within it, so the viewport never narrows. CursorEnd forces
	// a right-edge recalculation with the new width; SetCursor restores the
	// actual position.
	im.input.CursorEnd()
	im.input.SetCursor(pos)
	im.syncFromInput()
}

// View renders the input area.
func (im *InputModel) View(running bool) string {
	im.ensureInput()
	if running {
		p := paletteOrDark(im.Palette)
		prefix := lipgloss.NewStyle().
			Foreground(p.Primary).
			Bold(true).
			Render("> ")
		dim := lipgloss.NewStyle().Foreground(p.Dim)
		return prefix + dim.Render("(waiting for response...)")
	}
	return im.input.View()
}

// InsertText inserts pasted or programmatic text at cursor position.
func (im *InputModel) InsertText(text string) {
	im.ensureInput()
	pos := im.CursorPos
	beforeByte := charOffsetToByteOffset(im.Text, im.CursorPos)
	im.setValue(im.Text[:beforeByte] + text + im.Text[beforeByte:])
	im.input.SetCursor(pos + utf8.RuneCountInString(text))
	im.syncFromInput()
}

// Clear resets the input text and cursor.
func (im *InputModel) Clear() {
	im.ensureInput()
	im.setValue("")
}

// SetText replaces the input text and moves the cursor to the end.
func (im *InputModel) SetText(text string) {
	im.ensureInput()
	im.setValue(text)
	im.input.CursorEnd()
	im.syncFromInput()
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

// Cursor returns the real Bubble Tea cursor for the input's current position.
func (im *InputModel) Cursor() *tea.Cursor {
	im.ensureInput()
	return im.input.Cursor()
}

// applyPaletteStyles paints the text input's prompt and cursor from the current
// palette and records which palette they came from.
func (im *InputModel) applyPaletteStyles() {
	p := paletteOrDark(im.Palette)
	promptStyle := lipgloss.NewStyle().
		Foreground(p.Primary).
		Bold(true)
	styles := im.input.Styles()
	styles.Focused.Prompt = promptStyle
	styles.Blurred.Prompt = promptStyle
	styles.Cursor.Color = p.Primary
	styles.Cursor.Shape = tea.CursorBar
	im.input.SetStyles(styles)
	im.stylePaletteKey = paletteKey(p)
}

// RefreshTheme repaints the input's prompt and cursor when the palette has
// changed since they were built, and reports whether it did.
func (im *InputModel) RefreshTheme() bool {
	if im.input.KeyMap.CharacterForward.Keys() == nil {
		// Not constructed yet; ensureInput will pick up the current palette.
		return false
	}
	if im.stylePaletteKey == paletteKey(paletteOrDark(im.Palette)) {
		return false
	}
	im.applyPaletteStyles()
	return true
}

func (im *InputModel) ensureInput() {
	if im.input.KeyMap.CharacterForward.Keys() == nil {
		im.input = textinput.New()
		im.applyPaletteStyles()
		im.input.Prompt = "> "
		im.input.SetVirtualCursor(false)
		im.input.SetWidth(0)
		_ = im.input.Focus()
	}
	if im.input.Value() != im.Text {
		im.input.SetValue(im.Text)
	}
	im.input.SetCursor(im.CursorPos)
	im.syncFromInput()
}

func (im *InputModel) setValue(text string) {
	im.input.SetValue(text)
	im.syncFromInput()
}

func (im *InputModel) syncFromInput() {
	im.Text = im.input.Value()
	im.CursorPos = im.input.Position()
}

func isLineStartKey(key tea.Key) bool {
	return key.Code == tea.KeyHome ||
		(key.Code == 'a' && key.Mod == tea.ModCtrl) ||
		key.Code == 0x01
}

func isLineEndKey(key tea.Key) bool {
	return key.Code == tea.KeyEnd ||
		(key.Code == 'e' && key.Mod == tea.ModCtrl) ||
		key.Code == 0x05
}

// slashCommands is the list of available slash commands for autocomplete.
// Skill subcommands (/skill-list, /skill-load, /skill-create) are handled
// as args to /skills and omitted from the top-level list to keep it concise.
// It is derived from slashCommandSpecs (commands.go), which is the single
// source of truth for name, description and handler.
var slashCommands = func() []string {
	names := make([]string, 0, len(slashCommandSpecs))
	for _, spec := range slashCommandSpecs {
		if spec.hidden {
			continue
		}
		names = append(names, spec.name)
	}
	return names
}()

// slashCommandDesc returns the description for a slash command.
func slashCommandDesc(cmd string) string {
	return slashCommandByName[cmd].desc
}

// completeSlashCommand returns the best matching slash command for the current input.
// Only suggests completions when at least 2 characters have been typed after '/'.
func completeSlashCommand(input string) string {
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
