package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInit creates a git repository at dir with every file committed.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
		{"add", "-A"},
		{"commit", "-q", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// writeSkillFixture writes a skill directory that LoadSkills can read.
func writeSkillFixture(t *testing.T, dir, name string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, name, "SKILL.md"),
		"---\nname: "+name+"\ndescription: test skill\n---\nBody.\n")
}

// marketplaceFixture builds a marketplace repository with one relative-path
// plugin and one git-subdir plugin, mirroring the real catalog shapes.
func marketplaceFixture(t *testing.T) (marketplaceDir string) {
	t.Helper()
	root := t.TempDir()

	// In-repo plugin: source "./plugins/local-plugin".
	local := filepath.Join(root, "plugins", "local-plugin")
	writeSkillFixture(t, filepath.Join(local, "skills"), "local-skill")
	writeFile(t, filepath.Join(local, ManifestDir, PluginName),
		`{"name":"local-plugin","version":"1.2.3"}`)

	// Remote plugin in its own repository: source git-subdir.
	remoteRepo := t.TempDir()
	sub := filepath.Join(remoteRepo, "plugins", "remote-plugin")
	writeSkillFixture(t, filepath.Join(sub, "skills"), "remote-skill")
	writeFile(t, filepath.Join(sub, ManifestDir, PluginName),
		`{"name":"remote-plugin","version":"2.0.0"}`)
	gitInit(t, remoteRepo)

	writeFile(t, filepath.Join(root, ManifestDir, MarketplaceName), `{
	  "name": "test-marketplace",
	  "owner": {"name": "Test"},
	  "metadata": {"description": "fixture", "version": "1.0.0"},
	  "plugins": [
	    {"name":"local-plugin","version":"1.2.3","source":"./plugins/local-plugin"},
	    {"name":"remote-plugin","version":"2.0.0","source":{"source":"git-subdir","url":"`+remoteRepo+`","path":"plugins/remote-plugin"}}
	  ]
	}`)
	gitInit(t, root)
	return root
}

