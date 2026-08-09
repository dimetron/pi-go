package tui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// swatchGlyph is wide enough to read as a block of color at a glance without
// crowding the label beside it.
const swatchGlyph = "███"

// swatch renders a block of color, or blanks of the same width when the color
// is unset, so columns stay aligned.
func swatch(c color.Color) string {
	if c == nil {
		return strings.Repeat(" ", len(swatchGlyph))
	}
	return lipgloss.NewStyle().Foreground(c).Render(swatchGlyph)
}

// themeSwatchRoles are the roles shown in the one-line strip beside each theme
// in the /theme list: enough hues to tell two themes apart at a glance.
func themeSwatchRoles(c ThemeColors) []color.Color {
	return []color.Color{
		c.PrimaryColor(), c.ToolColor(), c.SuccessColor(),
		c.WarningColor(), c.ErrorColor(), c.InfoColor(),
	}
}

// swatchStrip renders the compact multi-color bar used in theme lists.
func swatchStrip(c ThemeColors) string {
	var b strings.Builder
	for _, col := range themeSwatchRoles(c) {
		b.WriteString(lipgloss.NewStyle().Foreground(col).Render("██"))
	}
	return b.String()
}

// paletteRow is one line of a preview table.
type paletteRow struct {
	role  string
	color color.Color
	note  string
}

// themeColorRows lists the 13 roles a theme declares in themes.json.
func themeColorRows(c ThemeColors) []paletteRow {
	return []paletteRow{
		{"text", c.TextColor(), "body text"},
		{"base", c.BaseColor(), "background"},
		{"primary", c.PrimaryColor(), "prompts, links"},
		{"secondary", c.SecondaryColor(), "muted text"},
		{"tool", c.ToolColor(), "tool names"},
		{"success", c.SuccessColor(), "success"},
		{"error", c.ErrorColor(), "errors"},
		{"warning", c.WarningColor(), "warnings"},
		{"info", c.InfoColor(), "info, accents"},
		{"diffAdded", c.DiffAddedColor(), "diff + background"},
		{"diffRemoved", c.DiffRemovedColor(), "diff - background"},
		{"diffAddedText", c.DiffAddedTextColor(), "diff + text"},
		{"diffRemovedText", c.DiffRemovedTextColor(), "diff - text"},
	}
}

// renderPaletteRows draws an aligned swatch/role/hex/note table.
func renderPaletteRows(rows []paletteRow, p Palette) string {
	p = paletteOrDark(p)
	labelStyle := lipgloss.NewStyle().Foreground(p.Text)
	hexStyle := lipgloss.NewStyle().Foreground(p.Dim)
	noteStyle := lipgloss.NewStyle().Foreground(p.Faint)

	roleWidth := 0
	for _, r := range rows {
		if len(r.role) > roleWidth {
			roleWidth = len(r.role)
		}
	}

	var b strings.Builder
	for i, r := range rows {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "  %s  %s  %s",
			swatch(r.color),
			labelStyle.Render(fmt.Sprintf("%-*s", roleWidth, r.role)),
			hexStyle.Render(fmt.Sprintf("%-9s", colorString(r.color))),
		)
		if r.note != "" {
			b.WriteString("  " + noteStyle.Render(r.note))
		}
	}
	return b.String()
}

// formatThemePreview renders the swatch table for one theme.
//
// It shows the colors the theme declares in themes.json. Those are not
// currently what the chat pane draws with — paletteFor collapses every theme
// onto one of two built-in palettes, chosen only by the theme's light/dark type
// — so the preview says so rather than implying a fidelity it does not have.
func formatThemePreview(t Theme, p Palette, isCurrent bool) string {
	p = paletteOrDark(p)
	head := lipgloss.NewStyle().Foreground(p.Primary).Bold(true)
	dim := lipgloss.NewStyle().Foreground(p.Dim)
	faint := lipgloss.NewStyle().Foreground(p.Faint)

	kind := "dark"
	icon := "🌙"
	if t.ThemeType == "light" {
		kind, icon = "light", "☀️"
	}

	var b strings.Builder
	b.WriteString(head.Render(fmt.Sprintf("%s %s", icon, t.DisplayName)))
	b.WriteString(dim.Render(fmt.Sprintf("  %s  %s", t.Name, kind)))
	if isCurrent {
		b.WriteString(lipgloss.NewStyle().Foreground(p.Success).Render("  ← active"))
	}
	b.WriteString("\n\n")
	b.WriteString(renderPaletteRows(themeColorRows(t.Colors), p))
	b.WriteString("\n\n")
	b.WriteString(faint.Render(
		"  Declared colors. The transcript currently renders from a built-in " + kind + " palette;"))
	b.WriteString("\n")
	b.WriteString(faint.Render(
		"  a theme selects which one by its light/dark type, not by these values."))
	return b.String()
}

// formatPalettePreview renders the palette the renderers actually draw with.
func formatPalettePreview(p Palette) string {
	p = paletteOrDark(p)
	head := lipgloss.NewStyle().Foreground(p.Primary).Bold(true)

	kind := "dark"
	if p.IsLight {
		kind = "light"
	}

	rows := []paletteRow{
		{"Text", p.Text, "body text"},
		{"Subtext", p.Subtext, "secondary text"},
		{"Dim", p.Dim, "muted text"},
		{"Faint", p.Faint, "line numbers"},
		{"Primary", p.Primary, "prompt, user label"},
		{"Accent", p.Accent, "reply bullet"},
		{"Tool", p.Tool, "tool names"},
		{"Success", p.Success, "success"},
		{"Error", p.Error, "errors"},
		{"Warning", p.Warning, "warnings"},
		{"Peach", p.Peach, "warning messages"},
		{"Blue", p.Blue, ""},
		{"Cyan", p.Cyan, ""},
		{"Teal", p.Teal, ""},
		{"Green", p.Green, ""},
		{"Yellow", p.Yellow, ""},
		{"Red", p.Red, ""},
		{"Pink", p.Pink, ""},
		{"Mauve", p.Mauve, "agent labels"},
		{"Surface", p.Surface, "rules, separators"},
		{"Surface0", p.Surface0, "popup background"},
		{"Overlay", p.Overlay, "gauge track"},
		{"Background", p.Background, "sidebar background"},
		{"Control", p.Control, "gauge clear button"},
	}

	var b strings.Builder
	b.WriteString(head.Render(fmt.Sprintf("Active render palette (%s)", kind)))
	b.WriteString("\n\n")
	b.WriteString(renderPaletteRows(rows, p))
	return b.String()
}
