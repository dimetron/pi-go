package plugin

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// commitAll stages and commits everything in dir. Signing is disabled for the
// same reason gitInit disables it: fixtures must not depend on the host's
// signing setup, which would otherwise hang on a hardware-backed agent.
func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "commit.gpgsign=false", "commit", "-q", "-m", msg},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

// ---------------------------------------------------------------------------
// copyTree / copyFile edge cases
//
// TestCopyTree covers the happy path. These cover the branches that make it a
// safe fallback: a nested tree with empty directories, a link whose target is
// absent, and a caller asking for a destination that already exists.
// ---------------------------------------------------------------------------

func TestCopyTree_NestedAndEmptyDirs(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "skills", "a", "SKILL.md"), "---\nname: a\n---\nA\n")
	writeFile(t, filepath.Join(src, "skills", "b", "SKILL.md"), "---\nname: b\n---\nB\n")
	// An empty directory still has to be reproduced: a plugin may keep assets
	// there that its skills reference by path.
	if err := os.MkdirAll(filepath.Join(src, "assets", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(src, "top.md"), "top\n")

	dst := filepath.Join(t.TempDir(), "out")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}
	for _, rel := range []string{"skills/a/SKILL.md", "skills/b/SKILL.md", "top.md"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}
	if info, err := os.Stat(filepath.Join(dst, "assets", "empty")); err != nil || !info.IsDir() {
		t.Errorf("empty directory not reproduced: err=%v", err)
	}
	// Content survives, not just existence.
	got, err := os.ReadFile(filepath.Join(dst, "skills", "b", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "B") {
		t.Errorf("content = %q, want it to carry B", got)
	}
}

// ---------------------------------------------------------------------------
// Update
// ---------------------------------------------------------------------------

// Update with no argument refreshes every installed plugin.
func TestUpdate_AllPlugins(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"local-plugin", "remote-plugin"} {
		if _, err := m.Install(ctx, name+"@test-marketplace"); err != nil {
			t.Fatalf("Install %s: %v", name, err)
		}
	}

	// Neither source moved, so nothing reports as changed.
	changed, err := m.Update(ctx, "")
	if err != nil {
		t.Fatalf("Update all: %v", err)
	}
	if changed {
		t.Error("Update reported a change when no source moved")
	}

	// Both are still installed and still registered afterwards.
	installed, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 2 {
		t.Fatalf("installed = %d, want 2", len(installed))
	}
}

