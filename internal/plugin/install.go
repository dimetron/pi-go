package plugin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// cloneTimeout bounds a single git operation. Cloning a plugin repository is a
// network round trip to a third party, so it must not be able to hang a CLI
// invocation or a TUI turn indefinitely.
const cloneTimeout = 3 * time.Minute

// Logf is a progress sink. A nil Logf discards messages — the TUI passes one
// that routes into the session log rather than the terminal, and the CLI
// passes one that prints.
type Logf func(format string, args ...any)

// Manager performs plugin and marketplace operations against one pi-go home.
type Manager struct {
	// PiHome is the pi-go home directory, normally ~/.pi-go.
	PiHome string
	// Log receives progress messages; nil is safe and discards them.
	Log Logf
}

// logf forwards to Log when one is set.
func (m *Manager) logf(format string, args ...any) {
	if m.Log != nil {
		m.Log(format, args...)
	}
}

// git runs a git command in dir and returns its trimmed stdout. Both streams
// are captured so a failure can report git's own message rather than just an
// exit status.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	// Never prompt: a credential prompt would hang a non-interactive run.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// ensureGit reports a clear error when git is not installed, before any
// operation that needs it.
func ensureGit(ctx context.Context) error {
	if _, err := git(ctx, "", "version"); err != nil {
		return fmt.Errorf("git is required to install plugins: %w", err)
	}
	return nil
}

// headSha returns the commit checked out in a repository.
func headSha(ctx context.Context, dir string) (string, error) {
	return git(ctx, dir, "rev-parse", "HEAD")
}

// cloneAt clones url into dir at an optional branch or tag. A shallow clone is
// enough: a plugin is read, not developed.
func cloneAt(ctx context.Context, url, ref, dir string) error {
	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, dir)
	_, err := git(ctx, "", args...)
	return err
}

// syncRepo makes dir match the remote repository: it clones when dir is absent
// and fast-forwards when it exists.
func (m *Manager) syncRepo(ctx context.Context, url, ref, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		m.logf("updating %s", dir)
		if _, err := git(ctx, dir, "fetch", "--depth", "1", "origin"); err != nil {
			return err
		}
		// Reset rather than merge: a plugin directory is a cache of the
		// remote, so any local modification is discarded deliberately.
		target := "FETCH_HEAD"
		if ref != "" {
			target = ref
		}
		if _, err := git(ctx, dir, "reset", "--hard", target); err != nil {
			return err
		}
		return nil
	}
	m.logf("cloning %s", url)
	return cloneAt(ctx, url, ref, dir)
}

// NormalizeSource turns a user-supplied marketplace or plugin source into a git
// URL or a local directory path.
//
// Accepted forms: an absolute or relative directory, an http(s) or git URL, an
// scp-style git address (git@host:owner/repo), and the owner/repo shorthand for
// GitHub.
func NormalizeSource(source string) string {
	trimmed := strings.TrimSuffix(strings.TrimSpace(source), "/")
	if trimmed == "" {
		return ""
	}
	if dirExists(trimmed) || dirExists(filepath.Join(trimmed, ManifestDir)) {
		if abs, err := filepath.Abs(trimmed); err == nil {
			return abs
		}
		return trimmed
	}
	if strings.HasPrefix(trimmed, "http://") ||
		strings.HasPrefix(trimmed, "https://") ||
		strings.HasPrefix(trimmed, "git://") ||
		strings.HasPrefix(trimmed, "ssh://") ||
		strings.HasPrefix(trimmed, "git@") ||
		strings.HasSuffix(trimmed, ".git") {
		return trimmed
	}
	// owner/repo shorthand: exactly one slash, no path traversal.
	if parts := strings.Split(trimmed, "/"); len(parts) == 2 && parts[0] != "" && parts[1] != "" &&
		!strings.Contains(trimmed, "..") {
		return "https://github.com/" + trimmed + ".git"
	}
	return trimmed
}

