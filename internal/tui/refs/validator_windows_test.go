//go:build windows

package refs

import (
	"path/filepath"
	"testing"
)

func TestRootedButNotLocal(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{`/etc/passwd`, true},  // drive-relative rooted path: not absolute, not local
		{`\Windows\x`, true},   // same with the native separator
		{`C:x.txt`, true},      // drive-relative without a root
		{`CON`, true},          // reserved device name
		{`a\b\c.go`, false},    // ordinary relative path
		{`C:\abs\x.go`, false}, // absolute: handled by the IsAbs check, not here
	}
	for _, tc := range cases {
		if got := rootedButNotLocal(tc.path, filepath.Clean(tc.path)); got != tc.want {
			t.Errorf("rootedButNotLocal(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
