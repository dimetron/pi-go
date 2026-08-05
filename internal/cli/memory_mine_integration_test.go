//go:build integration

package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

// Tests in this file require a live Ollama daemon and embedding model. They
// run only under `go test -tags integration ./internal/cli/...`; the regular
// `go test` build excludes them entirely.
//
// Run locally:
//
//	ollama serve
//	ollama pull embeddinggemma
//	make test-integration
//
// Or directly:
//
//	go test -tags integration -run 'TestRunMemoryMine|TestRunMemoryStatus_WithDB' ./internal/cli/...

func TestRunMemoryMine_ProjectFiles(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	// (hugot.DownloadModel → gomlx/go-huggingface hub.(*Repo).DownloadFiles).
	if raceEnabled {
		t.Skip("skipping: go-huggingface has race condition")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"),
		[]byte("package main\n\nfunc main() { println(\"hello world test content for chunk threshold minimum\") }"), 0o644)

	output := captureStdout(t, func() {
		err := runMemoryMine(dir, "testproject", false)
		if err != nil {
			t.Fatalf("runMemoryMine: %v", err)
		}
	})
	if output == "" {
		t.Error("expected mining output")
	}
}

func TestRunMemoryMine_Conversations(t *testing.T) {
	// Skip race detection due to race in third-party go-huggingface library
	// (hugot.DownloadModel → gomlx/go-huggingface hub.(*Repo).DownloadFiles).
	if raceEnabled {
		t.Skip("skipping: go-huggingface has race condition")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "chat.jsonl"),
		[]byte(`{"role":"user","content":"question"}
{"role":"assistant","content":"answer"}
`), 0o644)

	output := captureStdout(t, func() {
		err := runMemoryMine(dir, "testconv", true)
		if err != nil {
			t.Fatalf("runMemoryMine convos: %v", err)
		}
	})
	if output == "" {
		t.Error("expected mining output")
	}
}

func TestRunMemoryStatus_WithDB(t *testing.T) {
	// AddDrawer embeds its content, so this needs a backend like the mine tests.
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	// Create a real palace with some data
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	p.AddDrawer(context.Background(), palace.DrawerInput{
		Wing: "test", Room: "api", Content: "test content",
	})
	p.KGAdd(context.Background(), palace.TripleInput{
		Subject: "Alice", Predicate: "works_on", Object: "api",
	})
	p.Close()

	output := captureStdout(t, func() {
		err := runMemoryStatus(dbPath)
		if err != nil {
			t.Fatalf("runMemoryStatus: %v", err)
		}
	})
	if output == "" {
		t.Error("expected status output")
	}
}
