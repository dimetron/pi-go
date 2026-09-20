package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// The git pipelines read the fields their tools actually emit:
//
//	git-file-diff → file, diff, lines_added, lines_removed  (git_diff.go:20-32)
//	git-overview  → branch, recent_commits, staged_files,    (git_overview.go:32-48)
//	                unstaged_files, untracked_files, ...
//	git-hunk      → file, hunks, total_hunks                 (git_hunk.go:32-38)
//
// The old versions read "diff" for git-hunk (a field it does not have) and
// "output" for git-overview (a field no tool has), and routed on underscore
// names the registry never produces — so all three were no-ops.

// compactGitFileDiff compacts the diff text of a git-file-diff result.
//
// This pipeline already read the right field ("diff") and applyCompaction
// already wrote it; only the name was wrong. It is therefore the smallest fix
// in the set, and the one to land first.
func compactGitFileDiff(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	return gitTextResult(result, "diff", cfg,
		func(s string, c CompactorConfig) (string, bool) { return compactGitDiffText(s, c) },
		func(s string, c CompactorConfig) (string, bool) { return hardTruncate(s, c.MaxChars) },
	)
}

// compactGitOverview compacts the fields of a git-overview result.
//
// It caps recent_commits but deliberately does NOT cap the staged/unstaged/
// untracked file lists. rtk made the same call for the same reason
// (tmp/rtk/src/cmds/git/git_cmd.rs, format_status_inner): truncating the status
// list hides which files are dirty, and "hiding it misleads the user about the
// true repo state". The counts in ahead/behind and the file lists stay exact;
// only the commit log is head-capped, matching rtk's DEFAULT_LOG_LIMIT.
func compactGitOverview(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	commits, ok := result["recent_commits"].([]any)
	if !ok || len(commits) == 0 {
		return nil
	}

	capped, cut := capArray(commits, cfg.MaxLogEntries)
	if !cut {
		return nil
	}

	writes := []CompactWrite{{Key: "recent_commits", Value: capped}}
	origSize, compSize := measureCompaction(result, writes)
	if compSize >= origSize {
		return nil // never-worse guard
	}
	return &CompactResult{
		Writes:     writes,
		Techniques: []string{"git-log-cap"},
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// compactGitHunk compacts the parsed hunks of a git-hunk result.
//
// It reads "hunks" — the field the tool emits — rather than "diff", which
// git-hunk does not have. total_hunks is preserved so the trimmed list is not
// mistaken for the complete change set.
func compactGitHunk(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	hunks, ok := result["hunks"].([]any)
	if !ok || len(hunks) == 0 {
		return nil
	}

	capped, cut := capArray(hunks, cfg.MaxDiffLines)
	if !cut {
		return nil
	}

	writes := []CompactWrite{{Key: "hunks", Value: capped}}
	origSize, compSize := measureCompaction(result, writes)
	if compSize >= origSize {
		return nil // never-worse guard
	}
	return &CompactResult{
		Writes:     writes,
		Techniques: []string{"git-hunk-cap"},
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// gitTextResult runs text stages over one string field and returns a
// CompactResult whose single write targets that field.
func gitTextResult(result map[string]any, key string, cfg CompactorConfig,
	stages ...func(string, CompactorConfig) (string, bool)) *CompactResult {
	text, ok := result[key].(string)
	if !ok || text == "" {
		return nil
	}

	origSize := len(text)
	var techniques []string
	for _, stage := range stages {
		st := stage
		text = runStage(text, &techniques, "git-compact", func(s string) (string, bool) {
			return st(s, cfg)
		})
	}
	techniques = dedup(techniques)

	if len(text) >= origSize {
		return nil // never-worse guard
	}
	return &CompactResult{
		Writes:     []CompactWrite{{Key: key, Value: text}},
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   len(text),
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
