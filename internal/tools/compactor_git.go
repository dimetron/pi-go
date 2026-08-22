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

// diffTextCompactor accumulates the compacted form of a unified diff as a caller
// feeds it one line at a time.
type diffTextCompactor struct {
	cfg         CompactorConfig
	b           strings.Builder
	totalLines  int
	hunkLines   int
	inHunk      bool
	currentFile string
	additions   int
	deletions   int
}

// emit passes one line through to the output and counts it against MaxDiffLines.
func (c *diffTextCompactor) emit(line string) {
	c.b.WriteString(line)
	c.b.WriteString("\n")
	c.totalLines++
}

// flushFile writes the "(+n -m)" tally for the file being read, if it changed.
func (c *diffTextCompactor) flushFile() {
	if c.currentFile != "" && (c.additions > 0 || c.deletions > 0) {
		fmt.Fprintf(&c.b, "  (+%d -%d)\n", c.additions, c.deletions)
	}
}

// startFile closes off the previous file's tally and begins a new file.
func (c *diffTextCompactor) startFile(name, header string) {
	c.flushFile()
	c.currentFile = name
	c.emit(header)
	c.additions = 0
	c.deletions = 0
	c.inHunk = false
	c.hunkLines = 0
}

// consumeHunkLine keeps the first MaxDiffHunkLines lines of a hunk while counting
// additions and deletions across the whole hunk.
func (c *diffTextCompactor) consumeHunkLine(line string) {
	c.hunkLines++
	if c.hunkLines <= c.cfg.MaxDiffHunkLines {
		c.emit(line)
	}
	if strings.HasPrefix(line, "+") {
		c.additions++
	} else if strings.HasPrefix(line, "-") {
		c.deletions++
	}
}

// consume routes one diff line to the file-header, hunk-header, hunk-body or
// passthrough case.
func (c *diffTextCompactor) consume(line string) {
	if m := diffFileHeader.FindStringSubmatch(line); m != nil {
		c.startFile(m[2], line)
		return
	}

	if diffHunkHeader.MatchString(line) {
		c.inHunk = true
		c.hunkLines = 0
		c.emit(line)
		return
	}

	if c.inHunk {
		c.consumeHunkLine(line)
		return
	}

	// Non-hunk content (--- +++ headers, etc.)
	c.emit(line)
}

// compactGitDiffText summarizes a unified diff to file-level changes with limited hunks.
func compactGitDiffText(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) <= cfg.MaxDiffLines {
		return s, false
	}

	c := &diffTextCompactor{cfg: cfg}
	for _, line := range lines {
		if c.totalLines >= cfg.MaxDiffLines {
			break
		}
		c.consume(line)
	}

	// Final file summary
	c.flushFile()

	if c.totalLines < len(lines) {
		fmt.Fprintf(&c.b, "\n... (%d lines omitted from diff)\n", len(lines)-c.totalLines)
	}

	result := c.b.String()
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
