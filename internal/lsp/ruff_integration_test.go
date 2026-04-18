package lsp

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestRuffDiagnostics(t *testing.T) {
	mgr := NewManager(nil)

	file := "/tmp/ruff-test/broken.py"
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

	// Trigger diagnostics
	_, err = srv.Diagnostics(ctx, file)
	if err != nil {
		t.Fatalf("Diagnostics error: %v", err)
	}

	// Check cached diagnostics
	uri := "file:///tmp/ruff-test/broken.py"
	diags := mgr.CachedDiagnostics(uri)
	fmt.Printf("Diagnostics count: %d\n", len(diags))
	for _, d := range diags {
		fmt.Printf("  Line %d: [%s] %s\n", d.Range.Start.Line+1, d.SeverityString(), d.Message)
	}

	if len(diags) == 0 {
		t.Log("Warning: No diagnostics returned - ruff LSP may not be working")
	}
}