// A failed update must leave the plugin registered with its previous record,
// rather than silently dropping it from the registry.
func TestUpdate_FailureRestoresRegistryEntry(t *testing.T) {
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

	// Break the marketplace so the re-install cannot resolve the plugin.
	record, err := m.MarketplacePlugins(ctx, "test-marketplace")
	if err != nil {
		t.Fatal(err)
	}
	if len(record) == 0 {
		t.Fatal("fixture has no plugins")
	}
	// A local marketplace is used where it lies, so break the original source
	// rather than the (nonexistent) managed copy under the plugins dir.
	catalog := filepath.Join(mpDir, ManifestDir, MarketplaceName)
	if err := os.WriteFile(catalog, []byte(`{"name":"test-marketplace","plugins":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Update(ctx, "local-plugin"); err == nil {
		t.Fatal("expected the update to fail once the catalog no longer lists the plugin")
	}

	registry, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Plugins["local-plugin"]; !ok {
		t.Error("a failed update dropped the plugin from the registry")
	}
	// The installed files survive a failed update too.
	if _, err := os.Stat(PluginDir(home, "local-plugin")); err != nil {
		t.Errorf("a failed update removed the installed files: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Remote (cloned) sources
//
// The local-repository tests exercise the relative and git-subdir paths. These
// pin the bare `url` path, where the clone itself is the plugin, using a
// file:// remote so no network is involved.
// ---------------------------------------------------------------------------

// remoteOnlyFixture builds a marketplace listing a single url-source plugin.
func remoteOnlyFixture(t *testing.T) (marketplaceDir, pluginRepo string) {
	t.Helper()

	pluginRepo = t.TempDir()
	writeSkillFixture(t, filepath.Join(pluginRepo, "skills"), "url-skill")
	writeFile(t, filepath.Join(pluginRepo, ManifestDir, PluginName),
		`{"name":"url-plugin","version":"3.1.4"}`)
	gitInit(t, pluginRepo)

	mp := t.TempDir()
	writeCatalog(t, mp, "url-market", map[string]any{
		"url-plugin": Source{Kind: SourceURL, URL: fileURL(pluginRepo)},
	})
	writeFile(t, filepath.Join(mp, "README.md"), "catalog\n")
	gitInit(t, mp)
	return mp, pluginRepo
}

func TestInstall_RemoteURLSource(t *testing.T) {
	mpDir, _ := remoteOnlyFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatalf("AddMarketplace: %v", err)
	}
	inst, err := m.Install(ctx, "url-plugin@url-market")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if inst.Sha == "" {
		t.Error("expected the cloned commit to be recorded")
	}
	if inst.URL == "" {
		t.Error("expected the source URL to be recorded")
	}
	// The clone's skills are installed, and the temp clone is gone.
	if _, err := os.Stat(filepath.Join(PluginDir(home, "url-plugin"), "skills", "url-skill", "SKILL.md")); err != nil {
		t.Errorf("skill not installed from the cloned repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(Root(home), "tmp-plugin")); !os.IsNotExist(err) {
		t.Error("the temporary clone was left behind")
	}
}

// A ref pin must be honored when the catalog names one.
func TestInstall_RemoteRefPin(t *testing.T) {
	pluginRepo := t.TempDir()
	writeSkillFixture(t, filepath.Join(pluginRepo, "skills"), "pinned-skill")
	gitInit(t, pluginRepo)
	// Tag the current commit, then move main on, so the pin is observable.
	//
	// The tag is created with -c tag.gpgsign=false and -m, which pins it as a
	// lightweight tag. On a developer machine tag.gpgsign is true, and a bare
	// `git tag v1` then makes an *annotated* tag needing a message, which opens
	// an editor and blocks forever in a non-interactive test process.
	tagCmd := exec.Command("git", "-c", "tag.gpgsign=false", "tag", "-m", "v1", "v1")
	tagCmd.Dir = pluginRepo
	tagCmd.Env = append(os.Environ(), "GIT_EDITOR=true", "GIT_TERMINAL_PROMPT=0")
	if out, err := tagCmd.CombinedOutput(); err != nil {
		t.Fatalf("tagging: %v\n%s", err, out)
	}
	pinned, err := headSha(context.Background(), pluginRepo)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pluginRepo, "later.txt"), "moved on\n")
	commitAll(t, pluginRepo, "later")

	mp := t.TempDir()
	writeCatalog(t, mp, "pin-market", map[string]any{
		"pinned": Source{Kind: SourceURL, URL: fileURL(pluginRepo), Ref: "v1"},
	})
	writeFile(t, filepath.Join(mp, "README.md"), "x\n")
	gitInit(t, mp)

	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mp); err != nil {
		t.Fatal(err)
	}
	inst, err := m.Install(ctx, "pinned@pin-market")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if inst.Sha != pinned {
		t.Errorf("installed commit = %s, want the pinned %s", inst.Sha, pinned)
	}
	// The commit made after the tag must not be present.
	if _, err := os.Stat(filepath.Join(PluginDir(home, "pinned"), "later.txt")); !os.IsNotExist(err) {
		t.Error("the ref pin was ignored: the clone advanced past the tag")
	}
}

// A url source that claims a subdirectory but does not contain one is an error
// naming the missing path, not a silent install of the whole repository.
func TestInstall_RemoteSubdirMissing(t *testing.T) {
	pluginRepo := t.TempDir()
	writeSkillFixture(t, filepath.Join(pluginRepo, "skills"), "s")
	gitInit(t, pluginRepo)

	mp := t.TempDir()
	writeCatalog(t, mp, "bad-subdir", map[string]any{
		"missing": Source{Kind: SourceGitSubdir, URL: fileURL(pluginRepo), Path: "plugins/not-here"},
	})
	writeFile(t, filepath.Join(mp, "README.md"), "x\n")
	gitInit(t, mp)

	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mp); err != nil {
		t.Fatal(err)
	}
	_, err := m.Install(ctx, "missing@bad-subdir")
	if err == nil {
		t.Fatal("expected an error for a missing subdirectory")
	}
	if !strings.Contains(err.Error(), "not-here") {
		t.Errorf("error = %v, want it to name the missing subdirectory", err)
	}
}

// A relative source naming a path the marketplace does not contain is an error
// naming both the path and the marketplace.
func TestInstall_RelativePathMissing(t *testing.T) {
	mp := t.TempDir()
	writeCatalog(t, mp, "ghost-path", map[string]any{"ghost": "./plugins/ghost"})
	writeFile(t, filepath.Join(mp, "README.md"), "x\n")
	gitInit(t, mp)

	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mp); err != nil {
		t.Fatal(err)
	}
	_, err := m.Install(ctx, "ghost@ghost-path")
	if err == nil {
		t.Fatal("expected an error for a missing relative path")
	}
	if !strings.Contains(err.Error(), "ghost-path") || !strings.Contains(err.Error(), "plugins/ghost") {
		t.Errorf("error = %v, want it to name the path and the marketplace", err)
	}
}

// ---------------------------------------------------------------------------
// Argument and resolution errors
// ---------------------------------------------------------------------------

func TestAddMarketplace_EmptySource(t *testing.T) {
	m := &Manager{PiHome: t.TempDir()}
	if _, err := m.AddMarketplace(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for an empty marketplace source")
	}
}

func TestMarketplacePlugins_Unknown(t *testing.T) {
	m := &Manager{PiHome: t.TempDir()}
	_, err := m.MarketplacePlugins(context.Background(), "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown marketplace")
	}
	if !strings.Contains(err.Error(), "marketplace add") {
		t.Errorf("error = %v, want it to say how to add a marketplace", err)
	}
}

func TestResolvePlugin_UnknownMarketplaceSuffix(t *testing.T) {
	mpDir := marketplaceFixture(t)
	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := m.ResolvePlugin("local-plugin@no-such-market")
	if err == nil {
		t.Fatal("expected an error for an unknown marketplace suffix")
	}
	if !strings.Contains(err.Error(), "no-such-market") {
		t.Errorf("error = %v, want it to name the marketplace", err)
	}
}

func TestResolvePlugin_EmptyName(t *testing.T) {
	mpDir := marketplaceFixture(t)
	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.ResolvePlugin("@test-marketplace"); err == nil {
		t.Fatal("expected an error for an empty plugin name")
	}
}

// A plugin the named marketplace does not list is an error naming both.
func TestResolvePlugin_MarketplaceLacksPlugin(t *testing.T) {
	mpDir := marketplaceFixture(t)
	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := m.ResolvePlugin("absent@test-marketplace")
	if err == nil {
		t.Fatal("expected an error for a plugin the marketplace does not offer")
	}
	if !strings.Contains(err.Error(), "absent") || !strings.Contains(err.Error(), "test-marketplace") {
		t.Errorf("error = %v, want it to name the plugin and the marketplace", err)
	}
}

// A marketplace whose directory has been removed must not stop a plugin from
// being resolved out of a marketplace that is intact.
func TestResolvePlugin_SkipsBrokenMarketplace(t *testing.T) {
	goodDir := marketplaceFixture(t)
	brokenDir := t.TempDir()
	writeFile(t, filepath.Join(brokenDir, ManifestDir, MarketplaceName),
		`{"name":"broken","plugins":[{"name":"x","source":"./x"}]}`)
	writeFile(t, filepath.Join(brokenDir, "README.md"), "x\n")
	gitInit(t, brokenDir)

	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, goodDir); err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddMarketplace(ctx, brokenDir); err != nil {
		t.Fatal(err)
	}
	// Remove the broken marketplace's copy, leaving a registry entry behind.
	if err := os.RemoveAll(MarketplaceDir(m.PiHome, "broken")); err != nil {
		t.Fatal(err)
	}

	p, _, _, err := m.ResolvePlugin("local-plugin")
	if err != nil {
		t.Fatalf("a broken marketplace blocked resolution from a good one: %v", err)
	}
	if p.Name != "local-plugin" {
		t.Errorf("resolved %q", p.Name)
	}
}

// NormalizeSource must reject a name that would traverse out of the plugins
// directory rather than treating "a/../../b" as an owner/repo pair.
func TestNormalizeSource_TraversalShorthand(t *testing.T) {
	got := NormalizeSource("../../etc/passwd")
	if strings.HasPrefix(got, "https://github.com/") {
		t.Errorf("NormalizeSource(%q) = %q — a traversing path must not become a GitHub repo",
			"../../etc/passwd", got)
	}
}

// A plugin that is already installed refuses a second install, naming the
// command that would update it instead.
func TestInstall_AlreadyInstalled(t *testing.T) {
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
	_, err := m.Install(ctx, "local-plugin@test-marketplace")
	if err == nil {
		t.Fatal("expected an error re-installing an installed plugin")
	}
	if !strings.Contains(err.Error(), "already installed") || !strings.Contains(err.Error(), "update") {
		t.Errorf("error = %v, want it to explain and suggest an update", err)
	}
}

// insideDir must recognize containment, and must not be fooled into thinking a
// sibling directory is inside.
func TestInsideDir(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	if !insideDir(inner, root) {
		t.Error("a directory should be inside itself")
	}
	if !insideDir(filepath.Join(inner, "..", "inner"), root) {
		t.Error("an uncleaned path inside root was not recognized")
	}
	if insideDir(filepath.Join(root, ".."), root) {
		t.Error("a parent directory must not report as inside")
	}
	if insideDir(filepath.Join(t.TempDir(), "other"), root) {
		t.Error("a sibling directory must not report as inside")
	}
}

// ---------------------------------------------------------------------------
// Registry store errors
// ---------------------------------------------------------------------------

func TestLoadRegistry_Malformed(t *testing.T) {
	home := t.TempDir()
	writeFile(t, RegistryPath(home), "{not json")
	if _, err := LoadRegistry(home); err == nil {
		t.Fatal("expected an error for a malformed registry")
	}
}

// A registry file with explicit nulls (or omitted maps) decodes to nil maps,
// which must not panic on the next write.
func TestLoadRegistry_NullMapsBecomeUsable(t *testing.T) {
	home := t.TempDir()
	writeFile(t, RegistryPath(home), `{"version":1,"marketplaces":null,"plugins":null}`)

	r, err := LoadRegistry(home)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	r.Plugins["x"] = Installed{Name: "x"}
	r.Marketplaces["m"] = MarketplaceRecord{Name: "m"}
	if err := r.Save(home); err != nil {
		t.Fatalf("Save after loading null maps: %v", err)
	}
	again, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Plugins["x"]; !ok {
		t.Error("a write after loading null maps was lost")
	}
}

// A registry that omits the version field is treated as the current version.
func TestLoadRegistry_MissingVersion(t *testing.T) {
	home := t.TempDir()
	writeFile(t, RegistryPath(home), `{"plugins":{}}`)
	r, err := LoadRegistry(home)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if r.Version != RegistryVersion {
		t.Errorf("version = %d, want %d", r.Version, RegistryVersion)
	}
}

// Save into a home that does not exist yet must create the tree.
func TestSave_CreatesDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "pi-go")
	r := NewRegistry()
	r.Plugins["p"] = Installed{Name: "p"}
	if err := r.Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(RegistryPath(home)); err != nil {
		t.Errorf("registry not written: %v", err)
	}
}

func TestValidName_CleanPathElement(t *testing.T) {
	// A name that is a prefix of a traversal, or contains a separator-looking
	// character, must be rejected even though it survives Base/Clean.
	for _, bad := range []string{"a/..", "./a", "a//b", "a/../b"} {
		if err := ValidName(bad); err == nil {
			t.Errorf("ValidName(%q) = nil, want an error", bad)
		}
	}
}

// ---------------------------------------------------------------------------
// Manifest parsing errors
// ---------------------------------------------------------------------------

// A source object whose fields are the wrong type is a parse error, not a
// silently empty source.
func TestSourceUnmarshal_TypeError(t *testing.T) {
	var s Source
	if err := s.UnmarshalJSON([]byte(`123`)); err == nil {
		t.Error("expected an error for a non-string, non-object source")
	}
}

// An object with no recognized fields and no explicit kind is a relative
// source with an empty path — it must not be mistaken for a remote.
func TestSourceUnmarshal_BareObjectInfersRelative(t *testing.T) {
	var s Source
	if err := s.UnmarshalJSON([]byte(`{"unexpected":true}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Kind != SourceRelative {
		t.Errorf("kind = %q, want %q", s.Kind, SourceRelative)
	}
	if s.IsRemote() {
		t.Error("a source with no remote fields must not be treated as remote")
	}
}

// A malformed plugin.json is reported, not ignored: a broken manifest should
// not be silently treated as an absent one.
func TestReadManifest_Malformed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestDir, PluginName), `{broken`)
	if _, err := ReadManifest(dir); err == nil {
		t.Fatal("expected an error for a malformed plugin.json")
	}
}

