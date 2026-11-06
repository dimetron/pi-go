// The failure modes an operator can actually hit — a malformed document, a shape
// the writer cannot edit, a document that is valid JSON but not an object —
// expressed without filesystem permission bits, so every platform runs them. The
// cases that need an unwritable file live in mcp_store_readonly_test.go behind a
// !windows build tag.
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A document that is valid JSON but not an object is a shape AddMCPServer cannot
// edit. It must be refused rather than replaced with an empty one.
func TestAddMCPServer_RejectsNonObjectDocument(t *testing.T) {
	path := writeMCPFile(t, `["not", "an", "object"]`)

	err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://example.com/mcp"}, false)
	if err == nil {
		t.Fatal("expected an error")
	}

	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("reading back: %v", readErr)
	}
	if string(data) != `["not", "an", "object"]` {
		t.Errorf("file was modified:\n%s", data)
	}
}

// The legacy array form can carry entries that make no sense: a non-object
// element, and an object with no name. Both are dropped rather than written back
// under a key like "".
func TestAddMCPServer_LegacyArraySkipsUnusableElements(t *testing.T) {
	path := writeMCPFile(t, `{"mcpServers": ["junk", {"command": "nameless"}, {"name": "kept", "command": "echo"}]}`)

	if err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://example.com/mcp"}, false); err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}

	entries := mcpEntries(t, path)
	if _, ok := entries["kept"]; !ok {
		t.Errorf("valid legacy entry was dropped: %v", entries)
	}
	if _, ok := entries[""]; ok {
		t.Errorf("nameless element was written under an empty key: %v", entries)
	}
	if len(entries) != 2 {
		t.Errorf("entries = %v, want only \"kept\" and \"srv\"", entries)
	}
}

// Headers and the OAuth marker are emitted only when set, and both travel with
// the entry so another MCP client sees the same configuration pi-go uses.
func TestAddMCPServer_EmitsHeadersAndOAuth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	err := AddMCPServer(path, MCPServer{
		Name:    "guarded",
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"X-Api-Key": "abc"},
		OAuth:   true,
	}, false)
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}

	entry := mcpEntries(t, path)["guarded"].(map[string]any)
	headers, ok := entry["headers"].(map[string]any)
	if !ok || headers["X-Api-Key"] != "abc" {
		t.Errorf("headers = %v", entry["headers"])
	}
	if entry["oauth"] != true {
		t.Errorf("oauth = %v, want true", entry["oauth"])
	}
}

// RemoveMCPServer reports a malformed document the same way AddMCPServer does,
// so a user editing the file sees one consistent error.
func TestRemoveMCPServer_RejectsMalformedDocument(t *testing.T) {
	path := writeMCPFile(t, `{"mcpServers": 7}`)

	err := RemoveMCPServer(path, "srv")
	if err == nil || !strings.Contains(err.Error(), "must be an object or an array") {
		t.Fatalf("err = %v, want the shape error", err)
	}
}

// ReadMCPServerEntry answers false rather than surfacing an error, so a caller
// reporting state cannot fail just because the file is unreadable.
func TestReadMCPServerEntry_MalformedDocumentIsAbsent(t *testing.T) {
	path := writeMCPFile(t, `{"mcpServers": 7}`)

	if _, ok := ReadMCPServerEntry(path, "srv"); ok {
		t.Error("reported an entry from a malformed document")
	}
}

// A project write into a directory pi cannot create is an error, not a silent
// no-op — and it must not be reported as the global path.
func TestMCPStoreFile_ProjectDefaultPathWhenNoneExists(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	path, err := MCPStoreFile(true)
	if err != nil {
		t.Fatalf("MCPStoreFile: %v", err)
	}
	want := filepath.Join(dir, ".pi-go", "mcp.json")
	if path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

// With a project file already present, a project write must target that same
// file — the one LoadMCPServersFrom would load — not a new one in a subdirectory.
func TestMCPStoreFile_ProjectReusesExistingFile(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "pkg")
	if err := os.MkdirAll(filepath.Join(root, ".pi-go"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, ".pi-go", "mcp.json")
	if err := os.WriteFile(existing, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)

	path, err := MCPStoreFile(true)
	if err != nil {
		t.Fatalf("MCPStoreFile: %v", err)
	}
	if path != existing {
		t.Errorf("path = %q, want the existing %q", path, existing)
	}
}

// The global path must follow HOME, which is what makes it testable at all.
func TestMCPStoreFile_GlobalFollowsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path, err := MCPStoreFile(false)
	if err != nil {
		t.Fatalf("MCPStoreFile: %v", err)
	}
	if want := filepath.Join(home, ".pi-go", "mcp.json"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}
