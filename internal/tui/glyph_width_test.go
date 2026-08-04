package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"golang.org/x/text/width"
)

// Why this test exists
//
// ansi.StringWidth — what every layout calculation in this package measures with
// — counts an East Asian Ambiguous rune as one cell. A terminal configured for
// CJK (or one that falls back to a CJK font because the primary font has no
// glyph) draws it as two. The row is then one cell wider than we measured, the
// rail slides off its column, and the rows that carry decoration — the matrix
// tape, the rules, the bullets — visibly shift while plain text rows stay put.
//
// Fonts do not fix this and neither does a wider terminal. The only reliable
// remedy is to draw chrome from runes whose width no terminal is free to
// reinterpret, so that is what these tests pin.

// ambiguousChrome lists the ambiguous-width runes the TUI knowingly keeps. Box
// drawing has no Neutral equivalent — dropping it means dropping rules, rails
// and joints altogether — and every terminal pi targets renders these at one
// cell unless the user has explicitly opted into wide ambiguous glyphs. Adding
// to this set is a deliberate act; the rest of the chrome may not be ambiguous
// at all.
var ambiguousChrome = map[rune]string{
	'─': "panel and full-width rules",
	'│': "rail track",
	'┴': "rail foot",
	'╭': "popup border",
	'╮': "popup border",
	'╰': "popup border",
	'╯': "popup border",
	'├': "popup border",
	'┤': "popup border",
	'█': "progress bar fill",
	'░': "progress bar track",
	'▌': "block marker",
	'π': "the mascot's nose — the product's name, kept deliberately",
}

// widthSafe reports whether r is guaranteed to occupy exactly one cell in any
// terminal: no ambiguity for the width table to resolve differently, and no
// double-width form.
func widthSafe(r rune) bool {
	switch width.LookupRune(r).Kind() {
	case width.EastAsianAmbiguous, width.EastAsianWide, width.EastAsianFullwidth:
		return false
	default:
		return ansi.StringWidth(string(r)) == 1
	}
}

// The matrix tape is the top row of the frame, so a single mis-measured glyph in
// it shifts every column to its right — including the rail. It is also purely
// decorative, which means there is no reason to spend a risky rune on it.
func TestMatrixTapeIsWidthSafe(t *testing.T) {
	for _, r := range matrixRunes {
		if !widthSafe(r) {
			t.Errorf("matrixChars contains width-unsafe rune %q (U+%04X, %v)",
				r, r, width.LookupRune(r).Kind())
		}
		// Braille is the one block that is both Neutral in every width table and
		// present in every monospace font pi ships against.
		if r < 0x2800 || r > 0x28FF {
			t.Errorf("matrixChars contains non-braille rune %q (U+%04X); the tape is braille-only", r, r)
		}
	}
}

// The rail owns one column on every row. A two-cell thumb would widen exactly
// the rows it appears on, which reads as the conversation jittering as you
// scroll.
func TestRailGlyphsAreOneCell(t *testing.T) {
	for _, g := range []string{railGlyph, railThumb, railFoot} {
		if got := ansi.StringWidth(g); got != 1 {
			t.Errorf("rail glyph %q measures %d cells, want 1", g, got)
		}
		for _, r := range g {
			if !widthSafe(r) {
				if _, allowed := ambiguousChrome[r]; !allowed {
					t.Errorf("rail glyph %q is width-unsafe (U+%04X, %v) and not in ambiguousChrome",
						g, r, width.LookupRune(r).Kind())
				}
			}
		}
	}
	// The thumb must stay distinguishable from the track, or the scroll position
	// disappears.
	if railThumb == railGlyph {
		t.Error("rail thumb and track are the same glyph")
	}
}

