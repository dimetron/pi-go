package palace

import (
	"encoding/binary"
	"math"
)

// MarshalEmbedding encodes a float32 slice as a little-endian byte slice for BLOB storage.
func MarshalEmbedding(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// UnmarshalEmbedding decodes a little-endian byte slice back into a float32 slice.
func UnmarshalEmbedding(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	n := len(b) / 4
	v := make([]float32, n)
	for i := range n {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
