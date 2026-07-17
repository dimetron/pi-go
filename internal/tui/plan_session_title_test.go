package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dimetron/pi-go/internal/agent"
)

// TestStartPlanSession_DefaultTitleSeedsSessionTitle covers the new branch
// in startPlanSession that propagates the default title returned by
// agent.CreateSession to m.sessionTitle, so /plan sessions show a sensible
// label in the terminal window/tab title before the first turn completes.
//
// We do NOT stub gitToplevelFn / gitCurrentBranch (they're package vars in
// internal/agent and not reachable from the tui test package). Instead, the
// test chdirs into a real git repo (the pi-go root) so defaultSessionTitle()
// returns a non-empty "<repo> <branch> <folder>" value and the
// if-defaultTitle != "" branch is exercised.
func TestStartPlanSession_DefaultTitleSeedsSessionTitle(t *testing.T) {
	// Locate the pi-go repo root (the parent of internal/tui where this
	// test file lives) so the test runs from a real git work tree.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".git")); err != nil {
		t.Skipf("pi-go repo root %q is not a git repo; cannot exercise defaultTitle != \"\": %v", repoRoot, err)
	}
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir %q: %v", repoRoot, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevDir) })

	tmp := t.TempDir()
	specsRoot := filepath.Join(tmp, "specs", "tools")
	if err := os.MkdirAll(specsRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Pre-create a spec directory so createSpecSkeleton returns "already
	// exists" and the function re-enters with the existing dir. (We want
	// the existing-dir branch because that path doesn't write a new spec.)
	taskName := "001-existing"
	specDir := filepath.Join(specsRoot, taskName)
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatalf("mkdir spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "rough-idea.md"), []byte("# x"), 0o644); err != nil {
		t.Fatalf("write rough-idea: %v", err)
	}

	svc := &recordingSessionService{}
	ag, err := agent.New(agent.Config{
		Model:          &stubLLM{name: "stub"},
		SessionService: svc,
	})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	m := &model{
		cfg: Config{
			WorkDir:   tmp,
			Agent:     ag,
			SessionID: "old-session",
		},
		ctx:          ctx,
		cancel:       cancel,
		inputModel:   NewInputModel(make([]HistoryEntry, 0), nil, nil, ""),
		chatModel:    ChatModel{},
		themeManager: NewThemeManager(),
		face:         NewFaceRenderer(),
		width:        80,
		height:       24,
	}

	before := m.sessionTitle
	_, _ = m.startPlanSession("tools/"+taskName, "test idea", specDir)

	if m.sessionTitle == before {
		t.Errorf("expected sessionTitle to be set from defaultTitle, still %q", m.sessionTitle)
	}

	// The agent's session service should have recorded the title the same way
	// the in-memory model did, so /sessions listings stay consistent with
	// the terminal tab title.
	if got := svc.lastTitle(); got == "" {
		t.Error("expected the session service to receive a non-empty default title")
	}
}
