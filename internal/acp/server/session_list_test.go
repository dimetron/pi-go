package server

import (
	"context"
	"testing"

	acp "github.com/coder/acp-go-sdk"
	adksession "google.golang.org/adk/session"

	piagent "github.com/dimetron/pi-go/internal/agent"
	pisession "github.com/dimetron/pi-go/internal/session"
)

func TestAgentListSessionsReturnsPersistedSessions(t *testing.T) {
	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService() error = %v", err)
	}
	if _, err := svc.Create(context.Background(), &adksession.CreateRequest{
		AppName:   piagent.AppName,
		UserID:    piagent.DefaultUserID,
		SessionID: "persisted-acp-session",
		State: map[string]any{
			"cwd": "/tmp/project",
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	a := &Agent{SessionService: svc}
	resp, err := a.ListSessions(context.Background(), acp.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("len(Sessions) = %d, want 1", len(resp.Sessions))
	}
	if resp.Sessions[0].SessionId != acp.SessionId("persisted-acp-session") {
		t.Fatalf("SessionId = %q, want persisted-acp-session", resp.Sessions[0].SessionId)
	}
	if resp.Sessions[0].Cwd == "" {
		t.Fatalf("Cwd is empty")
	}
	if resp.Sessions[0].UpdatedAt == nil || *resp.Sessions[0].UpdatedAt == "" {
		t.Fatalf("UpdatedAt is empty")
	}
}

func TestResolvePiSessionIDReusesPersistedSession(t *testing.T) {
	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService() error = %v", err)
	}
	if _, err := svc.Create(context.Background(), &adksession.CreateRequest{
		AppName:   piagent.AppName,
		UserID:    piagent.DefaultUserID,
		SessionID: "loaded-session",
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	sid, err := resolvePiSessionID(context.Background(), svc, "loaded-session")
	if err != nil {
		t.Fatalf("resolvePiSessionID() error = %v", err)
	}
	if sid != "loaded-session" {
		t.Fatalf("session id = %q, want loaded-session", sid)
	}

	list, err := svc.List(context.Background(), &adksession.ListRequest{AppName: piagent.AppName, UserID: piagent.DefaultUserID})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list.Sessions) != 1 {
		t.Fatalf("len(Sessions) = %d, want 1", len(list.Sessions))
	}
}

func TestResolvePiSessionIDCreatesLoadedSessionWhenMissing(t *testing.T) {
	svc, err := pisession.NewFileService(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileService() error = %v", err)
	}

	sid, err := resolvePiSessionID(context.Background(), svc, "new-loaded-session")
	if err != nil {
		t.Fatalf("resolvePiSessionID() error = %v", err)
	}
	if sid != "new-loaded-session" {
		t.Fatalf("session id = %q, want new-loaded-session", sid)
	}
	if _, err := svc.Get(context.Background(), &adksession.GetRequest{
		AppName:   piagent.AppName,
		UserID:    piagent.DefaultUserID,
		SessionID: "new-loaded-session",
	}); err != nil {
		t.Fatalf("Get(created session) error = %v", err)
	}
}
