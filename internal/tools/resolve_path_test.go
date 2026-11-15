package tools

import (
	"io/fs"
	"testing"
)

// Resolve must return an fs.ValidPath, not the caller's spelling.
//
// find and grep pass Resolve's result straight to fs.WalkDir on the Root's FS,
// which requires fs.ValidPath. A "./sub" the caller wrote is a valid request but
// an invalid fs.FS path, and fs rejects it with "invalid argument". Both walk
// sites discard the walk error, so before Resolve cleaned its result these
// searches returned zero results and a nil error — silently wrong, not failing.
func TestSandbox_Resolve_ReturnsValidFSPath(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	if err := sb.WriteFile("sub/a.go", []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, in := range []string{".", "./", "./sub", "sub/", "sub/./a.go", "sub//a.go"} {
		got, err := sb.Resolve(in)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", in, err)
		}
		if !fs.ValidPath(got) {
			t.Errorf("Resolve(%q) = %q, which is not an fs.ValidPath; find/grep cannot walk it", in, got)
		}
		if _, statErr := fs.Stat(sb.FS(), got); statErr != nil {
			t.Errorf("fs.Stat(FS(), %q) = %v; Resolve(%q) produced a path fs cannot use", got, statErr, in)
		}
	}
}

// The escaping cases must still be rejected after cleaning.
func TestSandbox_Resolve_CleaningDoesNotAllowEscape(t *testing.T) {
	sb := testSandbox(t, t.TempDir())

	// Each of these cleans to ".." or a "../" prefix and must fail. The "./"
	// and "sub/../" prefixes must not talk the check out of rejecting it.
	for _, in := range []string{"..", "../", "./../x", "sub/../../x", "sub/../../../etc/passwd"} {
		if _, err := sb.Resolve(in); err == nil {
			t.Errorf("Resolve(%q) = nil error, want escape rejection", in)
		}
	}
}

// End-to-end: a "./"-prefixed search path must find what the bare one finds.
func TestFind_Grep_DotSlashPaths(t *testing.T) {
	sb := testSandbox(t, t.TempDir())
	if err := sb.WriteFile("sub/a.go", []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{".", "./", "sub", "./sub"} {
		out, err := findHandler(sb, FindInput{Path: path, Pattern: "*.go"})
		if err != nil {
			t.Fatalf("find path=%q: %v", path, err)
		}
		if len(out.Files) != 1 {
			t.Errorf("find path=%q = %v, want one file", path, out.Files)
		}

		grep, err := grepHandler(sb, GrepInput{Path: path, Pattern: "package"})
		if err != nil {
			t.Fatalf("grep path=%q: %v", path, err)
		}
		if grep.TotalMatches != 1 {
			t.Errorf("grep path=%q matches = %d, want 1", path, grep.TotalMatches)
		}
	}
}
