package tools

const (
	maxOutputBytes = 100 * 1024 // 100KB output limit
	maxLineLength  = 500        // max chars per match/content line
)

func truncateOutput(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + "\n... (output truncated)"
}

// truncateLine trims a single line to maxLineLength characters.
func truncateLine(s string) string {
	if len(s) <= maxLineLength {
		return s
	}
	return s[:maxLineLength] + "..."
}
