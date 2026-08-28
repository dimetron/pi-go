package renderer

import (
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/textwidth"
)

// boxChars is the set of all box-drawing characters that participate in
// junction merging.
var boxChars map[rune]bool

// junctionTable maps (existing, new) rune pairs to their merged result.
var junctionTable map[[2]rune]rune

func init() {
	boxChars = make(map[rune]bool)
	for _, r := range "─│┌┐└┘├┤┬┴┼━┃╋┄┆╭╮╰╯═║╔╗╚╝◆◇◯" {
		boxChars[r] = true
	}

	type triple struct{ a, b, c rune }

	pairs := []triple{
		{'─', '│', '┼'}, {'│', '─', '┼'},
		{'─', '┌', '┬'}, {'─', '┐', '┬'}, {'─', '└', '┴'}, {'─', '┘', '┴'},
		{'┌', '─', '┬'}, {'┐', '─', '┬'}, {'└', '─', '┴'}, {'┘', '─', '┴'},
		{'│', '┌', '├'}, {'│', '└', '├'}, {'│', '┐', '┤'}, {'│', '┘', '┤'},
		{'┌', '│', '├'}, {'└', '│', '├'}, {'┐', '│', '┤'}, {'┘', '│', '┤'},
		{'├', '─', '┼'}, {'┤', '─', '┼'}, {'┬', '│', '┼'}, {'┴', '│', '┼'},
		{'─', '├', '┼'}, {'─', '┤', '┼'}, {'│', '┬', '┼'}, {'│', '┴', '┼'},
		{'├', '┐', '┼'}, {'├', '┘', '┼'}, {'┤', '┌', '┼'}, {'┤', '└', '┼'},
		{'┬', '└', '┼'}, {'┬', '┘', '┼'}, {'┴', '┌', '┼'}, {'┴', '┐', '┼'},
		{'├', '┤', '┼'}, {'┤', '├', '┼'}, {'┬', '┴', '┼'}, {'┴', '┬', '┼'},
		{'┌', '┘', '┼'}, {'┘', '┌', '┼'}, {'┐', '└', '┼'}, {'└', '┐', '┼'},
		{'┌', '┐', '┬'}, {'┐', '┌', '┬'}, {'└', '┘', '┴'}, {'┘', '└', '┴'},
		{'┌', '└', '├'}, {'└', '┌', '├'}, {'┐', '┘', '┤'}, {'┘', '┐', '┤'},
		{'━', '┃', '╋'}, {'┃', '━', '╋'},
		{'┄', '┆', '┼'}, {'┆', '┄', '┼'},
		{'─', '┃', '┼'}, {'┃', '─', '┼'}, {'━', '│', '┼'}, {'│', '━', '┼'},
		{'─', '╭', '┬'}, {'─', '╮', '┬'}, {'─', '╰', '┴'}, {'─', '╯', '┴'},
		{'╭', '─', '┬'}, {'╮', '─', '┬'}, {'╰', '─', '┴'}, {'╯', '─', '┴'},
		{'│', '╭', '├'}, {'│', '╰', '├'}, {'│', '╮', '┤'}, {'│', '╯', '┤'},
		{'╭', '│', '├'}, {'╰', '│', '├'}, {'╮', '│', '┤'}, {'╯', '│', '┤'},
		{'╭', '╯', '┼'}, {'╯', '╭', '┼'}, {'╮', '╰', '┼'}, {'╰', '╮', '┼'},
		{'╭', '╮', '┬'}, {'╮', '╭', '┬'}, {'╰', '╯', '┴'}, {'╯', '╰', '┴'},
		{'╭', '╰', '├'}, {'╰', '╭', '├'}, {'╮', '╯', '┤'}, {'╯', '╮', '┤'},
		{'├', '╮', '┼'}, {'├', '╯', '┼'}, {'┤', '╭', '┼'}, {'┤', '╰', '┼'},
		{'┬', '╰', '┼'}, {'┬', '╯', '┼'}, {'┴', '╭', '┼'}, {'┴', '╮', '┼'},
		{'═', '│', '┼'}, {'│', '═', '┼'}, {'║', '─', '┼'}, {'─', '║', '┼'},
		{'╔', '─', '┬'}, {'╗', '─', '┬'}, {'╚', '─', '┴'}, {'╝', '─', '┴'},
		{'╔', '│', '├'}, {'╚', '│', '├'}, {'╗', '│', '┤'}, {'╝', '│', '┤'},
		{'║', '┌', '├'}, {'║', '└', '├'}, {'║', '┐', '┤'}, {'║', '┘', '┤'},
		{'═', '┌', '┬'}, {'═', '┐', '┬'}, {'═', '└', '┴'}, {'═', '┘', '┴'},
	}

	// Shape markers override any box-drawing character.
	allBox := "─│┌┐└┘├┤┬┴┼━┃╋┄┆╭╮╰╯═║╔╗╚╝"
	markers := []rune{'◆', '◇', '◯'}
	for _, marker := range markers {
		for _, bc := range allBox {
			pairs = append(pairs, triple{marker, bc, marker})
			pairs = append(pairs, triple{bc, marker, marker})
		}
	}

	junctionTable = make(map[[2]rune]rune, len(pairs))
	for _, p := range pairs {
		junctionTable[[2]rune{p.a, p.b}] = p.c
	}
}

