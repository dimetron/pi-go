package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runMCPCmd runs an `pi mcp` subcommand with HOME pointed at a temp dir, so the
// global scope is somewhere disposable, and returns its combined output.
func runMCPCmd(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	// os.UserHomeDir reads HOME on Unix and USERPROFILE on Windows; HOMEDRIVE
	// and HOMEPATH are cleared because a set HOMEDRIVE+HOMEPATH pair takes
	// precedence there and would point the global scope somewhere the test
	// cannot inspect.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")

	cmd := newMCPCmd()
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return buf.String(), err
}

// readServers decodes the mcpServers object written under path.
func readServers(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v\n%s", path, err, data)
	}
	return doc.MCPServers
}

func TestMCPCmd_Structure(t *testing.T) {
	cmd := newMCPCmd()
	if cmd.Use != "mcp" {
		t.Errorf("Use = %q, want \"mcp\"", cmd.Use)
	}
	if cmd.Short == "" || cmd.Long == "" {
		t.Error("expected help text")
	}
	want := map[string]bool{"add": false, "login": false, "list": false, "remove": false}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q missing", name)
		}
	}
}

// The exact invocation from the issue: `pi mcp add superlogical --url …`.
func TestMCPAdd_RemoteServerWritesGlobalFile(t *testing.T) {
	home := t.TempDir()

	out, err := runMCPCmd(t, home, "add", "superlogical",
		"--url", "https://www.superlogical.com/rex/community-mcp")
	if err != nil {
		t.Fatalf("add: %v\n%s", err, out)
	}

	path := filepath.Join(home, ".pi-go", "mcp.json")
	entry, ok := readServers(t, path)["superlogical"].(map[string]any)
	if !ok {
		t.Fatalf("superlogical not written to %s", path)
	}
	if entry["url"] != "https://www.superlogical.com/rex/community-mcp" {
		t.Errorf("url = %v", entry["url"])
	}
	if !strings.Contains(out, "Added MCP server") {
		t.Errorf("output does not confirm the add: %q", out)
	}
}

func TestMCPAdd_StdioServer(t *testing.T) {
	home := t.TempDir()

	_, err := runMCPCmd(t, home, "add", "filesystem",
		"--command", "npx", "--arg", "-y", "--arg", "@modelcontextprotocol/server-filesystem", "--arg", "/tmp")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	entry := readServers(t, filepath.Join(home, ".pi-go", "mcp.json"))["filesystem"].(map[string]any)
	if entry["command"] != "npx" {
		t.Errorf("command = %v", entry["command"])
	}
	args := entry["args"].([]any)
	if len(args) != 3 || args[2] != "/tmp" {
		t.Errorf("args = %v", args)
	}
}

func TestMCPAdd_Validation(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{
			name:    "no transport",
			args:    []string{"add", "srv"},
			wantErr: "a transport is required",
		},
		{
			name:    "both transports",
			args:    []string{"add", "srv", "--url", "https://example.com/mcp", "--command", "npx"},
			wantErr: "mutually exclusive",
		},
		{
			name:    "header on a stdio server",
			args:    []string{"add", "srv", "--command", "npx", "--header", "X=1"},
			wantErr: "--header applies to --url servers only",
		},
		{
			name:    "url without a scheme",
			args:    []string{"add", "srv", "--url", "example.com/mcp"},
			wantErr: "scheme must be http or https",
		},
		{
			name:    "url with no host",
			args:    []string{"add", "srv", "--url", "https://"},
			wantErr: "no host",
		},
		{
			name:    "malformed header",
			args:    []string{"add", "srv", "--url", "https://example.com/mcp", "--header", "not-a-pair"},
			wantErr: "key=value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			_, err := runMCPCmd(t, home, tc.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %v, want it to mention %q", err, tc.wantErr)
			}
			// A rejected add must not leave a file behind: a half-configured
			// server is worse than none.
			if _, statErr := os.Stat(filepath.Join(home, ".pi-go", "mcp.json")); statErr == nil {
				t.Error("a rejected add still wrote mcp.json")
			}
		})
	}
}

func TestMCPAdd_DuplicateRequiresForce(t *testing.T) {
	home := t.TempDir()
	if _, err := runMCPCmd(t, home, "add", "srv", "--url", "https://original.example/mcp"); err != nil {
		t.Fatalf("first add: %v", err)
	}

	_, err := runMCPCmd(t, home, "add", "srv", "--url", "https://typo.example/mcp")
	if err == nil {
		t.Fatal("expected the second add to be refused")
	}
	if !strings.Contains(err.Error(), "--force") {
		t.Errorf("err = %v, want it to point at --force", err)
	}

	if _, err := runMCPCmd(t, home, "add", "srv", "--url", "https://fixed.example/mcp", "--force"); err != nil {
		t.Fatalf("forced add: %v", err)
	}
	entry := readServers(t, filepath.Join(home, ".pi-go", "mcp.json"))["srv"].(map[string]any)
	if entry["url"] != "https://fixed.example/mcp" {
		t.Errorf("url = %v, want the replacement", entry["url"])
	}
}

