//go:build !ORT && !XLA

package palace

import (
	"context"

	"github.com/knights-analytics/hugot"
)

// This is the default build: pure Go, no cgo, no native libraries. `go install`
// works with nothing but a Go toolchain, which is the property that makes the
// other backends opt-in rather than default.
//
// It is also, by a wide margin, the slowest. hugot's Go session runs gomlx's
// simplego backend — a hand-written interpreter with no SIMD or BLAS kernels —
// so a 22M-parameter MiniLM embeds at single-digit chunks/sec. Build with
// `-tags ORT` for Apple GPU / Neural Engine acceleration (see
// embedder_backend_ort.go).

// backendName identifies the compiled-in inference backend, for logs and
// `pi memory model status`.
const backendName = "go (pure Go, CPU only)"

// newSession creates the hugot session for the compiled-in backend.
func newSession(ctx context.Context) (*hugot.Session, error) {
	return hugot.NewGoSession(ctx)
}

// platformOnnxFile is the weights file this backend should use.
//
// fp32, even on arm64. int8 quantization only pays off on a runtime with
// optimized int8 kernels; simplego has none and runs its generic quantized path
// ~3x slower than its float32 one (measured: 1.8 vs 5.6 chunks/sec on an M2 Max).
// The ORT build reverses this choice, which is exactly why it belongs here and
// not in a shared constant.
func platformOnnxFile() string {
	return "onnx/model.onnx"
}

// maxEmbedSessions is how many embedders may run concurrently.
//
// gomlx's intra-op parallelism tops out around 25% CPU, so several independent
// embedders are what actually saturates the machine. Measured on an M2 Max
// (fp32, batch 8): 1 worker 7.1 chunks/sec, 2 -> 11.4, 4 -> 14.1, 6 -> 10.5
// (contention). Each holds its own copy of the weights, so this trades memory
// for speed.
const maxEmbedSessions = 4