// Canvas is a 2D character grid with optional per-cell style annotations.
// Indexing is row-major: grid[row][col].
type Canvas struct {
	Width     int
	Height    int
	grid      [][]rune
	styleGrid [][]string
	fillGrid  [][]string // background fill layer (composed with styleGrid in ToColorString)
}

// NewCanvas creates a Canvas of the given width and height, filled with spaces.
func NewCanvas(width, height int) *Canvas {
	grid := make([][]rune, height)
	styleGrid := make([][]string, height)
	fillGrid := make([][]string, height)
	for r := range height {
		row := make([]rune, width)
		srow := make([]string, width)
		frow := make([]string, width)
		for c := range width {
			row[c] = ' '
			srow[c] = "default"
		}
		grid[r] = row
		styleGrid[r] = srow
		fillGrid[r] = frow
	}
	return &Canvas{
		Width:     width,
		Height:    height,
		grid:      grid,
		styleGrid: styleGrid,
		fillGrid:  fillGrid,
	}
}

// Get returns the rune at (row, col). Out-of-bounds returns a space.
func (c *Canvas) Get(row, col int) rune {
	if row < 0 || row >= c.Height || col < 0 || col >= c.Width {
		return ' '
	}
	return c.grid[row][col]
}

// Put places a character on the canvas, optionally merging box-drawing junctions.
// Spaces are silently ignored. Out-of-bounds writes are silently ignored.
func (c *Canvas) Put(row, col int, ch rune, merge bool, style string) {
	if row < 0 || row >= c.Height || col < 0 || col >= c.Width {
		return
	}
	// Neutralize control characters at the one gate every glyph passes
	// through. Diagram text is attacker-influenceable — it arrives in a model
	// reply, and a model can be steered by a page it fetched or a file it was
	// asked to summarize — and a label carrying OSC 52 writes the viewer's
	// clipboard, OSC 0 rewrites the window title, and stray CSI corrupts the
	// alternate screen.
	//
	// The parser has stripControlChars, but it reaches only the flowchart
	// node-label path: edge labels leaked, and so did the other sixteen
	// diagram types. Guarding per parser has already failed once inside the
	// flowchart parser itself, so the check belongs here, where it covers
	// every renderer and every output form — ToString, ToColorString and
	// ToStyledPairs alike.
	//
	// A blank rather than a drop: the canvas is a fixed grid, so removing a
	// cell would shift the rest of the row.
	if ch < 0x20 || ch == 0x7f {
		ch = ' '
	}
	if ch == ' ' {
		return
	}
	existing := c.grid[row][col]
	if existing == ' ' {
		c.grid[row][col] = ch
	} else if merge && boxChars[existing] && boxChars[ch] {
		if merged, ok := junctionTable[[2]rune{existing, ch}]; ok {
			c.grid[row][col] = merged
		} else {
			c.grid[row][col] = ch
		}
	} else {
		c.grid[row][col] = ch
	}
	if style != "" {
		c.styleGrid[row][col] = style
	}
}

// wideContinuation marks the cell a double-width glyph spills into.
//
// The grid holds one rune per cell, but a wide glyph paints two terminal
// columns. Without a marker the cell to its right is printed as a space, so
// the line comes out one column too wide for every emoji it carries and the
// box border no longer lines up. Output skips these cells; nothing else ever
// writes one.
const wideContinuation = '\uFFFF'

