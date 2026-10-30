package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The defect these tests pin: an unquoted heredoc whose body bash tries to
// expand aborts part-way, the redirect has already created the target file, and
// bash exits 0. The caller sees success and an empty file.
//
// Reproduced from session 261030-1206-80357-8852b, where a model wrote a Python
// patcher this way and then acted on the truncated file.
const heredocCorruptingCommand = `cat > out.py <<EOF
import re
x = "${env.BRANCH_SIMPLE_NAME}"
EOF
echo "wrote $(wc -c < out.py) bytes"`

func TestBashHandler_NotesUnquotedHeredocTruncation(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, NewBashSupervisor(), nil, BashInput{
		Command: heredocCorruptingCommand,
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}

	// The precondition: bash really does report success on this corruption.
	// If that ever stops being true the note is unnecessary and this test should
	// be deleted rather than kept green.
	if out.ExitCode != 0 {
		t.Fatalf("expected exit 0 from the corrupting command, got %d (stderr: %s)", out.ExitCode, out.Stderr)
	}
	written, readErr := os.ReadFile(filepath.Join(dir, "out.py"))
	if readErr != nil {
		t.Fatalf("reading out.py: %v", readErr)
	}
	if len(written) != 0 {
		t.Fatalf("expected the heredoc to truncate out.py to 0 bytes, got %d", len(written))
	}

	if !strings.Contains(out.Note, "unquoted") {
		t.Errorf("note should name the unquoted delimiter, got %q", out.Note)
	}
	if !strings.Contains(out.Note, "empty or truncated") {
		t.Errorf("note should warn the redirected file may be empty, got %q", out.Note)
	}
	if !strings.Contains(out.Note, "<<'EOF'") {
		t.Errorf("note should show the quoted-delimiter fix, got %q", out.Note)
	}
}

