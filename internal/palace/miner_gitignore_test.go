package palace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// helper: initialize a temporary git repository at dir with the given
// .gitignore contents and the named files (empty). Returns a cleanup.
func initGitRepo(t *testing.T, dir, gitignore string, files ...string) {
	t.Helper()
	git := gitLookPath(t)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	// git init needs an identity for some operations; configure locally only.
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if gitignore != "" {
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignore), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range files {
		if strings.HasSuffix(f, "/") {
			if err := os.MkdirAll(filepath.Join(dir, strings.TrimSuffix(f, "/")), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("content\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func gitLookPath(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary not available:", err)
	}
	return git
}

// TestGitIgnoredSet_FromRepositoryRoot: exact file, directory, glob, and
// negated entries are handled by git's own ignore machinery.
func TestGitIgnoredSet_FromRepositoryRoot(t *testing.T) {
	gitLookPath(t)
	dir := t.TempDir()
	initGitRepo(t, dir,
		"*.log\n/dist\n/tmp/\n!important.log\n",
		"app.log", "dist/bundle.js", "tmp/pi/cache.db", "important.log", "src/main.go",
	)

	ignored := GitIgnoredSet(dir)
	if ignored == nil {
		t.Fatal("GitIgnoredSet returned nil for a git repository")
	}

	// *.log → app.log ignored.
	if !ignored["app.log"] {
		t.Errorf("app.log should be ignored")
	}
	// Negated important.log is NOT ignored.
	if ignored["important.log"] {
		t.Errorf("important.log should not be ignored (negated)")
	}
	// /dist/ → entire directory ignored.
	if !ignored["dist/bundle.js"] && !ignored["dist/"] {
		t.Errorf("dist/bundle.js should be under an ignored directory, got set=%v", ignored)
	}
	// /tmp/ → ignored.
	if !ignored["tmp/pi/cache.db"] && !ignored["tmp/pi/"] && !ignored["tmp/"] {
		t.Errorf("tmp/pi/cache.db should be ignored")
	}
	// Ordinary tracked file not in the set.
	if ignored["src/main.go"] {
		t.Errorf("src/main.go should not be ignored")
	}
}

// TestGitIgnoredSet_FromNestedDirectory: paths come back relative to the
// mining directory, not the repo root.
func TestGitIgnoredSet_FromNestedDirectory(t *testing.T) {
	gitLookPath(t)
	dir := t.TempDir()
	initGitRepo(t, dir, "sub/**/generated/\n", "sub/one/generated/x.go", "sub/one/normal.go")

	nested := filepath.Join(dir, "sub", "one")
	ignored := GitIgnoredSet(nested)
	if ignored == nil {
		t.Fatal("GitIgnoredSet returned nil for a nested repo directory")
	}
	if !ignored["generated/x.go"] && !ignored["generated/"] {
		t.Errorf("expected generated/x.go relative to nested dir, got set=%v", ignored)
	}
	if ignored["normal.txt"] {
		t.Errorf("normal.txt should not be ignored")
	}
	// No path may escape the mining directory.
	for key := range ignored {
		if strings.HasPrefix(key, "..") || filepath.IsAbs(key) {
			t.Errorf("ignored key escapes the mining dir: %q", key)
		}
	}
}

// TestGitIgnoredSet_NonRepository: a plain temp dir yields nil, not an error.
func TestGitIgnoredSet_NonRepository(t *testing.T) {
	gitLookPath(t)
	if got := GitIgnoredSet(t.TempDir()); got != nil {
		t.Errorf("GitIgnoredSet on a non-repo = %v, want nil", got)
	}
}

// TestIsGitIgnoredSet_ExactAndAncestor covers the ancestor-walk that finds
// files inside a collapsed ignored directory.
func TestIsGitIgnoredSet_ExactAndAncestor(t *testing.T) {
	ignored := map[string]bool{
		"tmp/pi": true,
		"dist":   true,
	}

	tests := []struct {
		path string
		want bool
	}{
		{"", false},
		{"src/main.go", false},
		{"tmp/pi/cache.db", true},    // exact ancestor
		{"dist/bundle.js", true},     // ancestor dir in set
		{"tmp/piano/note.go", false}, // similar prefix must not match
		{"tmp/pi", true},             // exact
		{"dist", true},               // exact
	}
	for _, tt := range tests {
		if got := IsGitIgnoredSet(tt.path, ignored); got != tt.want {
			t.Errorf("IsGitIgnoredSet(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}

	// nil and empty sets are always false.
	if IsGitIgnoredSet("tmp/pi/cache.db", nil) {
		t.Error("nil ignored set should return false")
	}
	if IsGitIgnoredSet("tmp/pi/cache.db", map[string]bool{}) {
		t.Error("empty ignored set should return false")
	}
}
