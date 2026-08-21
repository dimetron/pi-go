package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// compactBash applies the bash compaction pipeline based on command detection.
func compactBash(result, args map[string]any, cfg CompactorConfig) *CompactResult {
	stdout, _ := result["stdout"].(string)
	stderr, _ := result["stderr"].(string)
	input := stdout
	if input == "" {
		input = stderr
	}
	if input == "" {
		return nil
	}

	origSize := len(stdout) + len(stderr)
	var techniques []string

	// Stage 1: ANSI stripping
	if cfg.StripAnsi {
		stdout = runStage(stdout, &techniques, "ansi", func(s string) (string, bool) {
			return stripAnsi(s)
		})
		stderr = runStage(stderr, &techniques, "ansi", func(s string) (string, bool) {
			return stripAnsi(s)
		})
		// Deduplicate technique name
		techniques = dedup(techniques)
	}

	// Detect command type for selective stages
	cmd := detectCommand(args)

	// Stage 2: Build output filtering
	if cfg.FilterBuildOutput && isBuildCommand(cmd) {
		stdout = runStage(stdout, &techniques, "build-filter", func(s string) (string, bool) {
			return filterBuildOutput(s, cfg)
		})
	}

	// Stage 3: Test output aggregation
	if cfg.AggregateTestOutput && isTestCommand(cmd) {
		stdout = runStage(stdout, &techniques, "test-aggregate", func(s string) (string, bool) {
			return aggregateTestOutput(s, cfg)
		})
	}

	// Stage 4: Git output compaction (for bash git commands)
	if cfg.CompactGitOutput && isGitCommand(cmd) {
		stdout = runStage(stdout, &techniques, "git-compact", func(s string) (string, bool) {
			return compactGitBashOutput(s, cmd, cfg)
		})
	}

	// Stage 5: Linter aggregation
	if cfg.AggregateLinterOutput && isLinterCommand(cmd) {
		stdout = runStage(stdout, &techniques, "linter-aggregate", func(s string) (string, bool) {
			return aggregateLinterOutput(s, cfg)
		})
	}

	// Stage 6: Smart truncation
	if cfg.SmartTruncate {
		stdout = runStage(stdout, &techniques, "smart-truncate", func(s string) (string, bool) {
			return smartTruncate(s, cfg)
		})
	}

	// Stage 7: Hard truncation
	stdout = runStage(stdout, &techniques, "hard-truncate", func(s string) (string, bool) {
		return hardTruncate(s, cfg.MaxChars)
	})
	stdout = runStage(stdout, &techniques, "hard-truncate-lines", func(s string) (string, bool) {
		return hardTruncateLines(s, cfg.MaxLines)
	})
	techniques = dedup(techniques)

	compSize := len(stdout) + len(stderr)
	if compSize >= origSize {
		return nil // no savings
	}

	return &CompactResult{
		Output:     stdout,
		Techniques: techniques,
		OrigSize:   origSize,
		CompSize:   compSize,
	}
}

// detectCommand extracts the command string from bash tool args.
func detectCommand(args map[string]any) string {
	if args == nil {
		return ""
	}
	cmd, _ := args["command"].(string)
	return cmd
}

func isTestCommand(cmd string) bool {
	return strings.Contains(cmd, "go test") ||
		strings.Contains(cmd, "pytest") ||
		strings.Contains(cmd, "npm test") ||
		strings.Contains(cmd, "jest") ||
		strings.Contains(cmd, "cargo test")
}

func isBuildCommand(cmd string) bool {
	return strings.Contains(cmd, "go build") ||
		strings.Contains(cmd, "make") ||
		strings.Contains(cmd, "cargo build") ||
		strings.Contains(cmd, "npm run build") ||
		strings.Contains(cmd, "gcc") ||
		strings.Contains(cmd, "g++")
}

func isGitCommand(cmd string) bool {
	return strings.HasPrefix(strings.TrimSpace(cmd), "git ")
}

func isLinterCommand(cmd string) bool {
	return strings.Contains(cmd, "golangci-lint") ||
		strings.Contains(cmd, "eslint") ||
		strings.Contains(cmd, "pylint") ||
		strings.Contains(cmd, "flake8") ||
		strings.Contains(cmd, "clippy")
}

// filterBuildOutput keeps only error/warning lines from build output.
func filterBuildOutput(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) < 10 {
		return s, false // not worth filtering short output
	}

	var filtered []string
	errorCount := 0
	linesInError := 0
	inError := false

	for _, line := range lines {
		lower := strings.ToLower(line)

		isErrorLine := strings.Contains(lower, "error") ||
			strings.Contains(lower, "warning:") ||
			strings.Contains(lower, "fatal") ||
			strings.Contains(lower, "cannot") ||
			strings.Contains(lower, "undefined")

		if isErrorLine {
			if errorCount < cfg.MaxBuildErrors {
				inError = true
				linesInError = 0
				filtered = append(filtered, line)
				errorCount++
			}
			continue
		}

		// Context lines after an error
		if inError && linesInError < cfg.MaxBuildErrLines {
			filtered = append(filtered, line)
			linesInError++
			if linesInError >= cfg.MaxBuildErrLines {
				inError = false
			}
			continue
		}

		// Keep summary lines
		if strings.HasPrefix(line, "FAIL") || strings.HasPrefix(line, "ok ") ||
			strings.Contains(lower, "build failed") || strings.Contains(lower, "exit status") {
			filtered = append(filtered, line)
		}
	}

	if len(filtered) >= len(lines) {
		return s, false
	}

	result := strings.Join(filtered, "\n")
	if errorCount >= cfg.MaxBuildErrors {
		result += fmt.Sprintf("\n... (%d errors shown, may have more)", errorCount)
	}
	return result, true
}

