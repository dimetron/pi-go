//go:build !windows

package lsp

import "path/filepath"

// URIPath converts an absolute OS path into the path component of a file URI.
// On Unix an absolute path already starts with a slash, so only the separator
// form can differ; the drive-letter handling lives in uri_windows.go.
func URIPath(abs string) string {
	return filepath.ToSlash(abs)
}

// PathFromURIPath converts the path component of a file URI back into an OS
// path. There is no drive-letter slash to strip on Unix; see uri_windows.go.
func PathFromURIPath(p string) string {
	return filepath.FromSlash(p)
}
