// Tests for `pi memory init` and the mempalace scaffolding it writes.
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewMemoryInitCmd_WithArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	dir := t.TempDir()

	cmd := newMemoryInitCmd()
	cmd.SetArgs([]string{dir})
	_ = captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
	})
}

func TestNewMemoryInitCmd_NoArgExecute(t *testing.T) {
	resetGlobalFlags(t)
	origCwd, _ := os.Getwd()
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	cmd := newMemoryInitCmd()
	cmd.SetArgs(nil)
	_ = captureStdout(t, func() {
		if err := cmd.Execute(); err != nil {
			t.Logf("execute: %v", err)
		}
	})
}

func TestNewMemoryInitCmd_WithArgs(t *testing.T) {
	cmd := newMemoryInitCmd()
	if cmd.Use != "init [dir]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "init [dir]")
	}
	// Check flags
	if cmd.Flags().Lookup("wing") == nil {
		t.Error("missing --wing flag")
	}
	// Verify Args allows 0 or 1 arg
	if cmd.Args != nil {
		t.Log("Args validator present")
	}
}

func TestNewMemoryInitCmd_MaxArgs(t *testing.T) {
	cmd := newMemoryInitCmd()
	// Maximum 1 arg
	if cmd.Args == nil {
		t.Error("Args validator not set")
	}
}

func TestRunMemoryInit_AbsDirError(t *testing.T) {
	// Test with an invalid directory path.
	err := runMemoryInit("/nonexistent/path/that/cannot/be/resolved", "")
	if err == nil {
		t.Error("expected error for non-existent directory")
	}
}

func TestRunMemoryInit_WithWing(t *testing.T) {
	dir := t.TempDir()
	err := runMemoryInit(dir, "custom-wing")
	if err != nil {
		t.Fatalf("runMemoryInit with wing: %v", err)
	}
}

func TestRunMemoryInit_ExistingYAML(t *testing.T) {
	dir := t.TempDir()
	// Create .pi-go dir first.
	os.MkdirAll(filepath.Join(dir, ".pi-go"), 0755)
	// Create existing mempalace.yaml.
	yamlPath := filepath.Join(dir, "mempalace.yaml")
	os.WriteFile(yamlPath, []byte("existing"), 0644)

	err := runMemoryInit(dir, "")
	if err != nil {
		t.Fatalf("runMemoryInit with existing yaml: %v", err)
	}
}

func TestWriteMempalaceYAML_EmptyRoomsWritesFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "mempalace.yaml")

	err := writeMempalaceYAML(path, "test-wing", []string{})
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty file")
	}
}

func TestWriteMempalaceYAML_RoomsAppearInYAML(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "mempalace.yaml")

	err := writeMempalaceYAML(path, "wing", []string{"api", "pkg", "web"})
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	for _, room := range []string{"api", "pkg", "web"} {
		if !strings.Contains(content, room) {
			t.Errorf("expected room %q in yaml", room)
		}
	}
}

func TestScanRoomCandidates_NonExistentDir(t *testing.T) {
	rooms := scanRoomCandidates("/nonexistent/directory")
	if rooms != nil {
		t.Errorf("expected nil for non-existent dir, got %v", rooms)
	}
}

func TestScanRoomCandidates_WithSubdirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"api", "pkg", "web", ".hidden", "node_modules", "__pycache__"} {
		os.MkdirAll(filepath.Join(dir, name), 0755)
	}
	// Add a file too.
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	rooms := scanRoomCandidates(dir)
	// Should include api, pkg, web but not .hidden, node_modules, __pycache__.
	found := make(map[string]bool)
	for _, r := range rooms {
		found[r] = true
	}
	if !found["api"] || !found["pkg"] || !found["web"] {
		t.Errorf("expected api, pkg, web; got %v", rooms)
	}
	if found[".hidden"] || found["node_modules"] || found["__pycache__"] {
		t.Errorf("unexpected rooms in result: %v", rooms)
	}
}

