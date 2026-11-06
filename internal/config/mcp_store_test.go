package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeMCPFile writes content to a fresh mcp.json in a temp dir and returns its
// path, so each case states the document it starts from literally.
func writeMCPFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

// readMCPFile decodes the document at path, so assertions can talk about JSON
// structure rather than string offsets.
func readMCPFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v\n%s", path, err, data)
	}
	return doc
}

// mcpEntries returns the mcpServers object of a written file.
func mcpEntries(t *testing.T, path string) map[string]any {
	t.Helper()
	doc := readMCPFile(t, path)
	raw, ok := doc[MCPServersKey]
	if !ok {
		t.Fatalf("%s has no %q key: %v", path, MCPServersKey, doc)
	}
	entries, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("%q is %T, want an object", MCPServersKey, raw)
	}
	return entries
}

func TestAddMCPServer_CreatesFileOnFirstUse(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".pi-go", "mcp.json")

	err := AddMCPServer(path, MCPServer{
		Name: "superlogical",
		URL:  "https://www.superlogical.com/rex/community-mcp",
	}, false)
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}

	entries := mcpEntries(t, path)
	entry, ok := entries["superlogical"].(map[string]any)
	if !ok {
		t.Fatalf("superlogical entry missing: %v", entries)
	}
	if got := entry["url"]; got != "https://www.superlogical.com/rex/community-mcp" {
		t.Errorf("url = %v", got)
	}
	// A URL-only server must not gain a command or an oauth flag it never had:
	// the written entry is what another MCP client re-reads.
	for _, unwanted := range []string{"command", "args", "oauth", "headers"} {
		if _, ok := entry[unwanted]; ok {
			t.Errorf("entry carries %q it was not given: %v", unwanted, entry)
		}
	}
}

func TestAddMCPServer_PreservesOtherEntriesAndUnknownFields(t *testing.T) {
	// env and disabled are fields pi-go does not model. A round trip through
	// the MCPServer struct would silently drop them from the neighbor entry.
	path := writeMCPFile(t, `{
  "mcpServers": {
    "existing": {
      "command": "npx",
      "args": ["-y", "chrome-devtools-mcp@latest"],
      "env": {"TOKEN": "abc"},
      "disabled": true
    }
  },
  "globalShortcut": "Cmd+;"
}`)

	err := AddMCPServer(path, MCPServer{Name: "added", URL: "https://example.com/mcp"}, false)
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}

	doc := readMCPFile(t, path)
	entries := mcpEntries(t, path)
	existing, ok := entries["existing"].(map[string]any)
	if !ok {
		t.Fatalf("existing entry lost: %v", entries)
	}
	if existing["env"] == nil {
		t.Error("neighbor entry lost its env field")
	}
	if existing["disabled"] != true {
		t.Error("neighbor entry lost its disabled field")
	}
	if doc["globalShortcut"] != "Cmd+;" {
		t.Error("unrelated top-level key was dropped")
	}
	if _, ok := entries["added"]; !ok {
		t.Error("added entry missing")
	}
}

func TestAddMCPServer_RefusesExistingNameWithoutForce(t *testing.T) {
	path := writeMCPFile(t, `{"mcpServers": {"srv": {"url": "https://original.example/mcp"}}}`)

	err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://typo.example/mcp"}, false)
	if !errors.Is(err, ErrMCPServerExists) {
		t.Fatalf("err = %v, want ErrMCPServerExists", err)
	}

	entries := mcpEntries(t, path)
	entry := entries["srv"].(map[string]any)
	if entry["url"] != "https://original.example/mcp" {
		t.Errorf("refused write still changed the entry: %v", entry)
	}
}

func TestAddMCPServer_ForceReplacesEntry(t *testing.T) {
	path := writeMCPFile(t, `{"mcpServers": {"srv": {"url": "https://original.example/mcp"}}}`)

	err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://fixed.example/mcp"}, true)
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}

	entry := mcpEntries(t, path)["srv"].(map[string]any)
	if entry["url"] != "https://fixed.example/mcp" {
		t.Errorf("url = %v, want the replacement", entry["url"])
	}
}

func TestAddMCPServer_StdioEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")

	err := AddMCPServer(path, MCPServer{
		Name:    "filesystem",
		Command: "npx",
		Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
	}, false)
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}

	entry := mcpEntries(t, path)["filesystem"].(map[string]any)
	if entry["command"] != "npx" {
		t.Errorf("command = %v", entry["command"])
	}
	args, ok := entry["args"].([]any)
	if !ok || len(args) != 3 {
		t.Fatalf("args = %v, want 3 elements", entry["args"])
	}
}

