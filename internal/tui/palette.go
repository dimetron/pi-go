package tui

import (
	"fmt"
	"image/color"

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
}

// paletteFor resolves a Palette from a Theme. Dark themes keep the Mocha
// palette the renderers used before theming, so existing dark output is
// byte-for-byte unchanged; light themes get the Catppuccin Latte palette, which
// is legible on a light terminal background. This is what makes a light theme
// actually render instead of drawing dark-theme foregrounds on a light screen.
func paletteFor(t Theme) Palette {
	if t.ThemeType == "light" {
		return lightPalette
	}
	return darkPalette
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

// paletteKey fingerprints a palette so the chat render cache can invalidate
// when the theme changes. It folds the palette's distinguishing colors into a
// single uint64; two palettes that render identically share a key.
func paletteKey(p Palette) uint64 {
	h := fnvOffset64
	for _, c := range []color.Color{
		p.Text, p.Primary, p.Accent, p.Tool, p.Success, p.Error,
		p.Warning, p.Dim, p.Faint, p.Surface, p.Background,
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
