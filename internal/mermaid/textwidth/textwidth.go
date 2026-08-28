// Package textwidth measures how many terminal columns a string occupies.
//
// The diagram engine sized every box, centered every label and placed every
// edge caption with len(s) — the byte count. That is the display width only
// for ASCII. A label carrying an emoji measured four columns per glyph and
// drew two, so its box came out too wide and its text sat off-center; CJK had
// the opposite problem, measuring one column per glyph and drawing two, so the
// text overran its border.
//
// The rules implemented here are the ones that matter for diagram labels:
// combining marks and zero-width joiners take no space, East Asian Wide and
// Fullwidth characters and emoji take two columns, and everything else takes
// one. This is a deliberate subset of UAX #11 rather than the whole table —
// enough to place a label correctly, and small enough that this package keeps
// its property of depending on nothing outside the standard library.
package textwidth

import "unicode"

// String returns the number of terminal columns s occupies.
func String(s string) int {
	w := 0
	for _, e := range Split(s) {
		w += e.Width
	}
	return w
}

// Cell is one rune together with the columns it occupies.
type Cell struct {
	Rune  rune
	Width int
}

// Split walks s and reports each rune with the width it will actually render
// at, resolving the one thing a per-rune answer cannot: a variation selector.
//
// U+FE0F asks for emoji presentation, which makes an otherwise narrow symbol
// render two columns wide. It carries no width of its own, so measuring rune
// by rune misses it — "🛠️" is two columns and "🛠" is one, and the only
// difference between them is the selector that follows.
func Split(s string) []Cell {
	rs := []rune(s)
	out := make([]Cell, 0, len(rs))
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		w := Rune(r)
		if w == 0 {
			continue
		}
		if w == 1 && i+1 < len(rs) && rs[i+1] == 0xFE0F {
			w = 2 // emoji presentation requested
		}
		out = append(out, Cell{Rune: r, Width: w})
	}
	return out
}

// Max returns the width of the widest line in lines.
func Max(lines []string) int {
	widest := 0
	for _, l := range lines {
		if n := String(l); n > widest {
			widest = n
		}
	}
	return widest
}

// Rune returns the number of terminal columns r occupies: 0, 1 or 2.
func Rune(r rune) int {
	switch {
	case r == 0:
		return 0
	case zeroWidth(r):
		return 0
	case wide(r):
		return 2
	default:
		return 1
	}
}

// zeroWidth reports whether r occupies no columns of its own: combining marks
// that stack onto the previous glyph, and the joiners and selectors that glue
// an emoji sequence together. Counting these would inflate the width of every
// accented or composed label.
func zeroWidth(r rune) bool {
	switch {
	case r == 0x200B, r == 0x200C, r == 0x200D: // zero-width space/non-joiner/joiner
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
		return true
	case r >= 0xE0100 && r <= 0xE01EF: // variation selectors supplement
		return true
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r):
		return true
	}
	return false
}

// wideRanges lists the blocks that render two columns wide in a terminal:
// the East Asian Wide and Fullwidth blocks, and the emoji planes.
var wideRanges = [...][2]rune{
	{0x1100, 0x115F},   // Hangul Jamo initial consonants
	{0x2E80, 0x303E},   // CJK radicals, Kangxi, CJK symbols
	{0x3041, 0x33FF},   // Hiragana through CJK compatibility
	{0x3400, 0x4DBF},   // CJK extension A
	{0x4E00, 0x9FFF},   // CJK unified ideographs
	{0xA000, 0xA4CF},   // Yi
	{0xAC00, 0xD7A3},   // Hangul syllables
	{0xF900, 0xFAFF},   // CJK compatibility ideographs
	{0xFE10, 0xFE19},   // vertical forms
	{0xFE30, 0xFE6F},   // CJK compatibility forms
	{0xFF00, 0xFF60},   // fullwidth forms
	{0xFFE0, 0xFFE6},   // fullwidth signs
	{0x1F300, 0x1F64F}, // symbols, pictographs, emoticons
	{0x1F680, 0x1F6FF}, // transport and map symbols
	{0x1F900, 0x1F9FF}, // supplemental symbols and pictographs
	{0x1FA70, 0x1FAFF}, // symbols and pictographs extended-A
	{0x20000, 0x2FFFD}, // CJK extension B and beyond
	{0x30000, 0x3FFFD},
}

// textPresentation lists the codepoints inside the emoji blocks below that
// Unicode gives Emoji_Presentation=No: they are symbols first and emoji only
// when followed by U+FE0F, and a terminal draws them one column wide.
//
// Treating the emoji blocks as uniformly wide made these overcount, so a label
// carrying one measured a column wider than it drew and its box centered a
// column off — visible as a line of text sitting left of the box it is in
// while its neighbors sit straight.
func textPresentation(r rune) bool {
	switch {
	case r >= 0x1F321 && r <= 0x1F32C, r == 0x1F336, r == 0x1F37D,
		r >= 0x1F396 && r <= 0x1F397, r >= 0x1F399 && r <= 0x1F39B,
		r >= 0x1F39E && r <= 0x1F39F, r >= 0x1F3CB && r <= 0x1F3CE,
		r >= 0x1F3D4 && r <= 0x1F3DF, r >= 0x1F3F3 && r <= 0x1F3F5,
		r == 0x1F3F7, r == 0x1F43F, r == 0x1F441, r == 0x1F4FD,
		r >= 0x1F549 && r <= 0x1F54A, r >= 0x1F56F && r <= 0x1F570,
		r >= 0x1F573 && r <= 0x1F57A, r == 0x1F587,
		r >= 0x1F58A && r <= 0x1F58D, r == 0x1F590, r == 0x1F5A5,
		r == 0x1F5A8, r >= 0x1F5B1 && r <= 0x1F5B2, r == 0x1F5BC,
		r >= 0x1F5C2 && r <= 0x1F5C4, r >= 0x1F5D1 && r <= 0x1F5D3,
		r >= 0x1F5DC && r <= 0x1F5DE, r == 0x1F5E1, r == 0x1F5E3,
		r == 0x1F5E8, r == 0x1F5EF, r == 0x1F5F3, r == 0x1F5FA,
		r >= 0x1F6CB && r <= 0x1F6CF, r >= 0x1F6E0 && r <= 0x1F6E5,
		r == 0x1F6E9, r == 0x1F6F0, r == 0x1F6F3:
		return true
	}
	return false
}

// wide reports whether r renders two columns wide.
func wide(r rune) bool {
	if textPresentation(r) {
		return false
	}
	// A handful of older symbols are emoji-presentation by default and sit
	// outside the blocks above; they are the ones that show up in real
	// diagram labels rather than an exhaustive list.
	switch r {
	case 0x231A, 0x231B, 0x23E9, 0x23EA, 0x23EB, 0x23EC, 0x23F0, 0x23F3,
		0x25FD, 0x25FE, 0x2614, 0x2615, 0x2648, 0x267F, 0x2693, 0x26A1,
		0x26AA, 0x26AB, 0x26BD, 0x26BE, 0x26C4, 0x26C5, 0x26CE, 0x26D4,
		0x26EA, 0x26F2, 0x26F3, 0x26F5, 0x26FA, 0x26FD, 0x2705, 0x270A,
		0x270B, 0x2728, 0x274C, 0x274E, 0x2753, 0x2754, 0x2755, 0x2757,
		0x2795, 0x2796, 0x2797, 0x27B0, 0x27BF, 0x2B1B, 0x2B1C, 0x2B50,
		0x2B55:
		return true
	}
	for _, rg := range wideRanges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}
