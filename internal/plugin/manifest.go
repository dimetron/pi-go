// Package plugin installs and tracks pi-go plugins — bundles of skills
// published through a Claude Code-compatible plugin marketplace.
//
// It reads the same manifest format other agents already use, so the existing
// ecosystem (obra/superpowers-marketplace, claude-plugins-official, and any
// repository carrying a .claude-plugin/marketplace.json) works unchanged:
//
//	marketplace repo/.claude-plugin/marketplace.json   catalog of plugins
//	plugin repo/.claude-plugin/plugin.json             one plugin's identity
package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Source kinds a marketplace entry can use. All three appear in the wild —
// across the marketplaces on this machine, url is the most common, git-subdir
// and plain relative paths follow.
const (
	// SourceRelative is a path inside the marketplace repository.
	SourceRelative = "relative"
	// SourceURL is a whole remote git repository holding one plugin.
	SourceURL = "url"
	// SourceGitSubdir is a subdirectory of a remote git repository.
	SourceGitSubdir = "git-subdir"
	// SourceGitHub is an "owner/name" shorthand for a GitHub repository.
	SourceGitHub = "github"
)

// Manifest directory and file names, fixed by the convention this package
// reads and writes.
const (
	ManifestDir     = ".claude-plugin"
	MarketplaceName = "marketplace.json"
	PluginName      = "plugin.json"
)

// Owner identifies a marketplace or plugin author.
type Owner struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// MarketplaceMetadata is the optional metadata block of a marketplace.
type MarketplaceMetadata struct {
	Description string `json:"description"`
	Version     string `json:"version"`
}

// Marketplace is a catalog of installable plugins.
type Marketplace struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Owner       Owner               `json:"owner"`
	Metadata    MarketplaceMetadata `json:"metadata"`
	Plugins     []MarketplacePlugin `json:"plugins"`
}

// MarketplacePlugin is one catalog entry.
type MarketplacePlugin struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Strict      bool   `json:"strict"`
	Source      Source `json:"source"`
}

// Manifest is a plugin's own identity, read from
// <plugin>/.claude-plugin/plugin.json.
type Manifest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Version     string   `json:"version"`
	Author      Owner    `json:"author"`
	Homepage    string   `json:"homepage"`
	Repository  string   `json:"repository"`
	License     string   `json:"license"`
	Keywords    []string `json:"keywords"`
}

// Source locates a plugin. The convention expresses it either as a bare
// string path or as an object, so it unmarshals from both:
//
//	"./plugins/foo"
//	{"source":"url","url":"https://…/x.git","ref":"main","sha":"abc"}
//	{"source":"git-subdir","url":"https://…/x.git","path":"plugins/foo"}
//	{"source":"github","repo":"owner/name"}
type Source struct {
	// Kind is one of the Source* constants.
	Kind string
	// Path is the plugin's directory: relative to the marketplace repository
	// for SourceRelative, and to the cloned repository for SourceGitSubdir.
	Path string
	// URL is the git remote for the remote kinds.
	URL string
	// Repo is the "owner/name" form, for SourceGitHub.
	Repo string
	// Ref is a branch or tag to check out.
	Ref string
	// Sha is a commit the catalog pins, when it records one.
	Sha string
}

// UnmarshalJSON accepts either the bare-string or the object form. A bare
// string is always a path within the marketplace repository.
func (s *Source) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		s.Kind = SourceRelative
		s.Path = str
		return nil
	}

	var raw struct {
		Source string `json:"source"`
		URL    string `json:"url"`
		Repo   string `json:"repo"`
		Path   string `json:"path"`
		Ref    string `json:"ref"`
		Sha    string `json:"sha"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing plugin source: %w", err)
	}
	s.URL, s.Repo, s.Path, s.Ref, s.Sha = raw.URL, raw.Repo, raw.Path, raw.Ref, raw.Sha

	s.Kind = raw.Source
	if s.Kind == "" {
		// An object with no explicit kind: infer from the fields present.
		switch {
		case s.Repo != "":
			s.Kind = SourceGitHub
		case s.URL != "":
			s.Kind = SourceURL
		default:
			s.Kind = SourceRelative
		}
	}
	if s.Kind == SourceGitHub && s.Repo != "" {
		s.URL = "https://github.com/" + s.Repo + ".git"
	}
	return nil
}

// IsRemote reports whether the source must be cloned rather than read from the
// marketplace repository already on disk.
func (s Source) IsRemote() bool {
	return s.Kind == SourceURL || s.Kind == SourceGitSubdir || s.Kind == SourceGitHub
}

// ReadMarketplace loads .claude-plugin/marketplace.json from dir. A marketplace
// that does not name itself falls back to its directory name, so a catalog can
// always be addressed by name.
func ReadMarketplace(dir string) (Marketplace, error) {
	var m Marketplace
	path := filepath.Join(dir, ManifestDir, MarketplaceName)
	if err := readJSON(path, &m); err != nil {
		return Marketplace{}, err
	}
	if m.Name == "" {
		m.Name = filepath.Base(filepath.Clean(dir))
	}
	if len(m.Plugins) == 0 {
		return Marketplace{}, fmt.Errorf("%s lists no plugins", path)
	}
	return m, nil
}

// ReadManifest loads .claude-plugin/plugin.json from dir. A missing or
// nameless manifest is not an error — the caller falls back to the name the
// catalog used, and only treats the file as a best-effort version source.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	if err := readJSON(filepath.Join(dir, ManifestDir, PluginName), &m); err != nil {
		if os.IsNotExist(err) {
			return Manifest{}, nil
		}
		return Manifest{}, err
	}
	return m, nil
}

// FindPlugin returns the named entry from a marketplace catalog.
func FindPlugin(m Marketplace, name string) (MarketplacePlugin, bool) {
	for _, p := range m.Plugins {
		if p.Name == name {
			return p, true
		}
	}
	return MarketplacePlugin{}, false
}

// readJSON decodes a JSON file into v, wrapping the path into any error so a
// malformed manifest is traceable to the file that produced it.
func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	return nil
}
