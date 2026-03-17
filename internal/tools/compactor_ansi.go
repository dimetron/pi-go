package tools

import "regexp"

// ansiPattern matches ANSI escape sequences (SGR, cursor movement, etc.).
var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x07|\x1b\(B`)

// stripAnsi removes all ANSI escape codes from the input.
func stripAnsi(s string) (string, bool) {
	if !ansiPattern.MatchString(s) {
		return s, false
	}
	return ansiPattern.ReplaceAllString(s, ""), true
}

// hardTruncate enforces an absolute character limit on output.
func hardTruncate(s string, maxChars int) (string, bool) {
	if maxChars <= 0 || len(s) <= maxChars {
		return s, false
	}
	return s[:maxChars] + "\n... (output truncated)", true
}

// hardTruncateLines enforces an absolute line limit on output.
func hardTruncateLines(s string, maxLines int) (string, bool) {
	if maxLines <= 0 {
		return s, false
	}

	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			count++
			if count >= maxLines {
				return s[:i] + "\n... (output truncated)", true
			}
		}
	}
	return s, false
}