// goTestResultPattern matches Go test result lines like "ok  pkg  0.5s" or "FAIL pkg  0.5s".
var goTestResultPattern = regexp.MustCompile(`^(ok|FAIL)\s+\S+\s+[\d.]+s`)

// goTestFailPattern matches Go test failure output headers.
var goTestFailPattern = regexp.MustCompile(`^--- FAIL: (\S+)`)

// testRunSummary is what one scan of `go test` output yields: the counts, the
// per-package result lines, and the captured detail for the first
// MaxTestFailures failures. failedTests keeps counting past that cap so the
// renderer can report how many failures were not shown.
type testRunSummary struct {
	passCount   int
	failCount   int
	skipCount   int
	failedTests []string
	failDetails []string
	resultLines []string
}

// parsed reports whether the scan recognized the input as test output at all.
// Only a package result line moves either counter, so an input with none of
// them is something other than `go test` output and is left untouched.
func (t testRunSummary) parsed() bool {
	return t.passCount != 0 || t.failCount != 0
}

// scanTestOutput folds `go test` output into a summary. Failure detail is
// captured as a run: a "--- FAIL:" header opens one and the next
// MaxTestFailLines lines become its body, so any unrelated line in that window
// — an "=== RUN" for the following test, say — is absorbed into the detail.
func scanTestOutput(lines []string, cfg CompactorConfig) testRunSummary {
	var (
		summary           testRunSummary
		inFail            bool
		failLines         int
		currentFailDetail []string
	)

	// flushFail records the run that just ended, if there is still room. Note
	// the asymmetry with failedTests, which is never capped: the cap limits how
	// much detail is kept, not how many failures are counted.
	flushFail := func() {
		if len(currentFailDetail) > 0 && len(summary.failDetails) < cfg.MaxTestFailures {
			summary.failDetails = append(summary.failDetails, strings.Join(currentFailDetail, "\n"))
		}
	}

	for _, line := range lines {
		// Go test result lines
		if goTestResultPattern.MatchString(line) {
			summary.resultLines = append(summary.resultLines, line)
			if strings.HasPrefix(line, "FAIL") {
				summary.failCount++
			} else {
				summary.passCount++
			}
			continue
		}

		if strings.Contains(line, "--- SKIP") {
			summary.skipCount++
			continue
		}

		// Capture failure details
		if m := goTestFailPattern.FindStringSubmatch(line); m != nil {
			flushFail()
			summary.failedTests = append(summary.failedTests, m[1])
			currentFailDetail = []string{line}
			inFail = true
			failLines = 0
			continue
		}

		if inFail && failLines < cfg.MaxTestFailLines {
			currentFailDetail = append(currentFailDetail, line)
			failLines++
			if failLines >= cfg.MaxTestFailLines {
				inFail = false
			}
		}
	}

	// Flush last failure
	flushFail()
	return summary
}

