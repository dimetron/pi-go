package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adksession "google.golang.org/adk/v2/session"

	piagent "github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
)

func TestStoreSessionID(t *testing.T) {
	t.Parallel()

	passthrough := []string{
		"sess_0123456789abcdef01234567",
		"7f0c7a4e-2f0e-4b9b-9d1a-0c2a5b3f8e11", // kagent A2A context id
		"260903-1830-ab12c-d34ef",              // CLI-generated id
		"a.b-c_d",
	}
	for _, id := range passthrough {
		if got := StoreSessionID(id); got != id {
			t.Errorf("StoreSessionID(%q) = %q, want unchanged", id, got)
		}
	}

	hashed := []string{
		"",
		"../../etc/passwd",
		"a/b",
		".hidden",
		"with space",
		"tab\tchar",
		strings.Repeat("x", 129),
	}
	seen := map[string]string{}
	for _, id := range hashed {
		got := StoreSessionID(id)
		if !strings.HasPrefix(got, "acp-") || len(got) != len("acp-")+32 {
			t.Errorf("StoreSessionID(%q) = %q, want acp-<32 hex>", id, got)
		}
		if filepath.Base(got) != got {
			t.Errorf("StoreSessionID(%q) = %q is not a bare path element", id, got)
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("StoreSessionID(%q) collides with %q", id, prev)
		}
		seen[got] = id
		if again := StoreSessionID(id); again != got {
			t.Errorf("StoreSessionID(%q) not stable: %q then %q", id, got, again)
		}
	}
}

// newTempFileStore opens a FileService in a temp dir and returns it with the
// ACP-facing store over it and the directory it lives in.
func newTempFileStore(t *testing.T) (*pisession.FileService, *FileSessionStore, string) {
	t.Helper()
	dir := t.TempDir()
	svc, err := pisession.NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	return svc, NewFileSessionStore(svc), dir
}

// persistSession creates a transcript under id with one user turn and one
// model reply.
func persistSession(t *testing.T, svc *pisession.FileService, id string) {
	t.Helper()
	ctx := context.Background()
	resp, err := svc.Create(ctx, &adksession.CreateRequest{
		AppName: piagent.AppName, UserID: piagent.DefaultUserID, SessionID: id,
	})
	if err != nil {
		t.Fatalf("Create(%s): %v", id, err)
	}
	for _, ev := range []*adksession.Event{userTextEvent("hello"), modelEvent(textPart("hi there"))} {
		if err := svc.AppendEvent(ctx, resp.Session, ev); err != nil {
			t.Fatalf("AppendEvent: %v", err)
		}
	}
}

func TestFileSessionStoreExistsAndReplay(t *testing.T) {
	t.Parallel()
	svc, store, _ := newTempFileStore(t)
	ctx := context.Background()

	const id = "sess_persisted"
	if store.Exists(ctx, id) {
		t.Fatal("Exists() = true before the session was created")
	}
	persistSession(t, svc, id)
	if !store.Exists(ctx, id) {
		t.Fatal("Exists() = false for a persisted session")
	}
	if store.Exists(ctx, "../"+id) {
		t.Fatal("Exists() = true for a traversal id; it must hash to a different session")
	}

	u := &recordingUpdater{}
	if err := store.Replay(ctx, id, u); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	if len(u.updates) != 2 {
		t.Fatalf("Replay() sent %d updates, want 2", len(u.updates))
	}
	if u.updates[0].UserMessageChunk == nil || u.updates[1].AgentMessageChunk == nil {
		t.Errorf("Replay() order = %+v, want user chunk then agent chunk", u.updates)
	}

	if err := store.Replay(ctx, "sess_missing", u); err == nil {
		t.Error("Replay() of an unknown session error = nil, want not found")
	}
	if err := store.Replay(ctx, id, nil); err != nil {
		t.Errorf("Replay() with nil updater error = %v, want a no-op", err)
	}
}

func TestFileSessionStoreListNewestFirst(t *testing.T) {
	t.Parallel()
	svc, store, _ := newTempFileStore(t)
	ctx := context.Background()

	persistSession(t, svc, "sess_older")
	persistSession(t, svc, "sess_newer")
	if err := svc.SetSessionTitle("sess_newer", "fix the bug"); err != nil {
		t.Fatalf("SetSessionTitle: %v", err)
	}
	if err := svc.SetSessionWorkDir("sess_newer", "/work/newer"); err != nil {
		t.Fatalf("SetSessionWorkDir: %v", err)
	}

	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List() = %d sessions, want 2", len(got))
	}
	if got[0].ID != "sess_newer" {
		t.Errorf("List()[0] = %q, want the most recently updated session first", got[0].ID)
	}
	if got[0].Title != "fix the bug" || got[0].Cwd != "/work/newer" || got[0].UpdatedAt.IsZero() {
		t.Errorf("List()[0] = %+v, want title, cwd and updatedAt filled", got[0])
	}
}

func TestFileSessionStoreListSurfacesFailure(t *testing.T) {
	t.Parallel()
	svc, store, dir := newTempFileStore(t)
	persistSession(t, svc, "sess_gone")

	// Remove the directory the service lists so ListMeta's read fails.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := store.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want the read failure surfaced")
	}
}
