//go:build !windows

package refs

// rootedButNotLocal reports whether a path that filepath.IsAbs does not call
// absolute still escapes the sandbox. On Unix a cleaned path that is neither
// absolute nor ".."-traversing is always local, so there is nothing to add to
// the checks isPathTraversal already made; see validator_windows.go.
func rootedButNotLocal(string, string) bool {
	return false
}
