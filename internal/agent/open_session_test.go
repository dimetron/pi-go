package agent

import (
	"context"
	"testing"

	"google.golang.org/adk/v2/session"

	pisession "github.com/dimetron/pi-go/internal/session"
)

func TestOpenSession(t *testing.T) {
	ctx := context.Background()
	sessionsDir := t.TempDir()
	svc, err := pisession.NewFileService(sessionsDir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	workDir := t.TempDir()
	a, err := New(Config{Model: &mockLLM{name: "test-model", response: "ok"}, SessionService: svc, WorkingDir: workDir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := a.OpenSession(ctx, "  "); err == nil {
		t.Error("OpenSession(blank) error = nil, want rejection")
	}

	const id = "ctx-7f0c7a4e"
	resumed, err := a.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession() first error = %v", err)
	}
	if resumed {
		t.Error("first OpenSession() reported resumed = true for a brand-new id")
	}

	metas, err := svc.ListMeta(AppName, DefaultUserID)
	if err != nil {
		t.Fatalf("ListMeta: %v", err)
	}
	if len(metas) != 1 || metas[0].ID != id {
		t.Fatalf("ListMeta() = %+v, want exactly the opened session", metas)
	}
	if metas[0].Model != "test-model" {
		t.Errorf("recorded model = %q, want test-model", metas[0].Model)
	}
	if metas[0].WorkDir != workDir {
		t.Errorf("recorded workDir = %q, want the configured %q", metas[0].WorkDir, workDir)
	}

	resumed, err = a.OpenSession(ctx, id)
	if err != nil {
		t.Fatalf("OpenSession() second error = %v", err)
	}
	if !resumed {
		t.Error("second OpenSession() reported resumed = false for an existing id")
	}

	// A fresh service over the same directory — a restarted process — finds it too.
	reopened, err := pisession.NewFileService(sessionsDir)
	if err != nil {
		t.Fatalf("NewFileService(reopen): %v", err)
	}
	b, err := New(Config{Model: &mockLLM{name: "test-model", response: "ok"}, SessionService: reopened})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if resumed, err := b.OpenSession(ctx, id); err != nil || !resumed {
		t.Errorf("OpenSession() after restart = (%v, %v), want (true, nil)", resumed, err)
	}
}

func TestOpenSessionInMemoryService(t *testing.T) {
	a, err := New(Config{Model: &mockLLM{name: "m", response: "ok"}, SessionService: session.InMemoryService()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if resumed, err := a.OpenSession(context.Background(), "mem-1"); err != nil || resumed {
		t.Fatalf("first OpenSession() = (%v, %v), want (false, nil)", resumed, err)
	}
	if resumed, err := a.OpenSession(context.Background(), "mem-1"); err != nil || !resumed {
		t.Fatalf("second OpenSession() = (%v, %v), want (true, nil)", resumed, err)
	}
}
