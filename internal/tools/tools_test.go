package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		"grep": true, "find": true, "ls": true, "tree": true,
		"git-overview": true, "git-file-diff": true, "git-hunk": true,
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
