package tui

import (
	"fmt"
	"image/color"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Palette is the resolved set of colors the renderers draw with. It is the
// bridge between the theme system and the renderers: the theme manager picks a
// Theme, and paletteFor turns it into a Palette the renderers can read.
//
// Before this existed every renderer hardcoded Catppuccin Mocha (dark) hex
// values, so switching to a light theme changed nothing on screen — the dark
// foregrounds were invisible on a light terminal background. Threading a
// Palette through the render inputs is what makes light themes actually render.
//
// Valid distinguishes a real palette from the zero value, so renderers that
// receive an unset Palette (e.g. in tests) fall back to the dark palette and
// keep their existing output byte-for-byte.
type Palette struct {
	Valid bool

	// IsLight reports whether this palette is meant for a light terminal
	// background. Renderers that must pick a whole foreign stylesheet — the
	// glamour markdown style, the chroma syntax style — branch on this rather
	// than sniffing Background, which only worked while every light theme
	// collapsed to one palette.
	IsLight bool

	// Text roles.
	Text    color.Color // primary text
	Subtext color.Color // secondary text
	Dim     color.Color // muted text (thinking, separators)
	Faint   color.Color // faintest (line numbers, very muted)

	// Semantic accents.
	Primary color.Color // blue — prompts, user labels, links
	Accent  color.Color // violet — reply bullets
	Tool    color.Color // green — tool names
	Success color.Color // green
	Error   color.Color // red
	Warning color.Color // yellow

	// Extended hues.
	Blue     color.Color
	Cyan     color.Color
	Teal     color.Color
	Green    color.Color
	Yellow   color.Color
	Peach    color.Color // orange
	Red      color.Color
	Pink     color.Color
	Mauve    color.Color
	Sky      color.Color
	Sapphire color.Color
	Lavender color.Color

	// Surfaces.
	Surface     color.Color // rules, separators (surface2)
	Surface1    color.Color
	Overlay     color.Color
	Overlay2    color.Color
	Background  color.Color
	Surface0    color.Color
	Overlay0    color.Color
	Overlay1    color.Color
	White       color.Color // deliberately outside the gauge vocabulary
	Control     color.Color // controls that must sit outside a gauge's color vocabulary
	Transparent color.Color

	// Diff roles. The theme file declares four of them (see ThemeColors) and
	// the /theme preview strip has always drawn them, but the Palette — the
	// struct the renderers actually read — never carried them, so every diff
	// line rendered in a generic accent. DiffAdded/DiffRemoved are the row
	// backgrounds; the *Text pair carries the legible foreground on top.
	DiffAdded       color.Color
	DiffRemoved     color.Color
	DiffAddedText   color.Color
	DiffRemovedText color.Color
}

// darkPalette is the Catppuccin Mocha palette the renderers used before theming
// existed. It is the default so existing dark output is unchanged.
var darkPalette = Palette{
	Valid:       true,
	IsLight:     false,
	Text:        lipgloss.Color("#cdd6f4"),
	Subtext:     lipgloss.Color("#a6adc8"),
	Dim:         lipgloss.Color("#a6adc8"),
	Faint:       lipgloss.Color("#7f849c"),
	Primary:     lipgloss.Color("#89b4fa"),
	Accent:      lipgloss.Color("#cba6f7"),
	Tool:        lipgloss.Color("#a6e3a1"),
	Success:     lipgloss.Color("#a6e3a1"),
	Error:       lipgloss.Color("#f38ba8"),
	Warning:     lipgloss.Color("#f9e2af"),
	Blue:        lipgloss.Color("#89b4fa"),
	Cyan:        lipgloss.Color("#89dceb"),
	Teal:        lipgloss.Color("#94e2d5"),
	Green:       lipgloss.Color("#a6e3a1"),
	Yellow:      lipgloss.Color("#f9e2af"),
	Peach:       lipgloss.Color("#fab387"),
	Red:         lipgloss.Color("#f38ba8"),
	Pink:        lipgloss.Color("#f5c2e7"),
	Mauve:       lipgloss.Color("#cba6f7"),
	Sky:         lipgloss.Color("#89dceb"),
	Sapphire:    lipgloss.Color("#74c7ec"),
	Lavender:    lipgloss.Color("#b4befe"),
	Surface:     lipgloss.Color("#585b70"),
	Surface1:    lipgloss.Color("#45475a"),
	Overlay:     lipgloss.Color("#6c7086"),
	Overlay2:    lipgloss.Color("#9399b2"),
	Background:  lipgloss.Color("#1e1e2e"),
	Surface0:    lipgloss.Color("#313244"),
	Overlay0:    lipgloss.Color("#6c7086"),
	Overlay1:    lipgloss.Color("#7f849c"),
	White:       lipgloss.Color("#ffffff"),
	Control:     lipgloss.Color("#ffffff"),
	Transparent: lipgloss.Color("#00000000"),

	DiffAdded:       lipgloss.Color("#1e3a29"),
	DiffRemoved:     lipgloss.Color("#3a1e2a"),
	DiffAddedText:   lipgloss.Color("#a6e3a1"),
	DiffRemovedText: lipgloss.Color("#f38ba8"),
}

// lightPalette is a high-contrast light palette, chosen so every role stays
// legible on a light terminal background. It keeps the Catppuccin Latte hue
// family but pushes the text and accent tones darker than stock Latte so the
// UI reads clearly on a light screen. The extended hues are the Latte
// equivalents of the Mocha hues the renderers used, so the light theme reads as
// the same UI, just on a light background.
var lightPalette = Palette{
	Valid:      true,
	IsLight:    true,
	Text:       lipgloss.Color("#2c2e3e"),
	Subtext:    lipgloss.Color("#4c4f69"),
	Dim:        lipgloss.Color("#5c5f73"),
	Faint:      lipgloss.Color("#63667c"),
	Primary:    lipgloss.Color("#1a4fd8"),
	Accent:     lipgloss.Color("#6c2bd9"),
	Tool:       lipgloss.Color("#2f7d1f"),
	Success:    lipgloss.Color("#2f7d1f"),
	Error:      lipgloss.Color("#b30e2f"),
	Warning:    lipgloss.Color("#9a5c0c"),
	Blue:       lipgloss.Color("#1a4fd8"),
	Cyan:       lipgloss.Color("#0369a1"),
	Teal:       lipgloss.Color("#0f766e"),
	Green:      lipgloss.Color("#2f7d1f"),
	Yellow:     lipgloss.Color("#9a5c0c"),
	Peach:      lipgloss.Color("#c2410c"),
	Red:        lipgloss.Color("#b30e2f"),
	Pink:       lipgloss.Color("#be185d"),
	Mauve:      lipgloss.Color("#6c2bd9"),
	Sky:        lipgloss.Color("#0369a1"),
	Sapphire:   lipgloss.Color("#0e7490"),
	Lavender:   lipgloss.Color("#4f46e5"),
	Surface:    lipgloss.Color("#9ca0b0"),
	Surface1:   lipgloss.Color("#bcc0cc"),
	Overlay:    lipgloss.Color("#8c8fa1"),
	Overlay2:   lipgloss.Color("#7c7f93"),
	Background: lipgloss.Color("#eff1f5"),
	Surface0:   lipgloss.Color("#ccd0da"),
	// Overlay0 carries incidental text (gutters, elided detail), so unlike
	// Overlay — which only draws separators — it has to clear 3:1 against the
	// background. #8c8fa1 managed 2.83:1.
	Overlay0:    lipgloss.Color("#7c7f93"),
	Overlay1:    lipgloss.Color("#7c7f93"),
	White:       lipgloss.Color("#ffffff"),
	Control:     lipgloss.Color("#1c1e26"),
	Transparent: lipgloss.Color("#00000000"),

	// Diff tints chosen for a light background. The stock Catppuccin Latte
	// tokens pair #40a02b on #e0f0e0 for 2.82:1, which fails the 4.5:1 body-text
	// floor these rows are read at; these hold 4.53:1 and 5.95:1 and still
	// contrast over the page background, so a diff stays legible whether the
	// renderer tints the row or leaves it on the page.
	DiffAdded:       lipgloss.Color("#e6f4e6"),
	DiffRemoved:     lipgloss.Color("#fbe9ec"),
	DiffAddedText:   lipgloss.Color("#2f7d1f"),
	DiffRemovedText: lipgloss.Color("#b30e2f"),
}

// paletteFor resolves a Palette from a Theme.
//
// It starts from the variant's base palette and overlays every non-empty hex
// the theme declares. The base palette decides the roles the theme file has no
// token for — the extended hues, the surfaces, the gutters — so a theme that
// names only the 13 semantic roles still gets a complete, coherent palette
// rather than zero-value colors that render as terminal defaults.
//
// Only 13 roles are themeable because those are the roles a theme author can
// reason about. Everything else is derived from the variant, which is why a
// theme declaring the same `base` for dark and light still renders differently.
func paletteFor(t Theme) Palette {
	p := darkPalette
	if t.ThemeType == "light" {
		p = lightPalette
	}
	p.Valid = true
	p.IsLight = t.ThemeType == "light"

	c := t.Colors
	// overlay assigns c only when it parses, so a malformed or absent token
	// leaves the variant default in place instead of blanking the role.
	overlay := func(dst *color.Color, hex string) {
		if col, ok := parseHexColor(hex); ok {
			*dst = col
		}
	}

	overlay(&p.Text, c.Text)
	overlay(&p.Primary, c.Primary)
	overlay(&p.Tool, c.Tool)
	overlay(&p.Success, c.Success)
	overlay(&p.Error, c.Error)
	overlay(&p.Warning, c.Warning)
	overlay(&p.DiffAdded, c.DiffAdded)
	overlay(&p.DiffRemoved, c.DiffRemoved)
	overlay(&p.DiffAddedText, c.DiffAddedText)
	overlay(&p.DiffRemovedText, c.DiffRemovedText)

	// Roles the theme file has no token for but which must follow the ones it
	// does name: a prompt drawn in `primary` next to a `tool` badge drawn in
	// the default green reads as two different themes at once.
	//
	// `info` has no Palette role of its own, so it drives the violet accent
	// triple — which is the role the theme file means by "info".
	if col, ok := parseHexColor(c.Primary); ok {
		p.Blue, p.Accent = col, col
	}
	if col, ok := parseHexColor(c.Info); ok {
		p.Mauve, p.Lavender, p.Pink = col, col, col
	}
	if col, ok := parseHexColor(c.Tool); ok {
		p.Teal, p.Cyan = col, col
	}
	if col, ok := parseHexColor(c.Secondary); ok {
		// Secondary is the theme's quietest ink — tokyo-night's #565f89 is
		// 2.76:1 on its own base, which is a comment color, not a body-text
		// one. It therefore drives the rules and gutters, which are allowed to
		// recede, and NOT Subtext/Dim/Faint, which carry text a reader has to
		// read. Those stay on the variant's contrast-checked ladder.
		p.Surface, p.Overlay, p.Overlay1 = col, col, col
	}
	if col, ok := parseHexColor(c.Success); ok {
		p.Green = col
	}
	if col, ok := parseHexColor(c.Warning); ok {
		p.Yellow, p.Peach = col, col
	}
	if col, ok := parseHexColor(c.Error); ok {
		p.Red = col
	}
	// The page background. It is the one role an overlay would lose rather than
	// gain: lightPalette's surface roles are derived to sit *above* it in
	// lightness, so applying it first would leave the derived tints reading as
	// unrelated greys.
	overlay(&p.Background, c.Base)

	return p
}

// parseHexColor parses "#rrggbb" (or "#rgb") into a color. It reports false for
// the empty string, so paletteFor can treat an absent token as "keep the
// variant default" rather than "set this role to black".
func parseHexColor(hex string) (color.Color, bool) {
	hex = strings.TrimSpace(hex)
	hex = strings.TrimPrefix(hex, "#")
	switch len(hex) {
	case 3:
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	case 6:
	default:
		return nil, false
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return nil, false
	}
	return lipgloss.Color(fmt.Sprintf("#%06x", v)), true
}

// paletteOrDark returns p when it is a real palette, otherwise the dark
// default. Renderers call this so an unset Palette (tests, zero-value inputs)
// renders exactly as before.
func paletteOrDark(p Palette) Palette {
	if p.Valid {
		return p
	}
	return darkPalette
}

// PaletteForName resolves the palette for a configured theme name, falling back
// to the dark default for an unknown or empty name. It exists for entry points
// that run outside a chat session — `pi setup` — which have no ThemeManager and
// should still draw in the user's colors rather than always in the default.
func PaletteForName(name string) Palette {
	tm := NewThemeManager()
	if name != "" {
		_ = tm.SetTheme(name)
	}
	return paletteFor(tm.Current())
}

// SetupProgramOptions returns the terminal options a standalone program should
// use for the wizard: the same environment and color-profile negotiation the
// main TUI performs, so color detection is consistent between the two.
func SetupProgramOptions() []tea.ProgramOption {
	return terminalProgramOptions(os.Stdout, os.Environ())
}

// paletteKey fingerprints a palette so the chat render cache can invalidate
// when the theme changes. It folds the palette's distinguishing colors into a
// single uint64; two palettes that render identically share a key.
func paletteKey(p Palette) uint64 {
	h := fnvOffset64
	for _, c := range []color.Color{
		p.Text, p.Primary, p.Accent, p.Tool, p.Success, p.Error,
		p.Warning, p.Dim, p.Faint, p.Surface, p.Background,
		// Diff colors are here because renderKey folds this key: two themes
		// whose chrome matches but whose diff tints differ would otherwise
		// share a cache entry and keep the previous theme's diff rows.
		p.DiffAdded, p.DiffRemoved, p.DiffAddedText, p.DiffRemovedText,
	} {
		if c == nil {
			continue
		}
		r, g, b, a := c.RGBA()
		h = fnvByte(h, byte(r>>8))
		h = fnvByte(h, byte(g>>8))
		h = fnvByte(h, byte(b>>8))
		h = fnvByte(h, byte(a>>8))
	}
	return h
}

// colorString returns the hex form of a color.Color, or the empty string when
// it is nil. Used where a renderer needs the raw color token (e.g. the
// minimap's rail cells).
func colorString(c color.Color) string {
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}
