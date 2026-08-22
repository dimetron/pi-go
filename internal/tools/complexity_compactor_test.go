package tools

// Golden tests pinning the exact output of the text-transformation functions
// refactored for cyclomatic complexity: filterBuildOutput, aggregateTestOutput,
// smartTruncate, compactGitDiffText, gitOverviewHandler and grepHandler.
//
// Every `want` string in this file was captured from the pre-refactor
// implementation, so a passing run is the evidence that the extraction of
// helpers preserved behavior byte for byte.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cxCfg returns the default compactor config for complexity-refactor tests.
func cxCfg(mutate func(*CompactorConfig)) CompactorConfig {
	cfg := DefaultCompactorConfig()
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

func cxLines(ss ...string) string { return strings.Join(ss, "\n") }

// --------------------------------------------------------------------------
// filterBuildOutput
// --------------------------------------------------------------------------

func TestFilterBuildOutput_Golden(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		cfg     CompactorConfig
		want    string
		applied bool
	}{
		{
			name: "errors with context and cap",
			cfg: cxCfg(func(c *CompactorConfig) {
				c.MaxBuildErrors = 2
				c.MaxBuildErrLines = 2
			}),
			in: cxLines(
				"go: downloading x",
				"main.go:3:5: undefined: foo",
				"  context one",
				"  context two",
				"  context three",
				"main.go:9:1: error: bad thing",
				"  detail a",
				"main.go:12:1: warning: hmm",
				"other.go:1:1: cannot find package",
				"FAIL\tpkg/a\t0.1s",
				"ok  \tpkg/b\t0.2s",
				"build failed",
				"exit status 2",
				"trailing noise",
			),
			want: "main.go:3:5: undefined: foo\n  context one\n  context two\n" +
				"main.go:9:1: error: bad thing\n  detail a\n" +
				"FAIL\tpkg/a\t0.1s\nok  \tpkg/b\t0.2s\nbuild failed\nexit status 2\n" +
				"... (2 errors shown, may have more)",
			applied: true,
		},
		{
			name: "summary lines only, no errors",
			cfg:  cxCfg(nil),
			in: cxLines(
				"line one", "line two", "line three", "line four", "line five",
				"FAIL\tpkg/a\t0.1s", "ok  \tpkg/b\t0.2s", "build failed",
				"exit status 1", "line ten", "line eleven",
			),
			want:    "FAIL\tpkg/a\t0.1s\nok  \tpkg/b\t0.2s\nbuild failed\nexit status 1",
			applied: true,
		},
		{
			name: "nothing filtered out",
			cfg:  cxCfg(func(c *CompactorConfig) { c.MaxBuildErrors = 100 }),
			in: cxLines(
				"error 1", "error 2", "error 3", "error 4", "error 5",
				"error 6", "error 7", "error 8", "error 9", "error 10",
			),
			want: cxLines(
				"error 1", "error 2", "error 3", "error 4", "error 5",
				"error 6", "error 7", "error 8", "error 9", "error 10",
			),
			applied: false,
		},
		{
			name:    "short output untouched",
			cfg:     cxCfg(nil),
			in:      cxLines("a", "b", "c"),
			want:    cxLines("a", "b", "c"),
			applied: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, applied := filterBuildOutput(tc.in, tc.cfg)
			if applied != tc.applied {
				t.Errorf("applied = %v, want %v", applied, tc.applied)
			}
			if got != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// aggregateTestOutput
// --------------------------------------------------------------------------

func cxTestOutput() string {
	lines := []string{
		"=== RUN   TestOne",
		"--- FAIL: TestOne (0.00s)",
		"    one_test.go:10: boom",
		"    one_test.go:11: more",
		"    one_test.go:12: dropped",
		"=== RUN   TestTwo",
		"--- FAIL: TestTwo (0.00s)",
		"    two_test.go:20: bang",
		"=== RUN   TestThree",
		"--- SKIP: TestThree (0.00s)",
		"FAIL",
		"FAIL\tpkg/a\t0.15s",
		"ok  \tpkg/b\t0.25s",
	}
	for i := 0; i < 10; i++ {
		lines = append(lines, fmt.Sprintf("noise %d", i))
	}
	return cxLines(lines...)
}

func cxRepeat(n int, f func(int) string) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, f(i))
	}
	return out
}

