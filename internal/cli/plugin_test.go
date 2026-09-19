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

// A plugin with no version anywhere reports "unknown" rather than an empty
// column, so the list stays aligned and readable.
func TestPluginCmd_ListShowsUnknownVersion(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "plugins", "unversioned")
	skillDir := filepath.Join(pluginDir, "skills", "s")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"),
		[]byte("---\nname: s\n---\nB\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No version in the catalog entry and no plugin.json at all.
	manifest := `{"name":"noversion","plugins":[{"name":"unversioned","source":"./plugins/unversioned"}]}`
	if err := os.WriteFile(filepath.Join(root, ".claude-plugin", "marketplace.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"add", "-A"},
		{"-c", "commit.gpgsign=false", "commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	home := t.TempDir()
	if _, err := runPluginCmd(t, home, "marketplace", "add", root); err != nil {
		t.Fatal(err)
	}
	if _, err := runPluginCmd(t, home, "install", "unversioned@noversion"); err != nil {
		t.Fatal(err)
	}
	out, err := runPluginCmd(t, home, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "unknown") {
		t.Errorf("list output = %q, want it to show the version as unknown", out)
	}
}

// `pi plugin update` reports whether anything actually moved, and refreshes the
// catalog first so a new version is visible.
func TestPluginCmd_UpdateOutput(t *testing.T) {
	mpDir := pluginFixture(t)
	home := t.TempDir()

	if _, err := runPluginCmd(t, home, "marketplace", "add", mpDir); err != nil {
		t.Fatal(err)
	}
	if _, err := runPluginCmd(t, home, "install", "demo-plugin@demo-marketplace"); err != nil {
		t.Fatal(err)
	}

	// Nothing moved in the source, so the first update is a no-op.
	out, err := runPluginCmd(t, home, "update")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Already up to date") {
		t.Errorf("update output = %q, want the up-to-date message", out)
	}

	// Move the plugin's source commit on, then update again: the plugin's
	// recorded commit differs, so the command reports a change.
	if err := os.WriteFile(filepath.Join(mpDir, "plugins", "demo-plugin", "NEW.md"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "commit.gpgsign=false", "commit", "-q", "-m", "move on"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = mpDir
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, o)
		}
	}

	if _, err := runPluginCmd(t, home, "marketplace", "add", mpDir); err != nil {
		t.Fatalf("re-adding the marketplace: %v", err)
	}
	out, err = runPluginCmd(t, home, "update", "demo-plugin")
	if err != nil {
		t.Fatalf("update: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Updated.") {
		t.Errorf("update output = %q, want it to report the update", out)
	}
}

// A marketplace whose files have gone missing makes list report the failure
// rather than printing a partial view.
func TestPluginCmd_BrokenRegistry(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "plugins", "installed.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The registry path is a directory, which is not a readable registry.
	if _, err := runPluginCmd(t, home, "list"); err == nil {
		t.Error("expected an error when the registry cannot be read")
	}
	if _, err := runPluginCmd(t, home, "marketplace", "list"); err == nil {
		t.Error("expected an error when the registry cannot be read")
	}
}

// Registering a source that cannot be fetched reports the failure.
func TestPluginCmd_MarketplaceAddUnfetchable(t *testing.T) {
	home := t.TempDir()
	_, err := runPluginCmd(t, home, "marketplace", "add", "file://"+filepath.Join(t.TempDir(), "absent"))
	if err == nil {
		t.Fatal("expected an error adding an unfetchable marketplace")
	}
}

// With no home directory resolvable, every plugin subcommand reports the
// failure up front rather than operating on a bogus path.
//
// os.UserHomeDir returns an error when $HOME is unset, which is the only way to
// reach this path on a normal machine.
func TestPluginCmd_WithoutHome(t *testing.T) {
	t.Setenv("PI_GO_HOME", "")
	t.Setenv("HOME", "")

	if _, err := pluginHome(); err == nil {
		t.Fatal("expected pluginHome to fail with no home directory")
	}

	cases := [][]string{
		{"marketplace", "add", "obra/superpowers-marketplace"},
		{"marketplace", "list"},
		{"install", "x"},
		{"list"},
		{"uninstall", "x"},
		{"update"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			cmd := newPluginCmd()
			cmd.SetArgs(args)
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetContext(context.Background())
			if err := cmd.Execute(); err == nil {
				t.Errorf("expected an error with no home directory for %v", args)
			}
		})
	}
}

// A marketplace whose remote has gone away makes `update` warn and carry on to
// the plugin updates, rather than aborting the whole command.
func TestPluginCmd_UpdateWarnsWhenCatalogRefreshFails(t *testing.T) {
	mpDir := pluginFixture(t)
	home := t.TempDir()

	if _, err := runPluginCmd(t, home, "marketplace", "add", mpDir); err != nil {
		t.Fatal(err)
	}
	if _, err := runPluginCmd(t, home, "install", "demo-plugin@demo-marketplace"); err != nil {
		t.Fatal(err)
	}

	// Re-register the marketplace from a URL so the refresh has something to
	// fetch, then remove that remote.
	if _, err := runPluginCmd(t, home, "marketplace", "add", "file://"+filepath.ToSlash(mpDir)); err != nil {
		t.Fatalf("re-registering from a URL: %v", err)
	}
	if err := os.RemoveAll(mpDir); err != nil {
		t.Fatal(err)
	}

	out, err := runPluginCmd(t, home, "update")
	if err != nil {
		t.Fatalf("update should warn and continue, got: %v\n%s", err, out)
	}
	if !strings.Contains(out, "warning") {
		t.Errorf("output = %q, want a warning about the failed catalog refresh", out)
	}
}