// PutText places a string starting at (row, col) without junction merging.
//
// The column advances by each rune's display width, not by its index in the
// string. Ranging a Go string yields byte offsets, so using that offset as a
// column put the space after a four-byte emoji four cells along instead of
// one — "👤 You" was drawn as "👤    You", and every glyph after a non-ASCII
// one drifted right by its extra byte count.
//
// A double-width rune advances two cells so the glyph it draws over is not
// written twice, and a zero-width one is skipped: the grid holds one rune per
// cell and cannot compose a combining mark onto its neighbor, so placing it
// would replace the character it was meant to decorate.
func (c *Canvas) PutText(row, col int, text string, style string) {
	offset := 0
	for _, ch := range text {
		w := textwidth.Rune(ch)
		if w == 0 {
			continue
		}
		c.Put(row, col+offset, ch, false, style)
		if w == 2 && col+offset+1 >= 0 && col+offset+1 < c.Width && row >= 0 && row < c.Height {
			c.grid[row][col+offset+1] = wideContinuation
		}
		offset += w
	}
}

// PutStyledText places text with per-segment style keys.
// Each segment is a (text, style) pair.
func (c *Canvas) PutStyledText(row, col int, segments []StyledSegment) {
	offset := 0
	for _, seg := range segments {
		for _, ch := range seg.Text {
			w := textwidth.Rune(ch)
			if w == 0 {
				continue
			}
			c.Put(row, col+offset, ch, false, seg.Style)
			if w == 2 && col+offset+1 >= 0 && col+offset+1 < c.Width && row >= 0 && row < c.Height {
				c.grid[row][col+offset+1] = wideContinuation
			}
			offset += w
		}
	}
}

// StyledSegment is a run of text with an associated style key.
type StyledSegment struct {
	Text  string
	Style string
}

// ClearCell sets a cell back to a space with default style.
func (c *Canvas) ClearCell(row, col int) {
	if row < 0 || row >= c.Height || col < 0 || col >= c.Width {
		return
	}
	c.grid[row][col] = ' '
	c.styleGrid[row][col] = "default"
}

// SetFill sets a background fill style at (row, col).
// This is a separate layer that composes with the cell's content style in ToColorString.
// Content drawn on top keeps its foreground; the fill provides the background.
func (c *Canvas) SetFill(row, col int, fill string) {
	if row < 0 || row >= c.Height || col < 0 || col >= c.Width {
		return
	}
	c.fillGrid[row][col] = fill
}

// GetFill returns the fill style at (row, col).
func (c *Canvas) GetFill(row, col int) string {
	if row < 0 || row >= c.Height || col < 0 || col >= c.Width {
		return ""
	}
	return c.fillGrid[row][col]
}

// SetStyle sets the style key at (row, col) without changing the character.
func (c *Canvas) SetStyle(row, col int, style string) {
	if row < 0 || row >= c.Height || col < 0 || col >= c.Width {
		return
	}
	if style != "" {
		c.styleGrid[row][col] = style
	}
}

// GetStyle returns the style key at (row, col).
func (c *Canvas) GetStyle(row, col int) string {
	if row < 0 || row >= c.Height || col < 0 || col >= c.Width {
		return "default"
	}
	return c.styleGrid[row][col]
}

// DrawHorizontal draws a horizontal line from colStart to colEnd (inclusive).
func (c *Canvas) DrawHorizontal(row, colStart, colEnd int, ch rune, style string) {
	cMin, cMax := colStart, colEnd
	if cMin > cMax {
		cMin, cMax = cMax, cMin
	}
	for col := cMin; col <= cMax; col++ {
		c.Put(row, col, ch, true, style)
	}
}

// DrawVertical draws a vertical line from rowStart to rowEnd (inclusive).
func (c *Canvas) DrawVertical(col, rowStart, rowEnd int, ch rune, style string) {
	rMin, rMax := rowStart, rowEnd
	if rMin > rMax {
		rMin, rMax = rMax, rMin
	}
	for row := rMin; row <= rMax; row++ {
		c.Put(row, col, ch, true, style)
	}
}

