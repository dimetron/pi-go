package session

import (
	"context"
	"testing"
	"time"

	"google.golang.org/adk/v2/session"
)

func TestSetSessionWorkDir(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	resp, err := svc.Create(context.Background(), &session.CreateRequest{AppName: "pi-go", UserID: "local", SessionID: "wd-1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.SetSessionWorkDir(resp.Session.ID(), "/work/project"); err != nil {
		t.Fatalf("SetSessionWorkDir: %v", err)
	}

	// A fresh service over the same directory reads it back from meta.json.
	reopened, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService(reopen): %v", err)
	}
	metas, err := reopened.ListMeta("pi-go", "local")
	if err != nil {
		t.Fatalf("ListMeta: %v", err)
	}
	if len(metas) != 1 || metas[0].WorkDir != "/work/project" {
		t.Fatalf("ListMeta() = %+v, want workDir /work/project", metas)
	}

	if err := reopened.SetSessionWorkDir("missing", "/x"); err == nil {
		t.Error("SetSessionWorkDir(missing) error = nil, want not found")
	}
}

func TestListMeta(t *testing.T) {
	dir := t.TempDir()
	svc, err := NewFileService(dir)
	if err != nil {
		t.Fatalf("NewFileService: %v", err)
	}
	ctx := context.Background()
	for _, c := range []struct{ id, app, user string }{
		{"older", "pi-go", "local"},
		{"newer", "pi-go", "local"},
		{"other-app", "not-pi", "local"},
		{"other-user", "pi-go", "someone"},
	} {
		if _, err := svc.Create(ctx, &session.CreateRequest{AppName: c.app, UserID: c.user, SessionID: c.id}); err != nil {
			t.Fatalf("Create(%s): %v", c.id, err)
		}
	}
	// An event lands on "older" last, so ordering follows UpdatedAt rather
	// than name or creation order.
	older, err := svc.Get(ctx, &session.GetRequest{AppName: "pi-go", UserID: "local", SessionID: "older"})
	if err != nil {
		t.Fatalf("Get(older): %v", err)
	}
	if err := svc.AppendEvent(ctx, older.Session, &session.Event{Author: "user", Timestamp: time.Now().Add(time.Minute)}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	if _, err := svc.ListMeta("", ""); err == nil {
		t.Error("ListMeta(\"\") error = nil, want app_name required")
	}

	metas, err := svc.ListMeta("pi-go", "local")
	if err != nil {
		t.Fatalf("ListMeta: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("ListMeta(pi-go, local) = %d sessions, want 2 (other app and user excluded): %+v", len(metas), metas)
	}
	if metas[0].ID != "older" || metas[1].ID != "newer" {
		t.Errorf("ListMeta() order = [%s %s], want most recently updated first", metas[0].ID, metas[1].ID)
	}

	all, err := svc.ListMeta("pi-go", "")
	if err != nil {
		t.Fatalf("ListMeta(all users): %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListMeta(pi-go, any user) = %d sessions, want 3", len(all))
	}
}
