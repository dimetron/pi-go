//go:build !unix && !windows

package session

// diskStats is unimplemented outside Unix and Windows. Zero means "not
// reported" and is dropped from meta.json by the omitempty tags.
func diskStats(string) (total, free uint64) {
	return 0, 0
}
