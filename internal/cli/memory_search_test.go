package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

func TestMemorySearchCmd_Registered(t *testing.T) {
	cmd := newMemoryCmd()

	found := false
	for _, sub := range cmd.Commands() {
		if sub.Name() == "search" {
			found = true
			break
		}
	}
	if !found {
		t.Error("search subcommand not registered")
	}
}

func TestMemorySearch_NoResults(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	// Create an empty palace.
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	err = runMemorySearch("nonexistent", dbPath, "", "", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemorySearch_WithResults(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, err = p.AddDrawer(ctx, palace.DrawerInput{
		Wing:    "backend",
		Room:    "auth",
		Content: "authentication flow uses JWT tokens with refresh rotation",
	})
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	// Search should find the drawer via FTS5 (no embedder loaded).
	err = runMemorySearch("JWT tokens", dbPath, "", "", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemorySearch_WingFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, _ = p.AddDrawer(ctx, palace.DrawerInput{
		Wing: "backend", Room: "api", Content: "REST API endpoint design",
	})
	_, _ = p.AddDrawer(ctx, palace.DrawerInput{
		Wing: "frontend", Room: "ui", Content: "React API component hooks",
	})
	p.Close()

	// Should not error even with filter.
	err = runMemorySearch("API", dbPath, "backend", "", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewMemorySearchCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemorySearchCmd()
	cmd.SetArgs([]string{"query text", "--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}
