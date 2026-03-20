package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

func TestLoadBranchEvents_BranchDirExists(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()
	addTestEvents(t, svc, resp.Session, 3)

	// CreateBranch writes branch events to branches/<name>/events.jsonl.
	if err := svc.CreateBranch(sessionID, "test-app", "test-user", "mybranch"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	sessionDir := filepath.Join(svc.baseDir, sessionID)
	events, err := svc.loadBranchEvents(sessionDir, "mybranch")
	if err != nil {
		t.Fatalf("loadBranchEvents(mybranch): %v", err)
	}
	if len(events) != 3 {
		t.Errorf("loaded %d events, want 3", len(events))
	}
}

func TestLoadBranchEvents_MainFallsBackToRoot(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()
	addTestEvents(t, svc, resp.Session, 2)

	sessionDir := filepath.Join(svc.baseDir, sessionID)
	// "main" has no branch dir yet — falls back to root events.jsonl.
	events, err := svc.loadBranchEvents(sessionDir, "main")
	if err != nil {
		t.Fatalf("loadBranchEvents(main): %v", err)
	}
	if len(events) != 2 {
		t.Errorf("loaded %d events from main, want 2", len(events))
	}
}

func TestLoadBranchEvents_UnknownBranchErrors(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionDir := filepath.Join(svc.baseDir, resp.Session.ID())
	_, err := svc.loadBranchEvents(sessionDir, "ghost")
	if err == nil {
		t.Error("expected error for nonexistent non-main branch, got nil")
	}
}

func TestSaveBranchEventsCreatesDir(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()
	addTestEvents(t, svc, resp.Session, 2)

	// loadSession to get the internal fileSession.
	svc.mu.Lock()
	sess, err := svc.loadSession(sessionID, "test-app", "test-user")
	svc.mu.Unlock()
	if err != nil {
		t.Fatalf("loadSession: %v", err)
	}

	sessionDir := filepath.Join(svc.baseDir, sessionID)
	// Branch dir doesn't exist yet; saveBranchEvents should create it.
	if err := svc.saveBranchEvents(sessionDir, "newbranch", sess); err != nil {
		t.Fatalf("saveBranchEvents: %v", err)
	}

	branchDir := filepath.Join(sessionDir, "branches", "newbranch")
	if _, statErr := os.Stat(filepath.Join(branchDir, "events.jsonl")); statErr != nil {
		t.Errorf("branch events.jsonl not created: %v", statErr)
	}
}

func TestSaveBranchesAndLoadBranches(t *testing.T) {
	dir := t.TempDir()

	bs := &branchState{
		Active: "feature",
		Branches: map[string]BranchInfo{
			"main":    {Name: "main", Head: 5, Parent: nil, ForkPoint: 0},
			"feature": {Name: "feature", Head: 3, Parent: strPtr("main"), ForkPoint: 3},
		},
	}
	if err := saveBranches(dir, bs); err != nil {
		t.Fatalf("saveBranches: %v", err)
	}

	loaded, err := loadBranches(dir)
	if err != nil {
		t.Fatalf("loadBranches: %v", err)
	}
	if loaded.Active != "feature" {
		t.Errorf("active = %q, want feature", loaded.Active)
	}
	if len(loaded.Branches) != 2 {
		t.Errorf("branches count = %d, want 2", len(loaded.Branches))
	}
	feat := loaded.Branches["feature"]
	if feat.Head != 3 {
		t.Errorf("feature head = %d, want 3", feat.Head)
	}
	if feat.Parent == nil || *feat.Parent != "main" {
		t.Errorf("feature parent = %v, want main", feat.Parent)
	}
}

func TestLoadBranchesDefault(t *testing.T) {
	dir := t.TempDir()
	// No branches.json — should return default single main branch.
	bs, err := loadBranches(dir)
	if err != nil {
		t.Fatalf("loadBranches on missing file: %v", err)
	}
	if bs.Active != "main" {
		t.Errorf("default active = %q, want main", bs.Active)
	}
	if len(bs.Branches) != 1 {
		t.Errorf("default branches count = %d, want 1", len(bs.Branches))
	}
	main := bs.Branches["main"]
	if main.Name != "main" {
		t.Errorf("default branch name = %q, want main", main.Name)
	}
	if main.Parent != nil {
		t.Errorf("main parent should be nil, got %v", main.Parent)
	}
}

func TestLoadBranchesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "branches.json"), []byte("not json"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := loadBranches(dir)
	if err == nil {
		t.Error("expected error parsing invalid branches.json, got nil")
	}
}

func TestActiveBranchDefaultsToMain(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})

	// No branches.json yet — should default to "main".
	active := svc.ActiveBranch(resp.Session.ID())
	if active != "main" {
		t.Errorf("ActiveBranch() = %q, want main", active)
	}
}

func TestListBranchesInvalidSession(t *testing.T) {
	svc := newTestService(t)
	_, _, err := svc.ListBranches("nonexistent-id", "test-app", "test-user")
	if err == nil {
		t.Error("expected error listing branches of nonexistent session")
	}
}

// strPtr is a helper to get a pointer to a string value.
func strPtr(s string) *string { return &s }

func TestActiveBranch(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	resp, _ := svc.Create(ctx, &session.CreateRequest{
		AppName: "test-app",
		UserID:  "test-user",
	})
	sessionID := resp.Session.ID()

	// Create a branch
	if err := svc.CreateBranch(sessionID, "test-app", "test-user", "feature"); err != nil {
		t.Fatal(err)
	}
	if err := svc.SwitchBranch(sessionID, "test-app", "test-user", "feature"); err != nil {
		t.Fatal(err)
	}

	// ActiveBranch should return the current branch
	active := svc.ActiveBranch(sessionID)
	if active != "feature" {
		t.Errorf("active branch = %q, want %q", active, "feature")
	}
}
