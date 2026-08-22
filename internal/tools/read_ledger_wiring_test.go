package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newLedgerToolPair builds read and write over one sandbox and one shared
// ledger, the way the registry wires them in production.
func newLedgerToolPair(t *testing.T, dir string) (readTool, writeTool any) {
	t.Helper()
	sb, err := NewSandbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The sandbox holds an os.Root on dir. Leaving it open makes t.TempDir's
	// own cleanup fail on Windows, which refuses to remove a directory that
	// something still has a handle to.
	t.Cleanup(func() { _ = sb.Close() })
	ledger := NewReadLedger()
	rt, err := newReadTool(sb, ledger)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := newWriteTool(sb, ledger)
	if err != nil {
		t.Fatal(err)
	}
	return rt, wt
}

func runWrite(t *testing.T, writeTool any, path, content string) error {
	t.Helper()
	r, ok := writeTool.(runnableTool)
	if !ok {
		t.Fatalf("write tool %T does not implement Run", writeTool)
	}
	_, err := r.Run(mockToolCtx{Context: t.Context()}, map[string]any{
		"file_path": path,
		"content":   content,
	})
	return err
}

// The read tool must record into the ledger that write gates on. Threading the
// ledger only as far as the constructor leaves every overwrite of an existing
// file rejected, and re-reading cannot clear it — reading is what was supposed
// to record.
func TestReadTool_RecordsIntoLedgerSoWriteIsAllowed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readTool, writeTool := newLedgerToolPair(t, dir)

	runTool(t, readTool, map[string]any{"file_path": path})

	if err := runWrite(t, writeTool, path, "replaced\n"); err != nil {
		t.Fatalf("write after a full read was rejected: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "replaced\n" {
		t.Errorf("file content = %q, want %q", got, "replaced\n")
	}
}

// Overwriting a file that was never read stays refused — the gate is the point
// of the ledger, and wiring it must not disable it.
func TestReadTool_UnreadFileStillRefusesOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "untouched.txt")
	if err := os.WriteFile(path, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, writeTool := newLedgerToolPair(t, dir)

	err := runWrite(t, writeTool, path, "replaced\n")
	if err == nil {
		t.Fatal("overwriting an unread file should be refused")
	}
	if !strings.Contains(err.Error(), "has not been read yet") {
		t.Errorf("error = %v, want it to name the unread file", err)
	}
}

// A windowed read is a partial view: overwriting on the strength of it would
// discard lines the agent never saw, so it must still be refused.
func TestReadTool_PartialReadDoesNotUnlockOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "long.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 50)), 0o644); err != nil {
		t.Fatal(err)
	}

	readTool, writeTool := newLedgerToolPair(t, dir)

	// Read a window, not the whole file.
	runTool(t, readTool, map[string]any{"file_path": path, "limit": 10})

	err := runWrite(t, writeTool, path, "replaced\n")
	if err == nil {
		t.Fatal("overwriting after a partial read should be refused")
	}
	if !strings.Contains(err.Error(), "part of") {
		t.Errorf("error = %v, want it to name the partial read", err)
	}
}

// Creating a new file needs no prior read.
func TestReadTool_NewFileNeedsNoRead(t *testing.T) {
	dir := t.TempDir()
	_, writeTool := newLedgerToolPair(t, dir)

	if err := runWrite(t, writeTool, filepath.Join(dir, "brand-new.txt"), "hello\n"); err != nil {
		t.Fatalf("creating a new file should not require a read: %v", err)
	}
}
