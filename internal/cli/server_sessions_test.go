package cli

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionsDirHonoursEnvOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	t.Setenv("PI_SESSIONS_DIR", "")
	got, err := sessionsDir()
	if err != nil {
		t.Fatalf("sessionsDir() error = %v", err)
	}
	if want := filepath.Join(home, ".pi-go", "sessions"); got != want {
		t.Errorf("sessionsDir() = %q, want %q", got, want)
	}

	durable := filepath.Join(t.TempDir(), "durable-sessions")
	t.Setenv("PI_SESSIONS_DIR", " "+durable+" ")
	got, err = sessionsDir()
	if err != nil {
		t.Fatalf("sessionsDir() error = %v", err)
	}
	if got != durable {
		t.Errorf("sessionsDir() with PI_SESSIONS_DIR = %q, want %q", got, durable)
	}
}

func TestOpenServerSessionStore(t *testing.T) {
	durable := filepath.Join(t.TempDir(), "sessions")
	t.Setenv("PI_SESSIONS_DIR", durable)

	svc := openServerSessionStore(context.Background(), slog.New(slog.DiscardHandler))
	if svc == nil {
		t.Fatal("openServerSessionStore() = nil, want a store over PI_SESSIONS_DIR")
	}
	if _, err := os.Stat(durable); err != nil {
		t.Errorf("sessions dir not created: %v", err)
	}
	if serverSessionStore(svc) == nil {
		t.Error("serverSessionStore(svc) = nil, want an ACP store")
	}
	if serverSessionStore(nil) != nil {
		t.Error("serverSessionStore(nil) != nil, want in-memory sessions")
	}

	// A path that cannot be a directory degrades to in-memory sessions
	// instead of failing startup; a nil logger must be tolerated too.
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_SESSIONS_DIR", filepath.Join(blocker, "sessions"))
	if svc := openServerSessionStore(context.Background(), nil); svc != nil {
		t.Error("openServerSessionStore() over an unwritable path != nil, want nil")
	}
}
