package plugin

import (
	"context"
	"encoding/json"
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

	// Marshaled rather than concatenated: remoteRepo is a filesystem path, and
	// on Windows it contains backslashes. Interpolating it into a JSON string
	// literal produces invalid JSON (`\U` is not an escape sequence), so the
	// fixture failed to parse on Windows only. encoding/json escapes it
	// correctly on every platform.
	catalog := struct {
		Name     string                `json:"name"`
		Owner    struct{ Name string } `json:"owner"`
		Metadata struct {
			Description string `json:"description"`
			Version     string `json:"version"`
		} `json:"metadata"`
		Plugins []any `json:"plugins"`
	}{
		Name:  "test-marketplace",
		Owner: struct{ Name string }{Name: "Test"},
	}
	catalog.Metadata.Description = "fixture"
	catalog.Metadata.Version = "1.0.0"
	catalog.Plugins = []any{
		map[string]any{
			"name":    "local-plugin",
			"version": "1.2.3",
			"source":  "./plugins/local-plugin",
		},
		map[string]any{
			"name":    "remote-plugin",
			"version": "2.0.0",
			"source": map[string]any{
				"source": SourceGitSubdir,
				"url":    remoteRepo,
				"path":   "plugins/remote-plugin",
			},
		},
	}
	doc, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatalf("marshaling marketplace fixture: %v", err)
	}
	writeFile(t, filepath.Join(root, ManifestDir, MarketplaceName), string(doc))
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

// A marketplace entry's URL is a filesystem path for local repositories, so on
// Windows it carries backslashes. The fixture must let encoding/json escape
// them: interpolating the path into a JSON string literal yields invalid JSON
// (`\U` is not an escape sequence) and the catalog fails to parse — which is
// exactly how TestInstall_LocalRepositories failed on Windows while passing on
// macOS and Linux, where the path has no backslashes.
func TestMarketplaceFixture_PathsSurviveJSONEscaping(t *testing.T) {
	// A Windows-shaped path, independent of the host this runs on, so the
	// regression is caught on every platform rather than only on Windows.
	winPath := `C:\Users\runner\AppData\Local\Temp\TestX\001`

	catalog := struct {
		Plugins []any `json:"plugins"`
	}{}
	catalog.Plugins = []any{
		map[string]any{
			"name": "remote-plugin",
			"source": map[string]any{
				"source": SourceGitSubdir,
				"url":    winPath,
				"path":   "plugins/remote-plugin",
			},
		},
	}

	doc, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}

	var got Marketplace
	if err := json.Unmarshal(doc, &got); err != nil {
		t.Fatalf("the marshaled catalog must round-trip through the parser: %v", err)
	}
	if len(got.Plugins) != 1 {
		t.Fatalf("plugins = %d, want 1", len(got.Plugins))
	}
	if got.Plugins[0].Source.URL != winPath {
		t.Errorf("URL = %q, want %q", got.Plugins[0].Source.URL, winPath)
	}

	// Guard the actual failure mode: raw concatenation must NOT survive.
	raw := `{"plugins":[{"name":"p","source":{"source":"git-subdir","url":"` + winPath + `","path":"x"}}]}`
	if err := json.Unmarshal([]byte(raw), new(Marketplace)); err == nil {
		t.Error("expected concatenating a Windows path into a JSON literal to produce invalid JSON; " +
			"if this now parses, the fixture may have regressed to string concatenation")
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

// List and ListMarketplaces back `pi plugin list`; both must return a stable
// name-ordered view of the registry rather than Go's randomized map order.
func TestList_OrdersByName(t *testing.T) {
	home := t.TempDir()
	reg := &Registry{
		Marketplaces: map[string]MarketplaceRecord{
			"zeta":  {Name: "zeta"},
			"alpha": {Name: "alpha"},
			"mid":   {Name: "mid"},
		},
		Plugins: map[string]Installed{
			"zulu":  {Name: "zulu", Version: "1.0.0"},
			"alpha": {Name: "alpha", Version: "2.0.0"},
			"mike":  {Name: "mike", Version: "3.0.0"},
		},
	}
	if err := reg.Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := &Manager{PiHome: home}

	plugins, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	gotPlugins := make([]string, 0, len(plugins))
	for _, p := range plugins {
		gotPlugins = append(gotPlugins, p.Name)
	}
	if want := "alpha,mike,zulu"; strings.Join(gotPlugins, ",") != want {
		t.Errorf("List order = %v, want %s", gotPlugins, want)
	}
	if plugins[1].Version != "3.0.0" {
		t.Errorf("List dropped the version for %s: %+v", plugins[1].Name, plugins[1])
	}

	markets, err := m.ListMarketplaces()
	if err != nil {
		t.Fatalf("ListMarketplaces: %v", err)
	}
	gotMarkets := make([]string, 0, len(markets))
	for _, mk := range markets {
		gotMarkets = append(gotMarkets, mk.Name)
	}
	if want := "alpha,mid,zeta"; strings.Join(gotMarkets, ",") != want {
		t.Errorf("ListMarketplaces order = %v, want %s", gotMarkets, want)
	}
}

// An empty (but valid) registry is the state of a fresh install: both lists
// must come back empty rather than erroring.
func TestList_EmptyRegistry(t *testing.T) {
	home := t.TempDir()
	if err := (&Registry{}).Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}
	m := &Manager{PiHome: home}

	plugins, err := m.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("plugins = %+v, want none", plugins)
	}
	markets, err := m.ListMarketplaces()
	if err != nil {
		t.Fatalf("ListMarketplaces: %v", err)
	}
	if len(markets) != 0 {
		t.Errorf("marketplaces = %+v, want none", markets)
	}
}

