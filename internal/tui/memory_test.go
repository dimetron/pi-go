package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

func TestMemoryTickCmd_NoDB(t *testing.T) {
	// No .pi-go/palace.db in the temp dir → nil status.
	workDir := t.TempDir()
	cmd := memoryTickCmd(workDir)
	msg := cmd()
	m, ok := msg.(memoryTickMsg)
	if !ok {
		t.Fatalf("expected memoryTickMsg, got %T", msg)
	}
	if m.status != nil {
		t.Errorf("expected nil status, got %+v", m.status)
	}
}

func TestMemoryTickCmd_WithDB(t *testing.T) {
	workDir := t.TempDir()
	dbPath := filepath.Join(workDir, ".pi-go", "palace.db")

	// Create the palace DB so os.Stat passes and palace.New opens it.
	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	p.Close()

	cmd := memoryTickCmd(workDir)
	msg := cmd()
	m, ok := msg.(memoryTickMsg)
	if !ok {
		t.Fatalf("expected memoryTickMsg, got %T", msg)
	}
	if m.status == nil {
		t.Fatal("expected non-nil status")
	}
	if m.status.DrawerCount != 0 {
		t.Errorf("DrawerCount = %d, want 0", m.status.DrawerCount)
	}
}

func TestMemoryTickCmd_PalaceNewError(t *testing.T) {
	workDir := t.TempDir()
	// Make .pi-go/palace.db a directory so palace.New fails to open it as a DB.
	dbPath := filepath.Join(workDir, ".pi-go", "palace.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.MkdirAll(dbPath, 0o755); err != nil {
		t.Fatalf("mkdir dbPath: %v", err)
	}

	cmd := memoryTickCmd(workDir)
	msg := cmd()
	m, ok := msg.(memoryTickMsg)
	if !ok {
		t.Fatalf("expected memoryTickMsg, got %T", msg)
	}
	if m.status != nil {
		t.Errorf("expected nil status when palace.New fails, got %+v", m.status)
	}
}
