package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile drops content at dir/name and returns the path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// stripLineNumbers undoes the "%6d\t" prefix so a window can be compared with
// the bytes it came from.
func stripLineNumbers(t *testing.T, content string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if line == "" || strings.HasPrefix(line, "... (") {
			continue
		}
		tab := strings.Index(line, "\t")
		if tab < 0 {
			t.Fatalf("line has no number prefix: %q", line)
		}
		out = append(out, line[tab+1:])
	}
	return out
}

func TestCountFileLines(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	tests := []struct {
		name    string
		content string
		want    int
	}{
		{"trailing newline terminates, does not add", "a\nb\nc\n", 3},
		{"no trailing newline still counts the last line", "a\nb\nc", 3},
		{"single line with newline", "only\n", 1},
		{"single line without newline", "only", 1},
		{"blank lines count", "\n\n\n", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, dir, "count.txt", tt.content)
			got, err := countFileLines(sb, path)
			if err != nil {
				t.Fatalf("countFileLines: %v", err)
			}
			if got != tt.want {
				t.Errorf("countFileLines(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}
}

// TestRead_ResumeOffsetReassemblesTheFile is the property that makes the
// window usable: paging through with the offsets the tool hands back has to
// reproduce the file exactly — no gap at a boundary, no line served twice.
func TestRead_ResumeOffsetReassemblesTheFile(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	var b strings.Builder
	const total = 5000
	for i := 1; i <= total; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	path := writeFile(t, dir, "big.txt", b.String())

	var got []string
	offset := 1
	for range 10 { // generous bound; the loop should exit on its own
		out, err := readHandler(sb, ReadInput{FilePath: path, Offset: offset})
		if err != nil {
			t.Fatalf("read at offset %d: %v", offset, err)
		}
		got = append(got, stripLineNumbers(t, out.Content)...)
		if !out.Truncated {
			break
		}
		if out.NextOffset <= offset {
			t.Fatalf("NextOffset %d did not advance past %d", out.NextOffset, offset)
		}
		offset = out.NextOffset
	}

	if len(got) != total {
		t.Fatalf("reassembled %d lines, want %d", len(got), total)
	}
	for i, line := range got {
		want := fmt.Sprintf("line %d", i+1)
		if line != want {
			t.Fatalf("line %d = %q, want %q", i+1, line, want)
		}
	}
}

// TestRead_LimitOnTheLastLineIsNotTruncation guards the off-by-one that
// sends the model after a page that does not exist.
func TestRead_LimitOnTheLastLineIsNotTruncation(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "exact.txt", "a\nb\nc\n")

	out, err := readHandler(sb, ReadInput{FilePath: path, Limit: 3})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.Truncated {
		t.Error("a limit that lands exactly on the last line is not truncation")
	}
	if out.NextOffset != 0 {
		t.Errorf("NextOffset = %d, want 0 at end of file", out.NextOffset)
	}
}

// TestRead_ClampsLongLines is the third ceiling: a file that fits the line
// window can still be enormous if one line is minified.
func TestRead_ClampsLongLines(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	huge := strings.Repeat("x", 5_000_000)
	path := writeFile(t, dir, "minified.js", "short line\n"+huge+"\nlast\n")

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if len(out.Content) > readByteBudget {
		t.Errorf("content is %d bytes, over the %d budget", len(out.Content), readByteBudget)
	}
	if !strings.Contains(out.Content, "more characters on this line, clipped") {
		t.Error("the clipped line is not marked inline")
	}
	if !strings.Contains(out.Note, "were clipped") {
		t.Errorf("Note does not mention clipping: %q", out.Note)
	}
	if out.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", out.TotalLines)
	}
	// The clamp must not swallow the lines around it.
	if !strings.Contains(out.Content, "short line") || !strings.Contains(out.Content, "last") {
		t.Error("clamping a long line dropped its neighbors")
	}
}

func TestRead_EmptyFileSaysSo(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "empty.txt", "")

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if out.TotalLines != 0 {
		t.Errorf("TotalLines = %d, want 0", out.TotalLines)
	}
	if !strings.Contains(out.Note, "is empty") {
		t.Errorf("an empty file returned no explanation: Note=%q", out.Note)
	}
}

func TestRead_StripsBOMAndCRLF(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := writeFile(t, dir, "windows.txt", "\xef\xbb\xbfalpha\r\nbeta\r\n")

	out, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lines := stripLineNumbers(t, out.Content)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %q", len(lines), out.Content)
	}
	if lines[0] != "alpha" {
		t.Errorf("line 1 = %q, want %q (BOM should be stripped)", lines[0], "alpha")
	}
	if strings.Contains(out.Content, "\r") {
		t.Error("CR survived into the numbered output; exact-match editing would break")
	}
}

func TestRead_DirectoryIsAnError(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	_, err := readHandler(sb, ReadInput{FilePath: dir})
	if err == nil {
		t.Fatal("reading a directory should fail")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("error %q does not say it is a directory", err)
	}
}

// TestRead_ByteBudgetResumesOnTheCutLine checks the boundary rule: the resume
// offset points at the line that did not fit, not the one after it, so nothing
// is skipped between windows.
func TestRead_ByteBudgetResumesOnTheCutLine(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	// Wide lines, few enough to clear the line window, heavy enough to blow the
	// byte budget well before it.
	wide := strings.Repeat("y", 1500)
	var b strings.Builder
	for i := 1; i <= 400; i++ {
		fmt.Fprintf(&b, "%s %d\n", wide, i)
	}
	path := writeFile(t, dir, "wide.txt", b.String())

	first, err := readHandler(sb, ReadInput{FilePath: path})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !first.Truncated {
		t.Fatal("expected the byte budget to stop this read")
	}
	if len(first.Content) > readByteBudget+maxReadLineChars {
		t.Errorf("content is %d bytes, well over the %d budget", len(first.Content), readByteBudget)
	}

	firstLines := stripLineNumbers(t, first.Content)
	second, err := readHandler(sb, ReadInput{FilePath: path, Offset: first.NextOffset})
	if err != nil {
		t.Fatalf("resume read: %v", err)
	}
	secondLines := stripLineNumbers(t, second.Content)

	// The first line of the resumed window is the one the budget rejected, so
	// it must directly follow the last line that was kept.
	lastKept := firstLines[len(firstLines)-1]
	firstResumed := secondLines[0]
	wantNext := fmt.Sprintf("%s %d", wide, len(firstLines)+1)
	if firstResumed != wantNext {
		t.Errorf("resume skipped or repeated a line:\n  last kept:    ...%s\n  first resumed:...%s",
			lastKept[len(lastKept)-8:], firstResumed[max(0, len(firstResumed)-8):])
	}
}