func TestNormalizeSource(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(dir, "mp")
	os.MkdirAll(local, 0o755)

	tests := []struct{ in, want string }{
		{"obra/superpowers-marketplace", "https://github.com/obra/superpowers-marketplace.git"},
		{"https://github.com/obra/superpowers.git", "https://github.com/obra/superpowers.git"},
		{"git@github.com:obra/superpowers.git", "git@github.com:obra/superpowers.git"},
		{"https://github.com/obra/superpowers", "https://github.com/obra/superpowers"},
		{local, local},
		{"", ""},
	}
	for _, tt := range tests {
		if got := NormalizeSource(tt.in); got != tt.want {
			t.Errorf("NormalizeSource(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// End to end over local repositories: register a marketplace, install both
// plugin source shapes, and confirm the skills are loadable.
func TestInstall_LocalRepositories(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	rec, err := m.AddMarketplace(ctx, mpDir)
	if err != nil {
		t.Fatalf("AddMarketplace: %v", err)
	}
	if rec.Name != "test-marketplace" {
		t.Fatalf("marketplace name = %q", rec.Name)
	}

	plugins, err := m.MarketplacePlugins(ctx, "test-marketplace")
	if err != nil {
		t.Fatalf("MarketplacePlugins: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("plugins = %d, want 2", len(plugins))
	}

	t.Run("relative source", func(t *testing.T) {
		inst, err := m.Install(ctx, "local-plugin@test-marketplace")
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		if inst.Version != "1.2.3" {
			t.Errorf("version = %q, want 1.2.3", inst.Version)
		}
		if _, err := os.Stat(filepath.Join(PluginDir(home, "local-plugin"), "skills", "local-skill", "SKILL.md")); err != nil {
			t.Errorf("skill not installed: %v", err)
		}
	})

	t.Run("git-subdir source", func(t *testing.T) {
		inst, err := m.Install(ctx, "remote-plugin@test-marketplace")
		if err != nil {
			t.Fatalf("Install: %v", err)
		}
		if inst.Sha == "" {
			t.Error("expected a recorded commit sha")
		}
		if _, err := os.Stat(filepath.Join(PluginDir(home, "remote-plugin"), "skills", "remote-skill", "SKILL.md")); err != nil {
			t.Errorf("skill not installed: %v", err)
		}
	})

	t.Run("resolves without a marketplace suffix", func(t *testing.T) {
		if _, _, _, err := m.ResolvePlugin("local-plugin"); err != nil {
			t.Fatalf("ResolvePlugin: %v", err)
		}
	})

	t.Run("second install of the same plugin is refused", func(t *testing.T) {
		if _, err := m.Install(ctx, "local-plugin@test-marketplace"); err == nil {
			t.Fatal("expected an error re-installing an installed plugin")
		}
	})
}

// An installed plugin's skills must be discovered from the registry. That the
// loader then gives the user's own same-named skill precedence is asserted in
// internal/extension, where the directory order is defined.
func TestInstalledSkillsAreDiscovered(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatalf("AddMarketplace: %v", err)
	}
	if _, err := m.Install(ctx, "local-plugin@test-marketplace"); err != nil {
		t.Fatalf("Install: %v", err)
	}

	registry, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	dirs := registry.SkillDirs(home)
	if len(dirs) != 1 {
		t.Fatalf("SkillDirs = %v, want 1", dirs)
	}

	// The discovered directory is the plugin's skills dir, and the plugin's
	// skill is readable there.
	writeFile(t, filepath.Join(dirs[0], "SKILL.md"),
		"---\nname: shared-name\ndescription: from the plugin\n---\nPlugin body.\n")
	if _, err := os.Stat(filepath.Join(dirs[0], "local-skill", "SKILL.md")); err != nil {
		t.Fatalf("plugin skill not present on disk: %v", err)
	}
}

func TestUninstall(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ctx, "local-plugin@test-marketplace"); err != nil {
		t.Fatal(err)
	}
	if err := m.Uninstall("local-plugin"); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if _, err := os.Stat(PluginDir(home, "local-plugin")); !os.IsNotExist(err) {
		t.Error("plugin directory still present after uninstall")
	}
	registry, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Plugins["local-plugin"]; ok {
		t.Error("plugin still registered after uninstall")
	}
	if err := m.Uninstall("local-plugin"); err == nil {
		t.Error("expected an error uninstalling a plugin that is not installed")
	}
}

func TestUpdate(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ctx, "local-plugin@test-marketplace"); err != nil {
		t.Fatal(err)
	}
	// Nothing changed remotely, so the commit is identical.
	changed, err := m.Update(ctx, "local-plugin")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if changed {
		t.Error("Update reported a change when the source did not move")
	}
	if err := m.Uninstall("local-plugin"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Update(ctx, "local-plugin"); err == nil {
		t.Error("expected an error updating a plugin that is not installed")
	}
}

func TestAddMarketplace_RejectsNonMarketplace(t *testing.T) {
	plain := t.TempDir() // a git repo with no marketplace.json
	writeFile(t, filepath.Join(plain, "README.md"), "not a marketplace\n")
	gitInit(t, plain)

	m := &Manager{PiHome: t.TempDir()}
	if _, err := m.AddMarketplace(context.Background(), plain); err == nil {
		t.Fatal("expected an error adding a directory that is not a marketplace")
	}
}

// A marketplace is untrusted input; a name that escapes the plugins directory
// must be refused before anything is written.
func TestAddMarketplace_RejectsTraversalName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestDir, MarketplaceName),
		`{"name":"../../evil","plugins":[{"name":"p","source":"./"}]}`)
	gitInit(t, dir)

	home := t.TempDir()
	m := &Manager{PiHome: home}
	if _, err := m.AddMarketplace(context.Background(), dir); err == nil {
		t.Fatal("expected an error for a marketplace name containing path traversal")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(home), "evil")); err == nil {
		t.Fatal("traversal name escaped the plugins directory")
	}
}

func TestResolvePlugin_Ambiguity(t *testing.T) {
	mpA, mpB := t.TempDir(), t.TempDir()
	for i, dir := range []string{mpA, mpB} {
		name := []string{"market-a", "market-b"}[i]
		writeFile(t, filepath.Join(dir, ManifestDir, MarketplaceName),
			`{"name":"`+name+`","plugins":[{"name":"shared","source":"./p"}]}`)
		writeFile(t, filepath.Join(dir, "p", "SKILL.md"), "---\nname: shared\n---\nBody.\n")
		gitInit(t, dir)
	}

	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	for _, dir := range []string{mpA, mpB} {
		if _, err := m.AddMarketplace(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
	// Ambiguous without a marketplace suffix.
	_, _, _, err := m.ResolvePlugin("shared")
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	if !strings.Contains(err.Error(), "more than one marketplace") {
		t.Errorf("error = %v, want it to mention the ambiguity", err)
	}
	// Disambiguated by suffix.
	if _, _, _, err := m.ResolvePlugin("shared@market-a"); err != nil {
		t.Errorf("ResolvePlugin with a suffix: %v", err)
	}
}

func TestResolvePlugin_NoMarketplaces(t *testing.T) {
	m := &Manager{PiHome: t.TempDir()}
	_, _, _, err := m.ResolvePlugin("anything")
	if err == nil {
		t.Fatal("expected an error when no marketplaces are registered")
	}
	if !strings.Contains(err.Error(), "marketplace add") {
		t.Errorf("error should tell the user how to proceed, got: %v", err)
	}
}
