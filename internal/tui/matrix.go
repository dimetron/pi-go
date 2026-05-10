package tui

import (
	"image/color"
	"math/rand"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// matrixChars is a curated snake-like tape for a smooth single-line scrolling
// effect: soft dot ramps, wave dips, sparkles, braille, and block peaks.
const matrixChars = "⠀⠀⠁⠂⠄⠠⡀⢀⣀⣠⣤⣶⣷⣿⣿⣿⣷⣶⣤⣠⣀⢀⡀⠠⠄⠂⠁⌣·∙⋅∘◦○◌◎●◎◌○◦∘⋅∙·⠐⠈⠈⠐⠠⢀⣠⣴⣶⣷⣾⣿⣿⣾⣷⣶⣴⣠⢀⠠⠐⠈⠀⌣⌒⌁∿≈~˖⋆✧✦⊹●⊹✦✧⋆˖~≈∿⌁⌒⌣⠀⠁⠂⠄⡀⢀⣀⣠⣤⣶⣿⣶⣤⣠⣀⢀⡀⠄⠂⠁⌣"

// matrixRunes is the precomputed rune slice from matrixChars.
var matrixRunes = []rune(matrixChars)

// matrixColors are Catppuccin Mocha palette shades for matrix rain characters.
// Ordered from dimmest to brightest for position-based gradient.
var matrixColors = []color.Color{
	lipgloss.Color("#1e1e2e"), // base (nearly invisible)
	lipgloss.Color("#313244"), // surface0
	lipgloss.Color("#45475a"), // surface1
	lipgloss.Color("#585b70"), // surface2
	lipgloss.Color("#6c7086"), // overlay0
	lipgloss.Color("#7f849c"), // overlay1
	lipgloss.Color("#89b4fa"), // blue
	lipgloss.Color("#74c7ec"), // sapphire
	lipgloss.Color("#94e2d5"), // teal
	lipgloss.Color("#b4befe"), // lavender
	lipgloss.Color("#cba6f7"), // mauve
	lipgloss.Color("#f5c2e7"), // pink
}

// matrixLines is the number of matrix rain lines to render.
const matrixLines = 1

// matrixCell holds a single character and its color.
type matrixCell struct {
	ch    rune
	shade int // index into matrixColors
}

// matrixState holds the current matrix rain display buffer.
type matrixState struct {
	grid      [matrixLines][]matrixCell // per-cell character grid
	width     int                       // current grid width (60% of full)
	fullWidth int                       // full terminal width for centering
	seed      int64                     // accumulated entropy from token bytes
	active    bool
}

// randCell generates a random cell using the current RNG.
// shade carries randomness used as jitter in the position-based gradient.
func randCell(rng *rand.Rand) matrixCell {
	return matrixCell{
		ch:    matrixRunes[rng.Intn(len(matrixRunes))],
		shade: rng.Intn(len(matrixColors)),
	}
}

// ensureWidth resizes the grid rows if the width changed.
func (ms *matrixState) ensureWidth(width int) {
	if ms.width == width {
		return
	}
	ms.width = width
	rng := rand.New(rand.NewSource(ms.seed))
	for row := 0; row < matrixLines; row++ {
		ms.grid[row] = make([]matrixCell, width)
		for col := 0; col < width; col++ {
			ms.grid[row][col] = randCell(rng)
		}
	}
}

// shiftLeft shifts all rows left by one, inserting a new random character on the right.
func (ms *matrixState) shiftLeft() {
	rng := rand.New(rand.NewSource(ms.seed))
	for row := 0; row < matrixLines; row++ {
		if len(ms.grid[row]) == 0 {
			continue
		}
		copy(ms.grid[row], ms.grid[row][1:])
		ms.grid[row][len(ms.grid[row])-1] = randCell(rng)
		// Advance rng so each row gets different chars
		ms.seed = ms.seed*31 + int64(row+1)
		rng = rand.New(rand.NewSource(ms.seed))
	}
}

// renderLine renders a single grid row with a center-bright gradient.
func (ms *matrixState) renderLine(row int) string {
	n := len(ms.grid[row])
	if n == 0 {
		return ""
	}
	maxShade := len(matrixColors) - 1
	mid := float64(n) / 2.0

	var sb strings.Builder
	for i, cell := range ms.grid[row] {
		// Distance from center: 0.0 (center) to 1.0 (edge).
		dist := 1.0
		if mid > 0 {
			d := float64(i) - mid
			if d < 0 {
				d = -d
			}
			dist = d / mid
		}
		// Map: center → brightest (maxShade), edges → dimmest (0).
		shade := int(float64(maxShade) * (1.0 - dist))
		if shade < 0 {
			shade = 0
		}
		if shade > maxShade {
			shade = maxShade
		}
		// Add slight randomness from cell shade to avoid uniformity.
		jitter := (cell.shade - maxShade/2) / 3
		shade += jitter
		if shade < 0 {
			shade = 0
		}
		if shade > maxShade {
			shade = maxShade
		}
		style := lipgloss.NewStyle().Foreground(matrixColors[shade])
		sb.WriteString(style.Render(string(cell.ch)))
	}
	return sb.String()
}

// feed mixes token text into the entropy and shifts characters left.
// The number of shifts scales with input length so that large tool outputs
// (e.g. bash results) produce a visible burst of motion.
func (ms *matrixState) feed(tokenText string, width int) {
	for _, b := range []byte(tokenText) {
		ms.seed = ms.seed*31 + int64(b)
	}
	ms.active = true
	ms.fullWidth = width
	matrixW := width * 60 / 100
	if matrixW < 1 {
		matrixW = width
	}
	ms.ensureWidth(matrixW)

	// Scale shifts: 1 per ~64 bytes, minimum 1, capped at 1/4 of width.
	shifts := len(tokenText) / 64
	if shifts < 1 {
		shifts = 1
	}
	maxShifts := matrixW / 4
	if maxShifts < 1 {
		maxShifts = 1
	}
	if shifts > maxShifts {
		shifts = maxShifts
	}
	for i := 0; i < shifts; i++ {
		ms.shiftLeft()
	}
}

// render returns the matrix rain string centered at 60% width, or empty if inactive.
func (ms *matrixState) render() string {
	if !ms.active {
		return ""
	}
	// Center the matrix line within fullWidth.
	pad := 0
	if ms.fullWidth > ms.width {
		pad = (ms.fullWidth - ms.width) / 2
	}
	prefix := strings.Repeat(" ", pad)
	lines := make([]string, matrixLines)
	for row := 0; row < matrixLines; row++ {
		lines[row] = prefix + ms.renderLine(row)
	}
	return strings.Join(lines, "\n")
}

// tick advances the matrix animation by one step using time-based entropy.
func (ms *matrixState) tick(width int) {
	if !ms.active {
		return
	}
	ms.seed = ms.seed*31 + time.Now().UnixNano()
	ms.fullWidth = width
	matrixW := width * 60 / 100
	if matrixW < 1 {
		matrixW = width
	}
	ms.ensureWidth(matrixW)
	ms.shiftLeft()
}

// clear resets the matrix display.
func (ms *matrixState) clear() {
	ms.grid = [matrixLines][]matrixCell{}
	ms.width = 0
	ms.active = false
}

// matrixTickInterval is how often the matrix animates when stale.
const matrixTickInterval = 150 * time.Millisecond

// matrixTickMsg is sent periodically to animate the matrix rain.
type matrixTickMsg struct{}

// matrixTickCmd returns a command that sends a matrixTickMsg after the interval.
func matrixTickCmd() tea.Cmd {
	return tea.Tick(matrixTickInterval, func(time.Time) tea.Msg {
		return matrixTickMsg{}
	})
}
