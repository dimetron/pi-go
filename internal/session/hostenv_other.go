//go:build !darwin && !linux

package session

// memoryStats is unimplemented off Darwin and Linux. Zero means "not reported",
// which the omitempty JSON tags drop from meta.json entirely — better than
// recording a made-up number that a future reader would trust.
func memoryStats() (total, available uint64) {
	return 0, 0
}
