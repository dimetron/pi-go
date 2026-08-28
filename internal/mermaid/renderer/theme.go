package renderer

import (
	"fmt"
	"strconv"
	"strings"
)

// Theme maps semantic style keys to ANSI color/style sequences.
type Theme struct {
	Name          string
	Node          string
	Edge          string
	Arrow         string
	Subgraph      string
	Label         string
	EdgeLabel     string
	SubgraphLabel string
	Default       string
	BoldLabel     string
	ItalicLabel   string
	Note          string
	SubgraphFill  string // background-only style for subgraph interiors

	// Depth-based region coloring (for treemaps, nested diagrams): each depth
	// step lightens toward white.
	hasDepthColors bool

	// PieBaseColor, if set, generates monochromatic pie shades from this hue
	// instead of the default vibrant multi-color palette.
	pieBaseR, pieBaseG, pieBaseB int
	hasPieBase                   bool
}

// ansi helpers

func reset() string { return "\033[0m" }

func fg256(r, g, b int) string {
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", r, g, b)
}

func bg256(r, g, b int) string {
	return fmt.Sprintf("\033[48;2;%d;%d;%dm", r, g, b)
}

func hexBgColor(hex string) string {
	r, g, b := parseHex(hex)
	return bg256(r, g, b)
}

// Palette of distinct hues for top-level sections.
// Each is a dark base RGB that lightens per depth.
var regionPalette = [][3]int{
	{26, 58, 92}, // deep blue
	{26, 82, 52}, // deep green
	{82, 36, 82}, // deep purple
	{82, 56, 26}, // deep amber
	{26, 72, 82}, // deep teal
	{82, 26, 42}, // deep rose
	{52, 72, 26}, // deep olive
	{62, 42, 82}, // deep violet
}

// RegionStyle returns an ANSI fg+bg string for a region identified by
// its top-level section index and nesting depth within that section.
// Each section gets a distinct hue; depth lightens the shade.
// Returns empty string if the theme has no depth colors.
func (t Theme) RegionStyle(sectionIdx, depth int) string {
	if !t.hasDepthColors {
		return ""
	}
	base := regionPalette[sectionIdx%len(regionPalette)]
	step := 20
	r := min(235, base[0]+depth*step)
	g := min(235, base[1]+depth*step)
	b := min(235, base[2]+depth*step)
	return fg256(255, 255, 255) + bg256(r, g, b)
}

// RegionBorderStyle returns a brighter fg with the region bg for borders.
func (t Theme) RegionBorderStyle(sectionIdx, depth int) string {
	if !t.hasDepthColors {
		return ""
	}
	base := regionPalette[sectionIdx%len(regionPalette)]
	step := 20
	r := min(235, base[0]+depth*step)
	g := min(235, base[1]+depth*step)
	b := min(235, base[2]+depth*step)
	// Brighter fg for borders
	fr := min(255, r+60)
	fg := min(255, g+60)
	fb := min(255, b+60)
	return fg256(fr, fg, fb) + bg256(r, g, b)
}

// RegionLabelStyle returns bold white fg on the region bg for labels.
func (t Theme) RegionLabelStyle(sectionIdx, depth int) string {
	if !t.hasDepthColors {
		return ""
	}
	base := regionPalette[sectionIdx%len(regionPalette)]
	step := 20
	r := min(235, base[0]+depth*step)
	g := min(235, base[1]+depth*step)
	b := min(235, base[2]+depth*step)
	return "\033[1m" + fg256(255, 255, 255) + bg256(r, g, b)
}

// RegionTextStyle returns a bright fg color matching the section hue (no background).
// Use for labels that should hint at their section without a full colored background.
func (t Theme) RegionTextStyle(sectionIdx, depth int) string {
	if !t.hasDepthColors {
		return ""
	}
	base := regionPalette[sectionIdx%len(regionPalette)]
	step := 20
	// Bright version: lighten significantly for readability on dark wallpaper
	r := min(255, base[0]+depth*step+120)
	g := min(255, base[1]+depth*step+120)
	b := min(255, base[2]+depth*step+120)
	if depth == 0 {
		return "\033[1m" + fg256(r, g, b)
	}
	return fg256(r, g, b)
}

