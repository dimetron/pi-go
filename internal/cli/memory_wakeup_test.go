package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

func TestMemoryWakeUpCmd_Registered(t *testing.T) {
	cmd := newMemoryCmd()

	found := false
	for _, sub := range cmd.Commands() {
		if sub.Name() == "wake-up" {
			found = true
			break
		}
	}
	if !found {
		t.Error("wake-up subcommand not registered")
	}
}

func TestMemoryWakeUp_EmptyPalace(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	p.Close()

	err = runMemoryWakeUp(dbPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryWakeUp_WithDrawers(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, _ = p.AddDrawer(ctx, palace.DrawerInput{
		Wing:       "backend",
		Room:       "auth",
		Content:    "Authentication uses JWT with refresh tokens",
		Importance: 8,
	})
	_, _ = p.AddDrawer(ctx, palace.DrawerInput{
		Wing:       "backend",
		Room:       "database",
		Content:    "PostgreSQL with connection pooling via pgbouncer",
		Importance: 7,
	})
	p.Close()

	err = runMemoryWakeUp(dbPath, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryWakeUp_WingFilter(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	_, _ = p.AddDrawer(ctx, palace.DrawerInput{
		Wing: "backend", Room: "api", Content: "Backend API content", Importance: 5,
	})
	_, _ = p.AddDrawer(ctx, palace.DrawerInput{
		Wing: "frontend", Room: "ui", Content: "Frontend UI content", Importance: 5,
	})
	p.Close()

	err = runMemoryWakeUp(dbPath, "backend")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
