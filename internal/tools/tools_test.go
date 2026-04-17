package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain sets up test environment.
func TestMain(m *testing.M) {
	// Disable ripgrep during tests to use Go implementation
	// This ensures consistent behavior across environments
	grepRGDisabled = true
	os.Exit(m.Run())
}

// testSandbox creates a Sandbox rooted at the given directory for testing.
func testSandbox(t *testing.T, dir string) *Sandbox {
	t.Helper()
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatalf("NewSandbox(%s): %v", dir, err)
	}
	t.Cleanup(func() { sb.Close() })
	return sb
}

func TestReadHandler(t *testing.T) {
	// Create temp file
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\nline4\nline5\n"), 0o644)

	t.Run("read entire file", func(t *testing.T) {
		out, err := readHandler(sb, ReadInput{FilePath: path})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalLines != 6 { // trailing newline creates empty last line
			t.Errorf("expected 6 total lines, got %d", out.TotalLines)
		}
		if !strings.Contains(out.Content, "line1") {
			t.Error("expected content to contain 'line1'")
		}
	})

	t.Run("read with offset and limit", func(t *testing.T) {
		out, err := readHandler(sb, ReadInput{FilePath: path, Offset: 2, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "line2") {
			t.Error("expected content to contain 'line2'")
		}
		if !strings.Contains(out.Content, "line3") {
			t.Error("expected content to contain 'line3'")
		}
		if strings.Contains(out.Content, "line4") {
			t.Error("expected content NOT to contain 'line4'")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := readHandler(sb, ReadInput{FilePath: filepath.Join(dir, "nonexistent.txt")})
		if err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run("empty file_path", func(t *testing.T) {
		_, err := readHandler(sb, ReadInput{})
		if err == nil {
			t.Error("expected error for empty file_path")
		}
	})
}

func TestWriteHandler(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	t.Run("write new file", func(t *testing.T) {
		path := filepath.Join(dir, "new.txt")
		out, err := writeHandler(sb, WriteInput{FilePath: path, Content: "hello world"})
		if err != nil {
			t.Fatal(err)
		}
		if out.BytesWritten != 11 {
			t.Errorf("expected 11 bytes written, got %d", out.BytesWritten)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "hello world" {
			t.Errorf("file content mismatch: %q", string(data))
		}
	})

	t.Run("create nested directories", func(t *testing.T) {
		path := filepath.Join(dir, "a", "b", "c", "file.txt")
		_, err := writeHandler(sb, WriteInput{FilePath: path, Content: "nested"})
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "nested" {
			t.Errorf("file content mismatch: %q", string(data))
		}
	})
}

func TestEditHandler(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	t.Run("replace unique string", func(t *testing.T) {
		path := filepath.Join(dir, "edit.txt")
		os.WriteFile(path, []byte("hello world"), 0o644)

		out, err := editHandler(sb, EditInput{
			FilePath:  path,
			OldString: "hello",
			NewString: "goodbye",
		})
		if err != nil {
			t.Fatal(err)
		}
		if out.Replacements != 1 {
			t.Errorf("expected 1 replacement, got %d", out.Replacements)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "goodbye world" {
			t.Errorf("file content: %q", string(data))
		}
	})

	t.Run("reject non-unique without replace_all", func(t *testing.T) {
		path := filepath.Join(dir, "dup.txt")
		os.WriteFile(path, []byte("foo foo foo"), 0o644)

		_, err := editHandler(sb, EditInput{
			FilePath:  path,
			OldString: "foo",
			NewString: "bar",
		})
		if err == nil {
			t.Error("expected error for non-unique match")
		}
	})

	t.Run("replace_all", func(t *testing.T) {
		path := filepath.Join(dir, "all.txt")
		os.WriteFile(path, []byte("foo foo foo"), 0o644)

		out, err := editHandler(sb, EditInput{
			FilePath:   path,
			OldString:  "foo",
			NewString:  "bar",
			ReplaceAll: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if out.Replacements != 3 {
			t.Errorf("expected 3 replacements, got %d", out.Replacements)
		}
		data, _ := os.ReadFile(path)
		if string(data) != "bar bar bar" {
			t.Errorf("file content: %q", string(data))
		}
	})

	t.Run("string not found", func(t *testing.T) {
		path := filepath.Join(dir, "nf.txt")
		os.WriteFile(path, []byte("hello"), 0o644)

		_, err := editHandler(sb, EditInput{
			FilePath:  path,
			OldString: "missing",
			NewString: "replaced",
		})
		if err == nil {
			t.Error("expected error for missing string")
		}
	})

	t.Run("non-unique error includes line numbers", func(t *testing.T) {
		path := filepath.Join(dir, "multiline.txt")
		os.WriteFile(path, []byte("line one\nfoo\nline three\nfoo\nline five\n"), 0o644)

		_, err := editHandler(sb, EditInput{
			FilePath:  path,
			OldString: "foo",
			NewString: "bar",
		})
		if err == nil {
			t.Fatal("expected error for non-unique match")
		}
		errStr := err.Error()
		// Error should mention count
		if !strings.Contains(errStr, "2 times") && !strings.Contains(errStr, "found 2") {
			t.Errorf("error should mention occurrence count: %s", errStr)
		}
		// Error should mention replace_all
		if !strings.Contains(errStr, "replace_all=true") {
			t.Errorf("error should suggest replace_all=true: %s", errStr)
		}
		// Error should include line number hints
		if !strings.Contains(errStr, "line") && !strings.Contains(errStr, "2") {
			t.Errorf("error should hint at line numbers: %s", errStr)
		}
	})

	t.Run("missing file error is descriptive", func(t *testing.T) {
		_, err := editHandler(sb, EditInput{
			FilePath:  filepath.Join(dir, "nonexistent.txt"),
			OldString: "foo",
			NewString: "bar",
		})
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		errStr := err.Error()
		if !strings.Contains(errStr, "nonexistent.txt") {
			t.Errorf("error should mention the file path: %s", errStr)
		}
	})
}

func TestStringsSimilarity(t *testing.T) {
	tests := []struct {
		a, b   string
		minSim float64
	}{
		{"hello", "hello", 1.0},
		{"hello", "hello!", 0.8},
		{"foo", "bar", 0},
		{"func main()", "func main() {", 0.8},
		{"", "", 0},
		{"abc", "", 0},
	}
	for _, tt := range tests {
		sim := stringsSimilarity(tt.a, tt.b)
		if sim < tt.minSim {
			t.Errorf("stringsSimilarity(%q, %q) = %v, want >= %v", tt.a, tt.b, sim, tt.minSim)
		}
	}
}

func TestBashHandler(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	t.Run("simple command", func(t *testing.T) {
		out, err := bashHandler(sb, nil, BashInput{Command: "echo hello"})
		if err != nil {
			t.Fatal(err)
		}
		if out.ExitCode != 0 {
			t.Errorf("expected exit code 0, got %d", out.ExitCode)
		}
		if strings.TrimSpace(out.Stdout) != "hello" {
			t.Errorf("stdout: %q", out.Stdout)
		}
	})

	t.Run("nonzero exit", func(t *testing.T) {
		out, err := bashHandler(sb, nil, BashInput{Command: "exit 42"})
		if err != nil {
			t.Fatal(err)
		}
		if out.ExitCode != 42 {
			t.Errorf("expected exit code 42, got %d", out.ExitCode)
		}
	})

	t.Run("empty command", func(t *testing.T) {
		_, err := bashHandler(sb, nil, BashInput{})
		if err == nil {
			t.Error("expected error for empty command")
		}
	})
}

func TestGrepHandler(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("func main() {\n\tfmt.Println(\"hello\")\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("no match here\nfunc other()\n"), 0o644)

	t.Run("search directory", func(t *testing.T) {
		out, err := grepHandler(sb, GrepInput{Pattern: "func", Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalMatches != 2 {
			t.Errorf("expected 2 matches, got %d", out.TotalMatches)
		}
	})

	t.Run("glob filter", func(t *testing.T) {
		out, err := grepHandler(sb, GrepInput{Pattern: "func", Path: ".", Glob: "*.go"})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalMatches != 1 {
			t.Errorf("expected 1 match with glob filter, got %d", out.TotalMatches)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		out, err := grepHandler(sb, GrepInput{Pattern: "FUNC", Path: ".", CaseInsensitive: true})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalMatches != 2 {
			t.Errorf("expected 2 matches, got %d", out.TotalMatches)
		}
	})

	t.Run("invalid regex", func(t *testing.T) {
		_, err := grepHandler(sb, GrepInput{Pattern: "[invalid"})
		if err == nil {
			t.Error("expected error for invalid regex")
		}
	})
}

func TestFindHandler(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(dir, "test.go"), []byte("package test"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0o644)

	t.Run("find by pattern", func(t *testing.T) {
		out, err := findHandler(sb, FindInput{Pattern: "*.go", Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalFiles != 2 {
			t.Errorf("expected 2 files, got %d", out.TotalFiles)
		}
	})

	t.Run("find specific file", func(t *testing.T) {
		out, err := findHandler(sb, FindInput{Pattern: "*.md", Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalFiles != 1 {
			t.Errorf("expected 1 file, got %d", out.TotalFiles)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		out, err := findHandler(sb, FindInput{Pattern: "*.rs", Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalFiles != 0 {
			t.Errorf("expected 0 files, got %d", out.TotalFiles)
		}
	})
}

func TestLsHandler(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("content"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	t.Run("list directory", func(t *testing.T) {
		out, err := lsHandler(sb, LsInput{Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Entries) != 2 {
			t.Errorf("expected 2 entries, got %d", len(out.Entries))
		}
		// Check that we have one dir and one file
		dirs := 0
		files := 0
		for _, e := range out.Entries {
			if e.IsDir {
				dirs++
			} else {
				files++
			}
		}
		if dirs != 1 || files != 1 {
			t.Errorf("expected 1 dir and 1 file, got %d dirs and %d files", dirs, files)
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		_, err := lsHandler(sb, LsInput{Path: "nonexistent"})
		if err == nil {
			t.Error("expected error for missing directory")
		}
	})
}

func TestBashTimeout(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	out, err := bashHandler(sb, nil, BashInput{Command: "sleep 10", Timeout: 500})
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode == 0 {
		t.Error("expected non-zero exit code for timed-out command")
	}
}

func TestBashStderr(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	out, err := bashHandler(sb, nil, BashInput{Command: "echo error >&2; exit 1"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ExitCode != 1 {
		t.Errorf("expected exit code 1, got %d", out.ExitCode)
	}
	if !strings.Contains(out.Stderr, "error") {
		t.Errorf("expected 'error' in stderr, got %q", out.Stderr)
	}
}

func TestEditMissingFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	_, err := editHandler(sb, EditInput{
		FilePath:  filepath.Join(dir, "nonexistent.txt"),
		OldString: "foo",
		NewString: "bar",
	})
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestWriteOverwrite(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "overwrite.txt")
	os.WriteFile(path, []byte("original"), 0o644)

	_, err := writeHandler(sb, WriteInput{FilePath: path, Content: "replaced"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "replaced" {
		t.Errorf("file content: %q, want %q", string(data), "replaced")
	}
}

func TestReadBeyondOffset(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "short.txt")
	os.WriteFile(path, []byte("line1\n"), 0o644)

	out, err := readHandler(sb, ReadInput{FilePath: path, Offset: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "" {
		t.Errorf("expected empty content for offset beyond file, got %q", out.Content)
	}
}

func TestGrepNoMatches(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello world\n"), 0o644)

	out, err := grepHandler(sb, GrepInput{Pattern: "zzzzz", Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalMatches != 0 {
		t.Errorf("expected 0 matches, got %d", out.TotalMatches)
	}
}

func TestFindNestedDirs(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	sub := filepath.Join(dir, "sub", "deep")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "nested.go"), []byte("package nested"), 0o644)
	os.WriteFile(filepath.Join(dir, "top.go"), []byte("package top"), 0o644)

	out, err := findHandler(sb, FindInput{Pattern: "*.go", Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalFiles != 2 {
		t.Errorf("expected 2 files, got %d", out.TotalFiles)
	}
}

func TestFindDoublestarGlob(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	sub := filepath.Join(dir, "pkg", "api")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)
	os.WriteFile(filepath.Join(sub, "handler.go"), []byte("package api"), 0o644)
	os.WriteFile(filepath.Join(sub, "handler_test.go"), []byte("package api"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# readme"), 0o644)

	t.Run("**/*.go finds all go files recursively", func(t *testing.T) {
		out, err := findHandler(sb, FindInput{Pattern: "**/*.go", Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalFiles != 3 {
			t.Errorf("expected 3 files, got %d: %v", out.TotalFiles, out.Files)
		}
	})

	t.Run("pkg/**/*.go finds go files under pkg/", func(t *testing.T) {
		out, err := findHandler(sb, FindInput{Pattern: "pkg/**/*.go", Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalFiles != 2 {
			t.Errorf("expected 2 files, got %d: %v", out.TotalFiles, out.Files)
		}
	})

	t.Run("**/*_test.go finds test files", func(t *testing.T) {
		out, err := findHandler(sb, FindInput{Pattern: "**/*_test.go", Path: "."})
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalFiles != 1 {
			t.Errorf("expected 1 file, got %d: %v", out.TotalFiles, out.Files)
		}
	})
}

func TestLsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	out, err := lsHandler(sb, LsInput{Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 0 {
		t.Errorf("expected 0 entries for empty dir, got %d", len(out.Entries))
	}
}

func TestReadTruncatesLargeFiles(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "large.txt")

	// Create a file with 3000 lines (exceeds defaultReadLimit of 2000)
	var b strings.Builder
	for i := 1; i <= 3000; i++ {
		b.WriteString("line content here\n")
	}
	os.WriteFile(path, []byte(b.String()), 0o644)

	t.Run("default limit truncates at 2000 lines", func(t *testing.T) {
		out, err := readHandler(sb, ReadInput{FilePath: path})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Truncated {
			t.Error("expected Truncated=true for large file")
		}
		if !strings.Contains(out.Content, "truncated") {
			t.Error("expected truncation message in content")
		}
		// Should show 2000 lines, not 3000+
		lineCount := strings.Count(out.Content, "\n")
		if lineCount > 2010 { // 2000 lines + truncation message
			t.Errorf("expected ~2000 lines, got %d", lineCount)
		}
	})

	t.Run("explicit limit bypasses default", func(t *testing.T) {
		out, err := readHandler(sb, ReadInput{FilePath: path, Limit: 3000})
		if err != nil {
			t.Fatal(err)
		}
		if out.Truncated {
			t.Error("expected Truncated=false when explicit limit covers all lines")
		}
	})

	t.Run("small file not truncated", func(t *testing.T) {
		smallPath := filepath.Join(dir, "small.txt")
		os.WriteFile(smallPath, []byte("line1\nline2\nline3\n"), 0o644)
		out, err := readHandler(sb, ReadInput{FilePath: smallPath})
		if err != nil {
			t.Fatal(err)
		}
		if out.Truncated {
			t.Error("expected Truncated=false for small file")
		}
	})

	t.Run("source code files not truncated by default", func(t *testing.T) {
		for _, ext := range []string{".go", ".rs", ".py", ".ts"} {
			codePath := filepath.Join(dir, "large"+ext)
			os.WriteFile(codePath, []byte(b.String()), 0o644)
			out, err := readHandler(sb, ReadInput{FilePath: codePath})
			if err != nil {
				t.Fatalf("ext %s: %v", ext, err)
			}
			if out.Truncated {
				t.Errorf("ext %s: expected Truncated=false for source code file", ext)
			}
			if out.TotalLines != 3001 { // 3000 lines + trailing empty
				t.Errorf("ext %s: expected 3001 total lines, got %d", ext, out.TotalLines)
			}
		}
	})
}

func TestTruncateOutput(t *testing.T) {
	t.Run("short string unchanged", func(t *testing.T) {
		s := "hello world"
		if truncateOutput(s) != s {
			t.Error("short string should be unchanged")
		}
	})

	t.Run("long string truncated", func(t *testing.T) {
		s := strings.Repeat("x", maxOutputBytes+1000)
		result := truncateOutput(s)
		if len(result) > maxOutputBytes+100 { // some room for the message
			t.Errorf("expected truncation, got length %d", len(result))
		}
		if !strings.Contains(result, "truncated") {
			t.Error("expected truncation message")
		}
	})
}

func TestTruncateLine(t *testing.T) {
	t.Run("short line unchanged", func(t *testing.T) {
		s := "hello world"
		if truncateLine(s) != s {
			t.Error("short line should be unchanged")
		}
	})

	t.Run("long line truncated", func(t *testing.T) {
		s := strings.Repeat("x", maxLineLength+100)
		result := truncateLine(s)
		if len(result) > maxLineLength+10 {
			t.Errorf("expected truncation, got length %d", len(result))
		}
		if !strings.HasSuffix(result, "...") {
			t.Error("expected '...' suffix")
		}
	})
}

func TestGrepTruncatesLongLines(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create a file with a very long line
	longLine := "func " + strings.Repeat("x", maxLineLength+200)
	os.WriteFile(filepath.Join(dir, "long.go"), []byte(longLine+"\n"), 0o644)

	out, err := grepHandler(sb, GrepInput{Pattern: "func", Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(out.Matches))
	}
	if len(out.Matches[0].Content) > maxLineLength+10 {
		t.Errorf("match content not truncated: length %d", len(out.Matches[0].Content))
	}
}

func TestGrepTruncatedFlag(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create enough matches to exceed the limit
	var b strings.Builder
	for i := 0; i < maxGrepMatches+50; i++ {
		b.WriteString("match line\n")
	}
	os.WriteFile(filepath.Join(dir, "many.txt"), []byte(b.String()), 0o644)

	out, err := grepHandler(sb, GrepInput{Pattern: "match", Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Truncated {
		t.Error("expected Truncated=true when matches exceed limit")
	}
	if len(out.Matches) > maxGrepMatches {
		t.Errorf("expected at most %d matches, got %d", maxGrepMatches, len(out.Matches))
	}
}

func TestLsTruncation(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create a small directory — should not be truncated
	for i := 0; i < 5; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%d.txt", i)), []byte("x"), 0o644)
	}

	out, err := lsHandler(sb, LsInput{Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if out.Truncated {
		t.Error("expected Truncated=false for small directory")
	}
	if out.TotalEntries != 5 {
		t.Errorf("expected TotalEntries=5, got %d", out.TotalEntries)
	}
}

func TestFindTruncatedFlag(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Small set — should not be truncated
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("x"), 0o644)

	out, err := findHandler(sb, FindInput{Pattern: "*.go", Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if out.Truncated {
		t.Error("expected Truncated=false for small result set")
	}
}

func TestCoreTools(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	tools, err := CoreTools(sb)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 11 {
		t.Errorf("expected 11 core tools, got %d", len(tools))
	}

	expected := map[string]bool{
		"read": true, "write": true, "edit": true, "bash": true,
		"find": true, "ls": true, "tree": true,
		"git-overview": true, "git-file-diff": true, "git-hunk": true,
	}
	if rgAvailable {
		expected["ripgrep"] = true
	} else {
		expected["grep"] = true
	}
	for _, tool := range tools {
		if !expected[tool.Name()] {
			t.Errorf("unexpected tool: %s", tool.Name())
		}
		delete(expected, tool.Name())
	}
	for name := range expected {
		t.Errorf("missing tool: %s", name)
	}
}

func TestReadHandlerWithCache(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "cached.go")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)

	cache := NewFileContentCache(10, 0) // no age limit

	t.Run("first read populates cache", func(t *testing.T) {
		out, err := readHandlerWithCache(sb, ReadInput{FilePath: path}, cache)
		if err != nil {
			t.Fatal(err)
		}
		if out.TotalLines != 4 { // 3 lines + trailing empty
			t.Errorf("expected 4 lines, got %d", out.TotalLines)
		}
		if cache.Len() != 1 {
			t.Errorf("expected 1 cache entry, got %d", cache.Len())
		}
	})

	t.Run("second read hits cache", func(t *testing.T) {
		out, err := readHandlerWithCache(sb, ReadInput{FilePath: path}, cache)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "line1") {
			t.Error("expected cached content to contain line1")
		}
	})

	t.Run("modified file invalidates cache", func(t *testing.T) {
		os.WriteFile(path, []byte("new1\nnew2\n"), 0o644)
		out, err := readHandlerWithCache(sb, ReadInput{FilePath: path}, cache)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.Content, "new1") {
			t.Error("expected fresh content after file modification")
		}
	})

	t.Run("source code file not truncated via cache", func(t *testing.T) {
		bigPath := filepath.Join(dir, "big.go")
		var b strings.Builder
		for i := 0; i < 3000; i++ {
			b.WriteString("code line\n")
		}
		os.WriteFile(bigPath, []byte(b.String()), 0o644)

		out, err := readHandlerWithCache(sb, ReadInput{FilePath: bigPath}, cache)
		if err != nil {
			t.Fatal(err)
		}
		if out.Truncated {
			t.Error("source code file should not be truncated")
		}
	})
}

func TestFindSkipsGitignoredFiles(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create normal file
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	// Create a file that would be gitignored
	os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("password"), 0o644)

	// Create .gitignore that ignores *.txt
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.txt\n"), 0o644)

	out, err := findHandler(sb, FindInput{Pattern: "*", Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	if out.TotalFiles != 1 {
		t.Errorf("expected 1 file (main.go only), got %d: %v", out.TotalFiles, out.Files)
	}
	if out.Files[0] != "main.go" {
		t.Errorf("expected main.go, got %v", out.Files)
	}
}

func TestFindSkipsTargetDir(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	// Create target/ directory with a binary
	targetBin := filepath.Join(dir, "target", "bin", "output")
	os.MkdirAll(filepath.Dir(targetBin), 0o755)
	os.WriteFile(targetBin, []byte("binary content"), 0o644)

	out, err := findHandler(sb, FindInput{Pattern: "*", Path: "."})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range out.Files {
		if strings.Contains(f, "target") {
			t.Errorf("target directory should be skipped, but found %s", f)
		}
	}
}

func TestGrepSkipsTargetDir(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("func main() {}"), 0o644)

	// Create target/ directory with a binary containing the pattern
	targetBin := filepath.Join(dir, "target", "output")
	os.MkdirAll(filepath.Dir(targetBin), 0o755)
	os.WriteFile(targetBin, []byte("func main() {}"), 0o644)

	out, err := grepHandler(sb, GrepInput{Pattern: "func"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range out.Matches {
		if strings.Contains(m.File, "target") {
			t.Errorf("target directory should be skipped, but found match in %s", m.File)
		}
	}
}

func TestGrepSkipsGitignoredFiles(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	os.WriteFile(filepath.Join(dir, "main.go"), []byte("password = secret\n"), 0o644)

	// Create a .env file that would be gitignored
	os.WriteFile(filepath.Join(dir, ".env"), []byte("password = secret\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".env\n"), 0o644)

	out, err := grepHandler(sb, GrepInput{Pattern: "password"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 1 {
		t.Errorf("expected 1 match (main.go only), got %d: %v", len(out.Matches), out.Matches)
	}
	if out.Matches[0].File != "main.go" {
		t.Errorf("expected match in main.go, got %s", out.Matches[0].File)
	}
}

func TestGrepWithRipgrep(t *testing.T) {
	// Only run if ripgrep is available
	if !ripgrepAvailable() {
		t.Skip("ripgrep not installed")
	}

	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create test files
	os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("hello world\nfoo bar\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("hello there\nworld peace\n"), 0o644)

	// Test basic search with ripgrep
	out, err := grepHandler(sb, GrepInput{Pattern: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(out.Matches))
	}

	// Test case insensitive
	out, err = grepHandler(sb, GrepInput{Pattern: "HELLO", CaseInsensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 2 {
		t.Errorf("expected 2 matches with case insensitive, got %d", len(out.Matches))
	}

	// Test glob filter
	out, err = grepHandler(sb, GrepInput{Pattern: "hello", Glob: "*.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 2 {
		t.Errorf("expected 2 matches with glob, got %d", len(out.Matches))
	}

	// Test no matches
	out, err = grepHandler(sb, GrepInput{Pattern: "nonexistent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(out.Matches))
	}
}

func TestFindIncludesAgentDirs(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create files in agent directories that should NOT be skipped
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644)

	// Create .pi-go, .cursor, .claude directories with files
	piGoFile := filepath.Join(dir, ".pi-go", "config.json")
	os.MkdirAll(filepath.Dir(piGoFile), 0o755)
	os.WriteFile(piGoFile, []byte(`{"key": "value"}`), 0o644)

	cursorFile := filepath.Join(dir, ".cursor", "settings.json")
	os.MkdirAll(filepath.Dir(cursorFile), 0o755)
	os.WriteFile(cursorFile, []byte(`{}`), 0o644)

	claudeFile := filepath.Join(dir, ".claude", "commands.json")
	os.MkdirAll(filepath.Dir(claudeFile), 0o755)
	os.WriteFile(claudeFile, []byte(`[]`), 0o644)

	out, err := findHandler(sb, FindInput{Pattern: "**/*", Path: "."})
	if err != nil {
		t.Fatal(err)
	}

	// Verify all files are found
	foundFiles := make(map[string]bool)
	for _, f := range out.Files {
		foundFiles[filepath.Base(filepath.Dir(f))] = true // Check parent dir
		foundFiles[f] = true
	}

	// Check .pi-go is included
	hasPiGo := false
	for _, f := range out.Files {
		if strings.Contains(f, ".pi-go") {
			hasPiGo = true
			break
		}
	}
	if !hasPiGo {
		t.Errorf(".pi-go directory should NOT be skipped, but found no files in it")
	}

	// Check .cursor is included
	hasCursor := false
	for _, f := range out.Files {
		if strings.Contains(f, ".cursor") {
			hasCursor = true
			break
		}
	}
	if !hasCursor {
		t.Errorf(".cursor directory should NOT be skipped, but found no files in it")
	}

	// Check .claude is included
	hasClaude := false
	for _, f := range out.Files {
		if strings.Contains(f, ".claude") {
			hasClaude = true
			break
		}
	}
	if !hasClaude {
		t.Errorf(".claude directory should NOT be skipped, but found no files in it")
	}
}

func TestGrepIncludesAgentDirs(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Create files in agent directories that should NOT be skipped
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("func main() {}\n"), 0o644)

	// Create .pi-go, .cursor, .claude directories with files containing the pattern
	piGoFile := filepath.Join(dir, ".pi-go", "config.json")
	os.MkdirAll(filepath.Dir(piGoFile), 0o755)
	os.WriteFile(piGoFile, []byte(`{"agent": "pi"}`), 0o644)

	cursorFile := filepath.Join(dir, ".cursor", "settings.json")
	os.MkdirAll(filepath.Dir(cursorFile), 0o755)
	os.WriteFile(cursorFile, []byte(`{"agent": "cursor"}`), 0o644)

	claudeFile := filepath.Join(dir, ".claude", "commands.json")
	os.MkdirAll(filepath.Dir(claudeFile), 0o755)
	os.WriteFile(claudeFile, []byte(`{"agent": "claude"}`), 0o644)

	out, err := grepHandler(sb, GrepInput{Pattern: "agent"})
	if err != nil {
		t.Fatal(err)
	}

	// Verify matches from all agent directories are found
	foundPiGo := false
	foundCursor := false
	foundClaude := false
	for _, m := range out.Matches {
		if strings.Contains(m.File, ".pi-go") {
			foundPiGo = true
		}
		if strings.Contains(m.File, ".cursor") {
			foundCursor = true
		}
		if strings.Contains(m.File, ".claude") {
			foundClaude = true
		}
	}

	if !foundPiGo {
		t.Errorf(".pi-go directory should NOT be skipped, but found no matches in it")
	}
	if !foundCursor {
		t.Errorf(".cursor directory should NOT be skipped, but found no matches in it")
	}
	if !foundClaude {
		t.Errorf(".claude directory should NOT be skipped, but found no matches in it")
	}
}