// RegionBarStyle returns a style for bar/chart elements — section color as fg,
// slightly brighter as bg. With ▓/▒ characters this shows the section color
// prominently rather than white.
func (t Theme) RegionBarStyle(sectionIdx, depth int) string {
	if !t.hasDepthColors {
		return ""
	}
	base := regionPalette[sectionIdx%len(regionPalette)]
	step := 20
	r := min(235, base[0]+depth*step)
	g := min(235, base[1]+depth*step)
	b := min(235, base[2]+depth*step)
	// Brighter bg so ▓ blends section color (fg) with a lighter shade (bg)
	br := min(255, r+50)
	bg := min(255, g+50)
	bb := min(255, b+50)
	return fg256(r, g, b) + bg256(br, bg, bb)
}

// HasDepthColors reports whether this theme supports depth-based region coloring.
func (t Theme) HasDepthColors() bool {
	return t.hasDepthColors
}

func parseHex(hex string) (int, int, int) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) == 3 {
		hex = string([]byte{hex[0], hex[0], hex[1], hex[1], hex[2], hex[2]})
	}
	if len(hex) != 6 {
		return 255, 255, 255
	}
	r, _ := strconv.ParseInt(hex[0:2], 16, 32)
	g, _ := strconv.ParseInt(hex[2:4], 16, 32)
	b, _ := strconv.ParseInt(hex[4:6], 16, 32)
	return int(r), int(g), int(b)
}

func hexColor(hex string) string {
	r, g, b := parseHex(hex)
	return fg256(r, g, b)
}

// Named ANSI colors
var namedColors = map[string]string{
	"black":   "\033[30m",
	"red":     "\033[31m",
	"green":   "\033[32m",
	"yellow":  "\033[33m",
	"blue":    "\033[34m",
	"magenta": "\033[35m",
	"cyan":    "\033[36m",
	"white":   "\033[37m",
}

// buildANSI converts a rich-like style string to ANSI escape sequence.
// Supports: "bold", "dim", "italic", named colors, "#hex" colors, and combos.
func buildANSI(style string) string {
	if style == "" {
		return ""
	}
	parts := strings.Fields(style)
	var codes []string
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		switch p {
		case "bold":
			codes = append(codes, "\033[1m")
		case "dim":
			codes = append(codes, "\033[2m")
		case "italic":
			codes = append(codes, "\033[3m")
		case "on":
			// "on #hex" or "on colorname" = background color
			if i+1 < len(parts) {
				i++
				bg := parts[i]
				if strings.HasPrefix(bg, "#") {
					codes = append(codes, hexBgColor(bg))
				} else if ansi, ok := namedColors[bg]; ok {
					// Convert fg to bg: \033[3Xm -> \033[4Xm
					codes = append(codes, strings.Replace(ansi, "\033[3", "\033[4", 1))
				}
			}
		default:
			if strings.HasPrefix(p, "#") {
				codes = append(codes, hexColor(p))
			} else if ansi, ok := namedColors[p]; ok {
				codes = append(codes, ansi)
			}
		}
	}
	return strings.Join(codes, "")
}

// PieColors returns the pie chart color palette for this theme.
// For monochromatic themes, generates n evenly-spaced shades from the base hue.
// For other themes, returns the default vibrant multi-color palette.
func (t Theme) PieColors(n int) [][3]int {
	if !t.hasPieBase {
		return nil // caller uses default vibrant palette
	}
	if n < 2 {
		n = 2
	}
	shades := make([][3]int, n)
	r, g, b := float64(t.pieBaseR), float64(t.pieBaseG), float64(t.pieBaseB)
	for i := range n {
		// Spread from 35% to 100% brightness, evenly across n slices
		factor := 0.35 + 0.65*float64(i)/float64(n-1)
		shades[i] = [3]int{
			min(255, int(r*factor)),
			min(255, int(g*factor)),
			min(255, int(b*factor)),
		}
	}
	return shades
}

// HasPieBase reports whether this theme uses monochromatic pie shading.
func (t Theme) HasPieBase() bool {
	return t.hasPieBase
}

func buildThemeMono(name string, node, edge, arrow, subgraph, label, edgeLabel, sgLabel, def, sgFill string, pr, pg, pb int) Theme {
	t := buildTheme(name, node, edge, arrow, subgraph, label, edgeLabel, sgLabel, def, sgFill)
	t.pieBaseR, t.pieBaseG, t.pieBaseB = pr, pg, pb
	t.hasPieBase = true
	return t
}

