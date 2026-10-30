package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The three properties asserted here come from logged agent sessions, where the
// not-found error was the reason an edit loop could not self-correct: the model
// repeated 10 near-identical failures before the loop guard stopped the turn.
const (
	// editErrEscalation is the shape a model produced after being told its
	// old_string was wrong: it doubled the escaping of an already-literal \n.
	editErrEscalation = `    stage('E2E Test') {\n        steps {\n            node('x') {`
)

func TestBuildEditNotFoundError_DoesNotEscapeTheOldString(t *testing.T) {
	// The echo must not double backslashes. %q turned a literal \n into \\n,
	// which the model then read as "escape it more" and resent as \\\n.
	content := "line one\nstage('E2E Test') {\n    steps {\n"
	err := buildEditNotFoundError(EditInput{OldString: editErrEscalation}, content)
	msg := err.Error()

	if strings.Contains(msg, `\\n`) {
		t.Errorf("error doubles the backslash in old_string, which drives an escalation loop:\n%s", msg)
	}
	if !strings.Contains(msg, `\n`) {
		t.Errorf("error should echo the old_string as sent, with single backslashes:\n%s", msg)
	}
}

func TestBuildEditNotFoundError_QuotesAWindowForMultiLineMisses(t *testing.T) {
	// The suggestion test used to compare one line's length against the whole
	// old_string, so a multi-line target could never produce a suggestion.
	// 39 of 70 logged not-found errors offered none at all.
	content := "package x\n\nfunc Providers() []Provider {\n\treturn []Provider{\n\t\t{Name: \"agentgateway\"},\n\t}\n}\n"
	oldString := "func Providers() []Provider {\n\t\t\t{\n\t\t\t\tName: \"agentgateway\","
	err := buildEditNotFoundError(EditInput{OldString: oldString}, content)
	msg := err.Error()

	if !strings.Contains(msg, "FILE_WINDOW") {
		t.Fatalf("no file window quoted for a multi-line miss:\n%s", msg)
	}
	if !strings.Contains(msg, "Closest match in the file") {
		t.Errorf("window is quoted but not located:\n%s", msg)
	}
	if !strings.Contains(msg, "Provider") {
		t.Errorf("quoted window does not contain the near-matching text:\n%s", msg)
	}
}

func TestBuildEditNotFoundError_NamesWhitespaceOnlyDrift(t *testing.T) {
	// A caller one tab away from a match should be told that, not sent back to
	// re-read a file it already transcribed correctly.
	content := "func f() {\n\tx := 1\n\ty := 2\n}\n"
	oldString := "    x := 1\n    y := 2" // spaces where the file has tabs
	err := buildEditNotFoundError(EditInput{OldString: oldString}, content)

	if !strings.Contains(err.Error(), "only in whitespace") {
		t.Errorf("whitespace-only drift should be diagnosed as such:\n%s", err)
	}
}

func TestBuildEditNotFoundError_IsBounded(t *testing.T) {
	// The error is fed back into the prompt on every retry, so it must stay
	// small: quoting the whole file would make each failure grow the context.
	content := strings.Repeat("a line of source code with some length to it\n", 2000)
	err := buildEditNotFoundError(EditInput{OldString: "completely absent text"}, content)

	if n := len(err.Error()); n > 4000 {
		t.Errorf("not-found error is %d bytes for a 2000-line file; it must stay bounded", n)
	}
}

func TestStringsSimilarity_IsBoundedToUnitInterval(t *testing.T) {
	// The old implementation counted duplicates in the intersection and
	// measured the union in bytes, so it returned values above 1.
	cases := [][2]string{
		{"abc", "abc"},
		{"aaaa", "aa"},
		{"a\nb\nc", "a\nb\nc"},
		{strings.Repeat("x", 100), strings.Repeat("x", 100)},
		{"hello", "world"},
	}
	for _, c := range cases {
		got := stringsSimilarity(c[0], c[1])
		if got < 0 || got > 1 {
			t.Errorf("stringsSimilarity(%q, %q) = %v, want 0..1", c[0], c[1], got)
		}
	}
	if got := stringsSimilarity("abc", "abc"); got != 1 {
		t.Errorf("identical strings scored %v, want 1", got)
	}
	if got := stringsSimilarity("abc", "xyz"); got != 0 {
		t.Errorf("disjoint strings scored %v, want 0", got)
	}
}

func TestEditHandler_MissingOldStringPointsAtWriteTool(t *testing.T) {
	// 20 of 126 logged edit failures sent only file_path and new_string: the
	// model reached for whole-file write semantics through the edit tool.
	// Aliasing cannot synthesize a missing field, so the error must redirect.
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := editHandler(sb, EditInput{FilePath: path, NewString: "replacement\n"})
	if err == nil {
		t.Fatal("expected an error when old_string is omitted")
	}
	if !strings.Contains(err.Error(), "write tool") {
		t.Errorf("error should point at the write tool for a whole-file replacement:\n%s", err)
	}
}

func TestEditHandler_IdenticalStringsExplainsWhy(t *testing.T) {
	dir := t.TempDir()
	sb := testSandbox(t, dir)
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("same\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := editHandler(sb, EditInput{FilePath: path, OldString: "same", NewString: "same"})
	if err == nil {
		t.Fatal("expected an error when old_string equals new_string")
	}
	if !strings.Contains(err.Error(), "unchanged") {
		t.Errorf("error should say the file would be unchanged:\n%s", err)
	}
}