// UpdateMarketplace on a local directory is a no-op that must not shell out to
// git: a local marketplace has nothing to fetch, so requiring git would make
// the command fail on a machine without it.
func TestUpdateMarketplace_LocalIsNoop(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatalf("AddMarketplace: %v", err)
	}
	if err := m.UpdateMarketplace(ctx, "test-marketplace"); err != nil {
		t.Fatalf("UpdateMarketplace on a local dir should be a no-op, got: %v", err)
	}
	// Empty name means "every marketplace"; one local marketplace is all there is.
	if err := m.UpdateMarketplace(ctx, ""); err != nil {
		t.Fatalf("UpdateMarketplace(\"\") should refresh all, got: %v", err)
	}
	// An unknown marketplace must be reported, not silently ignored.
	if err := m.UpdateMarketplace(ctx, "ghost"); err == nil {
		t.Error("expected an error for an unknown marketplace")
	} else if !strings.Contains(err.Error(), "unknown marketplace") {
		t.Errorf("error = %v, want it to name the unknown marketplace", err)
	}
}

// copyTree is the cross-filesystem fallback for installing a plugin from a
// local directory. It must reproduce the tree, keep file modes, and preserve
// symlinks as links rather than following them.
func TestCopyTree(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "skills", "a", "SKILL.md"), "---\nname: a\n---\nBody.\n")
	writeFile(t, filepath.Join(src, "skills", "b", "SKILL.md"), "---\nname: b\n---\nBody.\n")
	if err := os.Chmod(filepath.Join(src, "skills", "a", "SKILL.md"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A symlink inside the tree stands in for a shared file a plugin links to.
	linkPath := filepath.Join(src, "linked.md")
	if err := os.Symlink("skills/a/SKILL.md", linkPath); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	for _, rel := range []string{"skills/a/SKILL.md", "skills/b/SKILL.md"} {
		info, err := os.Stat(filepath.Join(dst, rel))
		if err != nil {
			t.Errorf("missing %s after copy: %v", rel, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("%s is a directory, want a file", rel)
		}
	}

	// Modes are preserved for regular files.
	info, err := os.Stat(filepath.Join(dst, "skills", "a", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	// The link is reproduced as a link, not dereferenced into a file.
	li, err := os.Lstat(filepath.Join(dst, "linked.md"))
	if err != nil {
		t.Fatalf("link missing after copy: %v", err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was followed and copied as a regular file; it must be reproduced as a link")
	}
	if target, err := os.Readlink(filepath.Join(dst, "linked.md")); err != nil || target != "skills/a/SKILL.md" {
		t.Errorf("link target = %q (err %v), want skills/a/SKILL.md", target, err)
	}
}

// syncRepo against a path that is not a git repository must fall through to a
// clone, and fail loudly when the URL is not fetchable.
func TestSyncRepo_ClonesWhenAbsent(t *testing.T) {
	m := &Manager{PiHome: t.TempDir()}
	dir := filepath.Join(t.TempDir(), "not-yet")

	err := m.syncRepo(context.Background(), filepath.Join(t.TempDir(), "nope.git"), "", dir)
	if err == nil {
		t.Fatal("expected cloning from a nonexistent URL to fail")
	}
}

// An existing git repository is fast-forwarded rather than cloned, and a local
// modification in the cache is discarded on purpose.
func TestSyncRepo_ResetsExistingRepo(t *testing.T) {
	origin := t.TempDir()
	writeFile(t, filepath.Join(origin, "SKILL.md"), "v1\n")
	gitInit(t, origin)

	dir := filepath.Join(t.TempDir(), "cache")
	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if err := m.syncRepo(ctx, origin, "", dir); err != nil {
		t.Fatalf("initial clone: %v", err)
	}

	// Dirty the cache: syncRepo must discard this, not merge it.
	writeFile(t, filepath.Join(dir, "SKILL.md"), "locally modified\n")
	if err := m.syncRepo(ctx, origin, "", dir); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1\n" {
		t.Errorf("content = %q, want the remote version %q", got, "v1\n")
	}
}
