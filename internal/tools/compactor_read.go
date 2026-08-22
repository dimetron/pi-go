package tools

import (
	"strings"
)

// compactRead applies the read tool compaction pipeline.
func compactRead(result map[string]any, cfg CompactorConfig) *CompactResult {
	content, _ := result["content"].(string)
	if content == "" {
		return nil
	}

	origSize := len(content)
	var techniques []string

	// Stage 1: ANSI stripping
	if cfg.StripAnsi {
		content = runStage(content, &techniques, "ansi", func(s string) (string, bool) {
			return stripAnsi(s)
		})
	}

	// Stage 2: Source code filtering
	if cfg.SourceCodeFiltering != "none" && cfg.SourceCodeFiltering != "" {
		content = runStage(content, &techniques, "source-filter", func(s string) (string, bool) {
			return filterSourceCode(s, cfg.SourceCodeFiltering)
		})
	}

	// Stage 3: Smart truncation
	if cfg.SmartTruncate {
		content = runStage(content, &techniques, "smart-truncate", func(s string) (string, bool) {
			return smartTruncate(s, cfg)
		})
	}

	// Stage 4: Hard truncation
	content = runStage(content, &techniques, "hard-truncate", func(s string) (string, bool) {
		return hardTruncate(s, cfg.MaxChars)
	})
	content = runStage(content, &techniques, "hard-truncate-lines", func(s string) (string, bool) {
		return hardTruncateLines(s, cfg.MaxLines)
	})
	techniques = dedup(techniques)

	compSize := len(content)
	if compSize >= origSize {
		return nil
	}

	return &CompactResult{
		Output:     content,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// filterSourceCode removes comments and blank line runs based on the filtering level.
func filterSourceCode(s string, level string) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) < 50 {
		return s, false // not worth filtering short files
	}

	f := sourceLineFilter{level: level}
	var filtered []string
	for _, line := range lines {
		if f.keep(line) {
			filtered = append(filtered, line)
		}
	}

	if len(filtered) >= len(lines) {
		return s, false
	}

	return strings.Join(filtered, "\n"), true
}

// sourceLineFilter carries the line-by-line state filterSourceCode threads
// through a file: whether the scan is inside a /* */ block, and how many blank
// lines have run consecutively.
type sourceLineFilter struct {
	level             string
	inBlockComment    bool
	consecutiveBlanks int
}

// keep reports whether line survives filtering, advancing the filter state.
func (f *sourceLineFilter) keep(line string) bool {
	trimmed := strings.TrimSpace(line)

	if f.dropsComment(trimmed) {
		return false
	}

	// Collapse blank line runs
	if trimmed == "" {
		f.consecutiveBlanks++
		return f.consecutiveBlanks <= 1
	}
	f.consecutiveBlanks = 0

	return true
}

// dropsComment advances the block-comment state for trimmed and reports whether
// the configured level strips it as a comment.
func (f *sourceLineFilter) dropsComment(trimmed string) bool {
	// Track block comments
	if strings.Contains(trimmed, "/*") {
		f.inBlockComment = true
	}
	if f.inBlockComment {
		if strings.Contains(trimmed, "*/") {
			f.inBlockComment = false
		}
		// Skip block comment bodies in aggressive mode
		if f.level == "aggressive" {
			return true
		}
	}

	// Skip line comments in aggressive mode
	if f.level == "aggressive" && (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")) {
		return true
	}

	// Minimal: only strip doc comments (multi-line // blocks), keeping single
	// inline comments.
	return f.level == "minimal" && strings.HasPrefix(trimmed, "//")
}
