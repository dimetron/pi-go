//go:build integration

package server_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	shared "github.com/dimetron/pi-go/internal/acp"
	"github.com/dimetron/pi-go/internal/acp/client"
)

var piBinary string

func TestMain(m *testing.M) {
	code, err := buildAndRun(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

func buildAndRun(m *testing.M) (int, error) {
	dir, err := os.MkdirTemp("", "pi-acp-int-*")
	if err != nil {
		return 0, fmt.Errorf("tempdir: %w", err)
	}
	defer os.RemoveAll(dir)

	bin := filepath.Join(dir, "pi")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	repoRoot, err := moduleRoot()
	if err != nil {
		return 0, err
	}

	build := exec.Command("go", "build", "-o", bin, "./cmd/pi")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("build pi: %w\n%s", err, out)
	}
	piBinary = bin
	return m.Run(), nil
}

// moduleRoot walks up from the test file directory until it finds go.mod.
func moduleRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above %s", start)
		}
		dir = parent
	}
}

// TestPiACPServerRoundTrip spawns the real pi binary in acp-server mode and
// drives a full Initialize -> NewSession -> Prompt turn through the local ACP
// client runner. It asserts that the skeleton echo handler streams a message
// chunk and returns a successful result.
func TestPiACPServerRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping acp server integration test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	runner := client.Runner{}
	session, err := runner.Start(ctx, shared.RunRequest{
		Command: []string{piBinary, "acp-server"},
		Prompt:  "hello bidirectional",
		CWD:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var messages []string
	for event := range session.Events() {
		if event.Type == shared.EventTypeMessage {
			messages = append(messages, event.Content)
		}
	}

	result := session.Wait()
	if result.Status != shared.StatusSuccess {
		t.Fatalf("status = %q, error = %q", result.Status, result.Error)
	}

	want := "echo: hello bidirectional"
	if !strings.Contains(result.Result, want) {
		t.Fatalf("result = %q, want it to contain %q", result.Result, want)
	}
	if strings.TrimSpace(result.SessionID) == "" {
		t.Fatal("SessionID is empty; expected server-assigned id")
	}
	if len(messages) == 0 {
		t.Fatal("no streamed message chunks captured")
	}
	if got := strings.TrimSpace(strings.Join(messages, "")); got != want {
		t.Fatalf("streamed message = %q, want %q", got, want)
	}
}

// TestPiACPServerPromptValidation exercises the client-side validation path
// against the real server binary: an empty prompt should be rejected locally
// without spawning the subprocess.
func TestPiACPServerPromptValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping acp server integration test in -short mode")
	}

	runner := client.Runner{}
	_, err := runner.Start(context.Background(), shared.RunRequest{
		Command: []string{piBinary, "acp-server"},
		Prompt:  "",
		CWD:     t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected validation error for empty prompt")
	}
}
