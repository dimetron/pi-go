package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryCmd_SubcommandsRegistered(t *testing.T) {
	cmd := newMemoryCmd()

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}

	for _, name := range []string{"model", "init", "status", "mine", "search", "kg", "wake-up", "recent"} {
		if !subcommands[name] {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestMemoryModelCmd_SubcommandsRegistered(t *testing.T) {
	cmd := newMemoryModelCmd()

	subcommands := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subcommands[sub.Name()] = true
	}

	for _, name := range []string{"download", "status"} {
		if !subcommands[name] {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestMemoryModelStatus_NoModel(t *testing.T) {
	// Point to a nonexistent path so it reports "not downloaded".
	err := runMemoryModelStatus(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryInit_CreatesDB(t *testing.T) {
	dir := t.TempDir()
	// Create a subdirectory to be detected as a room candidate.
	if err := os.Mkdir(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := runMemoryInit(dir, "testproject")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify database was created.
	dbPath := filepath.Join(dir, ".pi-go", "palace.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("palace.db was not created")
	}

	// Verify mempalace.yaml was created.
	yamlPath := filepath.Join(dir, "mempalace.yaml")
	if _, err := os.Stat(yamlPath); os.IsNotExist(err) {
		t.Error("mempalace.yaml was not created")
	}

	// Verify yaml content references the wing and rooms.
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !contains(content, "wing: testproject") {
		t.Error("yaml missing wing name")
	}
	if !contains(content, "name: internal") {
		t.Error("yaml missing internal room")
	}
	if !contains(content, "name: cmd") {
		t.Error("yaml missing cmd room")
	}
}

func TestMemoryInit_ExistingYAML(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "mempalace.yaml")
	if err := os.WriteFile(yamlPath, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runMemoryInit(dir, "test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify existing yaml was not overwritten.
	data, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Error("existing mempalace.yaml was overwritten")
	}
}

func TestMemoryStatus_NoDB(t *testing.T) {
	// Point to a nonexistent path.
	err := runMemoryStatus(filepath.Join(t.TempDir(), "nonexistent.db"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMemoryStatus_EmptyPalace(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "palace.db")

	// Init to create the DB.
	err := runMemoryInit(dir, "test")
	if err != nil {
		t.Fatalf("init error: %v", err)
	}

	// Now check status using the created DB.
	err = runMemoryStatus(filepath.Join(dir, ".pi-go", "palace.db"))
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	_ = dbPath // used via init
}

func TestScanRoomCandidates(t *testing.T) {
	dir := t.TempDir()

	// Create various directories.
	for _, name := range []string{"internal", "cmd", ".git", "node_modules", "pkg"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Create a file (should be skipped).
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	rooms := scanRoomCandidates(dir)

	roomSet := make(map[string]bool)
	for _, r := range rooms {
		roomSet[r] = true
	}

	if !roomSet["internal"] || !roomSet["cmd"] || !roomSet["pkg"] {
		t.Errorf("expected internal, cmd, pkg in rooms, got %v", rooms)
	}
	if roomSet[".git"] {
		t.Error(".git should be excluded")
	}
	if roomSet["node_modules"] {
		t.Error("node_modules should be excluded")
	}
}

func TestNewMemoryStatusCmd_RunE(t *testing.T) {
	resetGlobalFlags(t)
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "palace.db")
	cmd := newMemoryStatusCmd()
	cmd.SetArgs([]string{"--db", dbPath})
	_ = captureStdout(t, func() {
		_ = cmd.Execute()
	})
}
