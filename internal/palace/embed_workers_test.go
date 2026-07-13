package palace

import (
	"os"
	"path/filepath"
	"testing"
)

// With no model there is nothing to parallelise: spinning up a worker pool would
// allocate one model instance per worker for zero work.
func TestEmbedWorkersWithoutModel(t *testing.T) {
	t.Parallel()

	if got := embedWorkers(""); got != 1 {
		t.Errorf("embedWorkers(\"\") = %d, want 1", got)
	}
}

// Each worker holds its own model instance, so the pool is bounded by what the
// compiled-in backend allows — never zero, never more than maxEmbedSessions.
func TestEmbedWorkersIsBounded(t *testing.T) {
	t.Parallel()

	got := embedWorkers("/some/model/path")
	if got < 1 {
		t.Errorf("embedWorkers() = %d, want at least 1", got)
	}
	if got > maxEmbedSessions {
		t.Errorf("embedWorkers() = %d, want at most maxEmbedSessions (%d)", got, maxEmbedSessions)
	}
}

// ModelReady checks for the weights file this backend actually needs, not merely
// for the directory: an install predating the fp32 switch has the directory but
// the wrong (slower) weights, and must be repaired rather than used forever.
func TestModelReady(t *testing.T) {
	t.Parallel()

	t.Run("empty path", func(t *testing.T) {
		t.Parallel()
		if ModelReady("") {
			t.Error("ModelReady(\"\") = true, want false")
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		if ModelReady(filepath.Join(t.TempDir(), "absent")) {
			t.Error("ModelReady() = true for a missing directory, want false")
		}
	})

	t.Run("directory exists but weights do not", func(t *testing.T) {
		t.Parallel()
		if ModelReady(t.TempDir()) {
			t.Error("ModelReady() = true for a directory with no weights, want false")
		}
	})

	t.Run("weights present but empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, OnnxModelFile()), nil, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if ModelReady(dir) {
			t.Error("ModelReady() = true for zero-byte weights, want false")
		}
	})

	t.Run("weights present", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, OnnxModelFile()), []byte("weights"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if !ModelReady(dir) {
			t.Error("ModelReady() = false with the backend's weights in place, want true")
		}
	})

	// A directory where the weights file should be is not a usable model.
	t.Run("weights path is a directory", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, OnnxModelFile()), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if ModelReady(dir) {
			t.Error("ModelReady() = true when the weights path is a directory, want false")
		}
	})
}

func TestEmbedderBackendIsNamed(t *testing.T) {
	t.Parallel()

	if EmbedderBackend() == "" {
		t.Error("EmbedderBackend() is empty; the compiled-in backend must name itself")
	}
	if OnnxModelFile() == "" {
		t.Error("OnnxModelFile() is empty")
	}
}
