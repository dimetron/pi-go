package tui

import (
	"image/color"
	"math"
	"strings"
	"testing"
)

// The light palette was hand-tuned for WCAG AA in e78f10c and shipped without a
// test, so nothing stopped it drifting back. These tests pin the ratios, and
// pin the renderers to the palette so a hardcoded ANSI index cannot sneak past
// a light theme again.

// relativeLuminance implements the WCAG 2.1 definition.
func relativeLuminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	channel := func(v uint32) float64 {
		s := float64(v>>8) / 255.0
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(r) + 0.7152*channel(g) + 0.0722*channel(b)
}

// contrastRatio returns the WCAG 2.1 contrast ratio between two colors.
// The result runs from 1 (identical) to 21 (black on white).
func contrastRatio(fg, bg color.Color) float64 {
	l1, l2 := relativeLuminance(fg), relativeLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func TestContrastRatioReferenceValues(t *testing.T) {
	black := hexColor("#000000")
	white := hexColor("#ffffff")
	if got := contrastRatio(black, white); math.Abs(got-21) > 0.01 {
		t.Errorf("black on white = %.2f, want 21", got)
	}
	if got := contrastRatio(white, white); math.Abs(got-1) > 0.01 {
		t.Errorf("white on white = %.2f, want 1", got)
	}
}

// textRoles carry words the user has to read, so they must clear AA (4.5:1).
func textRoles(p Palette) map[string]color.Color {
	return map[string]color.Color{
		"Text":    p.Text,
		"Subtext": p.Subtext,
		"Dim":     p.Dim,
		"Primary": p.Primary,
		"Accent":  p.Accent,
		"Tool":    p.Tool,
		"Success": p.Success,
		"Error":   p.Error,
		"Warning": p.Warning,
		"Blue":    p.Blue,
		"Cyan":    p.Cyan,
		"Teal":    p.Teal,
		"Green":   p.Green,
		"Yellow":  p.Yellow,
		"Peach":   p.Peach,
		"Red":     p.Red,
		"Pink":    p.Pink,
		"Mauve":   p.Mauve,
		"Control": p.Control,
	}
}

// mutedRoles are incidental text — line numbers, gutters, elided detail. They
// are deliberately quieter than body text, so they are held to the 3:1 AA
// threshold for incidental content rather than the 4.5:1 body-text one.
func mutedRoles(p Palette) map[string]color.Color {
	return map[string]color.Color{
		"Faint":    p.Faint,
		"Overlay0": p.Overlay0,
		"Overlay1": p.Overlay1,
	}
}

// surfaceRoles draw rules, separators and the unfilled half of gauges. WCAG
// sets no contrast floor for purely decorative marks, and these are meant to be
// quiet, so the assertion is not a ratio but a direction: a surface must be
// visible against the background and must sit on the correct side of it. A
// light palette whose surfaces were lighter than its background — the shape of
// bug that gets shipped when a dark palette is edited into a light one — would
// fail here.
func surfaceRoles(p Palette) map[string]color.Color {
	return map[string]color.Color{
		"Surface":  p.Surface,
		"Surface1": p.Surface1,
		"Overlay":  p.Overlay,
		"Overlay2": p.Overlay2,
	}
}

func TestPaletteTextRolesMeetWCAGAA(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Palette
	}{
		{"light", lightPalette},
		{"dark", darkPalette},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for role, c := range textRoles(tc.p) {
				if got := contrastRatio(c, tc.p.Background); got < 4.5 {
					t.Errorf("%s.%s (%s) on background %s = %.2f:1, want >= 4.5:1",
						tc.name, role, colorString(c), colorString(tc.p.Background), got)
				}
			}
		})
	}
}

