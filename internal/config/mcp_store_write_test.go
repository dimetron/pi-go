package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// failingFile is an *os.File whose writes and syncs fail. os.CreateTemp returns a
// real, successfully created file, so the only way to reach the write/sync error
// branches is to hand back one of these in its place.
//
// Both *os.File and this type satisfy the io.Writer interface the code uses, but
// the code needs *os.File for Close, so the branch is reached by closing the file
// underneath it: a write to a closed file fails deterministically and portably.
func TestWriteJSONDocument_ReportsWriteFailureAndCleansUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")

	prev := mcpCreateTemp
	t.Cleanup(func() { mcpCreateTemp = prev })
	mcpCreateTemp = func(dir, pattern string) (*os.File, error) {
		f, err := os.CreateTemp(dir, pattern)
		if err != nil {
			return nil, err
		}
		// Closed before use: every subsequent Write and Sync reports an error,
		// which is exactly the branch under test.
		_ = f.Close()
		return f, nil
	}

	err := writeJSONDocument(path, map[string]any{"mcpServers": map[string]any{}})
	if err == nil {
		t.Fatal("expected the write to fail on a closed file")
	}

	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("reading dir: %v", readErr)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file %q was left behind after a failed write", e.Name())
		}
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a failed write still produced the target file")
	}
}

// A directory that cannot be created is the first failure writeJSONDocument can
// hit. It must name the directory so the caller knows where the problem is.
func TestWriteJSONDocument_ReportsMkdirFailure(t *testing.T) {
	// A regular file where a directory needs to be: MkdirAll fails even for a
	// user who could otherwise write anywhere.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "nested", "mcp.json")

	err := writeJSONDocument(path, map[string]any{})
	if err == nil {
		t.Fatal("expected an error creating the directory")
	}
	if !strings.Contains(err.Error(), "creating") {
		t.Errorf("err = %v, want it to say the directory could not be created", err)
	}
}

// The document written must end in a newline and be indented: mcp.json is meant
// to be read and hand-edited by people, and another MCP client may append to it.
func TestWriteJSONDocument_Format(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	doc := map[string]any{
		"mcpServers": map[string]any{
			"srv": map[string]any{"url": "https://example.com/mcp"},
		},
	}

	if err := writeJSONDocument(path, doc); err != nil {
		t.Fatalf("writeJSONDocument: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("file does not end in a newline:\n%q", data)
	}
	if !strings.Contains(string(data), "\n  \"mcpServers\"") {
		t.Errorf("file is not indented:\n%s", data)
	}
}

// A directory created for the file must not be world-readable: it holds server
// URLs, which routinely carry API keys.
func TestWriteJSONDocument_CreatesPrivateDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".pi-go")
	path := filepath.Join(dir, "mcp.json")

	if err := writeJSONDocument(path, map[string]any{}); err != nil {
		t.Fatalf("writeJSONDocument: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("directory mode = %04o, want no group/other access", perm)
	}
}
