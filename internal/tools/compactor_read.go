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

	var filtered []string
	inBlockComment := false
	consecutiveBlanks := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track block comments
		if strings.Contains(trimmed, "/*") {
			inBlockComment = true
		}
		if inBlockComment {
			if strings.Contains(trimmed, "*/") {
				inBlockComment = false
			}
			if level == "aggressive" {
				continue
			}
		}

		// Skip line comments in aggressive mode
		if level == "aggressive" && (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#")) {
			continue
		}

		// Minimal: only strip doc comments (multi-line // blocks)
		if level == "minimal" && strings.HasPrefix(trimmed, "//") {
			// Keep single inline comments, skip doc blocks
			continue
		}

		// Collapse blank line runs
		if trimmed == "" {
			consecutiveBlanks++
			if consecutiveBlanks <= 1 {
				filtered = append(filtered, line)
			}
			continue
		}
		consecutiveBlanks = 0

		filtered = append(filtered, line)
	}

	if len(filtered) >= len(lines) {
		return s, false
	}

	return strings.Join(filtered, "\n"), true
}
