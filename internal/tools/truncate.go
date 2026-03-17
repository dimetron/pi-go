package tools

const (
	maxOutputBytes = 256 * 1024 // 256KB safety limit (smart compaction happens in AfterToolCallback)
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