func buildThemeWithDepth(name string, node, edge, arrow, subgraph, label, edgeLabel, sgLabel, def, sgFill string) Theme {
	t := buildTheme(name, node, edge, arrow, subgraph, label, edgeLabel, sgLabel, def, sgFill)
	t.hasDepthColors = true
	return t
}

func buildTheme(name string, node, edge, arrow, subgraph, label, edgeLabel, sgLabel, def, sgFill string) Theme {
	return Theme{
		Name:          name,
		Node:          buildANSI(node),
		Edge:          buildANSI(edge),
		Arrow:         buildANSI(arrow),
		Subgraph:      buildANSI(subgraph),
		Label:         buildANSI(label),
		EdgeLabel:     buildANSI(edgeLabel),
		SubgraphLabel: buildANSI(sgLabel),
		Default:       buildANSI(def),
		BoldLabel:     buildANSI("bold " + label),
		ItalicLabel:   buildANSI("italic " + label),
		Note:          buildANSI(node),
		SubgraphFill:  buildANSI(sgFill),
	}
}

// Themes is the set of available color themes.
var Themes = map[string]Theme{
	"default": buildTheme("default",
		"cyan",        // node
		"dim white",   // edge
		"bold yellow", // arrow
		"dim cyan",    // subgraph
		"bold white",  // label
		"italic dim",  // edge_label
		"bold cyan",   // subgraph_label
		"",            // default
		"",            // subgraph_fill
	),
	"terra": buildThemeMono("terra",
		"bold #D4845A", "#8B7E6A", "bold #E8A87C", "#A07858",
		"#F5E6D3", "italic #B89A7A", "bold #E8A87C", "", "",
		0xD4, 0x84, 0x5A, // warm brown
	),
	"neon": buildTheme("neon",
		"bold magenta", "dim cyan", "bold green", "dim magenta",
		"bold white", "italic cyan", "bold cyan", "", "",
	),
	"mono": buildThemeMono("mono",
		"bold white", "dim", "bold white", "dim",
		"white", "italic dim", "bold white", "", "",
		0xCC, 0xCC, 0xCC, // gray
	),
	"amber": buildThemeMono("amber",
		"bold #FFB000", "#806000", "bold #FFD080", "#906800",
		"#FFD580", "italic #B08030", "bold #FFC040", "", "",
		0xFF, 0xB0, 0x00, // amber
	),
	"blueprint": buildThemeWithDepth("blueprint",
		"bold #FFFFFF on #1A3A5C", // node
		"#6699CC",                 // edge
		"bold #FFD700",            // arrow
		"#4488AA on #14304A",      // subgraph (border+bg)
		"bold #FFFFFF on #1A3A5C", // label
		"italic #88BBDD",          // edge_label
		"bold #88CCFF",            // subgraph_label
		"",                        // default
		"on #14304A",              // subgraph_fill: slightly darker blue
	),
	"slate": buildThemeWithDepth("slate",
		"bold #E0E0E0 on #2D2D2D", // node
		"#808080",                 // edge
		"bold #FF6B35",            // arrow
		"#666666 on #222222",      // subgraph (border+bg)
		"bold #FFFFFF on #2D2D2D", // label
		"italic #999999",          // edge_label
		"bold #BBBBBB",            // subgraph_label
		"",                        // default
		"on #222222",              // subgraph_fill: slightly darker gray
	),
	"phosphor": buildThemeMono("phosphor",
		"bold #33FF33", "#1A8C1A", "bold #66FF66", "#228B22",
		"#AAFFAA", "italic #339933", "bold #55DD55", "", "",
		0x33, 0xFF, 0x33, // green phosphor
	),
	"sunset": buildThemeWithDepth("sunset",
		"bold #FFFFFF on #5C1A2A", // node: deep rose
		"#CC6677",                 // edge: dusty pink
		"bold #FFD700",            // arrow: gold
		"#AA4455 on #4A1422",      // subgraph
		"bold #FFFFFF on #5C1A2A", // label
		"italic #DD8899",          // edge_label
		"bold #FF8899",            // subgraph_label
		"",                        // default
		"on #4A1422",              // subgraph_fill: darker rose
	),
	"gruvbox": buildThemeWithDepth("gruvbox",
		"bold #EBDBB2 on #3C3836", // node: gruvbox fg on bg1
		"#928374",                 // edge: gray
		"bold #FABD2F",            // arrow: yellow
		"#7C6F64 on #282828",      // subgraph
		"bold #EBDBB2 on #3C3836", // label
		"italic #A89984",          // edge_label
		"bold #D5C4A1",            // subgraph_label
		"",                        // default
		"on #282828",              // subgraph_fill: gruvbox bg0
	),
	"monokai": buildThemeWithDepth("monokai",
		"bold #F8F8F2 on #3E3D32", // node: monokai fg on subtle bg
		"#75715E",                 // edge: comment gray
		"bold #F92672",            // arrow: monokai pink
		"#75715E on #272822",      // subgraph
		"bold #F8F8F2 on #3E3D32", // label
		"italic #A6E22E",          // edge_label: green
		"bold #66D9EF",            // subgraph_label: cyan
		"",                        // default
		"on #272822",              // subgraph_fill: monokai bg
	),
}

