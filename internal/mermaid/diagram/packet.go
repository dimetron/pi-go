package diagram

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/dimetron/pi-go/internal/mermaid/renderer"
)

// packetField is one bit-range field in a packet diagram. end is inclusive.
type packetField struct {
	start, end int
	label      string
}

func (f packetField) bits() int { return f.end - f.start + 1 }

// packetData is the parsed `packet-beta` diagram.
type packetData struct {
	fields  []packetField
	rowBits int // bits per row (32 = standard network packet)
}

const packetBitsPerCol = 3 // character columns per bit

var (
	rePacketRange  = regexp.MustCompile(`^(\d+)\s*-\s*(\d+)\s*:\s*"?([^"]*)"?`)
	rePacketAuto   = regexp.MustCompile(`^\+(\d+)\s*:\s*"?([^"]*)"?`)
	rePacketSingle = regexp.MustCompile(`^(\d+)\s*:\s*"?([^"]*)"?`)
)

// parsePacket parses a Mermaid packet definition.
//
//	packet-beta
//	    0-15: "Source Port"
//	    16-31: "Destination Port"
//	    +32: "Sequence Number"   (auto-increment from the previous field)
func parsePacket(source string) *packetData {
	pd := &packetData{rowBits: 32}
	lines := strings.Split(source, "\n")
	if len(lines) == 0 {
		return pd
	}

	nextBit := 0
	for _, line := range lines[1:] { // skip the "packet-beta" header line
		if i := strings.Index(line, "%%"); i >= 0 {
			line = line[:i]
		}
		stripped := strings.TrimSpace(line)
		if stripped == "" {
			continue
		}

		if m := rePacketRange.FindStringSubmatch(stripped); m != nil {
			start, _ := strconv.Atoi(m[1])
			end, _ := strconv.Atoi(m[2])
			pd.fields = append(pd.fields, packetField{start, end, strings.TrimSpace(m[3])})
			nextBit = end + 1
			continue
		}
		if m := rePacketAuto.FindStringSubmatch(stripped); m != nil {
			count, _ := strconv.Atoi(m[1])
			start := nextBit
			end := start + count - 1
			pd.fields = append(pd.fields, packetField{start, end, strings.TrimSpace(m[2])})
			nextBit = end + 1
			continue
		}
		if m := rePacketSingle.FindStringSubmatch(stripped); m != nil {
			start, _ := strconv.Atoi(m[1])
			pd.fields = append(pd.fields, packetField{start, start, strings.TrimSpace(m[2])})
			nextBit = start + 1
			continue
		}
	}
	return pd
}

// packetRowField is a field clipped to a single output row.
type packetRowField struct {
	colStart, colEnd int
	label            string
}

