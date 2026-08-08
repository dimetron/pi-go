//go:build !unix

package session

// diskStats is unimplemented off Unix. Zero means "not reported" and is dropped
// from meta.json by the omitempty tags.
func diskStats(string) (total, free uint64) {
	return 0, 0
}
