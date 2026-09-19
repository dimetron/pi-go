package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RegistryVersion is the on-disk schema version of installed.json.
const RegistryVersion = 1

// Layout under the pi-go home (~/.pi-go):
//
//	plugins/installed.json              this registry
//	plugins/marketplaces/<name>/        cloned marketplace repositories
//	plugins/<name>/                     installed plugins
//
// marketplacesDirName is reserved: a plugin may not take this name, or its
// directory would collide with the marketplace clones.
const marketplacesDirName = "marketplaces"

// Installed is one installed plugin.
type Installed struct {
	// Name is the plugin name, which is also its directory under plugins/.
	Name string `json:"name"`
	// Marketplace is the marketplace it was installed from.
	Marketplace string `json:"marketplace,omitempty"`
	// Version is the catalog's version, when it records one.
	Version string `json:"version,omitempty"`
	// URL is the git remote it was cloned from, empty for local installs.
	URL string `json:"url,omitempty"`
	// Ref is the branch or tag that was checked out.
	Ref string `json:"ref,omitempty"`
	// Sha is the commit that was checked out, recorded so an update can tell
	// whether anything actually moved.
	Sha string `json:"sha,omitempty"`
	// Path is the plugin's subdirectory within its repository: set for
	// git-subdir sources, empty when the repository root is the plugin.
	Path string `json:"path,omitempty"`
	// SkillsDir is the relative path from the plugin root to its skills
	// directory, empty when the default applies (see ResolveSkillsDir).
	SkillsDir string `json:"skillsDir,omitempty"`
	// InstalledAt is an RFC3339 timestamp.
	InstalledAt string `json:"installedAt"`
}

// MarketplaceRecord is one registered marketplace.
type MarketplaceRecord struct {
	Name string `json:"name"`
	// Source is the git URL or local directory the marketplace came from.
	Source string `json:"source"`
	// InstallLocation is the clone's directory.
	InstallLocation string `json:"installLocation"`
	// LastUpdated is an RFC3339 timestamp.
	LastUpdated string `json:"lastUpdated"`
}

// Registry is the persistent record of what a machine has installed.
type Registry struct {
	Version      int                          `json:"version"`
	Marketplaces map[string]MarketplaceRecord `json:"marketplaces,omitempty"`
	Plugins      map[string]Installed         `json:"plugins,omitempty"`
}

// Root returns the plugins directory under a pi-go home.
func Root(piHome string) string {
	return filepath.Join(piHome, "plugins")
}

// RegistryPath returns the path of the installed.json registry.
func RegistryPath(piHome string) string {
	return filepath.Join(Root(piHome), "installed.json")
}

// MarketplaceDir returns the clone directory for a marketplace.
func MarketplaceDir(piHome, name string) string {
	return filepath.Join(Root(piHome), marketplacesDirName, name)
}

// PluginDir returns the install directory for a plugin.
func PluginDir(piHome, name string) string {
	return filepath.Join(Root(piHome), name)
}

// NewRegistry returns an empty, usable registry. Both maps are initialized, so
// a caller can record into a freshly created registry without a nil-map panic.
func NewRegistry() *Registry {
	return &Registry{
		Version:      RegistryVersion,
		Marketplaces: map[string]MarketplaceRecord{},
		Plugins:      map[string]Installed{},
	}
}

// LoadRegistry reads installed.json, returning an empty registry when the file
// does not exist yet. A registry written by a newer pi-go is an error rather
// than a silent downgrade: the fields it relies on may not be understood here.
func LoadRegistry(piHome string) (*Registry, error) {
	data, err := os.ReadFile(RegistryPath(piHome))
	if err != nil {
		if os.IsNotExist(err) {
			return NewRegistry(), nil
		}
		return nil, fmt.Errorf("reading plugin registry: %w", err)
	}
	r := NewRegistry()
	if err := json.Unmarshal(data, r); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", RegistryPath(piHome), err)
	}
	if r.Version > RegistryVersion {
		return nil, fmt.Errorf("plugin registry %s has version %d, newer than this pi-go supports (%d)",
			RegistryPath(piHome), r.Version, RegistryVersion)
	}
	if r.Version == 0 {
		r.Version = RegistryVersion
	}
	// A registry file may omit either map (or hold an explicit null), which
	// JSON decodes to nil and would panic on the next write.
	if r.Marketplaces == nil {
		r.Marketplaces = map[string]MarketplaceRecord{}
	}
	if r.Plugins == nil {
		r.Plugins = map[string]Installed{}
	}
	return r, nil
}

// Save writes the registry, creating the plugins directory if needed. It writes
// to a temporary file and renames, so an interrupted save cannot leave a
// half-written registry that the next run would fail to parse.
func (r *Registry) Save(piHome string) error {
	if r.Version == 0 {
		r.Version = RegistryVersion
	}
	dir := Root(piHome)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding plugin registry: %w", err)
	}
	data = append(data, '\n')

	path := RegistryPath(piHome)
	tmp, err := os.CreateTemp(dir, "installed-*.json.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary registry: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing plugin registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing plugin registry: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("installing plugin registry: %w", err)
	}
	return nil
}

// SkillDirs returns the skills directory of every installed plugin, ordered by
// plugin name for a stable result.
//
// The order matters: skill loading lets a later directory override an earlier
// one, and callers place these *before* the user and project directories so an
// installed plugin can never silently replace a skill the user wrote or
// customized. Directories that do not exist are skipped, so a registry entry
// whose files were removed by hand does not break loading.
func (r *Registry) SkillDirs(piHome string) []string {
	names := make([]string, 0, len(r.Plugins))
	for name := range r.Plugins {
		names = append(names, name)
	}
	sort.Strings(names)

	dirs := make([]string, 0, len(names))
	for _, name := range names {
		dir := ResolveSkillsDir(PluginDir(piHome, name), r.Plugins[name])
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// ResolveSkillsDir returns the directory holding a plugin's skills. An explicit
// SkillsDir on the record wins; otherwise the pi-go convention (.pi-go/skills)
// is preferred and the Claude Code convention (skills/) is the fallback. The
// plugin root is returned when neither exists, which is the convention for a
// plugin whose skills sit at the top level.
func ResolveSkillsDir(pluginDir string, inst Installed) string {
	if inst.SkillsDir != "" {
		return filepath.Join(pluginDir, inst.SkillsDir)
	}
	for _, rel := range []string{
		filepath.Join(".pi-go", "skills"),
		"skills",
	} {
		dir := filepath.Join(pluginDir, rel)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			return dir
		}
	}
	return pluginDir
}

// ValidName reports whether a plugin or marketplace name is safe to use as a
// directory name.
//
// Names come from a marketplace manifest, which is untrusted input fetched from
// a third-party repository: a name like "../../.ssh" would otherwise let a
// malicious catalog write outside the plugins directory.
func ValidName(name string) error {
	if name == "" {
		return fmt.Errorf("name is empty")
	}
	if name == marketplacesDirName {
		return fmt.Errorf("name %q is reserved", name)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("name %q is not a valid directory name", name)
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("name %q must not contain a path separator", name)
	}
	if filepath.Base(name) != name || filepath.Clean(name) != name {
		return fmt.Errorf("name %q must be a single clean path element", name)
	}
	return nil
}