// dirExists reports whether path is an existing directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// insideDir reports whether path is dir or lies within it.
//
// Comparing cleaned paths is not enough: the install destination usually does
// not exist yet, while the source does, so resolving only the side that exists
// leaves the two rooted differently whenever a parent is a symlink — as /var is
// on macOS. Both sides are therefore resolved through their nearest existing
// ancestor, which puts them on the same footing whether or not they exist.
func insideDir(path, dir string) bool {
	rel, err := filepath.Rel(resolveExisting(dir), resolveExisting(path))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolveExisting returns p with symlinks resolved through its longest existing
// prefix, keeping any trailing elements that do not exist yet. A path that
// cannot be resolved at all is returned cleaned.
func resolveExisting(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	dir, base := filepath.Split(filepath.Clean(p))
	if base == "" || dir == "" {
		return filepath.Clean(p)
	}
	if filepath.Clean(dir) == filepath.Clean(p) {
		// Reached the root without resolving; nothing more to try.
		return filepath.Clean(p)
	}
	resolvedDir := resolveExisting(dir)
	if filepath.Base(resolvedDir) == base {
		return resolvedDir
	}
	return filepath.Join(resolvedDir, base)
}

// isLocalDir reports whether a normalized source is a directory rather than a
// remote to clone.
func isLocalDir(source string) bool {
	return filepath.IsAbs(source) && dirExists(source)
}

// AddMarketplace registers a marketplace from a local directory or a remote
// repository, and returns its record. Registering a marketplace that is already
// known refreshes it in place.
func (m *Manager) AddMarketplace(ctx context.Context, source string) (MarketplaceRecord, error) {
	normalized := NormalizeSource(source)
	if normalized == "" {
		return MarketplaceRecord{}, errors.New("no marketplace source given")
	}
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return MarketplaceRecord{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	dir := normalized
	if !isLocalDir(normalized) {
		if err := ensureGit(ctx); err != nil {
			return MarketplaceRecord{}, err
		}
		// The marketplace's own name lives in the manifest, so clone to a
		// temporary directory, read the name, then move it into place.
		tmp := filepath.Join(Root(m.PiHome), "tmp-marketplace")
		if err := os.RemoveAll(tmp); err != nil {
			return MarketplaceRecord{}, err
		}
		defer os.RemoveAll(tmp)

		if err := m.syncRepo(ctx, normalized, "", tmp); err != nil {
			return MarketplaceRecord{}, err
		}
		dir = tmp
	}

	mp, err := ReadMarketplace(dir)
	if err != nil {
		return MarketplaceRecord{}, fmt.Errorf("%s is not a plugin marketplace: %w", normalized, err)
	}
	if err := ValidName(mp.Name); err != nil {
		return MarketplaceRecord{}, fmt.Errorf("marketplace name from %s is unusable: %w", normalized, err)
	}

	final := MarketplaceDir(m.PiHome, mp.Name)
	if dir == normalized && isLocalDir(normalized) {
		// A local marketplace is used where it lies; nothing is copied.
		final = normalized
	} else if dir != final {
		if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
			return MarketplaceRecord{}, err
		}
		if err := os.RemoveAll(final); err != nil {
			return MarketplaceRecord{}, err
		}
		if err := os.Rename(dir, final); err != nil {
			return MarketplaceRecord{}, fmt.Errorf("installing marketplace %s: %w", mp.Name, err)
		}
	}

	rec := MarketplaceRecord{
		Name:            mp.Name,
		Source:          normalized,
		InstallLocation: final,
		LastUpdated:     time.Now().UTC().Format(time.RFC3339),
	}
	registry.Marketplaces[mp.Name] = rec
	if err := registry.Save(m.PiHome); err != nil {
		return MarketplaceRecord{}, err
	}
	m.logf("registered marketplace %s (%d plugins)", mp.Name, len(mp.Plugins))
	return rec, nil
}

// MarketplacePlugins lists the plugins a registered marketplace offers.
func (m *Manager) MarketplacePlugins(ctx context.Context, name string) ([]MarketplacePlugin, error) {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return nil, err
	}
	rec, ok := registry.Marketplaces[name]
	if !ok {
		return nil, fmt.Errorf("unknown marketplace %q — add it with: pi plugin marketplace add <source>", name)
	}
	mp, err := ReadMarketplace(rec.InstallLocation)
	if err != nil {
		return nil, err
	}
	return mp.Plugins, nil
}