// RenderPacket parses and renders a Mermaid packet diagram: bit-aligned
// field boxes that wrap every rowBits (32) bits, with boundary bit numbers
// and a legend for labels too wide to fit their field.
func RenderPacket(source string, useASCII bool, theme *renderer.Theme) *renderer.Canvas {
	pd := parsePacket(source)
	if len(pd.fields) == 0 {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[packet] no fields", "default")
		return c
	}

	rowBits := pd.rowBits
	colsPerRow := rowBits * packetBitsPerCol

	var hz, vt, tl, tr, bl, br, tj, bj rune
	if useASCII {
		hz, vt = '-', '|'
		tl, tr, bl, br = '+', '+', '+', '+'
		tj, bj = '+', '+'
	} else {
		hz, vt = '─', '│'
		tl, tr, bl, br = '╭', '╮', '╰', '╯'
		tj, bj = '┬', '┴'
	}

	const margin = 1
	const paddingY = 1

	// Group fields into rows, splitting any field that crosses a row boundary.
	var rows [][]packetRowField
	for _, f := range pd.fields {
		bit := f.start
		remainingLabel := f.label
		for bit <= f.end {
			rowIdx := bit / rowBits
			colInRow := bit % rowBits
			bitsThisRow := min(f.end-bit+1, rowBits-colInRow)
			for len(rows) <= rowIdx {
				rows = append(rows, nil)
			}
			rows[rowIdx] = append(rows[rowIdx], packetRowField{
				colStart: colInRow,
				colEnd:   colInRow + bitsThisRow - 1,
				label:    remainingLabel,
			})
			remainingLabel = ""
			bit += bitsThisRow
		}
	}
	// Drop trailing rows with no labels at all.
	for len(rows) > 0 {
		last := rows[len(rows)-1]
		empty := true
		for _, rf := range last {
			if rf.label != "" {
				empty = false
				break
			}
		}
		if !empty {
			break
		}
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		c := renderer.NewCanvas(30, 1)
		c.PutText(0, 0, "[packet] no fields", "default")
		return c
	}

	rowH := 3 + paddingY
	totalH := len(rows) * rowH
	totalW := margin + colsPerRow + 1

	c := renderer.NewCanvas(totalW+4, totalH+10) // extra height reserved for legend
	useRegion := theme != nil && theme.HasDepthColors()
	if useRegion {
		for r := 0; r < c.Height; r++ {
			for col := 0; col < c.Width; col++ {
				c.SetFill(r, col, "subgraph_fill")
			}
		}
	}

	const minFieldCols = 4 // a field needs this many cols to show its bit number

	for ri, rowFields := range rows {
		yNums := ri * rowH
		yTop := ri*rowH + 1
		yBottom := ri*rowH + 2 + paddingY
		yContent := yTop + (paddingY+1)/2
		rowStartBit := ri * rowBits

		placedNums := map[int]bool{}
		putNum := func(x int, label string) {
			c.PutText(yNums, x, label, "edge_label")
			for px := x; px < x+runeLen(label)+1; px++ {
				placedNums[px] = true
			}
		}
		freeFor := func(x int, label string) bool {
			for px := x; px < x+runeLen(label)+1; px++ {
				if placedNums[px] {
					return false
				}
			}
			return true
		}

		if len(rowFields) > 0 {
			// Row end number (right-aligned) and start number (left-aligned).
			lastCE := rowFields[len(rowFields)-1].colEnd
			endLabel := strconv.Itoa(rowStartBit + lastCE)
			putNum(margin+(lastCE+1)*packetBitsPerCol-runeLen(endLabel), endLabel)

			firstCS := rowFields[0].colStart
			startLabel := strconv.Itoa(rowStartBit + firstCS)
			putNum(margin+firstCS*packetBitsPerCol, startLabel)
		}

		// Intermediate boundary numbers, only where fields are wide enough.
		for fi := 1; fi < len(rowFields); fi++ {
			cur := rowFields[fi]
			prev := rowFields[fi-1]
			prevWidth := (prev.colEnd - prev.colStart + 1) * packetBitsPerCol
			curWidth := (cur.colEnd - cur.colStart + 1) * packetBitsPerCol

			if prevWidth >= minFieldCols {
				endLabel := strconv.Itoa(rowStartBit + prev.colEnd)
				ex := margin + (prev.colEnd+1)*packetBitsPerCol - runeLen(endLabel)
				if freeFor(ex, endLabel) {
					putNum(ex, endLabel)
				}
			}
			if curWidth >= minFieldCols {
				startLabel := strconv.Itoa(rowStartBit + cur.colStart)
				sx := margin + cur.colStart*packetBitsPerCol + 1
				if freeFor(sx, startLabel) {
					putNum(sx, startLabel)
				}
			}
		}

		// Top border with field separators.
		c.Put(yTop, margin, tl, false, "node")
		c.DrawHorizontal(yTop, margin+1, margin+colsPerRow-1, hz, "node")
		c.Put(yTop, margin+colsPerRow, tr, false, "node")
		for _, rf := range rowFields {
			if rf.colStart > 0 {
				c.Put(yTop, margin+rf.colStart*packetBitsPerCol, tj, false, "node")
			}
		}

		// Content rows: side and separator verticals.
		for py := range paddingY {
			yr := yTop + 1 + py
			c.Put(yr, margin, vt, false, "node")
			c.Put(yr, margin+colsPerRow, vt, false, "node")
			for _, rf := range rowFields {
				if rf.colStart > 0 {
					c.Put(yr, margin+rf.colStart*packetBitsPerCol, vt, false, "node")
				}
			}
		}

		// Centered (and truncated) field labels.
		for _, rf := range rowFields {
			if rf.label == "" {
				continue
			}
			xStart := margin + rf.colStart*packetBitsPerCol
			xEnd := margin + (rf.colEnd+1)*packetBitsPerCol
			avail := (xEnd - xStart) - 2
			disp := rf.label
			if runeLen(disp) > avail {
				cut := max(1, avail-1)
				disp = string([]rune(rf.label)[:cut]) + "."
			}
			lx := xStart + 1 + (avail-runeLen(disp))/2
			c.PutText(yContent, lx, disp, "label")
		}

		// Bottom border with field separators.
		c.Put(yBottom, margin, bl, false, "node")
		c.DrawHorizontal(yBottom, margin+1, margin+colsPerRow-1, hz, "node")
		c.Put(yBottom, margin+colsPerRow, br, false, "node")
		for _, rf := range rowFields {
			if rf.colStart > 0 {
				c.Put(yBottom, margin+rf.colStart*packetBitsPerCol, bj, false, "node")
			}
		}
	}

	// Legend for labels that had to be truncated to fit their field.
	type truncEntry struct {
		short, full string
		start, end  int
	}
	var truncated []truncEntry
	for _, f := range pd.fields {
		avail := f.bits()*packetBitsPerCol - 2
		if f.label != "" && avail < runeLen(f.label) {
			cut := max(1, avail-1)
			short := string([]rune(f.label)[:cut]) + "."
			truncated = append(truncated, truncEntry{short, f.label, f.start, f.end})
		}
	}
	if len(truncated) > 0 {
		yLegend := totalH + 1
		if needed := yLegend + len(truncated) + 1; needed > c.Height {
			c.Resize(c.Width, needed)
		}
		for i, e := range truncated {
			bits := "[" + strconv.Itoa(e.start) + "-" + strconv.Itoa(e.end) + "]"
			if e.start == e.end {
				bits = "[" + strconv.Itoa(e.start) + "]"
			}
			c.PutText(yLegend+i, margin, e.short+" = "+e.full+" "+bits, "edge_label")
		}
	}

	return c
}
