package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	home := t.TempDir()
	r, err := LoadRegistry(home)
	if err != nil {
		t.Fatalf("LoadRegistry on a fresh home: %v", err)
	}
	if r.Version != RegistryVersion {
		t.Errorf("version = %d, want %d", r.Version, RegistryVersion)
	}
	if len(r.Plugins) != 0 || len(r.Marketplaces) != 0 {
		t.Errorf("fresh registry not empty: %+v", r)
	}

	r.Marketplaces["superpowers-marketplace"] = MarketplaceRecord{
		Name:            "superpowers-marketplace",
		Source:          "https://github.com/obra/superpowers-marketplace.git",
		InstallLocation: MarketplaceDir(home, "superpowers-marketplace"),
		LastUpdated:     "2026-01-01T00:00:00Z",
	}
	r.Plugins["superpowers"] = Installed{
		Name:        "superpowers",
		Marketplace: "superpowers-marketplace",
		Version:     "6.3.0",
		URL:         "https://github.com/obra/superpowers.git",
		Sha:         "abc123",
		InstalledAt: "2026-01-01T00:00:00Z",
	}
	if err := r.Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := LoadRegistry(home)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if got.Plugins["superpowers"].Sha != "abc123" {
		t.Errorf("plugin round trip lost data: %+v", got.Plugins["superpowers"])
	}
	if got.Marketplaces["superpowers-marketplace"].Source == "" {
		t.Errorf("marketplace round trip lost data: %+v", got.Marketplaces)
	}
}

func TestLoadRegistry_NewerVersionIsAnError(t *testing.T) {
	home := t.TempDir()
	writeFile(t, RegistryPath(home), `{"version":999,"plugins":{}}`)
	if _, err := LoadRegistry(home); err == nil {
		t.Fatal("expected an error for a registry from a newer pi-go")
	}
}

func TestSave_LeavesNoTemporaryFiles(t *testing.T) {
	home := t.TempDir()
	if err := NewRegistry().Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(Root(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

// A plugin's skills directory defaults to .pi-go/skills, then skills/, then the
// plugin root. An explicit SkillsDir always wins.
func TestResolveSkillsDir(t *testing.T) {
	t.Run("prefers .pi-go/skills", func(t *testing.T) {
		dir := t.TempDir()
		want := filepath.Join(dir, ".pi-go", "skills")
		if err := os.MkdirAll(want, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := ResolveSkillsDir(dir, Installed{}); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("falls back to skills/", func(t *testing.T) {
		dir := t.TempDir()
		want := filepath.Join(dir, "skills")
		if err := os.MkdirAll(want, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := ResolveSkillsDir(dir, Installed{}); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
	t.Run("falls back to the plugin root", func(t *testing.T) {
		dir := t.TempDir()
		if got := ResolveSkillsDir(dir, Installed{}); got != dir {
			t.Errorf("got %q, want %q", got, dir)
		}
	})
	t.Run("explicit SkillsDir wins", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "skills"), 0o755); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(dir, "custom")
		if err := os.MkdirAll(want, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := ResolveSkillsDir(dir, Installed{SkillsDir: "custom"}); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

func TestSkillDirs(t *testing.T) {
	home := t.TempDir()
	r := NewRegistry()

	// Two plugins with skills, and one whose directory is gone.
	for _, name := range []string{"zeta", "alpha"} {
		dir := filepath.Join(PluginDir(home, name), "skills")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		r.Plugins[name] = Installed{Name: name}
	}
	r.Plugins["removed"] = Installed{Name: "removed"}

	dirs := r.SkillDirs(home)
	if len(dirs) != 2 {
		t.Fatalf("SkillDirs = %v, want 2 entries (the missing plugin is skipped)", dirs)
	}
	// Ordered by plugin name so the precedence is reproducible.
	if !strings.Contains(dirs[0], "alpha") || !strings.Contains(dirs[1], "zeta") {
		t.Errorf("SkillDirs not sorted by name: %v", dirs)
	}
}

// Names arrive from a third-party manifest and become directory names, so a
// traversal attempt must be rejected.
func TestValidName(t *testing.T) {
	bad := []string{
		"",
		".",
		"..",
		"../../.ssh",
		"../evil",
		"a/b",
		`a\b`,
		"/absolute",
		"marketplaces", // reserved for marketplace clones
	}
	for _, name := range bad {
		if err := ValidName(name); err == nil {
			t.Errorf("ValidName(%q) = nil, want an error", name)
		}
	}

	good := []string{"superpowers", "superpowers-chrome", "elements_of_style", "plugin.v2"}
	for _, name := range good {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
}