// Resize expands the canvas to at least the given dimensions.
func (c *Canvas) Resize(newWidth, newHeight int) {
	if newWidth <= c.Width && newHeight <= c.Height {
		return
	}
	w := max(c.Width, newWidth)
	h := max(c.Height, newHeight)
	// Extend existing rows
	for r := range c.Height {
		for range w - c.Width {
			c.grid[r] = append(c.grid[r], ' ')
			c.styleGrid[r] = append(c.styleGrid[r], "default")
			c.fillGrid[r] = append(c.fillGrid[r], "")
		}
	}
	// Add new rows
	for range h - c.Height {
		row := make([]rune, w)
		srow := make([]string, w)
		frow := make([]string, w)
		for i := range w {
			row[i] = ' '
			srow[i] = "default"
		}
		c.grid = append(c.grid, row)
		c.styleGrid = append(c.styleGrid, srow)
		c.fillGrid = append(c.fillGrid, frow)
	}
	c.Width = w
	c.Height = h
}

// ToString renders the canvas to a string, trimming trailing whitespace.
func (c *Canvas) ToString() string {
	lines := make([]string, c.Height)
	for y := range c.Height {
		var b strings.Builder
		for x := range c.Width {
			if c.grid[y][x] == wideContinuation {
				continue // the second column of the wide glyph to its left
			}
			b.WriteRune(c.grid[y][x])
		}
		lines[y] = strings.TrimRight(b.String(), " ")
	}
	// Trim trailing empty lines
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// flipVerticalMap maps runes to their vertically flipped counterparts.
var flipVerticalMap = map[rune]rune{
	'┌': '└', '┐': '┘', '└': '┌', '┘': '┐',
	'├': '├', '┤': '┤', '┬': '┴', '┴': '┬',
	'▼': '▲', '▲': '▼',
	'╭': '╰', '╮': '╯', '╰': '╭', '╯': '╮',
	'v': '^', '^': 'v',
	'╔': '╚', '╗': '╝', '╚': '╔', '╝': '╗',
}

// flipHorizontalMap maps runes to their horizontally flipped counterparts.
var flipHorizontalMap = map[rune]rune{
	'┌': '┐', '┐': '┌', '└': '┘', '┘': '└',
	'├': '┤', '┤': '├', '┬': '┬', '┴': '┴',
	'►': '◄', '◄': '►',
	'╭': '╮', '╮': '╭', '╰': '╯', '╯': '╰',
	'>': '<', '<': '>',
	'╔': '╗', '╗': '╔', '╚': '╝', '╝': '╚',
}

// FlipVertical flips the canvas vertically (rows reversed, chars remapped).
func (c *Canvas) FlipVertical() {
	// Reverse rows
	for i, j := 0, c.Height-1; i < j; i, j = i+1, j-1 {
		c.grid[i], c.grid[j] = c.grid[j], c.grid[i]
		c.styleGrid[i], c.styleGrid[j] = c.styleGrid[j], c.styleGrid[i]
	}
	// Remap characters
	for r := range c.Height {
		for col := range c.Width {
			if mapped, ok := flipVerticalMap[c.grid[r][col]]; ok {
				c.grid[r][col] = mapped
			}
		}
	}
}

// FlipHorizontal flips the canvas horizontally (columns reversed, chars remapped).
func (c *Canvas) FlipHorizontal() {
	for r := range c.Height {
		// Reverse columns in this row
		for i, j := 0, c.Width-1; i < j; i, j = i+1, j-1 {
			c.grid[r][i], c.grid[r][j] = c.grid[r][j], c.grid[r][i]
			c.styleGrid[r][i], c.styleGrid[r][j] = c.styleGrid[r][j], c.styleGrid[r][i]
		}
		// Remap characters
		for col := range c.Width {
			if mapped, ok := flipHorizontalMap[c.grid[r][col]]; ok {
				c.grid[r][col] = mapped
			}
		}
	}
}

// StyledPair holds a rune and its associated style string.
type StyledPair struct {
	Char  rune
	Style string
}

// ToStyledPairs returns the canvas content as a 2D slice of StyledPairs.
func (c *Canvas) ToStyledPairs() [][]StyledPair {
	result := make([][]StyledPair, c.Height)
	for y := range c.Height {
		row := make([]StyledPair, c.Width)
		for x := range c.Width {
			row[x] = StyledPair{Char: c.grid[y][x], Style: c.styleGrid[y][x]}
		}
		result[y] = row
	}
	return result
}
