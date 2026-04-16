package main

import (
	"os"
	"testing"

	"github.com/dimetron/pi-go/internal/cli"
)

// TestHelpFlag verifies that cli.Execute handles --help without panicking.
func TestHelpFlag(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Save original args and environment
	origArgs := os.Args
	origEnv := os.Getenv("ANTHROPIC_API_KEY")
	defer func() {
		os.Args = origArgs
		if origEnv == "" {
			_ = os.Unsetenv("ANTHROPIC_API_KEY")
		} else {
			_ = os.Setenv("ANTHROPIC_API_KEY", origEnv)
		}
	}()

	// Provide a dummy API key so we don't fail on auth.
	_ = os.Setenv("ANTHROPIC_API_KEY", "sk-test-dummy")
	os.Args = []string{"pi", "--help"}

	// Execute should return without panic; help flag may return err or exit 0.
	_ = cli.Execute()
}
