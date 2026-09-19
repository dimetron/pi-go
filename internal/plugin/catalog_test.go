package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeCatalog writes a marketplace.json for the given plugins.
//
// The catalog is marshaled rather than assembled by string concatenation. A
// local plugin's URL is a filesystem path, and on Windows that path contains
// backslashes — interpolating it into a JSON literal produces invalid JSON
// (`\U` is not an escape sequence), which fails the parse on Windows while
// passing everywhere the path has forward slashes.
//
// plugins is a map of plugin name to a source: either a Source value or a plain
// string for a relative path.
func writeCatalog(t *testing.T, dir, name string, plugins map[string]any) {
	t.Helper()
	entries := make([]map[string]any, 0, len(plugins))
	for pluginName, src := range plugins {
		entry := map[string]any{"name": pluginName, "version": "1.0.0"}
		switch v := src.(type) {
		case string:
			entry["source"] = v
		case Source:
			s := map[string]any{"source": v.Kind}
			if v.URL != "" {
				s["url"] = v.URL
			}
			if v.Repo != "" {
				s["repo"] = v.Repo
			}
			if v.Path != "" {
				s["path"] = v.Path
			}
			if v.Ref != "" {
				s["ref"] = v.Ref
			}
			entry["source"] = s
		default:
			t.Fatalf("unsupported source type %T for plugin %s", src, pluginName)
		}
		entries = append(entries, entry)
	}

	doc := map[string]any{"name": name, "plugins": entries}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshaling catalog: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ManifestDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestDir, MarketplaceName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fileURL renders a local path as a file:// URL that the git CLI accepts on
// every platform.
func fileURL(path string) string {
	return "file://" + filepath.ToSlash(path)
}

// A catalog written by writeCatalog must parse back even when a source carries
// a Windows-shaped path. This runs on every host, so a regression is caught on
// the developer's machine rather than only in CI.
func TestWriteCatalog_WindowsPathRoundTrips(t *testing.T) {
	winPath := `C:\Users\runner\AppData\Local\Temp\TestX\001`
	dir := t.TempDir()
	writeCatalog(t, dir, "win-market", map[string]any{
		"win-plugin": Source{Kind: SourceGitSubdir, URL: winPath, Path: "plugins/p"},
	})

	mp, err := ReadMarketplace(dir)
	if err != nil {
		t.Fatalf("a catalog carrying a Windows path must parse: %v", err)
	}
	got, ok := FindPlugin(mp, "win-plugin")
	if !ok {
		t.Fatal("plugin not found in the catalog")
	}
	if got.Source.URL != winPath {
		t.Errorf("URL = %q, want %q", got.Source.URL, winPath)
	}
	if got.Source.Path != "plugins/p" {
		t.Errorf("path = %q, want plugins/p", got.Source.Path)
	}
}
