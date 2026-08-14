package piagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/memory"
)

// An embedded agent runs inside someone else's process, where the environment
// is not the one a `pi` session assumes: the working directory may not exist,
// HOME may be unset, the memory store may be unwritable. These tests pin what
// happens then — which of those is fatal and which degrades quietly — because a
// library that panics or silently half-works in a host application is worse
// than one that refuses to start.

func TestNewRejectsAWorkingDirThatIsAFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := New(context.Background(),
		WithModel(&fakeLLM{name: "test-model"}),
		WithWorkingDir(file),
	)
	if err == nil {
		t.Fatal("New accepted a file as the working directory; the sandbox root must be a directory")
	}
}

func TestMemoryContextEmptyWithoutStore(t *testing.T) {
	var store memory.Store // nil
	if got := memoryContext(context.Background(), store, config.Config{}, "/tmp/project"); got != "" {
		t.Fatalf("memoryContext with no store = %q, want empty", got)
	}
}
