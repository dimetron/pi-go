package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// updateRunGolden regenerates the run.go golden files. Run with:
//
//	go test ./internal/tui/ -run 'Golden' -update-run-golden
var updateRunGolden = flag.Bool("update-run-golden", false,
	"rewrite the run.go golden files from the current implementation")

// assertRunGolden compares got against testdata/<group>/<name>.golden, or
// rewrites it under -update-run-golden. name is a test case name, so it is
// slugified into something safe to put on disk.
func assertRunGolden(t *testing.T, group, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", group, goldenSlug(name)+".golden")

	if *updateRunGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // fixed test-local path
	if err != nil {
		t.Fatalf("read golden: %v (rerun with -update-run-golden)", err)
	}
	if got != string(want) {
		t.Errorf("output drifted from %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}

// goldenSlug turns a test case name into a filename: lower case, with every
// run of non-alphanumeric characters collapsed to a single underscore.
func goldenSlug(name string) string {
	var b strings.Builder
	lastUnderscore := true // trims any leading separator
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastUnderscore = false
		case !lastUnderscore:
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.TrimSuffix(b.String(), "_")
}
