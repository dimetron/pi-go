package palace

import (
	"context"
	"os"
	"os/exec"
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

func TestMineProject_WithProgress(t *testing.T) {
	tmpDir := t.TempDir()

	writeTestFile(t, filepath.Join(tmpDir, "main.go"),
		"package main\n\n// Main function with enough content to pass the minimum chunk size for the miner.\nfunc main() {\n\tprintln(\"progress test\")\n}")

	p := newTestPalace(t)
	ctx := context.Background()

	var progressCalls int
	cfg := &MineConfig{
		Wing: "test",
		Progress: func(file string, added, skipped, errors int) {
			progressCalls++
		},
	}

	_, err := MineProject(ctx, p, tmpDir, cfg)
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}
	if progressCalls == 0 {
		t.Error("expected progress callback to be called")
	}
}

func TestMineProject_EmptyContentSkipped(t *testing.T) {
	tmpDir := t.TempDir()

	// Empty file and whitespace-only file should be skipped
	writeTestFile(t, filepath.Join(tmpDir, "empty.go"), "")
	writeTestFile(t, filepath.Join(tmpDir, "spaces.go"), "   \n\t\n  ")

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineProject(ctx, p, tmpDir, &MineConfig{Wing: "test"})
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}
	if result.Added != 0 {
		t.Errorf("expected 0 added for empty files, got %d", result.Added)
	}
}

func TestMineProject_NilConfig_WithYAML(t *testing.T) {
	tmpDir := t.TempDir()

	yamlContent := `wing: configured-wing
rooms:
  - name: core
    keywords: ["main"]
`
	writeTestFile(t, filepath.Join(tmpDir, "mempalace.yaml"), yamlContent)
	writeTestFile(t, filepath.Join(tmpDir, "app.go"),
		"package main\n\n// Application entry point with enough content for chunk threshold.\nfunc main() {\n\tprintln(\"hello world from configured project\")\n}")

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineProject(ctx, p, tmpDir, nil)
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}
	if result.Added == 0 {
		t.Error("expected at least 1 added")
	}

	drawers, _ := p.ListDrawers(ctx, DrawerFilter{Wing: "configured-wing"})
	if len(drawers) == 0 {
		t.Error("expected drawers in configured-wing from mempalace.yaml")
	}
}

func TestMineProject_UnsupportedExtSkipped(t *testing.T) {
	tmpDir := t.TempDir()

	writeTestFile(t, filepath.Join(tmpDir, "image.png"), "binary content here enough data")
	writeTestFile(t, filepath.Join(tmpDir, "data.csv"), "col1,col2\nval1,val2\n")

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineProject(ctx, p, tmpDir, &MineConfig{Wing: "test"})
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}
	if result.Processed != 0 {
		t.Errorf("expected 0 processed for unsupported extensions, got %d", result.Processed)
	}
}

// TestMineProject_GitNestedGitignore tests that nested .gitignore files are
// respected via the git-based check.
func TestMineProject_GitNestedGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tmpDir := t.TempDir()

	// Create directory structure.
	os.MkdirAll(filepath.Join(tmpDir, "src", "gen"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "src", "core"), 0o755)

	// Root .gitignore ignores all generated files.
	writeTestFile(t, filepath.Join(tmpDir, ".gitignore"), "*.gen.go\n")
	// Nested .gitignore un-ignores a specific file (negation).
	writeTestFile(t, filepath.Join(tmpDir, "src", "gen", ".gitignore"), "!keep.gen.go\n")

	writeTestFile(t, filepath.Join(tmpDir, "src", "core", "main.go"),
		"package core\n\n// Main function with enough content to pass the minimum chunk size threshold for the miner test.\\nfunc main() {}")
	writeTestFile(t, filepath.Join(tmpDir, "src", "gen", "auto.gen.go"),
		"package gen\n\n// Auto-generated file with enough content to pass the minimum chunk size threshold for the miner test.")
	writeTestFile(t, filepath.Join(tmpDir, "src", "gen", "keep.gen.go"),
		"package gen\n\n// This file is un-ignored by negation with enough content to pass the minimum chunk size threshold.")

	gitInitRepo(t, tmpDir)

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineProject(ctx, p, tmpDir, &MineConfig{Wing: "test"})
	if err != nil {
		t.Fatalf("MineProject: %v", err)
	}
	if result.Added == 0 {
		t.Error("expected at least 1 added drawer")
	}

	drawers, _ := p.ListDrawers(ctx, DrawerFilter{Wing: "test"})

	// auto.gen.go should be ignored by root .gitignore pattern *.gen.go.
	for _, d := range drawers {
		if filepath.Base(d.SourceFile) == "auto.gen.go" {
			t.Error("should not have mined auto.gen.go (gitignored by *.gen.go)")
		}
	}

	// keep.gen.go should be included (negation in nested .gitignore).
	foundKeep := false
	for _, d := range drawers {
		if filepath.Base(d.SourceFile) == "keep.gen.go" {
			foundKeep = true
		}
	}
	if !foundKeep {
		t.Error("should have mined keep.gen.go (un-ignored by nested !keep.gen.go)")
	}
}

// TestMineConversations_GitignoreRespected tests that conversation mining
// respects .gitignore when the directory is a git repo.
func TestMineConversations_GitignoreRespected(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	tmpDir := t.TempDir()

	os.MkdirAll(filepath.Join(tmpDir, "sessions"), 0o755)
	os.MkdirAll(filepath.Join(tmpDir, "archived"), 0o755)

	writeTestFile(t, filepath.Join(tmpDir, ".gitignore"), "archived/\n")
	writeTestFile(t, filepath.Join(tmpDir, "sessions", "chat.jsonl"),
		`{"role":"user","content":"What is authentication?"}`+"\n"+
			`{"role":"assistant","content":"Authentication verifies user identity."}`+"\n")
	writeTestFile(t, filepath.Join(tmpDir, "archived", "old.jsonl"),
		`{"role":"user","content":"Old conversation should be ignored by gitignore for testing."}`+"\n"+
			`{"role":"assistant","content":"Yes this is archived and should not be mined."}`+"\n")

	gitInitRepo(t, tmpDir)

	p := newTestPalace(t)
	ctx := context.Background()

	result, err := MineConversations(ctx, p, tmpDir, &MineConfig{Wing: "testconv"})
	if err != nil {
		t.Fatalf("MineConversations: %v", err)
	}
	if result.Added == 0 {
		t.Error("expected at least 1 added exchange")
	}

	drawers, _ := p.ListDrawers(ctx, DrawerFilter{Wing: "testconv"})
	for _, d := range drawers {
		if filepath.Dir(d.SourceFile) == "archived" {
			t.Error("should not have mined files in archived/ (gitignored)")
		}
	}
}

// --- helpers ---

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// gitInitRepo initializes a git repo in the given directory and commits all
// files. This is needed so `git ls-files --ignored` works correctly.
func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "test@test.com"},
		{"config", "user.name", "Test"},
		// This repo sets commit.gpgsign globally and signs through 1Password's
		// op-ssh-sign, which a temp repo inherits. Without turning it off the
		// commit below blocks on the 1Password agent and fails after a 60s
		// timeout — a failure that says nothing about the miner under test.
		{"config", "commit.gpgsign", "false"},
		{"config", "tag.gpgsign", "false"},
		{"add", "-A"},
		{"commit", "-m", "init", "--allow-empty"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// newTestPalace creates an in-memory palace for testing.
func newTestPalace(t *testing.T) *Palace {
	t.Helper()
	store := newTestStore(t)
	return NewWithStore(store, nil)
}
