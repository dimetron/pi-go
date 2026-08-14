package piagent

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/palace"
)

// The palace is opt-in for a library, so nothing else in this package exercises
// it. That left the whole opt-in path untested — which is the wrong way round:
// a feature nobody switches on by default is precisely the one that rots.

// newPalaceAt builds a palace with one drawer, so setupPalace's content gate
// opens. Uses the in-process embedder rather than Ollama so the test needs no
// daemon.
func newPalaceAt(t *testing.T, dbPath string) {
	t.Helper()
	p, err := palace.New(palace.WithDBPath(dbPath), palace.WithLocalEmbedder())
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	defer func() { _ = p.Close() }()

	if _, err := p.AddDrawer(context.Background(), palace.DrawerInput{
		Wing:       "pi-go",
		Room:       "piagent",
		Content:    "the palace gate reads drawer count, not file existence",
		Importance: 7,
	}); err != nil {
		t.Fatalf("AddDrawer: %v", err)
	}
}

func palaceCfg(dbPath string) config.Config {
	return config.Config{Palace: &config.PalaceConfig{DBPath: dbPath}}
}

func TestSetupPalaceEmptyPalaceRegistersNoTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "empty.db")
	p, err := palace.New(palace.WithDBPath(dbPath), palace.WithLocalEmbedder())
	if err != nil {
		t.Fatalf("palace.New: %v", err)
	}
	_ = p.Close()

	tools, _, closeFn := setupPalace(options{palaceEnabled: true}, palaceCfg(dbPath), nil)
	t.Cleanup(closeFn)

	if tools != nil {
		t.Fatalf("an empty palace registered %d tools; the gate reads drawer count", len(tools))
	}
}

// TestSetupPalaceWithContentRegistersTools is the path an embedder opts in for.
// It was entirely untested: palace.New, the content gate, tool building and the
// wake-up context all ran for the first time here.
func TestSetupPalaceWithContentRegistersTools(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "palace.db")
	newPalaceAt(t, dbPath)

	tools, wakeUp, closeFn := setupPalace(options{palaceEnabled: true}, palaceCfg(dbPath), nil)
	t.Cleanup(closeFn)

	if len(tools) == 0 {
		t.Fatal("a palace with drawers registered no tools")
	}
	// WakeUp over all wings should surface the drawer we just filed.
	if wakeUp == "" {
		t.Error("a palace with drawers produced no wake-up context")
	}
	if closeFn == nil {
		t.Fatal("closer is nil; the caller defers it unconditionally")
	}
}
