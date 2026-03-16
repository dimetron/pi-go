package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

func addTestEvents(t *testing.T, svc *FileService, sess session.Session, count int) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < count; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("message %d", i), genai.RoleUser)
		if err := svc.AppendEvent(ctx, sess, event); err != nil {
			t.Fatalf("AppendEvent(%d) error: %v", i, err)
		}
	}
}

func TestCreateBranch(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()

	addTestEvents(t, svc, resp.Session, 5)

	err := svc.CreateBranch(sessionID, "test-app", "test-user", "experiment")
	if err != nil {
		t.Fatalf("CreateBranch() error: %v", err)
	}

	// Verify branch appears in list.
	branches, active, err := svc.ListBranches(sessionID, "test-app", "test-user")
	if err != nil {
		t.Fatalf("ListBranches() error: %v", err)
	}
	if len(branches) != 2 {
		t.Errorf("ListBranches() returned %d branches, want 2", len(branches))
	}
	if active != "main" {
		t.Errorf("active branch = %q, want %q", active, "main")
	}

	// Find experiment branch.
	var expBranch BranchInfo
	for _, b := range branches {
		if b.Name == "experiment" {
			expBranch = b
		}
	}
	if expBranch.Name == "" {
		t.Fatal("experiment branch not found in list")
	}
	if expBranch.Parent == nil || *expBranch.Parent != "main" {
		t.Errorf("experiment parent = %v, want 'main'", expBranch.Parent)
	}
	if expBranch.ForkPoint != 4 { // 5 events, 0-indexed, head = 4
		t.Errorf("experiment fork point = %d, want 4", expBranch.ForkPoint)
	}
}

func TestCreateBranchDuplicateFails(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()
	addTestEvents(t, svc, resp.Session, 3)

	svc.CreateBranch(sessionID, "test-app", "test-user", "feat")
	err := svc.CreateBranch(sessionID, "test-app", "test-user", "feat")
	if err == nil {
		t.Error("expected error creating duplicate branch")
	}
}

func TestSwitchBranch(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()

	// Add 5 events on main.
	addTestEvents(t, svc, resp.Session, 5)

	// Create branch at event 4 (head).
	svc.CreateBranch(sessionID, "test-app", "test-user", "experiment")

	// Add 2 more events on main (events 5, 6).
	for i := 5; i < 7; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("main message %d", i), genai.RoleUser)
		svc.AppendEvent(ctx, resp.Session, event)
	}

	// Main should now have 7 events.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sessionID,
	})
	if getResp.Session.Events().Len() != 7 {
		t.Fatalf("main events = %d, want 7", getResp.Session.Events().Len())
	}

	// Switch to experiment branch (should have 5 events from fork point).
	err := svc.SwitchBranch(sessionID, "test-app", "test-user", "experiment")
	if err != nil {
		t.Fatalf("SwitchBranch() error: %v", err)
	}

	getResp, _ = svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sessionID,
	})
	if getResp.Session.Events().Len() != 5 {
		t.Errorf("experiment events = %d, want 5", getResp.Session.Events().Len())
	}

	// Verify active branch changed.
	if active := svc.ActiveBranch(sessionID); active != "experiment" {
		t.Errorf("active branch = %q, want %q", active, "experiment")
	}
}

func TestSwitchBranchBackToMain(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()

	addTestEvents(t, svc, resp.Session, 5)
	svc.CreateBranch(sessionID, "test-app", "test-user", "experiment")

	// Switch to experiment.
	svc.SwitchBranch(sessionID, "test-app", "test-user", "experiment")
	// Switch back to main.
	err := svc.SwitchBranch(sessionID, "test-app", "test-user", "main")
	if err != nil {
		t.Fatalf("SwitchBranch(main) error: %v", err)
	}

	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sessionID,
	})
	if getResp.Session.Events().Len() != 5 {
		t.Errorf("main events after switch back = %d, want 5", getResp.Session.Events().Len())
	}
	if active := svc.ActiveBranch(sessionID); active != "main" {
		t.Errorf("active branch = %q, want %q", active, "main")
	}
}

func TestSwitchBranchNonexistent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()

	err := svc.SwitchBranch(sessionID, "test-app", "test-user", "nonexistent")
	if err == nil {
		t.Error("expected error switching to nonexistent branch")
	}
}

func TestSwitchBranchSameIsNoop(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()

	// Switching to current branch should be a no-op.
	err := svc.SwitchBranch(sessionID, "test-app", "test-user", "main")
	if err != nil {
		t.Fatalf("SwitchBranch(same) error: %v", err)
	}
}

func TestListBranchesDefault(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	branches, active, err := svc.ListBranches(resp.Session.ID(), "test-app", "test-user")
	if err != nil {
		t.Fatalf("ListBranches() error: %v", err)
	}
	if len(branches) != 1 {
		t.Errorf("ListBranches() returned %d, want 1 (main)", len(branches))
	}
	if active != "main" {
		t.Errorf("active = %q, want %q", active, "main")
	}
	if branches[0].Name != "main" {
		t.Errorf("branch name = %q, want %q", branches[0].Name, "main")
	}
}

func TestBranchPersistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	svc1, _ := NewFileService(dir)
	resp, _ := svc1.Create(ctx, &session.CreateRequest{
		AppName:   "test-app",
		UserID:    "test-user",
		SessionID: "branch-persist",
	})
	addTestEvents(t, svc1, resp.Session, 5)
	svc1.CreateBranch("branch-persist", "test-app", "test-user", "feature")

	// New service instance (simulates restart).
	svc2, _ := NewFileService(dir)
	branches, active, err := svc2.ListBranches("branch-persist", "test-app", "test-user")
	if err != nil {
		t.Fatalf("ListBranches() after restart error: %v", err)
	}
	if len(branches) != 2 {
		t.Errorf("branches after restart = %d, want 2", len(branches))
	}
	if active != "main" {
		t.Errorf("active after restart = %q, want %q", active, "main")
	}
}

func TestBranchAddEventsAndSwitch(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()

	// Add 3 events on main.
	addTestEvents(t, svc, resp.Session, 3)

	// Create experiment branch (forks at head=2).
	svc.CreateBranch(sessionID, "test-app", "test-user", "experiment")

	// Switch to experiment and add 2 more events.
	svc.SwitchBranch(sessionID, "test-app", "test-user", "experiment")

	// Need to get fresh session ref after switch.
	getResp, _ := svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sessionID,
	})

	for i := 10; i < 12; i++ {
		event := &session.Event{
			ID:        fmt.Sprintf("exp-event-%d", i),
			Timestamp: time.Now().Add(time.Duration(i) * time.Second),
			Author:    "user",
		}
		event.Content = genai.NewContentFromText(fmt.Sprintf("experiment msg %d", i), genai.RoleUser)
		svc.AppendEvent(ctx, getResp.Session, event)
	}

	// Experiment should have 3 (forked) + 2 (new) = 5 events.
	getResp, _ = svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sessionID,
	})
	if getResp.Session.Events().Len() != 5 {
		t.Errorf("experiment events = %d, want 5", getResp.Session.Events().Len())
	}

	// Switch back to main — should still have 3 events.
	svc.SwitchBranch(sessionID, "test-app", "test-user", "main")
	getResp, _ = svc.Get(ctx, &session.GetRequest{
		AppName: "test-app", UserID: "test-user", SessionID: sessionID,
	})
	if getResp.Session.Events().Len() != 3 {
		t.Errorf("main events after branch work = %d, want 3", getResp.Session.Events().Len())
	}
}