// ResolvePlugin finds a plugin's catalog entry, its marketplace record, and the
// directory the marketplace repository occupies.
//
// A spec may be "plugin" — resolved against every registered marketplace, which
// is an error if more than one offers it — or "plugin@marketplace".
func (m *Manager) ResolvePlugin(spec string) (MarketplacePlugin, MarketplaceRecord, string, error) {
	name, marketplaceName, _ := strings.Cut(spec, "@")
	if name == "" {
		return MarketplacePlugin{}, MarketplaceRecord{}, "", errors.New("no plugin name given")
	}
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return MarketplacePlugin{}, MarketplaceRecord{}, "", err
	}
	if len(registry.Marketplaces) == 0 {
		return MarketplacePlugin{}, MarketplaceRecord{}, "",
			errors.New("no marketplaces registered — add one with: pi plugin marketplace add <source>")
	}

	names := make([]string, 0, len(registry.Marketplaces))
	if marketplaceName != "" {
		if _, ok := registry.Marketplaces[marketplaceName]; !ok {
			return MarketplacePlugin{}, MarketplaceRecord{}, "", fmt.Errorf("unknown marketplace %q", marketplaceName)
		}
		names = append(names, marketplaceName)
	} else {
		for n := range registry.Marketplaces {
			names = append(names, n)
		}
	}

	var found []MarketplacePlugin
	var foundRec MarketplaceRecord
	var foundRepo string
	for _, n := range names {
		rec := registry.Marketplaces[n]
		mp, err := ReadMarketplace(rec.InstallLocation)
		if err != nil {
			// A marketplace whose files have gone missing should not block
			// resolving a plugin from another one.
			m.logf("warning: skipping marketplace %s: %v", n, err)
			continue
		}
		if p, ok := FindPlugin(mp, name); ok {
			found = append(found, p)
			foundRec, foundRepo = rec, rec.InstallLocation
		}
	}

	switch len(found) {
	case 0:
		if marketplaceName != "" {
			return MarketplacePlugin{}, MarketplaceRecord{}, "", fmt.Errorf("marketplace %q has no plugin %q", marketplaceName, name)
		}
		return MarketplacePlugin{}, MarketplaceRecord{}, "", fmt.Errorf("no registered marketplace offers a plugin named %q", name)
	case 1:
		return found[0], foundRec, foundRepo, nil
	default:
		available := append([]string{}, names...)
		return MarketplacePlugin{}, MarketplaceRecord{}, "", fmt.Errorf(
			"plugin %q exists in more than one marketplace (%s) — install it as %s@<marketplace>",
			name, strings.Join(available, ", "), name)
	}
}

