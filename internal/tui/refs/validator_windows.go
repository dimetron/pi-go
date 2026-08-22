//go:build windows

package refs

import "path/filepath"

// rootedButNotLocal reports whether a path that filepath.IsAbs does not call
// absolute still escapes the sandbox. On Windows "/etc/passwd" is
// drive-relative rather than absolute, so the IsAbs check lets it through;
// filepath.IsLocal rejects it, along with drive-relative "C:x" and the
// reserved device names.
func rootedButNotLocal(path, cleaned string) bool {
	return !filepath.IsAbs(path) && !filepath.IsLocal(cleaned)
}
