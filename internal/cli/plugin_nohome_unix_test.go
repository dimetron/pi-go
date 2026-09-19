//go:build unix

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// With no home directory resolvable, every plugin subcommand reports the failure
// up front rather than operating on a bogus path.
//
// This is a unix-only test: os.UserHomeDir reads $HOME here, which can be
// emptied. On Windows it reads %USERPROFILE%, and clearing the environment
// variable does not make it fail, so the path is unreachable there.
func TestPluginCmd_WithoutHome(t *testing.T) {
	t.Setenv("PI_GO_HOME", "")
	t.Setenv("HOME", "")

	if _, err := pluginHome(); err == nil {
		t.Fatal("expected pluginHome to fail with no home directory")
	}

	cases := [][]string{
		{"marketplace", "add", "obra/superpowers-marketplace"},
		{"marketplace", "list"},
		{"install", "x"},
		{"list"},
		{"uninstall", "x"},
		{"update"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := newPluginCmd()
			cmd.SetArgs(args)
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetContext(context.Background())
			if err := cmd.Execute(); err == nil {
				t.Errorf("expected an error with no home directory for %v", args)
			}
		})
	}
}
