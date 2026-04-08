package palace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMineProject_BasicFixture(t *testing.T) {
	// Create a test fixture directory.
	tmpDir := t.TempDir()

	// Create subdirectories.
	os.MkdirAll(filepath.Join(tmpDir, "auth"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "api"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0o755)

	// Create source files.
	writeTestFile(t, filepath.Join(tmpDir, "auth", "handler.go"),
		"package auth\n\n// Handler validates JWT tokens and manages user sessions.\nfunc Handler() {\n\t// Authentication logic here with sufficient content to pass the minimum chunk size threshold for testing purposes.\n}")
	writeTestFile(t, filepath.Join(tmpDir, "api", "router.go"),
		"package api\n\n// Router sets up HTTP routes for the application.\nfunc Router() {\n\t// Routing logic here with sufficient content to pass the minimum chunk size threshold for testing purposes.\n}")
	writeTestFile(t, filepath.Join(tmpDir, "README.md"),
		"# Test Project\n\nThis is a test project for mining with enough content to pass the minimum chunk size threshold for unit testing purposes.")

	// Create a palace with in-memory DB.
	p := newTestPalace(t)
	ctx := context.Background()

	cfg := &MineConfig{
		Wing: "testproject",
		Rooms: []RoomDef{
			{Name: "auth", Patterns: []string{"auth/*"}},
			{Name: "api", Patterns: []string{"api/*"}},
		},
	}

	result, err := MineProject(ctx, p, tmpDir, cfg)
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}

	if result.Added == 0 {
		t.Error("expected at least 1 added drawer")
	}
	if result.Errors != 0 {
		t.Errorf("expected 0 errors, got %d", result.Errors)
	}

	// Verify drawers are in the palace.
	drawers, err := p.ListDrawers(ctx, DrawerFilter{Wing: "testproject"})
	if err != nil {
		t.Fatalf("ListDrawers: %v", err)
	}
	if len(drawers) == 0 {
		t.Error("expected drawers in palace")
	}

	// Check that .git was skipped.
	for _, d := range drawers {
		if d.SourceFile == ".git" {
			t.Error("should not have mined .git directory")
		}
	}
}

func TestMineProject_GitignoreRespected(t *testing.T) {
	tmpDir := t.TempDir()

	os.MkdirAll(filepath.Join(tmpDir, "src"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "logs"), 0o755)

	writeTestFile(t, filepath.Join(tmpDir, ".gitignore"), "logs\n")
	writeTestFile(t, filepath.Join(tmpDir, "src", "main.go"),
		"package main\n\n// Main function starts the application with enough content to pass the minimum chunk size filter.\nfunc main() {\n\tprintln(\"hello\")\n}")
	writeTestFile(t, filepath.Join(tmpDir, "logs", "app.txt"),
		"This is a log file with sufficient content for the minimum chunk size threshold during testing purposes of the miner.")

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineProject(ctx, p, tmpDir, &MineConfig{Wing: "test"})
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}

	// Check that log files were skipped via gitignore.
	drawers, _ := p.ListDrawers(ctx, DrawerFilter{Wing: "test"})
	for _, d := range drawers {
		if filepath.Dir(d.SourceFile) == "logs" {
			t.Error("should not have mined files in logs/ (gitignored)")
		}
	}

	if result.Added == 0 {
		t.Error("should have added at least one drawer from src/")
	}
}

func TestMineProject_DuplicateSkip(t *testing.T) {
	tmpDir := t.TempDir()

	writeTestFile(t, filepath.Join(tmpDir, "file.go"),
		"package main\n\n// Exact same content should be deduplicated when mining the same file twice with enough content here.\nfunc main() {}")

	p := newTestPalace(t)
	ctx := context.Background()

	cfg := &MineConfig{Wing: "test"}

	// Mine twice.
	result1, _ := MineProject(ctx, p, tmpDir, cfg)
	result2, _ := MineProject(ctx, p, tmpDir, cfg)

	if result1.Added == 0 {
		t.Error("first mine should add drawers")
	}
	// Second mine: same content generates same drawer ID, so InsertDrawer
	// may either skip or error depending on store behavior.
	// The key assertion is that we don't double the drawers.
	drawers, _ := p.ListDrawers(ctx, DrawerFilter{Wing: "test"})
	if len(drawers) > result1.Added {
		t.Errorf("expected no more than %d drawers after re-mine, got %d", result1.Added, len(drawers))
	}
	_ = result2
}

func TestMineProject_NilConfig(t *testing.T) {
	tmpDir := t.TempDir()

	writeTestFile(t, filepath.Join(tmpDir, "main.go"),
		"package main\n\n// Main starts the app with enough content to pass the minimum chunk size for the miner.\nfunc main() {\n\tprintln(\"test\")\n}")

	p := newTestPalace(t)
	ctx := context.Background()

	// Pass nil config — should use directory basename as wing.
	result, err := MineProject(ctx, p, tmpDir, nil)
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}
	if result.Added == 0 {
		t.Error("expected at least 1 added drawer")
	}
}

func TestMineProject_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineProject(ctx, p, tmpDir, &MineConfig{Wing: "empty"})
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}
	if result.Added != 0 {
		t.Errorf("expected 0 added, got %d", result.Added)
	}
}

// --- helpers ---

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newTestPalace creates an in-memory palace for testing.
func newTestPalace(t *testing.T) *Palace {
	t.Helper()
	store := newTestStore(t)
	return NewWithStore(store, nil)
}
