package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// compactGitFileDiff applies compaction to the git_file_diff tool result.
func compactGitFileDiff(result map[string]any, cfg CompactorConfig) *CompactResult {
	diff, _ := result["diff"].(string)
	if diff == "" {
		return nil
	}

	origSize := len(diff)
	var techniques []string

	if cfg.CompactGitOutput {
		diff = runStage(diff, &techniques, "git-compact", func(s string) (string, bool) {
			return compactGitDiffText(s, cfg)
		})
	}

	diff = runStage(diff, &techniques, "hard-truncate", func(s string) (string, bool) {
		return hardTruncate(s, cfg.MaxChars)
	})
	techniques = dedup(techniques)

	compSize := len(diff)
	if compSize >= origSize {
		return nil
	}

	return &CompactResult{
		Output:     diff,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// compactGitOverview applies compaction to git_overview tool result.
func compactGitOverview(result map[string]any, cfg CompactorConfig) *CompactResult {
	output, _ := result["output"].(string)
	if output == "" {
		return nil
	}

	origSize := len(output)
	var techniques []string

	if cfg.CompactGitOutput {
		output = runStage(output, &techniques, "git-compact", func(s string) (string, bool) {
			return compactGitStatusText(s, cfg)
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

// compactGitHunk applies compaction to git_hunk tool result.
func compactGitHunk(result map[string]any, cfg CompactorConfig) *CompactResult {
	diff, _ := result["diff"].(string)
	if diff == "" {
		output, _ := result["output"].(string)
		if output == "" {
			return nil
		}
		// Try output field instead
		origSize := len(output)
		var techniques []string
		if cfg.CompactGitOutput {
			output = runStage(output, &techniques, "git-compact", func(s string) (string, bool) {
				return compactGitDiffText(s, cfg)
			})
		}
		techniques = dedup(techniques)
		compSize := len(output)
		if compSize >= origSize {
			return nil
		}
		return &CompactResult{Output: output, Techniques: techniques, OrigSize: origSize, CompSize: compSize}
	}

	origSize := len(diff)
	var techniques []string

	if cfg.CompactGitOutput {
		diff = runStage(diff, &techniques, "git-compact", func(s string) (string, bool) {
			return compactGitDiffText(s, cfg)
		})
	}

	diff = runStage(diff, &techniques, "hard-truncate", func(s string) (string, bool) {
		return hardTruncate(s, cfg.MaxChars)
	})
	techniques = dedup(techniques)

	compSize := len(diff)
	if compSize >= origSize {
		return nil
	}

	return &CompactResult{
		Output:     diff,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// diffFileHeader matches diff file headers like "diff --git a/file b/file".
var diffFileHeader = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)

// diffHunkHeader matches hunk headers like "@@ -1,5 +1,7 @@".
var diffHunkHeader = regexp.MustCompile(`^@@.*@@`)

// compactGitDiffText summarizes a unified diff to file-level changes with limited hunks.
func compactGitDiffText(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) <= cfg.MaxDiffLines {
		return s, false
	}

	var b strings.Builder
	totalLines := 0
	hunkLines := 0
	inHunk := false
	currentFile := ""
	additions := 0
	deletions := 0

	for _, line := range lines {
		if totalLines >= cfg.MaxDiffLines {
			break
		}

		if m := diffFileHeader.FindStringSubmatch(line); m != nil {
			// Emit previous file summary if needed
			if currentFile != "" && (additions > 0 || deletions > 0) {
				fmt.Fprintf(&b, "  (+%d -%d)\n", additions, deletions)
			}
			currentFile = m[2]
			b.WriteString(line)
			b.WriteString("\n")
			totalLines++
			additions = 0
			deletions = 0
			inHunk = false
			hunkLines = 0
			continue
		}

		if diffHunkHeader.MatchString(line) {
			inHunk = true
			hunkLines = 0
			b.WriteString(line)
			b.WriteString("\n")
			totalLines++
			continue
		}

		if inHunk {
			hunkLines++
			if hunkLines <= cfg.MaxDiffHunkLines {
				b.WriteString(line)
				b.WriteString("\n")
				totalLines++
			}
			if strings.HasPrefix(line, "+") {
				additions++
			} else if strings.HasPrefix(line, "-") {
				deletions++
			}
			continue
		}

		// Non-hunk content (--- +++ headers, etc.)
		b.WriteString(line)
		b.WriteString("\n")
		totalLines++
	}

	// Final file summary
	if currentFile != "" && (additions > 0 || deletions > 0) {
		fmt.Fprintf(&b, "  (+%d -%d)\n", additions, deletions)
	}

	if totalLines < len(lines) {
		fmt.Fprintf(&b, "\n... (%d lines omitted from diff)\n", len(lines)-totalLines)
	}

	result := b.String()
	if len(result) >= len(s) {
		return s, false
	}
	return result, true
}

// compactGitLogText limits git log output to MaxLogEntries entries.
func compactGitLogText(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) <= cfg.MaxLogEntries*3 { // rough estimate: 3 lines per entry
		return s, false
	}

	var b strings.Builder
	entries := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "commit ") {
			entries++
			if entries > cfg.MaxLogEntries {
				fmt.Fprintf(&b, "\n... (%d more entries)\n", countGitLogEntries(lines)-cfg.MaxLogEntries)
				break
			}
		}
		if entries <= cfg.MaxLogEntries {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	result := b.String()
	if len(result) >= len(s) {
		return s, false
	}
	return result, true
}

func countGitLogEntries(lines []string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "commit ") {
			count++
		}
	}
	return count
}

// compactGitStatusText limits git status output to MaxStatusFiles.
func compactGitStatusText(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) <= cfg.MaxStatusFiles+5 { // some header lines
		return s, false
	}

	var b strings.Builder
	fileLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Status lines typically start with M, A, D, ??, etc.
		isFileLine := len(trimmed) > 2 && (trimmed[0] == 'M' || trimmed[0] == 'A' ||
			trimmed[0] == 'D' || trimmed[0] == 'R' || trimmed[0] == 'C' ||
			trimmed[0] == '?' || trimmed[0] == ' ')

		if isFileLine {
			fileLines++
			if fileLines > cfg.MaxStatusFiles {
				continue
			}
		}

		b.WriteString(line)
		b.WriteString("\n")
	}

	if fileLines > cfg.MaxStatusFiles {
		fmt.Fprintf(&b, "... and %d more files\n", fileLines-cfg.MaxStatusFiles)
	}

	result := b.String()
	if len(result) >= len(s) {
		return s, false
	}
	return result, true
}
