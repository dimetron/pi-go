package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain redirects HOME to a throwaway directory for every test in this
// package.
//
// Code under test resolves paths through os.UserHomeDir and writes there —
// ~/.pi-go/config.json, session history, logs. Tests that exercise those paths
// without isolating HOME first will overwrite the developer's real
// configuration: running "go test ./..." was enough to rewrite the default
// model role and theme of the machine running it. Isolating at package scope
// makes that impossible by construction, rather than relying on every future
// test remembering to call t.Setenv("HOME", ...).
//
// Tests that need their own HOME can still override it; t.Setenv restores this
// value rather than the developer's when they finish.
// testBrowserStubEnv switches this binary into stand-in-browser mode. A test
// that needs $BROWSER to point at something runnable sets it and points BROWSER
// at os.Args[0]: the child exits here, before m.Run, so no tests are re-run and
// no real browser is launched. A literal path like /usr/bin/true does not
// travel -- on Windows it fails exec.LookPath, and Open falls through to
// "cmd /c start", which opens the runner's actual browser.
const testBrowserStubEnv = "PI_TEST_BROWSER_STUB"

func TestMain(m *testing.M) {
	if os.Getenv(testBrowserStubEnv) != "" {
		os.Exit(0)
	}

	dir, err := os.MkdirTemp("", "pi-go-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolating HOME: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows

	// lastSessionFile is a package-level var resolved from $HOME at init time,
	// which happens before TestMain runs — setting HOME above cannot reach it.
	// Repoint it explicitly so print-mode tests that do not already override it
	// cannot write to the developer's real ~/.pi-go.
	lastSessionFile = filepath.Join(dir, ".pi-go", "last-session.json")

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