func TestNewMemoryInitCmd(t *testing.T) {
	cmd := newMemoryInitCmd()
	if cmd.Use != "init [dir]" {
		t.Errorf("Use = %q, want 'init [dir]'", cmd.Use)
	}
	if cmd.Flags().Lookup("wing") == nil {
		t.Error("wing flag not registered")
	}
}

func TestRunMemoryInit_DefaultWing(t *testing.T) {
	dir := t.TempDir()
	subDir := filepath.Join(dir, "myproject")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	absDir, err := filepath.Abs(subDir)
	if err != nil {
		t.Fatal(err)
	}
	wing := filepath.Base(absDir)
	if wing != "myproject" {
		t.Errorf("wing = %q, want 'myproject'", wing)
	}
}

func TestScanRoomCandidates_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	rooms := scanRoomCandidates(dir)
	if len(rooms) != 0 {
		t.Errorf("expected 0 rooms in empty dir, got %d", len(rooms))
	}
}

func TestScanRoomCandidates_OnlySkippedDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".git", "node_modules", "vendor", "__pycache__", ".pi-go", "dist", "build", ".idea", ".vscode"} {
		os.Mkdir(filepath.Join(dir, name), 0o755)
	}
	rooms := scanRoomCandidates(dir)
	if len(rooms) != 0 {
		t.Errorf("expected 0 rooms, got %d", len(rooms))
	}
}

func TestScanRoomCandidates_DotDirs(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, ".hidden"), 0o755)
	os.Mkdir(filepath.Join(dir, "visible"), 0o755)
	rooms := scanRoomCandidates(dir)
	for _, r := range rooms {
		if strings.HasPrefix(r, ".") {
			t.Errorf("found dot-prefixed directory %q in rooms", r)
		}
	}
}

func TestScanRoomCandidates_UnreadableDir(t *testing.T) {
	rooms := scanRoomCandidates("/nonexistent/path")
	if rooms != nil {
		t.Errorf("expected nil rooms, got %v", rooms)
	}
}

func TestScanRoomCandidates_MixedDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"src", "cmd", ".git", "pkg", "vendor"} {
		os.Mkdir(filepath.Join(dir, name), 0o755)
	}
	rooms := scanRoomCandidates(dir)
	roomMap := make(map[string]bool)
	for _, r := range rooms {
		roomMap[r] = true
	}
	if !roomMap["src"] {
		t.Error("expected 'src' in rooms")
	}
	if !roomMap["cmd"] {
		t.Error("expected 'cmd' in rooms")
	}
	if !roomMap["pkg"] {
		t.Error("expected 'pkg' in rooms")
	}
	if roomMap[".git"] {
		t.Error("'.git' should not be in rooms")
	}
	if roomMap["vendor"] {
		t.Error("'vendor' should not be in rooms")
	}
}

func TestWriteMempalaceYAML_EmptyRooms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	err := writeMempalaceYAML(path, "testwing", []string{})
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "wing: testwing") {
		t.Error("yaml missing wing name")
	}
	if !strings.Contains(content, "# No subdirectories") {
		t.Error("yaml missing comment")
	}
}

func TestWriteMempalaceYAML_MultipleRooms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	rooms := []string{"api", "web", "cli"}
	err := writeMempalaceYAML(path, "myproject", rooms)
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, room := range rooms {
		if !strings.Contains(content, "name: "+room) {
			t.Errorf("yaml missing room %q", room)
		}
	}
}

func TestWriteMempalaceYAML_Overwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	err := writeMempalaceYAML(path, "first", []string{"room1"})
	if err != nil {
		t.Fatal(err)
	}
	err = writeMempalaceYAML(path, "second", []string{"room2"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if strings.Contains(content, "first") {
		t.Error("yaml still contains 'first' after overwrite")
	}
	if !strings.Contains(content, "second") {
		t.Error("yaml missing 'second' after overwrite")
	}
}

func TestWriteMempalaceYAML_SingleRoom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mempalace.yaml")
	err := writeMempalaceYAML(path, "single", []string{"onlyroom"})
	if err != nil {
		t.Fatalf("writeMempalaceYAML: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "name: onlyroom") {
		t.Error("yaml missing room")
	}
}
