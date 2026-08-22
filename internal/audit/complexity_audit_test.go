package audit

import "testing"

// TestIsEmojiRangeBoundaries pins every range in emojiRanges at both ends and
// one code point outside each end. Converting the original || chain into a
// table is only safe if no bound moved, so each range contributes four cases.
func TestIsEmojiRangeBoundaries(t *testing.T) {
	// The ranges as the original || chain spelled them, restated literally so
	// the test does not read its expectations out of the table it is checking.
	ranges := []struct {
		name   string
		lo, hi rune
	}{
		{"Emoticons", 0x1F600, 0x1F64F},
		{"MiscSymbolsAndPictographs", 0x1F300, 0x1F5FF},
		{"TransportAndMap", 0x1F680, 0x1F6FF},
		{"Flags", 0x1F1E0, 0x1F1FF},
		{"MiscSymbols", 0x2600, 0x26FF},
		{"Dingbats", 0x2700, 0x27BF},
		{"SupplementalSymbols", 0x1F900, 0x1F9FF},
		{"ChessSymbols", 0x1FA00, 0x1FA6F},
		{"ExtendedA", 0x1FA70, 0x1FAFF},
	}

	// Bounds that abut a neighboring range: one past the edge is still an
	// emoji, so "just outside" cannot be asserted there.
	inRange := func(r rune) bool {
		for _, rg := range ranges {
			if r >= rg.lo && r <= rg.hi {
				return true
			}
		}
		return false
	}

	for _, rg := range ranges {
		t.Run(rg.name, func(t *testing.T) {
			if !isEmoji(rg.lo) {
				t.Errorf("isEmoji(%#x) = false, want true (low bound)", rg.lo)
			}
			if !isEmoji(rg.hi) {
				t.Errorf("isEmoji(%#x) = false, want true (high bound)", rg.hi)
			}
			if below := rg.lo - 1; !inRange(below) && isEmoji(below) {
				t.Errorf("isEmoji(%#x) = true, want false (below low bound)", below)
			}
			if above := rg.hi + 1; !inRange(above) && isEmoji(above) {
				t.Errorf("isEmoji(%#x) = true, want false (above high bound)", above)
			}
		})
	}
}

// TestIsEmojiNonEmoji covers code points far from any range, including the
// ASCII and Latin planes the scanner sees on every ordinary file.
func TestIsEmojiNonEmoji(t *testing.T) {
	for _, r := range []rune{
		0x0000, 'a', 'Z', '9', ' ', '\n',
		0x00E9,           // é
		0x0400,           // Cyrillic
		0x200B,           // zero-width space — hidden, but not an emoji
		0x2500,           // box drawing, just below Misc symbols
		0x2800,           // Braille, just above Dingbats
		0x1F000, 0x1F2FF, // below Misc Symbols and Pictographs
		0x1F700, 0x1F8FF, // between Transport and Supplemental
		0x1FB00, 0x10FFFF, // above every range
	} {
		if isEmoji(r) {
			t.Errorf("isEmoji(%#x) = true, want false", r)
		}
	}
}

// TestIsEmojiTableMatchesRanges asserts the table is contiguous with what the
// original expression encoded: nine ranges, each non-empty and correctly
// ordered. A transposed pair would make every member of that range fail.
func TestIsEmojiTableWellFormed(t *testing.T) {
	if got, want := len(emojiRanges), 9; got != want {
		t.Fatalf("len(emojiRanges) = %d, want %d", got, want)
	}
	for i, rg := range emojiRanges {
		if rg.lo > rg.hi {
			t.Errorf("emojiRanges[%d] = {%#x, %#x}: lo > hi", i, rg.lo, rg.hi)
		}
	}
}

// TestIsEmojiViaScanText checks the refactor through the caller that uses it,
// so the table is exercised on the real scanning path and not only directly.
func TestIsEmojiViaScanText(t *testing.T) {
	// A lone emoji is not itself a finding; the point is that scanning text
	// containing one still terminates and reports the same shape as before.
	res := ScanText("hello \U0001F600 world", "emoji.md")
	if res == nil {
		t.Fatal("ScanText returned nil")
	}
	if len(res.Files) != 1 || res.Files[0] != "emoji.md" {
		t.Errorf("Files = %v, want [emoji.md]", res.Files)
	}
}

// isEmojiOriginal is the expression isEmoji held before it became a table,
// transcribed unchanged. It exists only so the table can be proved equivalent.
func isEmojiOriginal(r rune) bool {
	return (r >= 0x1F600 && r <= 0x1F64F) || // Emoticons
		(r >= 0x1F300 && r <= 0x1F5FF) || // Misc Symbols and Pictographs
		(r >= 0x1F680 && r <= 0x1F6FF) || // Transport and Map
		(r >= 0x1F1E0 && r <= 0x1F1FF) || // Flags
		(r >= 0x2600 && r <= 0x26FF) || // Misc symbols
		(r >= 0x2700 && r <= 0x27BF) || // Dingbats
		(r >= 0x1F900 && r <= 0x1F9FF) || // Supplemental Symbols
		(r >= 0x1FA00 && r <= 0x1FA6F) || // Chess Symbols
		(r >= 0x1FA70 && r <= 0x1FAFF) // Symbols and Pictographs Extended-A
}

// TestIsEmojiExhaustivelyMatchesOriginal walks every code point in Unicode and
// asserts the table agrees with the original || chain. Sampling boundaries
// leaves an off-by-one invisible until a user pastes the one character that
// moved; over 1.1M code points there is nowhere for one to hide.
func TestIsEmojiExhaustivelyMatchesOriginal(t *testing.T) {
	mismatches := 0
	for r := rune(0); r <= 0x10FFFF; r++ {
		if got, want := isEmoji(r), isEmojiOriginal(r); got != want {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("isEmoji(%#x) = %v, original = %v", r, got, want)
			}
		}
	}
	if mismatches > 10 {
		t.Errorf("%d code points disagree in total", mismatches)
	}
}
