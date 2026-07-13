//go:build ORT

package palace

import (
	"context"
	"os"
	"path/filepath"
	"runtime"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/options"
)

// ONNX Runtime backend, with CoreML enabled on Apple Silicon.
//
// This is the Apple GPU path. CoreML is the execution provider that dispatches
// to the Metal GPU and the Neural Engine; there is no direct "Metal" provider in
// ONNX Runtime, and hugot's WithCoreML is ORT-only. (gomlx can in principle load
// Apple's PJRT Metal plugin under -tags XLA, but go-xla ships no installer for
// it and warns that dlopen'ing plugins after start does not work on macOS — so
// CoreML, not PJRT, is the supported route.)
//
// Requires cgo and libonnxruntime:
//
//	brew install onnxruntime
//	make build-accel          # CGO_ENABLED=1 go build -tags ORT
//
// Point PI_ONNXRUNTIME_LIB at the dylib if it is not on the default search path.
// Set PI_NO_COREML=1 to run ORT on the CPU only (useful to isolate whether a
// wrong result is CoreML's doing — its ANE path is lower precision).

const backendName = "ORT + CoreML (Apple GPU / Neural Engine)"

// coreMLDisabled reports whether the user asked for CPU-only ORT.
func coreMLDisabled() bool {
	switch os.Getenv("PI_NO_COREML") {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ortLibraryDirs are the directories libonnxruntime is normally installed into,
// tried in order.
//
// hugot's WithOnnxLibraryPath takes the *directory* and appends the platform's
// library name itself. ORT's built-in default is /usr/local/lib — where Homebrew
// installs on Intel Macs, but not on Apple Silicon — so an otherwise correct
// `brew install onnxruntime` fails with "cannot find the ort library" until
// /opt/homebrew/lib is searched. PI_ONNXRUNTIME_LIB overrides all of it.
var ortLibraryDirs = []string{
	"/opt/homebrew/lib", // Homebrew, Apple Silicon
	"/usr/local/lib",    // Homebrew on Intel; also ORT's own default
	"/usr/lib",          // Linux distro packages
}

// ortLibraryNames are the platform library filenames to look for in those dirs.
var ortLibraryNames = []string{"libonnxruntime.dylib", "libonnxruntime.so"}

// findORTLibraryDir returns the directory holding libonnxruntime, or "" to let
// hugot fall back to its own default.
func findORTLibraryDir() string {
	if dir := os.Getenv("PI_ONNXRUNTIME_LIB"); dir != "" {
		return dir
	}
	for _, dir := range ortLibraryDirs {
		for _, name := range ortLibraryNames {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
				return dir
			}
		}
	}
	return ""
}

func newSession(ctx context.Context) (*hugot.Session, error) {
	var opts []options.WithOption

	if dir := findORTLibraryDir(); dir != "" {
		opts = append(opts, options.WithOnnxLibraryPath(dir))
	}

	// CoreML only exists on Darwin; asking for it elsewhere is an error, not a
	// no-op, so gate on the platform rather than letting ORT reject the request.
	if runtime.GOOS == "darwin" && !coreMLDisabled() {
		// Keep to options ORT actually recognises: it rejects the whole session on
		// an unknown key rather than ignoring it ("Unknown option: ...").
		//
		// MLComputeUnits=ALL lets CoreML place ops on the Neural Engine and GPU
		// and fall back to the CPU for anything it cannot handle, instead of
		// failing. MLProgram is the modern model format; the legacy NeuralNetwork
		// format cannot express parts of a transformer graph.
		opts = append(opts, options.WithCoreML(map[string]string{
			"ModelFormat":    "MLProgram",
			"MLComputeUnits": "ALL",
		}))
	}

	return hugot.NewORTSession(ctx, opts...)
}

// platformOnnxFile is the weights file this backend should use.
//
// int8 on arm64 — the opposite of the pure-Go build's choice, and deliberately
// so. ONNX Runtime has optimized ARM int8 kernels (QNNPACK/XNNPACK) and CoreML's
// ANE is an int8 engine, so quantization is a real win here. On the pure-Go
// backend the same file is a 3x pessimization because simplego has no int8
// kernels at all.
func platformOnnxFile() string {
	if runtime.GOARCH == "arm64" {
		return "onnx/model_qint8_arm64.onnx"
	}
	return "onnx/model.onnx"
}

// maxEmbedSessions is 1: ONNX Runtime permits only one active session per
// process ("another session is currently active, and only one session can be
// active at one time"), so spawning a pool would just log failures and fall back
// to a single worker anyway.
//
// It costs nothing: ORT is internally multi-threaded and CoreML offloads to the
// GPU/ANE, so one session already saturates the hardware — it reached ~24
// chunks/sec against 14.1 for four pure-Go workers.
const maxEmbedSessions = 1