func TestMCPAdd_ProjectScope(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)

	if _, err := runMCPCmd(t, home, "add", "srv", "--url", "https://example.com/mcp", "--project"); err != nil {
		t.Fatalf("add --project: %v", err)
	}

	projectPath := filepath.Join(dir, ".pi-go", "mcp.json")
	if _, ok := readServers(t, projectPath)["srv"]; !ok {
		t.Fatalf("entry not written to %s", projectPath)
	}
	if _, err := os.Stat(filepath.Join(home, ".pi-go", "mcp.json")); err == nil {
		t.Error("--project also wrote the global file")
	}
}

// A project mcp.json replaces the global list, so an entry added to the global
// file from inside such a tree is invisible. Saying so at add time is the whole
// point: the alternative is a server that is configured and still not found.
func TestMCPAdd_WarnsWhenAProjectFileShadowsTheGlobalEntry(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".pi-go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".pi-go", "mcp.json"), []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)

	out, err := runMCPCmd(t, home, "add", "srv", "--url", "https://example.com/mcp")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "will not be visible from here") {
		t.Errorf("output does not warn about the shadowing project file:\n%s", out)
	}
	if !strings.Contains(out, "--project") {
		t.Errorf("output does not suggest --project:\n%s", out)
	}
}

func TestMCPAdd_NoShadowWarningWithoutAProjectFile(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)

	out, err := runMCPCmd(t, home, "add", "srv", "--url", "https://example.com/mcp")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if strings.Contains(out, "will not be visible") {
		t.Errorf("warned about shadowing with no project file:\n%s", out)
	}
}

func TestMCPLogin_RejectsStdioServer(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	if _, err := runMCPCmd(t, home, "add", "filesystem", "--command", "npx"); err != nil {
		t.Fatalf("add: %v", err)
	}

	_, err := runMCPCmd(t, home, "login", "filesystem")
	if err == nil {
		t.Fatal("expected an error for a stdio server")
	}
	if !strings.Contains(err.Error(), "no token to authorize") {
		t.Errorf("err = %v, want it to explain that a stdio server has no token", err)
	}
}

func TestMCPLogin_UnknownServerNamesTheConfiguredOnes(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	if _, err := runMCPCmd(t, home, "add", "superlogical", "--url", "https://example.com/mcp"); err != nil {
		t.Fatalf("add: %v", err)
	}

	_, err := runMCPCmd(t, home, "login", "superlogical-typo")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "configured servers: superlogical") {
		t.Errorf("err = %v, want it to list the configured names", err)
	}
}

func TestMCPList(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		home := t.TempDir()
		dir := t.TempDir()
		chdir(t, dir)

		out, err := runMCPCmd(t, home, "list")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !strings.Contains(out, "No MCP servers configured") {
			t.Errorf("output = %q", out)
		}
	})

	t.Run("shows transport and masks credentials in the target", func(t *testing.T) {
		home := t.TempDir()
		dir := t.TempDir()
		chdir(t, dir)
		if _, err := runMCPCmd(t, home, "add", "remote",
			"--url", "https://mcp.example.com/mcp?apiKey=sk-secret"); err != nil {
			t.Fatalf("add: %v", err)
		}

		out, err := runMCPCmd(t, home, "list")
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if !strings.Contains(out, "remote") || !strings.Contains(out, "http") {
			t.Errorf("output = %q", out)
		}
		if strings.Contains(out, "sk-secret") {
			t.Errorf("list leaked an API key:\n%s", out)
		}
	})
}

func TestMCPRemove(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	chdir(t, dir)
	for _, name := range []string{"keep", "drop"} {
		if _, err := runMCPCmd(t, home, "add", name, "--url", "https://example.com/mcp"); err != nil {
			t.Fatalf("add %s: %v", name, err)
		}
	}

	if _, err := runMCPCmd(t, home, "remove", "drop"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	servers := readServers(t, filepath.Join(home, ".pi-go", "mcp.json"))
	if _, ok := servers["drop"]; ok {
		t.Error("drop was not removed")
	}
	if _, ok := servers["keep"]; !ok {
		t.Error("keep was removed")
	}

	// Removing a name that is not there must fail: a silent no-op reads as
	// success and hides a typo in the name or the wrong --project scope.
	if _, err := runMCPCmd(t, home, "remove", "absent"); err == nil {
		t.Error("expected an error removing an absent server")
	}
}

func TestMCPRemove_RejectsConflictingScopeFlags(t *testing.T) {
	home := t.TempDir()
	_, err := runMCPCmd(t, home, "remove", "srv", "--project", "--global")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("err = %v, want a mutually-exclusive error", err)
	}
}

// chdir moves into dir for the duration of the test. `pi mcp add --project` and
// the shadowing warning both read the working directory, so a test that cares
// about scope has to control it.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restoring cwd: %v", err)
		}
	})
}
