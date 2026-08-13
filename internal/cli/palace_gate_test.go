package cli

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/palace"
)

// newTestPalace opens a palace backed by a temp database, with no embedder, so
// the gate is exercised against real storage rather than a stub.
func newTestPalace(t *testing.T) *palace.Palace {
	t.Helper()
	p, err := palace.New(
		palace.WithDBPath(filepath.Join(t.TempDir(), "palace.db")),
		palace.WithLocalEmbedder(),
	)
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

// TestPalaceHasContentEmpty is the case that motivated the gate: `pi memory
// init` leaves a valid database with zero drawers, and eleven tool
// declarations against it buy nothing.
func TestPalaceHasContentEmpty(t *testing.T) {
	if palaceHasContent(newTestPalace(t)) {
		t.Fatal("palaceHasContent() = true for a palace with no drawers, want false")
	}
}

func TestPalaceHasContentWithDrawer(t *testing.T) {
	p := newTestPalace(t)
	if _, err := p.AddDrawer(context.Background(), palace.DrawerInput{
		Wing:    "pi-go",
		Room:    "cli",
		Content: "the palace gate reads drawer count, not file existence",
	}); err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
	if !palaceHasContent(p) {
		t.Fatal("palaceHasContent() = false after adding a drawer, want true")
	}
}

// TestPalaceHasContentClosed pins the failure direction: when the count cannot
// be read the gate must stay shut, so a broken palace costs no context.
func TestPalaceHasContentClosed(t *testing.T) {
	p := newTestPalace(t)
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if palaceHasContent(p) {
		t.Fatal("palaceHasContent() = true on a closed palace, want false")
	}
}