// ---------------------------------------------------------------------------
// Remote marketplaces
//
// Registering a marketplace from a URL clones it to a temporary directory,
// reads the catalog for its name, then moves it into place. Only the local
// path was covered; this pins the remote one using file:// so no network is
// involved.
// ---------------------------------------------------------------------------

func TestAddMarketplace_RemoteSourceClones(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}

	rec, err := m.AddMarketplace(context.Background(), fileURL(mpDir))
	if err != nil {
		t.Fatalf("AddMarketplace from a URL: %v", err)
	}
	if rec.Name != "test-marketplace" {
		t.Errorf("name = %q", rec.Name)
	}
	// The catalog is readable from the managed location, and the temporary
	// clone is gone.
	if _, err := ReadMarketplace(rec.InstallLocation); err != nil {
		t.Errorf("catalog not readable at %s: %v", rec.InstallLocation, err)
	}
	if _, err := os.Stat(filepath.Join(Root(home), "tmp-marketplace")); !os.IsNotExist(err) {
		t.Error("the temporary marketplace clone was left behind")
	}
	if _, err := os.Stat(rec.InstallLocation); err != nil {
		t.Errorf("marketplace not installed: %v", err)
	}

	// The clone carries a .git directory, so an update can fast-forward it.
	if _, err := os.Stat(filepath.Join(rec.InstallLocation, ".git")); err != nil {
		t.Errorf("the managed clone is not a git repository: %v", err)
	}

	// Plugins install from the managed clone.
	if _, err := m.Install(context.Background(), "local-plugin@test-marketplace"); err != nil {
		t.Fatalf("Install from a remotely-registered marketplace: %v", err)
	}
}

