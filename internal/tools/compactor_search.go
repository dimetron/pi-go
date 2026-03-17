package tools

import (
	"fmt"
	"strings"
)

// compactGrep applies search grouping to grep tool output.
func compactGrep(result map[string]any, cfg CompactorConfig) *CompactResult {
	output, _ := result["output"].(string)
	if output == "" {
		return nil
	}

	origSize := len(output)
	var techniques []string

	if cfg.GroupSearchOutput {
		output = runStage(output, &techniques, "search-group", func(s string) (string, bool) {
			return groupSearchOutput(s, cfg)
		})
	}

	output = runStage(output, &techniques, "hard-truncate", func(s string) (string, bool) {
		return hardTruncate(s, cfg.MaxChars)
	})
	techniques = dedup(techniques)

	compSize := len(output)
	if compSize >= origSize {
		return nil
	}

	return &CompactResult{
		Output:     output,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// compactFind applies hard truncation to find tool output.
func compactFind(result map[string]any, cfg CompactorConfig) *CompactResult {
	output, _ := result["output"].(string)
	if output == "" {
		return nil
	}

	origSize := len(output)
	var techniques []string

	output = runStage(output, &techniques, "hard-truncate", func(s string) (string, bool) {
		return hardTruncate(s, cfg.MaxChars)
	})
	output = runStage(output, &techniques, "hard-truncate-lines", func(s string) (string, bool) {
		return hardTruncateLines(s, cfg.MaxLines)
	})
	techniques = dedup(techniques)

	compSize := len(output)
	if compSize >= origSize {
		return nil
	}

	return &CompactResult{
		Output:     output,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// compactTree applies hard truncation to tree tool output.
func compactTree(result map[string]any, cfg CompactorConfig) *CompactResult {
	return compactFind(result, cfg) // same strategy
}

// groupSearchOutput groups search results by file with match counts.
func groupSearchOutput(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) < 20 {
		return s, false
	}

	// Group lines by file prefix (file:line:content pattern)
	byFile := make(map[string][]string)
	var fileOrder []string

	for _, line := range lines {
		if line == "" {
			continue
		}
		// Parse file:line:content or file:content patterns
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		file := line[:idx]
		if _, seen := byFile[file]; !seen {
			fileOrder = append(fileOrder, file)
		}
		byFile[file] = append(byFile[file], line)
	}

	if len(byFile) == 0 {
		return s, false
	}

	var b strings.Builder
	totalShown := 0
	for _, file := range fileOrder {
		matches := byFile[file]
		fmt.Fprintf(&b, "%s (%d matches):\n", file, len(matches))
		shown := 0
		for _, m := range matches {
			if totalShown >= cfg.MaxSearchTotal {
				break
			}
			if shown >= cfg.MaxSearchPerFile {
				fmt.Fprintf(&b, "  ... and %d more matches\n", len(matches)-shown)
				break
			}
			// Strip file prefix for cleaner output
			if idx := strings.Index(m, ":"); idx >= 0 {
				b.WriteString("  ")
				b.WriteString(m[idx+1:])
			} else {
				b.WriteString("  ")
				b.WriteString(m)
			}
			b.WriteString("\n")
			shown++
			totalShown++
		}
		if totalShown >= cfg.MaxSearchTotal {
			fmt.Fprintf(&b, "\n... (%d total matches shown, limited to %d)\n",
				totalShown, cfg.MaxSearchTotal)
			break
		}
	}

	result := b.String()
	if len(result) >= len(s) {
		return s, false
	}
	return result, true
}