func TestAggregateTestOutput_Golden(t *testing.T) {
	passOnly := append(
		cxRepeat(22, func(i int) string { return fmt.Sprintf("=== RUN   TestP%d", i) }),
		"ok  \tpkg/one\t0.10s", "ok  \tpkg/two\t0.20s",
	)
	noise := cxRepeat(25, func(i int) string { return fmt.Sprintf("just noise %d", i) })
	blanksThenOK := append(cxRepeat(24, func(int) string { return "" }), "ok  \tx\t0.1s")

	tests := []struct {
		name    string
		in      string
		cfg     CompactorConfig
		want    string
		applied bool
	}{
		{
			name: "failures capped with details truncated",
			cfg: cxCfg(func(c *CompactorConfig) {
				c.MaxTestFailures = 1
				c.MaxTestFailLines = 2
			}),
			in: cxTestOutput(),
			want: "Test Summary: PASS=1 FAIL=1 SKIP=1\n\nFailure Details:\n" +
				"--- FAIL: TestOne (0.00s)\n    one_test.go:10: boom\n    one_test.go:11: more\n\n" +
				"... and 1 more failures\n\nPackage Results:\n" +
				"FAIL\tpkg/a\t0.15s\nok  \tpkg/b\t0.25s\n",
			applied: true,
		},
		{
			name: "passes only, no failure section",
			cfg:  cxCfg(nil),
			in:   cxLines(passOnly...),
			want: "Test Summary: PASS=2 FAIL=0 SKIP=0\n\nPackage Results:\n" +
				"ok  \tpkg/one\t0.10s\nok  \tpkg/two\t0.20s\n",
			applied: true,
		},
		{
			name:    "unparseable output untouched",
			cfg:     cxCfg(nil),
			in:      cxLines(noise...),
			want:    cxLines(noise...),
			applied: false,
		},
		{
			name:    "summary longer than input",
			cfg:     cxCfg(nil),
			in:      cxLines(blanksThenOK...),
			want:    cxLines(blanksThenOK...),
			applied: false,
		},
		{
			name:    "short output untouched",
			cfg:     cxCfg(nil),
			in:      "short output",
			want:    "short output",
			applied: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, applied := aggregateTestOutput(tc.in, tc.cfg)
			if applied != tc.applied {
				t.Errorf("applied = %v, want %v", applied, tc.applied)
			}
			if got != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// smartTruncate
// --------------------------------------------------------------------------

// cxMixedScores builds 40 lines cycling through every priority band:
// error (10), warning (7), declaration (5), blank (0) and plain (1).
func cxMixedScores() []string {
	return cxRepeat(40, func(i int) string {
		switch i % 5 {
		case 0:
			return fmt.Sprintf("line %d error here", i)
		case 1:
			return fmt.Sprintf("warning at %d", i)
		case 2:
			return fmt.Sprintf("func f%d() {}", i)
		case 3:
			return ""
		default:
			return fmt.Sprintf("plain %d", i)
		}
	})
}

// cxMostlyLowPriority builds 40 lines that are almost all score-1 or blank, so
// the low-priority fill loop runs after the high-priority one is exhausted.
func cxMostlyLowPriority() []string {
	return cxRepeat(40, func(i int) string {
		switch {
		case i == 7:
			return "boom error at seven"
		case i%4 == 3:
			return ""
		default:
			return fmt.Sprintf("plain %d", i)
		}
	})
}

func TestSmartTruncate_Golden(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		cfg     CompactorConfig
		want    string
		applied bool
	}{
		{
			name: "high priority lines fill the middle",
			cfg:  cxCfg(func(c *CompactorConfig) { c.MaxLines = 20 }),
			in:   cxLines(cxMixedScores()...),
			want: "line 0 error here\nwarning at 1\nfunc f2() {}\nline 5 error here\nwarning at 6\n" +
				"func f7() {}\nline 10 error here\nwarning at 11\nfunc f12() {}\nline 15 error here\n" +
				"warning at 16\nfunc f17() {}\nline 20 error here\nwarning at 21\nfunc f22() {}\n" +
				"line 25 error here\nwarning at 26\nfunc f27() {}\n... (20 lines omitted)\n\nplain 39",
			applied: true,
		},
		{
			name: "zero head and tail when MaxLines is small",
			cfg:  cxCfg(func(c *CompactorConfig) { c.MaxLines = 5 }),
			in:   cxLines(cxMixedScores()...),
			want: "line 0 error here\nwarning at 1\nfunc f2() {}\nline 5 error here\nwarning at 6\n" +
				"... (35 lines omitted)",
			applied: true,
		},
		{
			name: "low priority fill after high priority exhausted",
			cfg:  cxCfg(func(c *CompactorConfig) { c.MaxLines = 20 }),
			in:   cxLines(cxMostlyLowPriority()...),
			want: "plain 0\nplain 1\nboom error at seven\nplain 2\nplain 4\nplain 5\nplain 6\n" +
				"plain 8\nplain 9\nplain 10\nplain 12\nplain 13\nplain 14\nplain 16\nplain 17\n" +
				"plain 18\nplain 20\nplain 21\n... (20 lines omitted)\nplain 38\n",
			applied: true,
		},
		{
			name:    "short input untouched",
			cfg:     cxCfg(nil),
			in:      "short",
			want:    "short",
			applied: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, applied := smartTruncate(tc.in, tc.cfg)
			if applied != tc.applied {
				t.Errorf("applied = %v, want %v", applied, tc.applied)
			}
			if got != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// compactGitDiffText
// --------------------------------------------------------------------------

func TestCompactGitDiffText_Golden(t *testing.T) {
	multiFileDiff := cxLines(
		"diff --git a/a.go b/a.go",
		"index 111..222 100644",
		"--- a/a.go",
		"+++ b/a.go",
		"@@ -1,4 +1,6 @@",
		" ctx",
		"+add one",
		"+add two",
		"-del one",
		"+add three",
		"diff --git a/b.go b/b.go",
		"--- a/b.go",
		"+++ b/b.go",
		"@@ -10,2 +10,3 @@",
		"+bee one",
		"-bee two",
		" bee ctx",
		"diff --git a/c.go b/c.go",
		"@@ -1 +1 @@",
		"+cee",
	)

	tests := []struct {
		name    string
		in      string
		cfg     CompactorConfig
		want    string
		applied bool
	}{
		{
			name: "multi file diff with hunk and line caps",
			cfg: cxCfg(func(c *CompactorConfig) {
				c.MaxDiffLines = 14
				c.MaxDiffHunkLines = 3
			}),
			in: multiFileDiff,
			want: "diff --git a/a.go b/a.go\nindex 111..222 100644\n--- a/a.go\n+++ b/a.go\n" +
				"@@ -1,4 +1,6 @@\n ctx\n+add one\n+add two\n  (+3 -1)\n" +
				"diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -10,2 +10,3 @@\n" +
				"+bee one\n-bee two\n  (+1 -1)\n\n... (6 lines omitted from diff)\n",
			applied: true,
		},
		{
			name:    "non hunk content only, summary grows the output",
			cfg:     cxCfg(func(c *CompactorConfig) { c.MaxDiffLines = 3 }),
			in:      cxLines("plain one", "plain two", "plain three", "plain four", "plain five"),
			want:    cxLines("plain one", "plain two", "plain three", "plain four", "plain five"),
			applied: false,
		},
		{
			name:    "omission marker makes result longer than input",
			cfg:     cxCfg(func(c *CompactorConfig) { c.MaxDiffLines = 2 }),
			in:      "ab\ncd\nef",
			want:    "ab\ncd\nef",
			applied: false,
		},
		{
			name:    "short diff untouched",
			cfg:     cxCfg(nil),
			in:      "short diff",
			want:    "short diff",
			applied: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, applied := compactGitDiffText(tc.in, tc.cfg)
			if applied != tc.applied {
				t.Errorf("applied = %v, want %v", applied, tc.applied)
			}
			if got != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// gitOverviewHandler
// --------------------------------------------------------------------------

// cxGitRepoWithStatus builds a repo with one commit plus a staged, an unstaged
// and an untracked file, so every include flag has something to include.
func cxGitRepoWithStatus(t *testing.T) *Sandbox {
	t.Helper()
	dir := initGitRepo(t)

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		cxRunGit(t, dir, args...)
	}

	write("committed.go", "package main\n")
	write("modified.go", "package main\n")
	git("add", "committed.go", "modified.go")
	git("commit", "-m", "initial commit")

	write("staged.go", "package main\n")
	git("add", "staged.go")
	write("modified.go", "package main\n\nfunc main() {}\n")
	write("untracked.go", "package main\n")

	return testSandbox(t, dir)
}

func TestGitOverviewHandler_IncludeFlags(t *testing.T) {
	sb := cxGitRepoWithStatus(t)

	yes, no := true, false
	tests := []struct {
		name                                  string
		input                                 GitOverviewInput
		wantStaged, wantUnstaged, wantUntrack []string
	}{
		{
			name:         "defaults include everything",
			input:        GitOverviewInput{},
			wantStaged:   []string{"A staged.go"},
			wantUnstaged: []string{"M modified.go"},
			wantUntrack:  []string{"untracked.go"},
		},
		{
			name:         "explicit true is the same as default",
			input:        GitOverviewInput{IncludeStaged: &yes, IncludeUnstaged: &yes, IncludeUntracked: &yes},
			wantStaged:   []string{"A staged.go"},
			wantUnstaged: []string{"M modified.go"},
			wantUntrack:  []string{"untracked.go"},
		},
		{
			name:         "staged excluded",
			input:        GitOverviewInput{IncludeStaged: &no},
			wantUnstaged: []string{"M modified.go"},
			wantUntrack:  []string{"untracked.go"},
		},
		{
			name:        "unstaged excluded",
			input:       GitOverviewInput{IncludeUnstaged: &no},
			wantStaged:  []string{"A staged.go"},
			wantUntrack: []string{"untracked.go"},
		},
		{
			name:         "untracked excluded",
			input:        GitOverviewInput{IncludeUntracked: &no},
			wantStaged:   []string{"A staged.go"},
			wantUnstaged: []string{"M modified.go"},
		},
		{
			name:  "all excluded",
			input: GitOverviewInput{IncludeStaged: &no, IncludeUnstaged: &no, IncludeUntracked: &no},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := gitOverviewHandler(sb, nil, tc.input)
			if err != nil {
				t.Fatal(err)
			}
			cxWantSlice(t, "staged", out.StagedFiles, tc.wantStaged)
			cxWantSlice(t, "unstaged", out.UnstagedFiles, tc.wantUnstaged)
			cxWantSlice(t, "untracked", out.UntrackedFiles, tc.wantUntrack)

			if out.Branch == "" {
				t.Error("branch should always be populated")
			}
			if len(out.RecentCommits) != 1 {
				t.Errorf("recent commits = %d, want 1", len(out.RecentCommits))
			}
			if out.Upstream != "" {
				t.Errorf("upstream = %q, want empty for a repo with no remote", out.Upstream)
			}
			if out.Ahead != 0 || out.Behind != 0 {
				t.Errorf("ahead/behind = %d/%d, want 0/0", out.Ahead, out.Behind)
			}
		})
	}
}

// TestGitOverviewHandler_Upstream covers the upstream / ahead-behind branch,
// which a repo without a remote never reaches.
func TestGitOverviewHandler_Upstream(t *testing.T) {
	origin := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(origin, "a.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cxRunGit(t, origin, "add", "a.go")
	cxRunGit(t, origin, "commit", "-m", "one")

	clone := t.TempDir()
	cxRunGit(t, clone, "clone", origin, ".")
	cxRunGit(t, clone, "config", "user.email", "test@test.com")
	cxRunGit(t, clone, "config", "user.name", "Test")
	cxRunGit(t, clone, "config", "commit.gpgsign", "false")

	// One local commit ahead of the tracked upstream.
	if err := os.WriteFile(filepath.Join(clone, "b.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cxRunGit(t, clone, "add", "b.go")
	cxRunGit(t, clone, "commit", "-m", "two")

	out, err := gitOverviewHandler(testSandbox(t, clone), nil, GitOverviewInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Upstream == "" {
		t.Fatal("expected a tracked upstream after clone")
	}
	if out.Ahead != 1 {
		t.Errorf("ahead = %d, want 1", out.Ahead)
	}
	if out.Behind != 0 {
		t.Errorf("behind = %d, want 0", out.Behind)
	}
}

func cxWantSlice(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s = %v, want %v", label, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s = %v, want %v", label, got, want)
			return
		}
	}
}

// --------------------------------------------------------------------------
// grepHandler
// --------------------------------------------------------------------------

// cxGrepTree lays out a small tree covering directory recursion, glob
// filtering, skipped directories and a plain file target.
func cxGrepTree(t *testing.T) (*Sandbox, string) {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"a.go":              "package main\nfunc Alpha() {}\nfunc beta() {}\n",
		"b.txt":             "alpha in text\nFUNC upper\n",
		"sub/c.go":          "package sub\nfunc Gamma() {}\n",
		"node_modules/d.go": "func Skipped() {}\n",
	}
	for name, body := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return testSandbox(t, dir), dir
}

func TestGrepHandler_Table(t *testing.T) {
	sb, _ := cxGrepTree(t)

	tests := []struct {
		name      string
		input     GrepInput
		wantErr   string
		wantFiles []string // sorted set of files that must appear
		wantTotal int
	}{
		{
			name:    "empty pattern is rejected",
			input:   GrepInput{Pattern: ""},
			wantErr: "pattern is required",
		},
		{
			name:    "missing path is rejected",
			input:   GrepInput{Pattern: "func", Path: "nope"},
			wantErr: "path not found",
		},
		{
			name:    "invalid regex is rejected",
			input:   GrepInput{Pattern: "[invalid"},
			wantErr: "invalid regex pattern",
		},
		{
			name:      "directory walk finds all go files",
			input:     GrepInput{Pattern: "^func ", Path: "."},
			wantFiles: []string{"a.go", "c.go"},
			wantTotal: 3,
		},
		{
			name:      "glob narrows to txt",
			input:     GrepInput{Pattern: "alpha", Path: ".", Glob: "*.txt"},
			wantFiles: []string{"b.txt"},
			wantTotal: 1,
		},
		{
			name:      "case insensitive matches both cases",
			input:     GrepInput{Pattern: "ALPHA", Path: ".", CaseInsensitive: true},
			wantFiles: []string{"a.go", "b.txt"},
			wantTotal: 2,
		},
		{
			name:      "case sensitive misses",
			input:     GrepInput{Pattern: "ALPHA", Path: "."},
			wantTotal: 0,
		},
		{
			name:      "single file target",
			input:     GrepInput{Pattern: "func", Path: "a.go"},
			wantFiles: []string{"a.go"},
			wantTotal: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := grepHandler(sb, tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if out.TotalMatches != tc.wantTotal {
				t.Errorf("total = %d, want %d (matches: %+v)", out.TotalMatches, tc.wantTotal, out.Matches)
			}
			for _, want := range tc.wantFiles {
				found := false
				for _, m := range out.Matches {
					if strings.HasSuffix(m.File, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("no match in %s; got %+v", want, out.Matches)
				}
			}
			if out.Truncated {
				t.Error("did not expect truncation")
			}
		})
	}
}

// TestGrepHandler_Truncates covers the maxGrepMatches cap on both the directory
// walk and the single-file path.
func TestGrepHandler_Truncates(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < maxGrepMatches+50; i++ {
		fmt.Fprintf(&b, "hit %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.txt"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	sb := testSandbox(t, dir)

	for _, path := range []string{"many.txt", "."} {
		t.Run(path, func(t *testing.T) {
			out, err := grepHandler(sb, GrepInput{Pattern: "^hit ", Path: path})
			if err != nil {
				t.Fatal(err)
			}
			if len(out.Matches) != maxGrepMatches {
				t.Errorf("matches = %d, want %d", len(out.Matches), maxGrepMatches)
			}
			if out.TotalMatches != maxGrepMatches+50 {
				t.Errorf("total = %d, want %d", out.TotalMatches, maxGrepMatches+50)
			}
			if !out.Truncated {
				t.Error("expected Truncated = true")
			}
		})
	}
}

// TestGrepHandler_RegexCacheReuse runs the same pattern twice so the second call
// takes the cache-hit path rather than recompiling.
func TestGrepHandler_RegexCacheReuse(t *testing.T) {
	sb, _ := cxGrepTree(t)
	in := GrepInput{Pattern: "cxCacheProbe_" + t.Name(), Path: "."}

	first, err := grepHandler(sb, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := grepHandler(sb, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalMatches != second.TotalMatches {
		t.Errorf("cached run differs: %d vs %d", first.TotalMatches, second.TotalMatches)
	}
}

// cxRunGit runs a git command in dir with a deterministic identity and fails the
// test on error.
func cxRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@test.com",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=commit.gpgsign", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=tag.gpgsign", "GIT_CONFIG_VALUE_1=false",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %s: %v", strings.Join(args, " "), out, err)
	}
}

// --------------------------------------------------------------------------
// Extracted helpers
// --------------------------------------------------------------------------

func TestContainsAnyOf(t *testing.T) {
	tests := []struct {
		name string
		s    string
		subs []string
		want bool
	}{
		{name: "first marker", s: "an error here", subs: buildErrorMarkers, want: true},
		{name: "last marker", s: "undefined: foo", subs: buildErrorMarkers, want: true},
		{name: "middle marker", s: "fatal: nope", subs: buildErrorMarkers, want: true},
		{name: "no marker", s: "all good", subs: buildErrorMarkers, want: false},
		{name: "warning needs the colon", s: "warning about it", subs: buildErrorMarkers, want: false},
		{name: "empty marker list", s: "anything", subs: nil, want: false},
		{name: "summary marker", s: "exit status 2", subs: buildSummaryMarkers, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := containsAnyOf(tc.s, tc.subs); got != tc.want {
				t.Errorf("containsAnyOf(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

func TestHasAnyPrefix(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{name: "import", s: "import \"fmt\"", want: true},
		{name: "package", s: "package tools", want: true},
		{name: "func needs the space", s: "func main() {}", want: true},
		{name: "funcy is not func", s: "funcy()", want: false},
		{name: "type", s: "type T struct{}", want: true},
		{name: "indented declaration does not count", s: "\tfunc main() {}", want: false},
		{name: "plain", s: "hello", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasAnyPrefix(tc.s, truncateDeclPrefixes); got != tc.want {
				t.Errorf("hasAnyPrefix(%q) = %v, want %v", tc.s, got, tc.want)
			}
		})
	}
}

func TestSelectBuildErrorLines(t *testing.T) {
	cfg := cxCfg(func(c *CompactorConfig) {
		c.MaxBuildErrors = 1
		c.MaxBuildErrLines = 1
	})
	lines := []string{
		"noise before",
		"first error",
		"context kept",
		"context dropped",
		"second error over the cap",
		"ok  pkg",
		"unrelated",
	}
	filtered, count := selectBuildErrorLines(lines, cfg)
	wantFiltered := []string{"first error", "context kept", "ok  pkg"}
	cxWantSlice(t, "filtered", filtered, wantFiltered)
	if count != 1 {
		t.Errorf("errorCount = %d, want 1", count)
	}
}

func TestAppendFailDetail(t *testing.T) {
	tests := []struct {
		name        string
		details     []string
		current     []string
		maxFailures int
		want        []string
	}{
		{name: "appends joined detail", current: []string{"a", "b"}, maxFailures: 2, want: []string{"a\nb"}},
		{name: "nothing in progress", details: []string{"x"}, maxFailures: 2, want: []string{"x"}},
		{name: "at the cap", details: []string{"x"}, current: []string{"a"}, maxFailures: 1, want: []string{"x"}},
		{name: "zero cap keeps nothing", current: []string{"a"}, maxFailures: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := appendFailDetail(tc.details, tc.current, tc.maxFailures)
			cxWantSlice(t, "details", got, tc.want)
		})
	}
}

func TestLineTruncationScore(t *testing.T) {
	tests := []struct {
		name string
		line string
		want int
	}{
		{name: "error", line: "an ERROR occurred", want: 10},
		{name: "fail", line: "test failed", want: 10},
		{name: "panic", line: "panic: nil map", want: 10},
		{name: "fatal", line: "Fatal: stop", want: 10},
		{name: "warning", line: "WARNING here", want: 7},
		{name: "import declaration", line: "import \"os\"", want: 5},
		{name: "func declaration", line: "func f() {}", want: 5},
		{name: "blank", line: "   ", want: 0},
		{name: "empty", line: "", want: 0},
		{name: "ordinary", line: "just some text", want: 1},
		{name: "error beats warning", line: "warning: this error", want: 10},
		{name: "warning beats declaration", line: "func warning() {}", want: 7},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := lineTruncationScore(tc.line); got != tc.want {
				t.Errorf("lineTruncationScore(%q) = %d, want %d", tc.line, got, tc.want)
			}
		})
	}
}

func TestPartitionByPriority(t *testing.T) {
	middle := []scoredLine{
		{line: "err", score: 10},
		{line: "warn", score: 7},
		{line: "decl", score: 5},
		{line: "plain", score: 1},
		{line: "blank", score: 0},
	}
	high, low := partitionByPriority(middle)
	cxWantSlice(t, "high", high, []string{"err", "warn", "decl"})
	cxWantSlice(t, "low", low, []string{"plain"})
}

func TestAppendUpTo(t *testing.T) {
	tests := []struct {
		name          string
		dst, src      []string
		remaining     int
		want          []string
		wantRemaining int
	}{
		{name: "budget covers all", src: []string{"a", "b"}, remaining: 3, want: []string{"a", "b"}, wantRemaining: 1},
		{name: "budget runs out", src: []string{"a", "b", "c"}, remaining: 2, want: []string{"a", "b"}, wantRemaining: 0},
		{name: "no budget", src: []string{"a"}, remaining: 0, wantRemaining: 0},
		{name: "negative budget", src: []string{"a"}, remaining: -1, wantRemaining: -1},
		{name: "appends onto existing", dst: []string{"x"}, src: []string{"a"}, remaining: 1, want: []string{"x", "a"}, wantRemaining: 0},
		{name: "empty source", dst: []string{"x"}, remaining: 2, want: []string{"x"}, wantRemaining: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, remaining := appendUpTo(tc.dst, tc.src, tc.remaining)
			cxWantSlice(t, "appended", got, tc.want)
			if remaining != tc.wantRemaining {
				t.Errorf("remaining = %d, want %d", remaining, tc.wantRemaining)
			}
		})
	}
}

// TestSelectMiddleLines_FitsWholeBand covers the branch smartTruncate itself can
// never reach: it only calls in when the band is larger than the budget.
func TestSelectMiddleLines_FitsWholeBand(t *testing.T) {
	middle := []scoredLine{
		{line: "a", score: 1},
		{line: "", score: 0},
		{line: "c", score: 10},
	}
	cxWantSlice(t, "selected", selectMiddleLines(middle, 5), []string{"a", "", "c"})
	cxWantSlice(t, "selected exactly", selectMiddleLines(middle, 3), []string{"a", "", "c"})
	if got := selectMiddleLines(nil, 0); got != nil {
		t.Errorf("selectMiddleLines(nil, 0) = %v, want nil", got)
	}
}

func TestSelectMiddleLines_DropsAndMarks(t *testing.T) {
	middle := []scoredLine{
		{line: "err", score: 10},
		{line: "plain1", score: 1},
		{line: "blank", score: 0},
		{line: "plain2", score: 1},
	}
	// Budget of 2: the high-priority line first, then one low-priority line, then
	// the omission marker for the 2 lines that did not fit.
	cxWantSlice(t, "selected", selectMiddleLines(middle, 2),
		[]string{"err", "plain1", "... (2 lines omitted)"})
}

// TestDiffTextCompactor_FlushFile pins the guard on the per-file tally: it is
// written only when a file is open and something actually changed.
func TestDiffTextCompactor_FlushFile(t *testing.T) {
	tests := []struct {
		name                 string
		currentFile          string
		additions, deletions int
		want                 string
	}{
		{name: "no file open", additions: 3, want: ""},
		{name: "file open but unchanged", currentFile: "a.go", want: ""},
		{name: "additions only", currentFile: "a.go", additions: 2, want: "  (+2 -0)\n"},
		{name: "deletions only", currentFile: "a.go", deletions: 4, want: "  (+0 -4)\n"},
		{name: "both", currentFile: "a.go", additions: 1, deletions: 1, want: "  (+1 -1)\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &diffTextCompactor{
				cfg:         cxCfg(nil),
				currentFile: tc.currentFile,
				additions:   tc.additions,
				deletions:   tc.deletions,
			}
			c.flushFile()
			if got := c.b.String(); got != tc.want {
				t.Errorf("flushFile wrote %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDiffTextCompactor_ConsumeRouting checks each arm of consume in isolation.
func TestDiffTextCompactor_ConsumeRouting(t *testing.T) {
	c := &diffTextCompactor{cfg: cxCfg(func(cfg *CompactorConfig) { cfg.MaxDiffHunkLines = 1 })}

	c.consume("--- a/x.go") // passthrough, no hunk open yet
	if c.inHunk {
		t.Error("passthrough line should not open a hunk")
	}

	c.consume("@@ -1 +1 @@")
	if !c.inHunk || c.hunkLines != 0 {
		t.Errorf("hunk header: inHunk=%v hunkLines=%d, want true/0", c.inHunk, c.hunkLines)
	}

	c.consume("+one") // kept, counted as an addition
	c.consume("-two") // over MaxDiffHunkLines: counted but not emitted
	if c.additions != 1 || c.deletions != 1 {
		t.Errorf("additions/deletions = %d/%d, want 1/1", c.additions, c.deletions)
	}

	c.consume("diff --git a/y.go b/y.go")
	if c.currentFile != "y.go" {
		t.Errorf("currentFile = %q, want y.go", c.currentFile)
	}
	if c.inHunk || c.additions != 0 || c.deletions != 0 {
		t.Errorf("file header should reset hunk state, got inHunk=%v +%d -%d", c.inHunk, c.additions, c.deletions)
	}

	want := "--- a/x.go\n@@ -1 +1 @@\n+one\ndiff --git a/y.go b/y.go\n"
	if got := c.b.String(); got != want {
		t.Errorf("output = %q, want %q", got, want)
	}
	if c.totalLines != 4 {
		t.Errorf("totalLines = %d, want 4", c.totalLines)
	}
}

func TestIncludeOrDefault(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name string
		flag *bool
		want bool
	}{
		{name: "unset defaults to true", want: true},
		{name: "explicit true", flag: &yes, want: true},
		{name: "explicit false", flag: &no, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := includeOrDefault(tc.flag); got != tc.want {
				t.Errorf("includeOrDefault = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCollectGitHead_EmptyRepo pins the "HEAD does not resolve yet" path, where
// both branch and commit list are left unset.
func TestCollectGitHead_EmptyRepo(t *testing.T) {
	dir := initGitRepo(t)
	var out GitOverviewOutput
	collectGitHead(nil, dir, &out)
	if len(out.RecentCommits) != 0 {
		t.Errorf("recent commits = %v, want none in an empty repo", out.RecentCommits)
	}
}

// TestCollectGitStatusFiles_NotARepo pins the early return when git status fails.
func TestCollectGitStatusFiles_NotARepo(t *testing.T) {
	var out GitOverviewOutput
	collectGitStatusFiles(nil, t.TempDir(), GitOverviewInput{}, &out)
	if out.StagedFiles != nil || out.UnstagedFiles != nil || out.UntrackedFiles != nil {
		t.Errorf("expected no file lists outside a repo, got %+v", out)
	}
}

// TestCollectGitUpstream_NoUpstream pins the early return when the branch tracks
// nothing.
func TestCollectGitUpstream_NoUpstream(t *testing.T) {
	var out GitOverviewOutput
	collectGitUpstream(nil, initGitRepo(t), &out)
	if out.Upstream != "" || out.Ahead != 0 || out.Behind != 0 {
		t.Errorf("expected no upstream info, got %+v", out)
	}
}

func TestCompileGrepPattern(t *testing.T) {
	tests := []struct {
		name    string
		input   GrepInput
		probe   string
		wantHit bool
		wantErr bool
	}{
		{name: "case sensitive miss", input: GrepInput{Pattern: "Needle"}, probe: "needle", wantHit: false},
		{name: "case sensitive hit", input: GrepInput{Pattern: "Needle"}, probe: "Needle", wantHit: true},
		{name: "case insensitive hit", input: GrepInput{Pattern: "Needle", CaseInsensitive: true}, probe: "needle", wantHit: true},
		{name: "invalid pattern", input: GrepInput{Pattern: "[unclosed"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			re, err := compileGrepPattern(tc.input)
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "invalid regex pattern") {
					t.Fatalf("err = %v, want invalid regex pattern", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := re.MatchString(tc.probe); got != tc.wantHit {
				t.Errorf("MatchString(%q) = %v, want %v", tc.probe, got, tc.wantHit)
			}
		})
	}
}

// TestCompileGrepPattern_CacheKeyIsCaseAware guards the cache key: the same
// pattern with and without CaseInsensitive must not share an entry.
func TestCompileGrepPattern_CacheKeyIsCaseAware(t *testing.T) {
	pattern := "CxCacheKeyProbe"

	sensitive, err := compileGrepPattern(GrepInput{Pattern: pattern})
	if err != nil {
		t.Fatal(err)
	}
	insensitive, err := compileGrepPattern(GrepInput{Pattern: pattern, CaseInsensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if sensitive.MatchString("cxcachekeyprobe") {
		t.Error("case-sensitive regex should not match lower case")
	}
	if !insensitive.MatchString("cxcachekeyprobe") {
		t.Error("case-insensitive regex should match lower case")
	}

	// Second call for the same key comes back from the cache.
	cached, err := compileGrepPattern(GrepInput{Pattern: pattern})
	if err != nil {
		t.Fatal(err)
	}
	if cached != sensitive {
		t.Error("expected the cached *regexp.Regexp to be reused")
	}
}