// Install installs the plugin named by spec, either "plugin" or
// "plugin@marketplace".
func (m *Manager) Install(ctx context.Context, spec string) (*Installed, error) {
	entry, rec, repoDir, err := m.ResolvePlugin(spec)
	if err != nil {
		return nil, err
	}
	if err := ValidName(entry.Name); err != nil {
		return nil, fmt.Errorf("plugin name from %s is unusable: %w", rec.Name, err)
	}

	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return nil, err
	}
	if existing, ok := registry.Plugins[entry.Name]; ok {
		return nil, fmt.Errorf("plugin %q is already installed (version %s) — update it with: pi plugin update %s",
			entry.Name, existing.Version, entry.Name)
	}

	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	// Resolve the entry's source into a directory on disk.
	//
	// remote is true when the plugin lives somewhere that can be consumed:
	// a fresh clone, which Install may move, or a local directory the caller
	// owns, which Install may also move (that is how a local plugin becomes
	// the install). It is false for a plugin inside a marketplace repository:
	// that repository is a managed cache shared by every plugin it lists, so
	// its contents must be copied, never moved.
	srcDir := repoDir
	remote := true
	var sha string
	if entry.Source.IsRemote() {
		if err := ensureGit(ctx); err != nil {
			return nil, err
		}
		tmp := filepath.Join(Root(m.PiHome), "tmp-plugin")
		if err := os.RemoveAll(tmp); err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)

		m.logf("cloning %s", entry.Source.URL)
		if err := cloneAt(ctx, entry.Source.URL, entry.Source.Ref, tmp); err != nil {
			return nil, err
		}
		sha, _ = headSha(ctx, tmp)
		srcDir = tmp
	} else {
		sha, _ = headSha(ctx, repoDir)
	}

	if entry.Source.Kind == SourceGitSubdir {
		sub := filepath.Join(srcDir, filepath.Clean(entry.Source.Path))
		if !dirExists(sub) {
			return nil, fmt.Errorf("plugin %s: subdirectory %q not found in %s",
				entry.Name, entry.Source.Path, entry.Source.URL)
		}
		srcDir = sub
	}
	if entry.Source.Kind == SourceRelative {
		sub := filepath.Join(repoDir, filepath.Clean(entry.Source.Path))
		if !dirExists(sub) {
			return nil, fmt.Errorf("plugin %s: path %q not found in marketplace %s",
				entry.Name, entry.Source.Path, rec.Name)
		}
		srcDir, remote = sub, false
	}

	// A plugin with no skills is still installable — it may ship only agents —
	// but the user should hear about it, since skills are what pi-go consumes
	// today.
	if !dirExists(filepath.Join(srcDir, "skills")) &&
		!dirExists(filepath.Join(srcDir, ".pi-go", "skills")) &&
		!dirExists(filepath.Join(srcDir, "SKILL.md")) {
		m.logf("note: plugin %s has no skills directory; pi-go will read no skills from it", entry.Name)
	}

	dst := PluginDir(m.PiHome, entry.Name)
	if err := os.RemoveAll(dst); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	// A rename is only valid within one filesystem, so a local plugin on
	// another volume falls back to a copy. A plugin inside a marketplace
	// repository is always copied, because that repository is a shared cache
	// and moving part of it away would break every other plugin it lists.
	switch {
	case remote:
		if err := os.Rename(srcDir, dst); err != nil {
			if err := copyTree(srcDir, dst); err != nil {
				return nil, fmt.Errorf("installing plugin %s: %w", entry.Name, err)
			}
		}
	default:
		if insideDir(dst, srcDir) {
			return nil, fmt.Errorf("plugin %s: refusing to install into %s, which contains its own source",
				entry.Name, dst)
		}
		if err := copyTree(srcDir, dst); err != nil {
			return nil, fmt.Errorf("installing plugin %s: %w", entry.Name, err)
		}
	}

	version := entry.Version
	if manifest, err := ReadManifest(dst); err == nil && manifest.Version != "" {
		version = manifest.Version
	}

	inst := Installed{
		Name:        entry.Name,
		Marketplace: rec.Name,
		Version:     version,
		URL:         entry.Source.URL,
		Ref:         entry.Source.Ref,
		Sha:         sha,
		Path:        entry.Source.Path,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	registry.Plugins[entry.Name] = inst
	if err := registry.Save(m.PiHome); err != nil {
		return nil, err
	}
	m.logf("installed %s %s", entry.Name, version)
	return &inst, nil
}

// List returns every installed plugin, ordered by name.
func (m *Manager) List() ([]Installed, error) {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(registry.Plugins))
	for name := range registry.Plugins {
		names = append(names, name)
	}
	sortStrings(names)
	out := make([]Installed, 0, len(names))
	for _, name := range names {
		out = append(out, registry.Plugins[name])
	}
	return out, nil
}

// ListMarketplaces returns every registered marketplace, ordered by name.
func (m *Manager) ListMarketplaces() ([]MarketplaceRecord, error) {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(registry.Marketplaces))
	for name := range registry.Marketplaces {
		names = append(names, name)
	}
	sortStrings(names)
	out := make([]MarketplaceRecord, 0, len(names))
	for _, name := range names {
		out = append(out, registry.Marketplaces[name])
	}
	return out, nil
}

