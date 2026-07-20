package agent

import (
	"fmt"
	"os"
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
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pi-go-test-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "isolating HOME: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("HOME", dir)
	os.Setenv("USERPROFILE", dir) // os.UserHomeDir on Windows

	code := m.Run()

	os.RemoveAll(dir)
	os.Exit(code)
}
