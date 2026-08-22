package palace

import (
	"os"
	"path/filepath"
	"testing"
)

// detectRoom matches a directory-style pattern ("internal/tools/") against the
// file's directory prefix, not just the full path, and does so with slash
// semantics on every OS.
func TestDetectRoom_DirectoryPatternMatchesFilesBelowIt(t *testing.T) {
	rooms := []RoomDef{
		{Name: "tooling", Patterns: []string{"internal/tools/"}},
		{Name: "ui", Patterns: []string{"internal/tui/*.go"}},
	}

	cases := []struct {
		path string
		want string
	}{
		{"internal/tools/find.go", "tooling"},                      // dir-prefix branch
		{filepath.Join("internal", "tools", "grep.go"), "tooling"}, // OS-native separators
		{"internal/tui/run.go", "ui"},                              // full-path glob
		{"internal/tui/refs/validator.go", "internal"},             // "*" must not cross "/"; no component names a room, so first component
		{"src/ui/app.go", "ui"},                                    // a component named like a room
		{"cmd/pi/main.go", "cmd"},                                  // first component fallback
	}
	for _, tc := range cases {
		if got := detectRoom(tc.path, rooms); got != tc.want {
			t.Errorf("detectRoom(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// A path-bearing .gitignore pattern is matched against the whole relative
// path, and "*" stops at "/" there: "generated/*.go" covers one level only.
func TestLoadGitignore_PathPatternsMatchWholeRelativePath(t *testing.T) {
	dir := t.TempDir()
	ignore := "# build output\n\ngenerated/*.go\n*.tmp\ncache/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(ignore), 0o644); err != nil {
		t.Fatal(err)
	}

	patterns := loadGitignore(dir)
	if len(patterns) != 3 {
		t.Fatalf("loadGitignore = %v, want 3 patterns with the comment and blank line dropped", patterns)
	}

	cases := []struct {
		path string
		want bool
	}{
		{"generated/a.go", true},                   // full-path match
		{filepath.Join("generated", "b.go"), true}, // OS-native separators
		{"generated/deep/a.go", false},             // "*" does not cross "/"
		{"generated/a.txt", false},                 // wrong suffix
		{"src/x.tmp", true},                        // component match at any depth
		{"cache/obj.o", true},                      // trailing "/" stripped, dir component matches
		{"src/main.go", false},                     // nothing applies
	}
	for _, tc := range cases {
		if got := isGitignored(tc.path, patterns); got != tc.want {
			t.Errorf("isGitignored(%q, %v) = %v, want %v", tc.path, patterns, got, tc.want)
		}
	}
}
