package tools

import (
	"io/fs"
	"path/filepath"
	"testing"
)

// Resolve must return a path a walking consumer can use.
//
// Resolve hands back an OS path; find and grep convert it with filepath.ToSlash
// before handing it to fs.WalkDir, which requires fs.ValidPath. This asserts
// that conversion produces a path fs accepts, for every spelling of the same
// request. Testing the raw return instead would fail on Windows, where
// filepath.Clean rewrites "sub/a.go" to `sub\a.go` and fs would reject it — a
// Windows-only failure that only CI catches.
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
		// The same conversion find and grep apply before walking.
		asFS := filepath.ToSlash(got)
		if !fs.ValidPath(asFS) {
			t.Errorf("Resolve(%q) = %q, ToSlash = %q which is not an fs.ValidPath; find/grep cannot walk it", in, got, asFS)
		}
		if _, statErr := fs.Stat(sb.FS(), asFS); statErr != nil {
			t.Errorf("fs.Stat(FS(), %q) = %v; Resolve(%q) produced a path fs cannot use", asFS, statErr, in)
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