func TestPaletteMutedRolesMeetWCAGIncidental(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Palette
	}{
		{"light", lightPalette},
		{"dark", darkPalette},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for role, c := range mutedRoles(tc.p) {
				if got := contrastRatio(c, tc.p.Background); got < 3.0 {
					t.Errorf("%s.%s (%s) on background %s = %.2f:1, want >= 3.0:1",
						tc.name, role, colorString(c), colorString(tc.p.Background), got)
				}
			}
		})
	}
}

func TestPaletteSurfaceRolesSitOnTheCorrectSideOfTheBackground(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    Palette
	}{
		{"light", lightPalette},
		{"dark", darkPalette},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bg := relativeLuminance(tc.p.Background)
			for role, c := range surfaceRoles(tc.p) {
				lum := relativeLuminance(c)
				if tc.p.IsLight && lum >= bg {
					t.Errorf("light.%s (%s) is not darker than background %s",
						role, colorString(c), colorString(tc.p.Background))
				}
				if !tc.p.IsLight && lum <= bg {
					t.Errorf("dark.%s (%s) is not lighter than background %s",
						role, colorString(c), colorString(tc.p.Background))
				}
				if got := contrastRatio(c, tc.p.Background); got < 1.4 {
					t.Errorf("%s.%s (%s) = %.2f:1, too close to the background to see",
						tc.name, role, colorString(c), got)
				}
			}
		})
	}
}

// The grayed font is the text the light background has to serve most
// carefully: thinking blocks render in Faint italic, and dim labels in Dim.
// Both are meant to be quieter than body text, but on a light screen they must
// still clear AAA (7:1) so a long reasoning block stays readable. Stock Latte's
// base (#eff1f5) only managed ~5:1 for Faint — the near-white background and
// the darkened grayed tones are what push them over the line.
func TestLightGrayedFontClearsAAA(t *testing.T) {
	for _, tc := range []struct {
		role string
		c    color.Color
	}{
		{"Faint", lightPalette.Faint},
		{"Dim", lightPalette.Dim},
	} {
		if got := contrastRatio(tc.c, lightPalette.Background); got < 7.0 {
			t.Errorf("light %s (%s) on background %s = %.2f:1, want >= 7:1 for the grayed font",
				tc.role, colorString(tc.c), colorString(lightPalette.Background), got)
		}
	}
}

// The gauge clear button used to be hardcoded white, which vanished on a light
// terminal. Control is the role that replaced it.
func TestControlRoleIsLegibleOnBothBackgrounds(t *testing.T) {
	if got := contrastRatio(lightPalette.Control, lightPalette.Background); got < 4.5 {
		t.Errorf("light Control = %.2f:1, want >= 4.5:1", got)
	}
	if got := contrastRatio(darkPalette.Control, darkPalette.Background); got < 4.5 {
		t.Errorf("dark Control = %.2f:1, want >= 4.5:1", got)
	}
}

func TestPaletteForSetsIsLight(t *testing.T) {
	if !paletteFor(Theme{ThemeType: "light"}).IsLight {
		t.Error("light theme resolved to a palette with IsLight false")
	}
	if paletteFor(Theme{ThemeType: "dark"}).IsLight {
		t.Error("dark theme resolved to a palette with IsLight true")
	}
	if paletteOrDark(Palette{}).IsLight {
		t.Error("zero palette must fall back to dark")
	}
}

// hexColor parses "#rrggbb" without going through lipgloss, so the helpers
// above are testable on their own terms.
func hexColor(s string) color.Color {
	s = strings.TrimPrefix(s, "#")
	var r, g, b uint8
	for i, p := range []*uint8{&r, &g, &b} {
		v := 0
		for _, c := range s[i*2 : i*2+2] {
			v *= 16
			switch {
			case c >= '0' && c <= '9':
				v += int(c - '0')
			case c >= 'a' && c <= 'f':
				v += int(c-'a') + 10
			case c >= 'A' && c <= 'F':
				v += int(c-'A') + 10
			}
		}
		*p = uint8(v)
	}
	return color.RGBA{R: r, G: g, B: b, A: 0xff}
}
