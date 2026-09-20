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
//
// It sets the tool's own `truncated` field when the diff is cut. Before this,
// the compactor could reduce a diff by 99% while leaving truncated unset, so the
// result claimed to be the complete diff of the file. GitFileDiffOutput already
// declares the field (git_diff.go:32) and the tool sets it when its own 256KB
// byte cap fires (git_diff.go:88-90) — compaction is the same kind of loss and
// must report it the same way.
func compactGitFileDiff(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	return gitTextResult(result, "diff", cfg,
		gitTextStage{"git-compact", func(s string, c CompactorConfig) (string, bool) {
			// The semantic diff rewrite is opt-out via CompactGitOutput, as it was
			// before this fix. Hard truncation below stays unconditional: it is the
			// byte-ceiling safety net, not a git-specific transformation.
			if !c.CompactGitOutput {
				return s, false
			}
			return compactGitDiffText(s, c)
		}},
		gitTextStage{"hard-truncate", func(s string, c CompactorConfig) (string, bool) {
			return hardTruncate(s, c.MaxChars)
		}},
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
	if !cfg.CompactGitOutput {
		return nil // opt-out, as the previous git pipelines were
	}
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
//
// Two caps are needed, because a hunk is not bounded by its count: capping only
// the number of hunks leaves a single hunk with a large Content untouched, and
// one hunk of a whole-file diff can be hundreds of KB. So each hunk's Content is
// trimmed as well, and the hunk's own Added/Removed counts are left exact.
func compactGitHunk(result, _ map[string]any, cfg CompactorConfig) *CompactResult {
	if !cfg.CompactGitOutput {
		return nil // opt-out, as the previous git pipelines were
	}
	hunks, ok := result["hunks"].([]any)
	if !ok || len(hunks) == 0 {
		return nil
	}

	writes := make([]CompactWrite, 0, 1)
	techniques := make([]string, 0, 1)

	// Cap the number of hunk records.
	if capped, cut := capArray(hunks, cfg.MaxDiffLines); cut {
		writes = append(writes, CompactWrite{Key: "hunks", Value: capped})
		techniques = append(techniques, "git-hunk-cap")
		hunks = capped
	}

	// Cap each remaining hunk's Content independently.
	trimmed := make([]any, len(hunks))
	copy(trimmed, hunks)
	contentTrimmed := false
	for i, h := range trimmed {
		hm, ok := h.(map[string]any)
		if !ok {
			continue
		}
		content, ok := hm["content"].(string)
		if !ok || len(content) <= cfg.MaxChars {
			continue
		}
		// Copy the map so the original element is not mutated in place.
		cp := make(map[string]any, len(hm))
		for k, v := range hm {
			cp[k] = v
		}
		cp["content"] = hardTruncateContent(content, cfg.MaxChars)
		trimmed[i] = cp
		contentTrimmed = true
	}
	if contentTrimmed {
		writes = append(writes, CompactWrite{Key: "hunks", Value: trimmed})
		techniques = append(techniques, "git-hunk-content-cap")
	}

	if len(writes) == 0 {
		return nil
	}

	origSize, compSize := measureCompaction(result, writes)
	if compSize >= origSize {
		return nil // never-worse guard
	}
	return &CompactResult{
		Writes:     writes,
		Techniques: dedup(techniques),
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// hardTruncateContent cuts a hunk body to maxChars on a line boundary, marking
// the cut so trimmed content is never read as the whole hunk.
func hardTruncateContent(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	cut := s[:maxChars]
	// Prefer a line boundary so the tail is not a half-written line.
	if i := strings.LastIndexByte(cut, '\n'); i > 0 {
		cut = cut[:i]
	}
	dropped := strings.Count(s[len(cut):], "\n")
	return cut + fmt.Sprintf("\n  ... (%d lines truncated)\n", dropped)
}

// gitTextStage pairs a compaction step with the technique name it reports, so
// the recorded technique describes what actually ran.
type gitTextStage struct {
	name string
	fn   func(string, CompactorConfig) (string, bool)
}

// gitTextResult runs text stages over one string field and returns a
// CompactResult whose writes target that field and, when the tool declares one,
// its truncation flag.
//
// The flag is written unconditionally rather than only when already present.
// These structs declare `truncated` with omitempty, so a complete result carries
// no such key — checking for it would mean never setting it, which is how a diff
// reduced by 99% could still claim to be the whole diff. A result that silently
// reads as complete is worse than a large one, so a missing key is the case that
// most needs the write.
func gitTextResult(result map[string]any, key string, cfg CompactorConfig,
	stages ...gitTextStage) *CompactResult {
	text, ok := result[key].(string)
	if !ok || text == "" {
		return nil
	}

	origSize := len(text)
	var techniques []string
	for _, stage := range stages {
		st := stage
		text = runStage(text, &techniques, st.name, func(s string) (string, bool) {
			return st.fn(s, cfg)
		})
	}
	techniques = dedup(techniques)

	if len(text) >= origSize {
		return nil // never-worse guard
	}

	writes := []CompactWrite{{Key: key, Value: text}}
	if _, hasFlag := result["truncated"]; hasFlag || declaresTruncated(result) {
		writes = append(writes, CompactWrite{Key: "truncated", Value: true})
	}

	origSize, compSize := measureCompaction(result, writes)
	if compSize >= origSize {
		return nil // never-worse guard, now counting the disclosure write
	}
	return &CompactResult{
		Writes:     writes,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// declaresTruncated reports whether the result belongs to a tool whose output
// type carries a truncation flag. The flag is absent from the map when false
// (omitempty), so its absence cannot distinguish "not truncated" from "no such
// field" — the tool is identified by the companion fields instead.
func declaresTruncated(result map[string]any) bool {
	// git-file-diff: file + diff (+ lines_added/lines_removed); git-hunk has no
	// truncated field and is excluded by requiring lines_added or lines_removed.
	_, hasFile := result["file"]
	_, hasAdded := result["lines_added"]
	_, hasRemoved := result["lines_removed"]
	return hasFile && (hasAdded || hasRemoved)
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

	// Per-hunk counters for the lines dropped past MaxDiffHunkLines. Without
	// them a cap that lands inside an unbalanced run shows only one sign, and
	// the hunk reads as a pure removal (or addition) when it was neither. rtk
	// reports the same figures (tmp/rtk/src/cmds/git/git_cmd.rs,
	// hunk_truncation_note) for the same reason: an anchored ^- / ^+ audit must
	// be able to tell what it did not see.
	hunkDroppedDels int
	hunkDroppedAdds int
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

// flushHunkNote discloses the change lines dropped from the hunk just closed,
// split by sign. Emitting nothing when the hunk was fully shown keeps the note
// from appearing on diffs that lost nothing.
func (c *diffTextCompactor) flushHunkNote() {
	d, a := c.hunkDroppedDels, c.hunkDroppedAdds
	if d == 0 && a == 0 {
		return
	}
	noun := func(n int, word string) string {
		if n == 1 {
			return fmt.Sprintf("%d %s", n, word)
		}
		return fmt.Sprintf("%d %ss", n, word)
	}
	switch {
	case d == 0:
		fmt.Fprintf(&c.b, "  ... (%s truncated)\n", noun(a, "addition"))
	case a == 0:
		fmt.Fprintf(&c.b, "  ... (%s truncated)\n", noun(d, "deletion"))
	default:
		fmt.Fprintf(&c.b, "  ... (%s, %s truncated)\n", noun(d, "deletion"), noun(a, "addition"))
	}
	c.hunkDroppedDels, c.hunkDroppedAdds = 0, 0
}

// startFile closes off the previous file's tally and begins a new file.
func (c *diffTextCompactor) startFile(name, header string) {
	c.flushHunkNote()
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
//
// Lines past the cap are counted by sign rather than silently discarded, so the
// hunk can disclose what it withheld. Keeping the first N lines is otherwise
// correct: it is the head of the change, which is where the reader starts.
func (c *diffTextCompactor) consumeHunkLine(line string) {
	c.hunkLines++
	isAdd := strings.HasPrefix(line, "+")
	isDel := strings.HasPrefix(line, "-")
	if c.hunkLines > c.cfg.MaxDiffHunkLines {
		if isDel {
			c.hunkDroppedDels++
		} else if isAdd {
			c.hunkDroppedAdds++
		}
	}
	if c.hunkLines <= c.cfg.MaxDiffHunkLines {
		c.emit(line)
	}
	if isAdd {
		c.additions++
	} else if isDel {
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
		// Close the previous hunk before opening this one. Its withheld counts
		// must be disclosed next to the hunk they belong to; without this flush
		// they carry into the next hunk and the note is emitted at the wrong
		// place, attributing one hunk's loss to another.
		c.flushHunkNote()
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

	// Final file summary, then any note for the hunk still open at the cut.
	c.flushHunkNote()
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