// The mascot sits in the sidebar's first rows and changes with the agent's
// mood. If one mood's face measures differently from another's, the top of the
// frame shifts as the agent works — which is what ★ (Ambiguous) in the happy
// face and ◑ (Ambiguous) in the processing face used to do.
func TestMascotGlyphsAreWidthSafe(t *testing.T) {
	moods := []AgentMood{
		MoodIdle, MoodThinking, MoodProcessing,
		MoodToolCall, MoodSpeaking, MoodHappy, MoodSad,
	}

	var wantRows []int
	for _, mood := range moods {
		face, ok := moodMascot[mood]
		if !ok {
			t.Fatalf("mood %v has no mascot", mood)
		}

		for _, r := range face + moodEyes[mood] {
			if r == '\n' || widthSafe(r) {
				continue
			}
			if _, allowed := ambiguousChrome[r]; allowed {
				continue
			}
			t.Errorf("mood %v draws width-unsafe rune %q (U+%04X, %v)",
				mood, r, r, width.LookupRune(r).Kind())
		}

		// Every mood must also occupy the same box, or the sidebar reflows on
		// each mood change even when every glyph is one cell.
		rows := strings.Split(face, "\n")
		widths := make([]int, len(rows))
		for i, row := range rows {
			widths[i] = ansi.StringWidth(row)
		}
		if wantRows == nil {
			wantRows = widths
			continue
		}
		if len(widths) != len(wantRows) {
			t.Errorf("mood %v mascot has %d rows, want %d", mood, len(widths), len(wantRows))
			continue
		}
		for i := range widths {
			if widths[i] != wantRows[i] {
				t.Errorf("mood %v mascot row %d is %d cells, want %d — the face changes width with mood",
					mood, i, widths[i], wantRows[i])
			}
		}
	}

	if got := ansi.StringWidth(moodEyes[MoodIdle]); got != 3 {
		t.Errorf("idle eyes measure %d cells, want 3", got)
	}
}

// The chrome the TUI draws around messages — bullets, spinners, sidebar markers
// — must not smuggle in an ambiguous rune. Anything found here must either be
// swapped for a Neutral equivalent or declared in ambiguousChrome.
func TestChromeGlyphsAreWidthSafe(t *testing.T) {
	chrome := map[string]string{
		"matrixChars":    matrixChars,
		"railGlyph":      railGlyph,
		"railThumb":      railThumb,
		"railFoot":       railFoot,
		"spinnerSymbols": string(spinnerSymbols),
	}

	for name, s := range chrome {
		for _, r := range s {
			if widthSafe(r) {
				continue
			}
			if reason, allowed := ambiguousChrome[r]; allowed {
				t.Logf("%s: keeping ambiguous %q (U+%04X) for %s", name, r, r, reason)
				continue
			}
			t.Errorf("%s contains width-unsafe rune %q (U+%04X, %v); swap it for a Neutral glyph or add it to ambiguousChrome",
				name, r, r, width.LookupRune(r).Kind())
		}
	}
}

// A frame rendered from realistic content must not contain an ambiguous rune
// outside the declared chrome set. Message text is the model's to write, so only
// the glyphs pi itself draws are in scope — the check runs over a frame built
// from messages that carry no exotic runes of their own.
func TestFrameChromeHasNoUndeclaredAmbiguousRunes(t *testing.T) {
	m := historyModel(t, "first")
	m.width, m.height = 120, 40
	m.applyResize()
	m.chatModel.Messages = []message{
		{role: "user", content: "plain ascii request"},
		{role: "assistant", content: "plain ascii reply"},
		{role: "tool", tool: "read", toolIn: `{"file_path":"server.go"}`, content: "package main"},
	}

	plain := ansi.Strip(m.View().Content)
	seen := map[rune]bool{}
	for _, r := range plain {
		if r == '\n' || r < 0x80 || seen[r] || widthSafe(r) {
			continue
		}
		seen[r] = true
		if _, allowed := ambiguousChrome[r]; !allowed {
			t.Errorf("rendered frame contains width-unsafe rune %q (U+%04X, %v) drawn by the TUI",
				r, r, width.LookupRune(r).Kind())
		}
	}

	// Guard the guard: a frame with no non-ASCII runes at all would pass this
	// vacuously, which would hide a regression rather than catch one.
	if !strings.ContainsAny(plain, string(railGlyph)) {
		t.Fatal("frame has no rail; the scan proves nothing")
	}
}