func TestUpdateMarketplace_RemoteRefreshes(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	rec, err := m.AddMarketplace(ctx, fileURL(mpDir))
	if err != nil {
		t.Fatal(err)
	}

	// Add a plugin to the source catalog, commit it, and refresh: the managed
	// clone must pick the new entry up.
	writeFile(t, filepath.Join(mpDir, ManifestDir, MarketplaceName), `{
	  "name": "test-marketplace",
	  "plugins": [
	    {"name":"local-plugin","version":"1.2.3","source":"./plugins/local-plugin"},
	    {"name":"added-later","version":"9.9.9","source":"./plugins/added-later"}
	  ]
	}`)
	commitAll(t, mpDir, "add a plugin")

	if err := m.UpdateMarketplace(ctx, rec.Name); err != nil {
		t.Fatalf("UpdateMarketplace: %v", err)
	}
	plugins, err := m.MarketplacePlugins(ctx, rec.Name)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := FindPlugin(Marketplace{Plugins: plugins}, "added-later"); !ok {
		t.Error("the refreshed catalog does not list the plugin added upstream")
	}

	// An unknown name is an error, and updating every marketplace works.
	if err := m.UpdateMarketplace(ctx, "no-such-market"); err == nil {
		t.Error("expected an error for an unknown marketplace")
	}
	if err := m.UpdateMarketplace(ctx, ""); err != nil {
		t.Errorf("UpdateMarketplace with no name should refresh all: %v", err)
	}
}