// renderTestSummary formats a scanned summary as markdown. A section with
// nothing to show is omitted entirely rather than rendered with an empty body.
func renderTestSummary(summary testRunSummary, cfg CompactorConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Test Summary: PASS=%d FAIL=%d SKIP=%d\n",
		summary.passCount, summary.failCount, summary.skipCount)

	if len(summary.failDetails) > 0 {
		fmt.Fprintf(&b, "\nFailure Details:\n")
		for _, d := range summary.failDetails {
			b.WriteString(d)
			b.WriteString("\n\n")
		}
		if len(summary.failedTests) > cfg.MaxTestFailures {
			fmt.Fprintf(&b, "... and %d more failures\n", len(summary.failedTests)-cfg.MaxTestFailures)
		}
	}

	if len(summary.resultLines) > 0 {
		fmt.Fprintf(&b, "\nPackage Results:\n")
		for _, r := range summary.resultLines {
			b.WriteString(r)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// aggregateTestOutput compacts test output into a summary with failure details.
func aggregateTestOutput(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) < 20 {
		return s, false // too short to benefit
	}

	summary := scanTestOutput(lines, cfg)
	if !summary.parsed() {
		return s, false // couldn't parse test output
	}

	result := renderTestSummary(summary, cfg)
	if len(result) >= len(s) {
		return s, false
	}
	return result, true
}

// compactGitBashOutput compacts git command output run via bash.
func compactGitBashOutput(s, cmd string, cfg CompactorConfig) (string, bool) {
	trimCmd := strings.TrimSpace(cmd)
	switch {
	case strings.HasPrefix(trimCmd, "git diff"):
		return compactGitDiffText(s, cfg)
	case strings.HasPrefix(trimCmd, "git log"):
		return compactGitLogText(s, cfg)
	case strings.HasPrefix(trimCmd, "git status"):
		return compactGitStatusText(s, cfg)
	default:
		return s, false
	}
}

// linterLinePattern matches typical linter output: file:line:col: message
var linterLinePattern = regexp.MustCompile(`^([^:]+):(\d+):(\d+):\s*(.+)$`)

// aggregateLinterOutput groups linter output by rule and file.
func aggregateLinterOutput(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) < 10 {
		return s, false
	}

	type lintIssue struct {
		file    string
		line    string
		col     string
		message string
	}

	byFile := make(map[string][]lintIssue)
	var otherLines []string

	for _, line := range lines {
		m := linterLinePattern.FindStringSubmatch(line)
		if m != nil {
			issue := lintIssue{file: m[1], line: m[2], col: m[3], message: m[4]}
			byFile[issue.file] = append(byFile[issue.file], issue)
		} else if strings.TrimSpace(line) != "" {
			otherLines = append(otherLines, line)
		}
	}

	if len(byFile) == 0 {
		return s, false
	}

	var b strings.Builder
	fileCount := 0
	totalIssues := 0
	for file, issues := range byFile {
		if fileCount >= cfg.MaxLinterFiles {
			break
		}
		fmt.Fprintf(&b, "%s (%d issues):\n", file, len(issues))
		shown := 0
		for _, issue := range issues {
			if shown >= cfg.MaxLinterRules {
				fmt.Fprintf(&b, "  ... and %d more\n", len(issues)-shown)
				break
			}
			fmt.Fprintf(&b, "  %s:%s: %s\n", issue.line, issue.col, issue.message)
			shown++
			totalIssues++
		}
		fileCount++
	}

	if fileCount < len(byFile) {
		fmt.Fprintf(&b, "\n... and %d more files\n", len(byFile)-fileCount)
	}
	fmt.Fprintf(&b, "\nTotal: %d issues in %d files\n", totalIssues, len(byFile))

	// Append non-issue lines (summary lines etc.)
	for _, line := range otherLines {
		b.WriteString(line)
		b.WriteString("\n")
	}

	result := b.String()
	if len(result) >= len(s) {
		return s, false
	}
	return result, true
}

// lineScore ranks a line by how much it would cost to drop: failures outrank
// warnings, which outrank declarations, and blank lines are free to drop.
func lineScore(line string) int {
	lower := strings.ToLower(line)
	switch {
	// High priority: errors, failures, important markers
	case strings.Contains(lower, "error"), strings.Contains(lower, "fail"),
		strings.Contains(lower, "panic"), strings.Contains(lower, "fatal"):
		return 10
	case strings.Contains(lower, "warning"):
		return 7
	case strings.HasPrefix(line, "import"), strings.HasPrefix(line, "package"),
		strings.HasPrefix(line, "func "), strings.HasPrefix(line, "type "):
		return 5
	case strings.TrimSpace(line) == "":
		return 0 // blank lines are lowest priority
	}
	return 1 // default priority
}

// selectMiddle picks at most want lines out of the middle of the output,
// scoring first and taking the high-priority lines before the rest. Blank
// lines are dropped outright. A note recording how many lines were cut is
// always appended, since the caller only reaches here when middle overflows.
func selectMiddle(middle []string, want int) []string {
	// Collect high-priority lines first
	var highPri, lowPri []string
	for _, line := range middle {
		switch score := lineScore(line); {
		case score >= 5:
			highPri = append(highPri, line)
		case score > 0:
			lowPri = append(lowPri, line)
		}
	}

	picked := make([]string, 0, want+1)
	for _, group := range [][]string{highPri, lowPri} {
		for _, line := range group {
			if len(picked) >= want {
				break
			}
			picked = append(picked, line)
		}
	}
	return append(picked, fmt.Sprintf("... (%d lines omitted)", len(middle)-want))
}

// smartTruncate applies priority-based line selection to keep the most important content.
func smartTruncate(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) <= cfg.MaxLines {
		return s, false
	}

	// Keep first and last 10% unconditionally for context. Because the guard
	// above already established len(lines) > MaxLines, the middle always
	// overflows and always needs selecting.
	headSize := cfg.MaxLines / 10
	tailSize := cfg.MaxLines / 10
	middleSize := cfg.MaxLines - headSize - tailSize

	result := make([]string, 0, cfg.MaxLines+1)
	result = append(result, lines[:headSize]...)
	result = append(result, selectMiddle(lines[headSize:len(lines)-tailSize], middleSize)...)
	result = append(result, lines[len(lines)-tailSize:]...)

	output := strings.Join(result, "\n")
	return output, len(output) < len(s)
}

func dedup(ss []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