// GetTheme returns a theme by name, falling back to "default".
func GetTheme(name string) Theme {
	if t, ok := Themes[name]; ok {
		return t
	}
	return Themes["default"]
}

// ToColorString renders the canvas using ANSI colors based on the theme.
// Each cell's style key is mapped to the theme's ANSI sequence.
func (c *Canvas) ToColorString(theme Theme) string {
	styleMap := map[string]string{
		"node":           theme.Node,
		"edge":           theme.Edge,
		"arrow":          theme.Arrow,
		"subgraph":       theme.Subgraph,
		"label":          theme.Label,
		"edge_label":     theme.EdgeLabel,
		"subgraph_label": theme.SubgraphLabel,
		"default":        theme.Default,
		"bold_label":     theme.BoldLabel,
		"italic_label":   theme.ItalicLabel,
		"note":           theme.Note,
		"subgraph_fill":  theme.SubgraphFill,
	}

	rst := reset()
	var b strings.Builder
	b.Grow(c.Height * (c.Width*4 + 1)) // rough estimate with escape codes

	lastNonEmpty := c.Height - 1
	for lastNonEmpty >= 0 {
		empty := true
		for x := range c.Width {
			if c.grid[lastNonEmpty][x] != ' ' || c.fillGrid[lastNonEmpty][x] != "" {
				empty = false
				break
			}
		}
		if !empty {
			break
		}
		lastNonEmpty--
	}

	for y := 0; y <= lastNonEmpty; y++ {
		// Find last non-space column for trimming (preserve cells with fill)
		lastCol := c.Width - 1
		for lastCol >= 0 && c.grid[y][lastCol] == ' ' && c.fillGrid[y][lastCol] == "" {
			lastCol--
		}

		prevStyle := ""
		for x := 0; x <= lastCol; x++ {
			ch := c.grid[y][x]
			styleKey := c.styleGrid[y][x]
			fillKey := c.fillGrid[y][x]

			// Direct ANSI: style keys starting with "_ansi:" contain raw escape sequences
			var ansi string
			if strings.HasPrefix(styleKey, "_ansi:") {
				ansi = styleKey[6:]
			} else {
				ansi = styleMap[styleKey]
			}

			// Compose with fill layer: if there's a fill style and the content
			// style doesn't already have a background, prepend the fill's background
			if fillKey != "" {
				var fillAnsi string
				if strings.HasPrefix(fillKey, "_ansi:") {
					fillAnsi = fillKey[6:]
				} else {
					fillAnsi = styleMap[fillKey]
				}
				if fillAnsi != "" && !strings.Contains(ansi, "\033[48") {
					ansi = fillAnsi + ansi
				}
			}

			if ansi == "" {
				if prevStyle != "" {
					b.WriteString(rst)
					prevStyle = ""
				}
				b.WriteRune(ch)
			} else {
				if ansi != prevStyle {
					if prevStyle != "" {
						b.WriteString(rst)
					}
					b.WriteString(ansi)
					prevStyle = ansi
				}
				b.WriteRune(ch)
			}
		}
		if prevStyle != "" {
			b.WriteString(rst)
		}
		if y < lastNonEmpty {
			b.WriteByte('\n')
		}
	}

	return b.String()
}