// A plugin whose source moves reports the change, so `pi plugin update` can
// tell the user something happened.
func TestUpdate_ReportsChangedCommit(t *testing.T) {
	mpDir, pluginRepo := remoteOnlyFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	first, err := m.Install(ctx, "url-plugin@url-market")
	if err != nil {
		t.Fatal(err)
	}

	// Move the plugin's source on, then update: the commit differs.
	writeFile(t, filepath.Join(pluginRepo, "CHANGELOG.md"), "new\n")
	commitAll(t, pluginRepo, "move on")

	changed, err := m.Update(ctx, "url-plugin")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !changed {
		t.Error("Update reported no change after the source commit moved")
	}
	after, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if after.Plugins["url-plugin"].Sha == first.Sha {
		t.Error("the updated plugin still records the old commit")
	}
}

// A plugin that ships no skills is still installable, and the note about it is
// emitted rather than the install failing.
func TestInstall_PluginWithoutSkills(t *testing.T) {
	mp := t.TempDir()
	pluginDir := filepath.Join(mp, "plugins", "agents-only")
	writeFile(t, filepath.Join(pluginDir, ManifestDir, PluginName), `{"name":"agents-only","version":"1.0.0"}`)
	writeCatalog(t, mp, "no-skills", map[string]any{"agents-only": "./plugins/agents-only"})
	gitInit(t, mp)

	var notes []string
	m := &Manager{
		PiHome: t.TempDir(),
		Log:    func(format string, args ...any) { notes = append(notes, format) },
	}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mp); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Install(ctx, "agents-only@no-skills"); err != nil {
		t.Fatalf("a plugin with no skills should still install: %v", err)
	}
	mentioned := false
	for _, n := range notes {
		if strings.Contains(n, "no skills") {
			mentioned = true
		}
	}
	if !mentioned {
		t.Errorf("expected a note that the plugin ships no skills; got %v", notes)
	}
}

