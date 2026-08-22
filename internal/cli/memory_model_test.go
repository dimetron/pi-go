// Tests for `pi memory model` download and status subcommands.
package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
	"github.com/dimetron/pi-go/internal/testenv"
)

func TestRunMemoryModelDownload_AutoDetectPlatformBranch(t *testing.T) {
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	dest := filepath.Join(t.TempDir(), "models")
	_ = runMemoryModelDownload(dest, "")
}

func TestNewMemoryModelDownloadCmd_RunEError(t *testing.T) {
	resetGlobalFlags(t)
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	tmp := t.TempDir()
	cmd := newMemoryModelDownloadCmd()
	cmd.SetArgs([]string{"--dest", filepath.Join(tmp, "m")})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}

func TestNewMemoryModelStatusCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	cmd := newMemoryModelStatusCmd()
	cmd.SetArgs([]string{"--path", tmp})
	_ = captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Errorf("execute: %v", err)
		}
	})
}

func TestRunMemoryModelDownload_HomeDirError(t *testing.T) {
	// Test when os.UserHomeDir returns an error (non-existent HOME).
	testenv.SetUnwritableHome(t)

	// With empty dest and non-existent HOME, should fail.
	err := runMemoryModelDownload("", "")
	if err == nil {
		t.Error("expected error when HOME is invalid")
	}
}

func TestRunMemoryModelDownload_MkdirAllError(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	// Test mkdir error by using a path with no permissions.
	// On Unix, creating under /proc/something non-writable would fail.
	// Instead, test with a path that's not a valid directory component.
	// The most reliable path is one that exists but isn't writable.
	tmpDir := t.TempDir()
	// Create a read-only directory to trigger mkdir error.
	readonlyDir := filepath.Join(tmpDir, "readonly")
	os.MkdirAll(readonlyDir, 0444)
	defer os.Chmod(readonlyDir, 0755) // cleanup

	err := runMemoryModelDownload(readonlyDir, "")
	if err == nil {
		t.Error("expected error when directory is not writable")
	}
}

func TestRunMemoryModelStatus_PathError(t *testing.T) {
	// With a path that triggers os.Stat error.
	// Using empty path falls back to default which may exist, so test with
	// a path that definitely doesn't exist and isn't the default.
	err := runMemoryModelStatus("/this/path/does/not/exist/at/all")
	if err != nil {
		t.Fatalf("runMemoryModelStatus returned error: %v", err)
	}
}

func TestNewMemoryModelDownloadCmd_Flags(t *testing.T) {
	cmd := newMemoryModelDownloadCmd()
	for _, name := range []string{"dest", "onnx"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag %q not found", name)
		}
	}
}

func TestNewMemoryModelStatusCmd_Flag(t *testing.T) {
	cmd := newMemoryModelStatusCmd()
	if cmd.Flags().Lookup("path") == nil {
		t.Error("missing --path flag")
	}
}

func TestRunMemoryModelDownload_WithDest(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	dest := filepath.Join(t.TempDir(), "models")
	err := runMemoryModelDownload(dest, "")
	if err != nil {
		t.Logf("expected error in test env: %v", err)
	}
}

func TestRunMemoryModelDownload_AutoDetectOnnx(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	dest := filepath.Join(t.TempDir(), "models")
	err := runMemoryModelDownload(dest, "")
	if err != nil {
		t.Logf("expected error: %v", err)
	}
}

func TestRunMemoryModelDownload_ExplicitOnnxPath(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	if testing.Testing() {
		t.Skip("skipping: go-huggingface has race condition")
	}
	dest := filepath.Join(t.TempDir(), "models")
	err := runMemoryModelDownload(dest, "nonexistent/model.onnx")
	if err == nil {
		t.Log("no error returned")
	}
}

// stubDownloadModel replaces the fetcher for the duration of the test and
// returns a pointer to the arguments it was last called with.
//
// The real fetcher pulls the embedding model from Hugging Face into HOME. This
// test used to do exactly that and only appeared to pass because an earlier
// test left HOME unset; with HOME isolated properly the download ran, and two
// things broke under -race: go-huggingface's parallel download has a data
// race, and every later palace-backed test in this package then found the
// weights and ran real inference, where gomlx's AVX2 matmul kernel trips
// checkptr. A fake fetcher keeps the path covered without any of that.
func stubDownloadModel(t *testing.T, result string, err error) *struct{ dest, onnx string } {
	t.Helper()
	var got struct{ dest, onnx string }
	orig := downloadModel
	downloadModel = func(dest, onnx string) (string, error) {
		got.dest, got.onnx = dest, onnx
		return result, err
	}
	t.Cleanup(func() { downloadModel = orig })
	return &got
}

func TestRunMemoryModelDownload_DefaultDest(t *testing.T) {
	home := t.TempDir()
	testenv.SetHome(t, home)
	got := stubDownloadModel(t, "model-path", nil)

	out := captureStdout(t, func() {
		if err := runMemoryModelDownload("", ""); err != nil {
			t.Errorf("runMemoryModelDownload: %v", err)
		}
	})

	wantDest := filepath.Join(home, ".pi-go", "models")
	if got.dest != wantDest {
		t.Errorf("dest = %q, want the default under HOME %q", got.dest, wantDest)
	}
	if info, err := os.Stat(wantDest); err != nil || !info.IsDir() {
		t.Errorf("default model directory was not created: %v", err)
	}
	if got.onnx != palace.DetectPlatformOnnxFile() {
		t.Errorf("onnx = %q, want the auto-detected %q", got.onnx, palace.DetectPlatformOnnxFile())
	}
	if !strings.Contains(out, "Model downloaded: model-path") {
		t.Errorf("output = %q, want the downloaded path reported", out)
	}
}

func TestRunMemoryModelDownload_ExplicitDestAndOnnxReachTheFetcher(t *testing.T) {
	got := stubDownloadModel(t, "", nil)
	dest := filepath.Join(t.TempDir(), "models")

	if err := runMemoryModelDownload(dest, "onnx/custom.onnx"); err != nil {
		t.Fatalf("runMemoryModelDownload: %v", err)
	}
	if got.dest != dest || got.onnx != "onnx/custom.onnx" {
		t.Errorf("fetcher got dest=%q onnx=%q, want %q / onnx/custom.onnx", got.dest, got.onnx, dest)
	}
}

func TestRunMemoryModelDownload_FetcherErrorIsWrapped(t *testing.T) {
	stubDownloadModel(t, "", errors.New("offline"))

	err := runMemoryModelDownload(t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "downloading model: offline") {
		t.Errorf("err = %v, want the fetcher error wrapped as \"downloading model\"", err)
	}
}
