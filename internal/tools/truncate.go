package tools

import "unicode/utf8"

const (
	maxOutputBytes = 256 * 1024 // 256KB safety limit (smart compaction happens in AfterToolCallback)
	maxLineLength  = 500        // max chars per match/content line
)

// utf8SafeCut returns s truncated to at most n bytes, backing up to the
// nearest rune boundary so the result is never invalid UTF-8.
//
// A plain s[:n] can land in the middle of a multi-byte rune, and the resulting
// broken byte is carried all the way to the provider.
func utf8SafeCut(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if n >= len(s) {
		return s
	}
	// A rune is at most 4 bytes, so at most 3 bytes need to be given back.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// utf8SafeCutFrom returns the suffix of s starting at or after byte offset n,
// advanced to the nearest rune boundary.
func utf8SafeCutFrom(s string, n int) string {
	if n <= 0 {
		return s
	}
	if n >= len(s) {
		return ""
	}
	for n < len(s) && !utf8.RuneStart(s[n]) {
		n++
	}
	return s[n:]
}

func truncateOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return utf8SafeCut(s, maxOutputBytes) + "\n... (output truncated)"
}

// truncateLine trims a single line to maxLineLength characters.
func truncateLine(s string) string {
	if len(s) <= maxLineLength {
		return s
	}
	return utf8SafeCut(s, maxLineLength) + "..."
}
