//go:build windows

package lsp

import (
	"path/filepath"
	"strings"
)

// URIPath converts an absolute OS path into the path component of a file URI.
//
// Windows paths start with a drive letter, not a slash. Handing url.URL a Path
// of "C:/x" makes String emit "file://C:/x", where the drive letter is read as
// the authority; the extra leading slash produces the "file:///C:/x" form
// language servers expect.
func URIPath(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// PathFromURIPath converts the path component of a file URI back into an OS
// path, undoing the leading slash URIPath puts in front of a drive letter:
// file:///C:/x is C:\x, not \C:\x.
func PathFromURIPath(p string) string {
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}