func TestAddMCPServer_LegacyArrayFormatIsConverted(t *testing.T) {
	// config.json historically accepted mcpServers as an array with the name
	// inside each element. A file in that shape must be writable, not refused.
	path := writeMCPFile(t, `{"mcpServers": [{"name": "old", "command": "echo", "args": ["hi"]}]}`)

	err := AddMCPServer(path, MCPServer{Name: "new", URL: "https://example.com/mcp"}, false)
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}

	entries := mcpEntries(t, path)
	old, ok := entries["old"].(map[string]any)
	if !ok {
		t.Fatalf("legacy entry lost in conversion: %v", entries)
	}
	if old["command"] != "echo" {
		t.Errorf("legacy entry command = %v", old["command"])
	}
	if _, ok := old["name"]; ok {
		t.Error("converted entry kept a redundant name field")
	}
	if _, ok := entries["new"]; !ok {
		t.Error("added entry missing")
	}
}

func TestAddMCPServer_RejectsMalformedDocument(t *testing.T) {
	tests := []struct{ name, content, wantSubstring string }{
		{"not JSON", `{"mcpServers": `, "parsing"},
		{"mcpServers is a string", `{"mcpServers": "nope"}`, "must be an object or an array"},
		{"mcpServers is a number", `{"mcpServers": 42}`, "must be an object or an array"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := writeMCPFile(t, tc.content)

			err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://example.com/mcp"}, false)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantSubstring)
			}
			// The original file must be untouched: refusing to edit is only
			// useful if it also does not truncate.
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("reading back: %v", readErr)
			}
			if string(data) != tc.content {
				t.Errorf("file was modified:\n%s", data)
			}
		})
	}
}

func TestAddMCPServer_ValidatesEntry(t *testing.T) {
	tests := []struct {
		name string
		srv  MCPServer
		want string
	}{
		{"no name", MCPServer{URL: "https://example.com/mcp"}, "name is required"},
		{"no transport", MCPServer{Name: "srv"}, "needs a url or a command"},
		{
			"both transports",
			MCPServer{Name: "srv", URL: "https://example.com/mcp", Command: "npx"},
			"both url and command",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mcp.json")
			err := AddMCPServer(path, tc.srv, false)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to mention %q", err, tc.want)
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Error("a rejected entry still created the file")
			}
		})
	}
}

func TestAddMCPServer_EmptyFileIsTreatedAsEmptyDocument(t *testing.T) {
	path := writeMCPFile(t, "\n\n")

	err := AddMCPServer(path, MCPServer{Name: "srv", URL: "https://example.com/mcp"}, false)
	if err != nil {
		t.Fatalf("AddMCPServer: %v", err)
	}
	if _, ok := mcpEntries(t, path)["srv"]; !ok {
		t.Error("entry missing")
	}
}

func TestRemoveMCPServer(t *testing.T) {
	t.Run("removes the named entry and keeps the rest", func(t *testing.T) {
		path := writeMCPFile(t, `{"mcpServers": {"keep": {"url": "https://a.example/mcp"}, "drop": {"url": "https://b.example/mcp"}}}`)

		if err := RemoveMCPServer(path, "drop"); err != nil {
			t.Fatalf("RemoveMCPServer: %v", err)
		}

		entries := mcpEntries(t, path)
		if _, ok := entries["drop"]; ok {
			t.Error("entry was not removed")
		}
		if _, ok := entries["keep"]; !ok {
			t.Error("unrelated entry was removed")
		}
	})

	t.Run("reports a name that is not present", func(t *testing.T) {
		path := writeMCPFile(t, `{"mcpServers": {}}`)

		err := RemoveMCPServer(path, "missing")
		if err == nil || !strings.Contains(err.Error(), `no MCP server named "missing"`) {
			t.Fatalf("err = %v, want a not-found error", err)
		}
	})
}

func TestReadMCPServerEntry(t *testing.T) {
	path := writeMCPFile(t, `{"mcpServers": {"srv": {"url": "https://example.com/mcp", "oauth": true}}}`)

	entry, ok := ReadMCPServerEntry(path, "srv")
	if !ok {
		t.Fatal("entry not found")
	}
	if entry["url"] != "https://example.com/mcp" || entry["oauth"] != true {
		t.Errorf("entry = %v", entry)
	}

	if _, ok := ReadMCPServerEntry(path, "absent"); ok {
		t.Error("reported an absent entry as present")
	}
	if _, ok := ReadMCPServerEntry(filepath.Join(t.TempDir(), "none.json"), "srv"); ok {
		t.Error("reported an entry from a missing file")
	}
}

func TestNearestProjectMCPFile(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	if got := NearestProjectMCPFile(nested); got != "" {
		t.Errorf("NearestProjectMCPFile with no file = %q, want empty", got)
	}

	projectPath := filepath.Join(root, ".pi-go", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectPath, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// A tree's own mcp.json must be found from a subdirectory: that is the file
	// LoadMCPServersFrom loads when pi runs at the root.
	if got := NearestProjectMCPFile(nested); got != projectPath {
		t.Errorf("NearestProjectMCPFile = %q, want %q", got, projectPath)
	}
}
