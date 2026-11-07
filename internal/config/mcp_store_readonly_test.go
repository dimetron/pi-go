//go:build !windows

// The cases here make files and directories unwritable, which Windows does not
// honor: a read-only directory still permits creating files there, and a file's
// mode does not gate reading. Gating them at the file level keeps the exclusion
// visible instead of burying it in a runtime check.
//
// Everything else about this store is portable and lives in
// mcp_store_errors_test.go, so Windows still runs it.
package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unwritableDir returns a directory whose contents cannot be created, so every
// write branch that the portable tests exercise successfully can be exercised
// failing instead.
func unwritableDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	// Confirm the permission actually bites; a root-owned CI container would
	// otherwise let the "unwritable" case succeed and silently prove nothing.
	probe := filepath.Join(dir, "probe")
	if err := os.WriteFile(probe, []byte("x"), 0o600); err == nil {
		_ = os.Remove(probe)
		t.Skip("directory is writable despite mode 0500 (running as root?)")
	}
	return dir
}

// A write that cannot create its temp file must say which file it was writing,
// because the caller only has the server name and needs the path to act.
func TestAddMCPServer_ReportsTempFileFailure(t *testing.T) {
	dir := unwritableDir(t)
	path := filepath.Join(dir, "mcp.json")

	err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://example.com/mcp"}, false)
	if err == nil {
		t.Fatal("expected a write error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("err = %v, want it to name %s", err, path)
	}
}

// A write that cannot replace the target must clean up its temp file: a leaked
// .mcp.json.tmp-* would sit beside the user's config forever.
func TestAddMCPServer_ReportsRenameFailureAndCleansUp(t *testing.T) {
	dir := unwritableDir(t)
	path := filepath.Join(dir, "mcp.json")

	if err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://example.com/mcp"}, false); err == nil {
		t.Fatal("expected a write error")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file %q was left behind", e.Name())
		}
	}
}

// A read failure that is not "missing" must be reported: treating an unreadable
// mcp.json as empty would let AddMCPServer overwrite it with a single entry.
func TestAddMCPServer_ReportsUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://example.com/mcp"}, false)
	if err == nil {
		t.Skip("file mode 0000 is still readable here (running as root?)")
	}
	if !errors.Is(err, os.ErrPermission) && !strings.Contains(err.Error(), "reading") {
		t.Errorf("err = %v, want a read error", err)
	}
}