func TestBashHandler_NoNoteWhenDelimiterIsQuoted(t *testing.T) {
	// The same body with a quoted delimiter is written literally and must not
	// carry a warning — this is the fix the note recommends.
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, NewBashSupervisor(), nil, BashInput{
		Command: `cat > out.py <<'EOF'
import re
x = "${env.BRANCH_SIMPLE_NAME}"
EOF
echo done`,
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	written, _ := os.ReadFile(filepath.Join(dir, "out.py"))
	if !strings.Contains(string(written), "${env.BRANCH_SIMPLE_NAME}") {
		t.Fatalf("quoted heredoc should write the body literally, got %q", string(written))
	}
	if strings.Contains(out.Note, "unquoted") {
		t.Errorf("quoted delimiter must not produce a heredoc note, got %q", out.Note)
	}
}

func TestBashHandler_NoNoteForOrdinaryHeredoc(t *testing.T) {
	// A body with nothing to expand cannot fail this way, so the common
	// `cat > x <<EOF` must stay note-free.
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, NewBashSupervisor(), nil, BashInput{
		Command: "cat > notes.md <<EOF\nplain text\nEOF\ncat notes.md",
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if out.Note != "" {
		t.Errorf("ordinary heredoc should carry no note, got %q", out.Note)
	}
}

func TestBashHandler_NoNoteForUnrelatedBadSubstitution(t *testing.T) {
	// `bad substitution` outside a heredoc is an ordinary error that bash itself
	// reports; the note is only for the silent, exit-0 heredoc case.
	dir := t.TempDir()
	sb := testSandbox(t, dir)

	out, err := bashHandler(sb, NewBashSupervisor(), nil, BashInput{
		Command: `echo "${env.NOPE}"`,
	})
	if err != nil {
		t.Fatalf("bashHandler: %v", err)
	}
	if strings.Contains(out.Note, "heredoc") {
		t.Errorf("non-heredoc bad substitution must not produce a heredoc note, got %q", out.Note)
	}
}

func TestFindUnquotedHeredoc(t *testing.T) {
	cases := []struct {
		name, command string
		wantOK        bool
		wantDelim     string
	}{
		{"unquoted with expansion", "cat > x <<EOF\nx = ${Y}\nEOF", true, "EOF"},
		{"unquoted without expansion", "cat > x <<EOF\nplain\nEOF", false, ""},
		{"single-quoted delimiter", "cat > x <<'EOF'\nx = ${Y}\nEOF", false, ""},
		{"double-quoted delimiter", "cat > x <<\"EOF\"\nx = ${Y}\nEOF", false, ""},
		{"dash form", "cat <<-EOF\n\tx = ${Y}\n\tEOF", true, "EOF"},
		{"backtick body", "cat > x <<EOF\necho `date`\nEOF", true, "EOF"},
		{"custom delimiter", "cat > x <<PYEOF\nx = ${Y}\nPYEOF", true, "PYEOF"},
		{"no heredoc", "echo hello", false, ""},
		{"expanded body only after the delimiter ends", "cat > x <<EOF\nplain\nEOF\necho ${Y}", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			delim, _, ok := findUnquotedHeredoc(c.command)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if ok && delim != c.wantDelim {
				t.Errorf("delimiter = %q, want %q", delim, c.wantDelim)
			}
		})
	}
}

func TestHeredocExpansionBody_SpansMultipleLines(t *testing.T) {
	// bash echoes the whole body across several lines before the diagnostic.
	// A single-line pattern (no (?s)) would never match this and the note would
	// silently never fire.
	stderr := "bash: line 1: import re\nx = \"${env.Y}\"\n: bad substitution\n"
	got, ok := heredocExpansionBody(stderr)
	if !ok {
		t.Fatal("expected the body to be recognized")
	}
	if !strings.Contains(got, "import re") {
		t.Errorf("body should include the echoed first line, got %q", got)
	}
	if !strings.Contains(got, "${env.Y}") {
		t.Errorf("body should include the rejected substitution, got %q", got)
	}
	if strings.Contains(got, "bad substitution") {
		t.Errorf("body should not include the diagnostic itself, got %q", got)
	}
}

func TestHeredocExpansionBody_Absent(t *testing.T) {
	if _, ok := heredocExpansionBody("bash: command not found\n"); ok {
		t.Error("expected no body in an unrelated diagnostic")
	}
}

func TestAppendNote(t *testing.T) {
	cases := []struct{ existing, add, want string }{
		{"", "", ""},
		{"a", "", "a"},
		{"", "b", "b"},
		{"a", "b", "a b"},
	}
	for _, c := range cases {
		if got := appendNote(c.existing, c.add); got != c.want {
			t.Errorf("appendNote(%q, %q) = %q, want %q", c.existing, c.add, got, c.want)
		}
	}
}

func TestHeredocNote_EmptyEchoStillExplains(t *testing.T) {
	// bash does not always echo the offending body — some versions emit only
	// the diagnostic. The note must still explain the truncation instead of
	// rendering an empty quote.
	command := "cat > out.py <<EOF\nx = ${env.Y}\nEOF"
	out := BashOutput{ExitCode: 0, Stderr: ": bad substitution\n"}
	note := heredocNote(command, out)

	if !strings.Contains(note, "a heredoc body was expanded") {
		t.Errorf("empty echo should fall back to a generic description, got %q", note)
	}
	if strings.Contains(note, `""`) {
		t.Errorf("note should not quote an empty string, got %q", note)
	}
}

func TestHeredocNote_SkipsWhenNoUnquotedHeredoc(t *testing.T) {
	// A 'bad substitution' with no expandable unquoted heredoc in the command is
	// an ordinary bash error, not a truncation, and must not be annotated.
	out := BashOutput{ExitCode: 0, Stderr: ": bad substitution\n"}
	if note := heredocNote("echo hello", out); note != "" {
		t.Errorf("expected no note without an unquoted heredoc, got %q", note)
	}
	if note := heredocNote("cat > x <<EOF\nplain body\nEOF", out); note != "" {
		t.Errorf("expected no note when the body cannot expand, got %q", note)
	}
}

func TestFirstLine_TruncatesLongText(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := firstLine(long)
	if len(got) > 80+len("...") {
		t.Errorf("firstLine should bound the output, got %d chars", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated text should be marked, got %q", got)
	}
	if short := firstLine("abc"); short != "abc" {
		t.Errorf("short text should pass through, got %q", short)
	}
	if multi := firstLine("first\nsecond"); multi != "first" {
		t.Errorf("should keep only the first line, got %q", multi)
	}
}
