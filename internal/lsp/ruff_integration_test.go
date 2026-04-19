package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRuffDiagnostics(t *testing.T) {
	// Requires the `ruff` binary on PATH; skip in environments that lack it
	// (default CI runners) so the test is not a fixed-file hazard.
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff not installed; skipping LSP integration test")
	}

	// Create the broken.py fixture under t.TempDir so the test owns its
	// lifecycle — earlier versions relied on a pre-existing /tmp/ruff-test
	// directory which CI does not provision.
	dir := t.TempDir()
	file := filepath.Join(dir, "broken.py")
	if err := os.WriteFile(file, []byte("import os\nx=1\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	mgr := NewManager(nil)
	srv, err := mgr.ServerFor(file)
	if err != nil {
		t.Fatalf("Server error: %v", err)
	}
	if srv == nil {
		t.Fatal("No server for this file type")
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err = srv.Diagnostics(ctx, file); err != nil {
		t.Fatalf("Diagnostics error: %v", err)
	}

	diags := mgr.CachedDiagnostics("file://" + file)
	fmt.Printf("Diagnostics count: %d\n", len(diags))
	for _, d := range diags {
		fmt.Printf("  Line %d: [%s] %s\n", d.Range.Start.Line+1, d.SeverityString(), d.Message)
	}

	if len(diags) == 0 {
		t.Log("Warning: No diagnostics returned - ruff LSP may not be working")
	}
}
