package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pluginFixture writes a marketplace with one local plugin into a temp dir and
// returns the marketplace directory.
func pluginFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	pluginDir := filepath.Join(root, "plugins", "demo-plugin")
	skillDir := filepath.Join(pluginDir, "skills", "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: demo-skill\ndescription: demo\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"demo-marketplace","plugins":[{"name":"demo-plugin","version":"1.0.0","source":"./plugins/demo-plugin"}]}`
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// A git repository is required, because a plugin's commit is recorded.
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	return root
}

// runPluginCmd runs a subcommand with PI_GO_HOME pointed at home, returning
// combined output.
func runPluginCmd(t *testing.T, home string, args ...string) (string, error) {
	t.Helper()
	t.Setenv("PI_GO_HOME", home)

	cmd := newPluginCmd()
	cmd.SetArgs(args)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	return buf.String(), err
}

func TestPluginCmd_Structure(t *testing.T) {
	cmd := newPluginCmd()
	if cmd.Use != "plugin" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if cmd.Short == "" || cmd.Long == "" {
		t.Error("expected help text")
	}

	want := map[string]bool{
		"marketplace": false,
		"install":     false,
		"list":        false,
		"uninstall":   false,
		"update":      false,
	}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Name()]; ok {
			want[sub.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q is not registered", name)
		}
	}

	// The nested marketplace group.
	var mp *bool
	for _, sub := range cmd.Commands() {
		if sub.Name() == "marketplace" {
			ok := len(sub.Commands()) >= 2
			mp = &ok
		}
	}
	if mp == nil || !*mp {
		t.Error("marketplace should have add and list subcommands")
	}
}

// The full lifecycle over a local marketplace: add, install, list, uninstall.
func TestPluginCmd_Lifecycle(t *testing.T) {
	mpDir := pluginFixture(t)
	home := t.TempDir()

	out, err := runPluginCmd(t, home, "marketplace", "add", mpDir)
	if err != nil {
		t.Fatalf("marketplace add: %v\n%s", err, out)
	}
	if !strings.Contains(out, "demo-marketplace") {
		t.Errorf("add output = %q", out)
	}

	out, err = runPluginCmd(t, home, "marketplace", "list")
	if err != nil {
		t.Fatalf("marketplace list: %v", err)
	}
	if !strings.Contains(out, "demo-marketplace") {
		t.Errorf("marketplace list output = %q", out)
	}

	out, err = runPluginCmd(t, home, "install", "demo-plugin@demo-marketplace")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "demo-plugin") || !strings.Contains(out, "1.0.0") {
		t.Errorf("install output = %q", out)
	}

	// The skill must be on disk under the plugin.
	skill := filepath.Join(home, "plugins", "demo-plugin", "skills", "demo-skill", "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("installed skill missing: %v", err)
	}

	out, err = runPluginCmd(t, home, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "demo-plugin") {
		t.Errorf("list output = %q", out)
	}

	out, err = runPluginCmd(t, home, "uninstall", "demo-plugin")
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, "plugins", "demo-plugin")); !os.IsNotExist(err) {
		t.Error("plugin directory survived uninstall")
	}
}

// The marketplace can be inferred when only one is registered.
func TestPluginCmd_InstallWithoutMarketplaceSuffix(t *testing.T) {
	mpDir := pluginFixture(t)
	home := t.TempDir()

	if _, err := runPluginCmd(t, home, "marketplace", "add", mpDir); err != nil {
		t.Fatal(err)
	}
	if _, err := runPluginCmd(t, home, "install", "demo-plugin"); err != nil {
		t.Fatalf("install without a marketplace suffix: %v", err)
	}
}

func TestPluginCmd_EmptyStates(t *testing.T) {
	home := t.TempDir()

	out, err := runPluginCmd(t, home, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "No plugins installed") {
		t.Errorf("empty list output = %q", out)
	}
	if !strings.Contains(out, "pi plugin install") {
		t.Error("empty list should tell the user how to install a plugin")
	}

	out, err = runPluginCmd(t, home, "marketplace", "list")
	if err != nil {
		t.Fatalf("marketplace list: %v", err)
	}
	if !strings.Contains(out, "No marketplaces registered") {
		t.Errorf("empty marketplace list output = %q", out)
	}
}

func TestPluginCmd_Errors(t *testing.T) {
	home := t.TempDir()

	if _, err := runPluginCmd(t, home, "install", "ghost"); err == nil {
		t.Error("expected an error installing with no marketplaces")
	}
	if _, err := runPluginCmd(t, home, "uninstall", "ghost"); err == nil {
		t.Error("expected an error uninstalling a plugin that is not installed")
	}
	if _, err := runPluginCmd(t, home, "update", "ghost"); err == nil {
		t.Error("expected an error updating a plugin that is not installed")
	}
	if _, err := runPluginCmd(t, home, "marketplace", "add", filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("expected an error adding a nonexistent marketplace")
	}
}

func TestPluginCmd_ArgValidation(t *testing.T) {
	home := t.TempDir()
	cases := [][]string{
		{"install"},
		{"install", "a", "b"},
		{"uninstall"},
		{"marketplace", "add"},
		{"list", "extra"},
		{"update", "a", "b"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if _, err := runPluginCmd(t, home, args...); err == nil {
				t.Errorf("expected an argument error for %v", args)
			}
		})
	}
}

func TestPluginHome(t *testing.T) {
	t.Run("honors PI_GO_HOME", func(t *testing.T) {
		t.Setenv("PI_GO_HOME", "/custom/home")
		got, err := pluginHome()
		if err != nil {
			t.Fatal(err)
		}
		if got != "/custom/home" {
			t.Errorf("pluginHome() = %q, want /custom/home", got)
		}
	})
	t.Run("defaults under the user home", func(t *testing.T) {
		t.Setenv("PI_GO_HOME", "")
		got, err := pluginHome()
		if err != nil {
			t.Fatal(err)
		}
		userHome, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no user home available")
		}
		if got != filepath.Join(userHome, ".pi-go") {
			t.Errorf("pluginHome() = %q", got)
		}
	})
}