// Uninstall removes an installed plugin and its files.
func (m *Manager) Uninstall(name string) error {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return err
	}
	if _, ok := registry.Plugins[name]; !ok {
		return fmt.Errorf("plugin %q is not installed", name)
	}
	// Validate before building a path from a name, even though this one was
	// read from our own registry: the registry can be edited by hand.
	if err := ValidName(name); err != nil {
		return fmt.Errorf("refusing to remove plugin %q: %w", name, err)
	}
	if err := os.RemoveAll(PluginDir(m.PiHome, name)); err != nil {
		return fmt.Errorf("removing plugin files: %w", err)
	}
	delete(registry.Plugins, name)
	if err := registry.Save(m.PiHome); err != nil {
		return err
	}
	m.logf("uninstalled %s", name)
	return nil
}

// Update re-installs a plugin from its recorded source, reporting whether the
// commit changed. An empty name updates every installed plugin.
func (m *Manager) Update(ctx context.Context, name string) (bool, error) {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return false, err
	}
	targets := []string{name}
	if name == "" {
		targets = targets[:0]
		for n := range registry.Plugins {
			targets = append(targets, n)
		}
		sortStrings(targets)
	}

	changed := false
	for _, target := range targets {
		inst, ok := registry.Plugins[target]
		if !ok {
			return changed, fmt.Errorf("plugin %q is not installed", target)
		}
		before := inst.Sha
		spec := target
		if inst.Marketplace != "" {
			spec = target + "@" + inst.Marketplace
		}

		// Install refuses to run while the plugin is already registered, so
		// the entry comes out first and goes back if the install fails.
		//
		// Both steps re-load the registry rather than writing back the
		// instance loaded above. Install persists through a registry of its
		// own, so re-saving a stale instance here would silently drop the
		// record of every plugin already updated in this loop — updating all
		// plugins would leave only the last one registered.
		if err := m.unregister(target); err != nil {
			return changed, err
		}
		fresh, err := m.Install(ctx, spec)
		if err != nil {
			if restoreErr := m.restore(inst); restoreErr != nil {
				m.logf("warning: could not restore registry entry for %s: %v", target, restoreErr)
			}
			return changed, fmt.Errorf("updating %s: %w", target, err)
		}
		if fresh.Sha != before {
			changed = true
		}
	}
	return changed, nil
}

// unregister drops a plugin's registry entry. It re-loads the registry so the
// write cannot clobber records that another operation — Install, most often —
// added after the caller last read the file.
func (m *Manager) unregister(name string) error {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return err
	}
	delete(registry.Plugins, name)
	return registry.Save(m.PiHome)
}

// restore puts a plugin's record back after a failed operation, under the
// current contents of the registry rather than a previously read copy.
func (m *Manager) restore(inst Installed) error {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return err
	}
	registry.Plugins[inst.Name] = inst
	return registry.Save(m.PiHome)
}

// UpdateMarketplace refreshes registered marketplaces so their catalogs pick up
// new plugin versions.
func (m *Manager) UpdateMarketplace(ctx context.Context, name string) error {
	registry, err := LoadRegistry(m.PiHome)
	if err != nil {
		return err
	}
	targets := []string{name}
	if name == "" {
		targets = targets[:0]
		for n := range registry.Marketplaces {
			targets = append(targets, n)
		}
		sortStrings(targets)
	}
	ctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()

	for _, target := range targets {
		rec, ok := registry.Marketplaces[target]
		if !ok {
			return fmt.Errorf("unknown marketplace %q", target)
		}
		if isLocalDir(rec.Source) {
			m.logf("marketplace %s is a local directory; nothing to fetch", target)
			continue
		}
		if err := ensureGit(ctx); err != nil {
			return err
		}
		if err := m.syncRepo(ctx, rec.Source, "", rec.InstallLocation); err != nil {
			return fmt.Errorf("updating marketplace %s: %w", target, err)
		}
		rec.LastUpdated = time.Now().UTC().Format(time.RFC3339)
		registry.Marketplaces[target] = rec
	}
	return registry.Save(m.PiHome)
}
