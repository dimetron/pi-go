package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func resetCPUProfileForTest(t *testing.T) {
	t.Helper()
	stopCPUProfile()
	cpuProfileMu.Lock()
	cpuProfileOnce = sync.Once{}
	cpuProfileMu.Unlock()
	t.Cleanup(func() {
		stopCPUProfile()
		cpuProfileMu.Lock()
		cpuProfileOnce = sync.Once{}
		cpuProfileMu.Unlock()
	})
}

func TestCPUProfileLifecycle(t *testing.T) {
	resetGlobalFlags(t)
	resetCPUProfileForTest(t)

	tmpDir := t.TempDir()
	profPath := filepath.Join(tmpDir, "test.pprof")
	flagCPUProfile = profPath

	startCPUProfile()

	cpuProfileMu.Lock()
	file := cpuProfileFile
	cpuProfileMu.Unlock()
	if file == nil {
		t.Fatal("expected cpuProfileFile to be non-nil after startCPUProfile")
	}

	// Idempotent stop
	stopCPUProfile()

	cpuProfileMu.Lock()
	closedFile := cpuProfileFile
	cpuProfileMu.Unlock()
	if closedFile != nil {
		t.Errorf("expected cpuProfileFile to be nil after stopCPUProfile, got %v", closedFile)
	}

	// Calling stop again should not panic or fail
	stopCPUProfile()

	// Verify the file was created
	info, err := os.Stat(profPath)
	if err != nil {
		t.Fatalf("stat profile file: %v", err)
	}
	if info.Size() == 0 {
		t.Error("expected profile file to have non-zero size")
	}
}

func TestStopCPUProfileWhenNotStarted(t *testing.T) {
	resetGlobalFlags(t)
	resetCPUProfileForTest(t)

	// Must be safe to call when flagCPUProfile is empty and startCPUProfile was not called
	stopCPUProfile()
}
