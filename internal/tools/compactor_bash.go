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

// buildErrorMarkers mark a build output line as an error or warning worth keeping.
var buildErrorMarkers = []string{"error", "warning:", "fatal", "cannot", "undefined"}

// buildSummaryMarkers mark a build output line as a run summary worth keeping.
var buildSummaryMarkers = []string{"build failed", "exit status"}

// containsAnyOf reports whether s contains any of the given substrings.
func containsAnyOf(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// selectBuildErrorLines keeps each error line up to cfg.MaxBuildErrors, a bounded
// run of context lines after each one, and any summary line. It returns the kept
// lines and how many errors were kept.
func selectBuildErrorLines(lines []string, cfg CompactorConfig) (filtered []string, errorCount int) {
	linesInError := 0
	inError := false

	for _, line := range lines {
		lower := strings.ToLower(line)

		if containsAnyOf(lower, buildErrorMarkers) {
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
			containsAnyOf(lower, buildSummaryMarkers) {
			filtered = append(filtered, line)
		}
	}
	return filtered, errorCount
}

// filterBuildOutput keeps only error/warning lines from build output.
func filterBuildOutput(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) < 10 {
		return s, false // not worth filtering short output
	}

	filtered, errorCount := selectBuildErrorLines(lines, cfg)
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

// testOutputSummary is the parsed shape of a test run: the per-status counts, the
// failure details kept, and the per-package result lines.
type testOutputSummary struct {
	passCount, failCount, skipCount int
	failedTests                     []string
	failDetails                     []string
	resultLines                     []string
}

// appendFailDetail flushes an in-progress failure detail if there is one and
// there is still room for it under maxFailures.
func appendFailDetail(details, current []string, maxFailures int) []string {
	if len(current) > 0 && len(details) < maxFailures {
		return append(details, strings.Join(current, "\n"))
	}
	return details
}

// parseTestOutput scans test output into counts, failure details and result lines.
func parseTestOutput(lines []string, cfg CompactorConfig) testOutputSummary {
	var (
		sum               testOutputSummary
		inFail            bool
		failLines         int
		currentFailDetail []string
	)

	for _, line := range lines {
		// Go test result lines
		if goTestResultPattern.MatchString(line) {
			sum.resultLines = append(sum.resultLines, line)
			if strings.HasPrefix(line, "FAIL") {
				sum.failCount++
			} else {
				sum.passCount++
			}
			continue
		}

		if strings.Contains(line, "--- SKIP") {
			sum.skipCount++
			continue
		}

		// Capture failure details
		if m := goTestFailPattern.FindStringSubmatch(line); m != nil {
			// Flush previous fail
			sum.failDetails = appendFailDetail(sum.failDetails, currentFailDetail, cfg.MaxTestFailures)
			sum.failedTests = append(sum.failedTests, m[1])
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
	sum.failDetails = appendFailDetail(sum.failDetails, currentFailDetail, cfg.MaxTestFailures)
	return sum
}

// renderTestSummary formats a parsed test run as the compacted summary text.
func renderTestSummary(sum testOutputSummary, cfg CompactorConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Test Summary: PASS=%d FAIL=%d SKIP=%d\n", sum.passCount, sum.failCount, sum.skipCount)

	if len(sum.failDetails) > 0 {
		fmt.Fprintf(&b, "\nFailure Details:\n")
		for _, d := range sum.failDetails {
			b.WriteString(d)
			b.WriteString("\n\n")
		}
		if len(sum.failedTests) > cfg.MaxTestFailures {
			fmt.Fprintf(&b, "... and %d more failures\n", len(sum.failedTests)-cfg.MaxTestFailures)
		}
	}

	if len(sum.resultLines) > 0 {
		fmt.Fprintf(&b, "\nPackage Results:\n")
		for _, r := range sum.resultLines {
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

	sum := parseTestOutput(lines, cfg)
	if sum.passCount == 0 && sum.failCount == 0 {
		return s, false // couldn't parse test output
	}

	result := renderTestSummary(sum, cfg)
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

// scoredLine pairs an output line with its smart-truncation priority.
type scoredLine struct {
	line  string
	score int
}

// truncateErrorMarkers mark the highest-priority lines for smart truncation.
var truncateErrorMarkers = []string{"error", "fail", "panic", "fatal"}

// truncateDeclPrefixes mark source declarations, ranked above ordinary content.
var truncateDeclPrefixes = []string{"import", "package", "func ", "type "}

// hasAnyPrefix reports whether s starts with any of the given prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// lineTruncationScore ranks a line for retention: errors and failures first, then
// warnings, then declarations, then ordinary content, with blank lines last.
func lineTruncationScore(line string) int {
	lower := strings.ToLower(line)
	switch {
	case containsAnyOf(lower, truncateErrorMarkers):
		return 10
	case strings.Contains(lower, "warning"):
		return 7
	case hasAnyPrefix(line, truncateDeclPrefixes):
		return 5
	case strings.TrimSpace(line) == "":
		return 0 // blank lines are lowest priority
	default:
		return 1 // default priority
	}
}

// partitionByPriority splits the middle band into high- and low-priority lines,
// dropping blank (score 0) lines entirely.
func partitionByPriority(middle []scoredLine) (highPri, lowPri []string) {
	for _, sl := range middle {
		if sl.score >= 5 {
			highPri = append(highPri, sl.line)
		} else if sl.score > 0 {
			lowPri = append(lowPri, sl.line)
		}
	}
	return highPri, lowPri
}

// appendUpTo appends lines from src to dst while the budget allows, returning the
// grown slice and the budget left.
func appendUpTo(dst, src []string, remaining int) ([]string, int) {
	for _, l := range src {
		if remaining <= 0 {
			break
		}
		dst = append(dst, l)
		remaining--
	}
	return dst, remaining
}

// selectMiddleLines keeps at most middleSize lines from the middle band,
// preferring high-priority ones, and appends an omission marker when it dropped
// any.
func selectMiddleLines(middle []scoredLine, middleSize int) []string {
	var result []string
	if len(middle) <= middleSize {
		for _, sl := range middle {
			result = append(result, sl.line)
		}
		return result
	}

	highPri, lowPri := partitionByPriority(middle)
	remaining := middleSize
	result, remaining = appendUpTo(result, highPri, remaining)
	result, remaining = appendUpTo(result, lowPri, remaining)

	// The original guard read `remaining < len(middle)-len(all)+headSize`, where
	// `all` was the whole output so far — the head lines plus what was picked
	// here. Substituting len(all) = headSize+len(result) cancels both headSize
	// terms, leaving this.
	if remaining < len(middle)-len(result) {
		result = append(result, fmt.Sprintf("... (%d lines omitted)", len(middle)-middleSize))
	}
	return result
}

// smartTruncate applies priority-based line selection to keep the most important content.
func smartTruncate(s string, cfg CompactorConfig) (string, bool) {
	lines := strings.Split(s, "\n")
	if len(lines) <= cfg.MaxLines {
		return s, false
	}

	// Priority scoring: errors/failures > imports/declarations > other content
	scoredLines := make([]scoredLine, len(lines))
	for i, line := range lines {
		scoredLines[i] = scoredLine{line: line, score: lineTruncationScore(line)}
	}

	// Keep first and last 10% unconditionally for context
	headSize := cfg.MaxLines / 10
	tailSize := cfg.MaxLines / 10
	middleSize := cfg.MaxLines - headSize - tailSize

	var result []string

	// Head
	for i := 0; i < headSize && i < len(scoredLines); i++ {
		result = append(result, scoredLines[i].line)
	}

	// Middle: select by priority
	middle := scoredLines[headSize : len(scoredLines)-tailSize]
	result = append(result, selectMiddleLines(middle, middleSize)...)

	// Tail
	for i := len(scoredLines) - tailSize; i < len(scoredLines); i++ {
		if i >= 0 {
			result = append(result, scoredLines[i].line)
		}
	}

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