// The github kind is inferred when an entry names a repo without an explicit
// source kind.
func TestSourceUnmarshal_InfersGitHubFromRepo(t *testing.T) {
	var s Source
	if err := s.UnmarshalJSON([]byte(`{"repo":"obra/superpowers"}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s.Kind != SourceGitHub {
		t.Errorf("kind = %q, want %q", s.Kind, SourceGitHub)
	}
	if s.URL != "https://github.com/obra/superpowers.git" {
		t.Errorf("url = %q", s.URL)
	}
}

// ---------------------------------------------------------------------------
// Fault injection
//
// The remaining uncovered statements are error returns after a filesystem or
// git operation fails. They are reachable in practice — a full disk, a
// read-only home, a marketplace whose remote has gone away — so they are
// exercised here by arranging the failure rather than left untested.
// ---------------------------------------------------------------------------

// onlyIfWritable skips a test when running as root, where directory
// permissions do not restrict access.
func onlyIfWritable(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		// Windows has no POSIX permission bits; Chmod only toggles the
		// read-only flag, so a "read-only" directory is still writable.
		t.Skip("POSIX permissions are not enforced on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions are not enforced")
	}
}

// An unreadable plugins directory must not be mistaken for an empty registry.
func TestRegistry_SaveAndLoadFailures(t *testing.T) {
	t.Run("registry path is a directory", func(t *testing.T) {
		home := t.TempDir()
		if err := os.MkdirAll(RegistryPath(home), 0o755); err != nil {
			t.Fatal(err)
		}
		// Reading a directory as a file fails with something other than
		// IsNotExist, which must surface rather than become an empty registry.
		if _, err := LoadRegistry(home); err == nil {
			t.Fatal("expected an error when the registry path is a directory")
		}
	})

	t.Run("plugins dir is not writable", func(t *testing.T) {
		onlyIfWritable(t)
		home := t.TempDir()
		if err := os.MkdirAll(Root(home), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(Root(home), 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(Root(home), 0o755) })

		if err := NewRegistry().Save(home); err == nil {
			t.Fatal("expected an error saving into a read-only plugins directory")
		}
	})
}

// A registry entry with a name that cannot be a directory must be refused
// before any filesystem removal, even though pi-go wrote the entry itself.
func TestUninstall_RefusesUnsafeName(t *testing.T) {
	home := t.TempDir()
	r := NewRegistry()
	r.Plugins["../escape"] = Installed{Name: "../escape"}
	if err := r.Save(home); err != nil {
		t.Fatal(err)
	}

	m := &Manager{PiHome: home}
	err := m.Uninstall("../escape")
	if err == nil {
		t.Fatal("expected Uninstall to refuse a traversing name")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("error = %v, want it to say it refused", err)
	}
	// The registry entry survives the refusal.
	after, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Plugins["../escape"]; !ok {
		t.Error("a refused uninstall still dropped the registry entry")
	}
}

// Updating a marketplace whose remote is gone must report the failure rather
// than silently leaving a stale catalog.
func TestUpdateMarketplace_RemoteGone(t *testing.T) {
	mpDir := marketplaceFixture(t)
	home := t.TempDir()
	m := &Manager{PiHome: home}
	ctx := context.Background()

	rec, err := m.AddMarketplace(ctx, fileURL(mpDir))
	if err != nil {
		t.Fatal(err)
	}
	// Remove the remote the managed clone was fetched from.
	if err := os.RemoveAll(mpDir); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateMarketplace(ctx, rec.Name); err == nil {
		t.Fatal("expected an error updating a marketplace whose remote is gone")
	}
}

// A source that cannot be fetched fails at the clone, carrying git's own
// message, rather than being reported as a malformed marketplace.
func TestAddMarketplace_UnfetchableSource(t *testing.T) {
	m := &Manager{PiHome: t.TempDir()}
	_, err := m.AddMarketplace(context.Background(), fileURL(filepath.Join(t.TempDir(), "absent")))
	if err == nil {
		t.Fatal("expected an error for a source that cannot be fetched")
	}
	if !strings.Contains(err.Error(), "git clone") {
		t.Errorf("error = %v, want it to carry the failing git command", err)
	}
}

// A fetchable repository that simply is not a marketplace is reported as such,
// so the user can tell the two failures apart.
func TestAddMarketplace_FetchedButNotAMarketplace(t *testing.T) {
	plain := t.TempDir()
	writeFile(t, filepath.Join(plain, "README.md"), "just a repo\n")
	gitInit(t, plain)

	m := &Manager{PiHome: t.TempDir()}
	_, err := m.AddMarketplace(context.Background(), "file://"+plain)
	if err == nil {
		t.Fatal("expected an error for a repository with no catalog")
	}
	if !strings.Contains(err.Error(), "not a plugin marketplace") {
		t.Errorf("error = %v, want it to explain the source is not a marketplace", err)
	}
}

// copyTree reports a source it cannot read rather than producing a partial
// copy that looks successful.
func TestCopyTree_UnreadableSource(t *testing.T) {
	onlyIfWritable(t)
	src := t.TempDir()
	secret := filepath.Join(src, "secret.md")
	writeFile(t, secret, "top secret\n")
	if err := os.Chmod(secret, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(secret, 0o644) })

	if err := copyTree(src, filepath.Join(t.TempDir(), "out")); err == nil {
		t.Fatal("expected an error copying an unreadable file")
	}
}

// ---------------------------------------------------------------------------
// Fault injection: a corrupt registry
//
// Every entry point reads the registry first, and a registry damaged by hand or
// by a partial write must surface as an error rather than being treated as
// empty — silently reporting "no plugins installed" would invite a user to
// reinstall over a working tree.
// ---------------------------------------------------------------------------

// corruptRegistry returns a pi-go home whose registry cannot be parsed.
func corruptRegistry(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	writeFile(t, RegistryPath(home), "{ this is not json")
	return home
}

func TestCorruptRegistryIsReported(t *testing.T) {
	ctx := context.Background()

	t.Run("AddMarketplace", func(t *testing.T) {
		mpDir := marketplaceFixture(t)
		m := &Manager{PiHome: corruptRegistry(t)}
		if _, err := m.AddMarketplace(ctx, mpDir); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("MarketplacePlugins", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if _, err := m.MarketplacePlugins(ctx, "x"); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("ResolvePlugin", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if _, _, _, err := m.ResolvePlugin("x"); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("Install", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if _, err := m.Install(ctx, "x"); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("Uninstall", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if err := m.Uninstall("x"); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("List", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if _, err := m.List(); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("ListMarketplaces", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if _, err := m.ListMarketplaces(); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("Update", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if _, err := m.Update(ctx, "x"); err == nil {
			t.Error("expected an error")
		}
	})
	t.Run("UpdateMarketplace", func(t *testing.T) {
		m := &Manager{PiHome: corruptRegistry(t)}
		if err := m.UpdateMarketplace(ctx, ""); err == nil {
			t.Error("expected an error")
		}
	})
}

// The registry path being a directory rather than a file is also an error, not
// an empty registry.
func TestRegistryPathIsDirectory(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(RegistryPath(home), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &Manager{PiHome: home}
	if _, err := m.List(); err == nil {
		t.Error("expected an error when the registry path is a directory")
	}
}

// An explicit "version":0 in the file is upgraded to the current schema rather
// than left at zero. (A registry that merely omits the field keeps the value
// NewRegistry already set, so this is the only way the branch is reached.)
func TestLoadRegistry_ExplicitZeroVersion(t *testing.T) {
	home := t.TempDir()
	writeFile(t, RegistryPath(home), `{"version":0,"plugins":{}}`)
	r, err := LoadRegistry(home)
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	if r.Version != RegistryVersion {
		t.Errorf("version = %d, want it upgraded to %d", r.Version, RegistryVersion)
	}
}

// A plugin absent from every registered marketplace is reported by name, with
// no marketplace suffix to disambiguate it.
func TestResolvePlugin_NotOfferedAnywhere(t *testing.T) {
	mpDir := marketplaceFixture(t)
	m := &Manager{PiHome: t.TempDir()}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mpDir); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := m.ResolvePlugin("nowhere-plugin")
	if err == nil {
		t.Fatal("expected an error for a plugin no marketplace offers")
	}
	if !strings.Contains(err.Error(), "nowhere-plugin") {
		t.Errorf("error = %v, want it to name the plugin", err)
	}
	if !strings.Contains(err.Error(), "no registered marketplace") {
		t.Errorf("error = %v, want it to say no marketplace offers it", err)
	}
}

// A source that is none of the recognized forms is returned unchanged rather
// than being turned into a nonsense GitHub URL.
func TestNormalizeSource_Unrecognized(t *testing.T) {
	for _, in := range []string{"a b", "not a url", "just-a-name"} {
		got := NormalizeSource(in)
		if strings.HasPrefix(got, "https://github.com/") {
			t.Errorf("NormalizeSource(%q) = %q, want it unchanged", in, got)
		}
	}
}

// With no git on PATH, an operation that needs it fails with an explanation
// rather than a bare exec error.
func TestEnsureGit_MissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // an empty directory: no git, no sh
	err := ensureGit(context.Background())
	if err == nil {
		t.Fatal("expected an error when git is not on PATH")
	}
	if !strings.Contains(err.Error(), "git is required") {
		t.Errorf("error = %v, want it to explain git is required", err)
	}
}

// copyTree must fail loudly when the destination cannot be created, rather than
// reporting a copy that produced nothing.
func TestCopyTree_DestinationUnwritable(t *testing.T) {
	onlyIfWritable(t)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.md"), "a\n")

	// A *file* where the destination directory should be: MkdirAll under it
	// cannot succeed.
	blocked := filepath.Join(t.TempDir(), "blocked")
	writeFile(t, blocked, "in the way\n")

	if err := copyTree(src, filepath.Join(blocked, "out")); err == nil {
		t.Fatal("expected an error when the destination cannot be created")
	}
}

// A destination directory that is read-only fails when a file is written into
// it, so copyFile surfaces the open error.
func TestCopyFile_DestinationUnwritable(t *testing.T) {
	onlyIfWritable(t)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.md"), "a\n")

	dst := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(dst, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dst, 0o755) })

	if err := copyTree(src, dst); err == nil {
		t.Fatal("expected an error writing into a read-only destination")
	}
}

// A symlink whose target cannot be read back fails rather than being skipped,
// since skipping would silently drop part of the tree.
func TestCopyTree_DanglingSymlinkIsReproduced(t *testing.T) {
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "a.md"), "a\n")
	// A link to a path that does not exist is still a link that must be
	// reproduced, not an error.
	if err := os.Symlink("nowhere.md", filepath.Join(src, "dangling")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	dst := filepath.Join(t.TempDir(), "out")
	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree with a dangling symlink: %v", err)
	}
	li, err := os.Lstat(filepath.Join(dst, "dangling"))
	if err != nil {
		t.Fatalf("dangling symlink was not reproduced: %v", err)
	}
	if li.Mode()&os.ModeSymlink == 0 {
		t.Error("dangling symlink was not reproduced as a link")
	}
}

// Save must report a plugins path that cannot be created — a file where the
// directory belongs — rather than failing later with a confusing error.
func TestSave_PluginsPathBlocked(t *testing.T) {
	home := t.TempDir()
	// A regular file occupies the plugins directory's path.
	writeFile(t, Root(home), "in the way\n")

	if err := NewRegistry().Save(home); err == nil {
		t.Fatal("expected an error when the plugins directory cannot be created")
	}
}

// insideDir resolves symlinks before comparing, and must still answer for a
// path that does not exist yet — the install destination is often created after
// the containment check.
func TestInsideDir_NonexistentPaths(t *testing.T) {
	root := t.TempDir()
	// Neither side exists: the cleaned paths are compared instead.
	if insideDir(filepath.Join(root, "absent"), filepath.Join(root, "elsewhere")) {
		t.Error("an absent path must not report as inside an absent directory")
	}
	if !insideDir(filepath.Join(root, "sub", "file.md"), root) {
		t.Error("a not-yet-created path under root must report as inside")
	}
}

// A git failure that writes nothing to stderr still carries git's exit error
// rather than reporting an empty reason.
func TestGit_ErrorWithoutStderr(t *testing.T) {
	// A directory that is not a repository: git writes to stderr, so this
	// covers the common path. A bad flag covers the "no output" fallback.
	dir := t.TempDir()
	_, err := git(context.Background(), dir, "rev-parse", "--verify", "nosuchref")
	if err == nil {
		t.Fatal("expected an error from a failing git command")
	}
	if !strings.Contains(err.Error(), "git ") {
		t.Errorf("error = %v, want it to name the failing command", err)
	}

	// A command git cannot even start, so no stderr is captured at all.
	_, err = git(context.Background(), dir, "not-a-real-git-subcommand")
	if err == nil {
		t.Fatal("expected an error for an unknown git subcommand")
	}
}

// A plugin whose source sits inside its own install destination must be refused
// *before* anything is removed. The destination is cleared by RemoveAll before
// the copy, so checking containment afterwards would delete the source and only
// then fail — losing the files the user asked to install.
func TestInstall_RefusesDestinationContainingSource(t *testing.T) {
	home := t.TempDir()
	// A local marketplace whose plugin lives inside the plugins directory, so
	// the install destination (plugins/<name>) contains the source.
	pluginSrc := filepath.Join(Root(home), "self")
	writeSkillFixture(t, filepath.Join(pluginSrc, "skills"), "self-skill")
	writeFile(t, filepath.Join(pluginSrc, ManifestDir, PluginName), `{"name":"self","version":"1.0.0"}`)

	// Point a marketplace at the plugins directory itself.
	mp := t.TempDir()
	writeFile(t, filepath.Join(mp, ManifestDir, MarketplaceName), `{
	  "name": "local-market",
	  "plugins": [{"name":"self","version":"1.0.0","source":"./self"}]
	}`)
	writeFile(t, filepath.Join(mp, "README.md"), "x\n")
	gitInit(t, mp)

	m := &Manager{PiHome: home}
	ctx := context.Background()
	if _, err := m.AddMarketplace(ctx, mp); err != nil {
		t.Fatal(err)
	}

	// Install from the in-plugins source by symlinking the marketplace's plugin
	// path at it: the containment guard is about the resolved destination.
	linked := filepath.Join(mp, "self")
	if err := os.Symlink(pluginSrc, linked); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := m.Install(ctx, "self@local-market")
	if err == nil {
		t.Fatal("expected an install whose destination contains its source to be refused")
	}
	if !strings.Contains(err.Error(), "contains its own source") {
		t.Errorf("error = %v, want it to explain the self-containment", err)
	}
	// The source survived: the guard ran before the destination was cleared.
	if _, statErr := os.Stat(filepath.Join(pluginSrc, "skills", "self-skill", "SKILL.md")); statErr != nil {
		t.Errorf("the guard deleted the plugin's own source files: %v", statErr)
	}
}

// An installed plugin whose directory cannot be removed must be reported rather
// than left half-removed and dropped from the registry.
func TestUninstall_RemovalFails(t *testing.T) {
	onlyIfWritable(t)
	home := t.TempDir()
	dir := PluginDir(home, "stuck")
	writeSkillFixture(t, filepath.Join(dir, "skills"), "s")
	// A non-empty subdirectory that cannot be written is still removable by
	// the owner, so make the *parent* read-only to block the removal.
	if err := os.Chmod(Root(home), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(Root(home), 0o755) })

	r := NewRegistry()
	r.Plugins["stuck"] = Installed{Name: "stuck"}
	if err := r.Save(home); err != nil {
		// Save needs the directory writable; restore, save, then re-block.
		_ = os.Chmod(Root(home), 0o755)
		if err := r.Save(home); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(Root(home), 0o500); err != nil {
			t.Fatal(err)
		}
	}

	m := &Manager{PiHome: home}
	if err := m.Uninstall("stuck"); err == nil {
		t.Fatal("expected an error removing a plugin from a read-only directory")
	}
	// The entry survives, so the user can retry once permissions are fixed.
	after, err := LoadRegistry(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Plugins["stuck"]; !ok {
		t.Error("a failed uninstall still dropped the registry entry")
	}
}
