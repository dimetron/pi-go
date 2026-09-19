package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The three source shapes that appear across real marketplaces must all parse.
func TestSourceUnmarshal_RealWorldShapes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Source
	}{
		{
			name: "bare relative path (79 in the wild)",
			in:   `"./plugins/codex"`,
			want: Source{Kind: SourceRelative, Path: "./plugins/codex"},
		},
		{
			name: "url with sha pin (160 in the wild)",
			in:   `{"source":"url","url":"https://github.com/SalesforceAIResearch/agentforce-adlc.git","sha":"09bf1539"}`,
			want: Source{
				Kind: SourceURL,
				URL:  "https://github.com/SalesforceAIResearch/agentforce-adlc.git",
				Sha:  "09bf1539",
			},
		},
		{
			name: "git-subdir with path and ref (96 in the wild)",
			in:   `{"source":"git-subdir","url":"https://github.com/42Crunch-AI/claude-plugins.git","path":"plugins/api-security-testing","ref":"v1.5.5","sha":"30287f5e"}`,
			want: Source{
				Kind: SourceGitSubdir,
				URL:  "https://github.com/42Crunch-AI/claude-plugins.git",
				Path: "plugins/api-security-testing",
				Ref:  "v1.5.5",
				Sha:  "30287f5e",
			},
		},
		{
			name: "superpowers self-reference",
			in:   `"./"`,
			want: Source{Kind: SourceRelative, Path: "./"},
		},
		{
			name: "github shorthand expands to a URL",
			in:   `{"source":"github","repo":"obra/superpowers"}`,
			want: Source{Kind: SourceGitHub, Repo: "obra/superpowers", URL: "https://github.com/obra/superpowers.git"},
		},
		{
			name: "object with no explicit kind infers url",
			in:   `{"url":"https://github.com/x/y.git"}`,
			want: Source{Kind: SourceURL, URL: "https://github.com/x/y.git"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := `{"name":"p","source":` + tt.in + `}`
			var got MarketplacePlugin
			if err := unmarshalPlugin(t, entry, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Source != tt.want {
				t.Errorf("source = %+v\nwant       %+v", got.Source, tt.want)
			}
		})
	}
}

func unmarshalPlugin(t *testing.T, entry string, v *MarketplacePlugin) error {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestDir, MarketplaceName), `{"name":"mp","plugins":[`+entry+`]}`)
	m, err := ReadMarketplace(dir)
	if err != nil {
		return err
	}
	*v = m.Plugins[0]
	return nil
}

func TestReadMarketplace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ManifestDir, MarketplaceName), `{
	  "name": "superpowers-marketplace",
	  "owner": {"name": "Jesse Vincent", "email": "jesse@fsck.com"},
	  "metadata": {"description": "Skills, workflows", "version": "1.0.13"},
	  "plugins": [
	    {"name":"superpowers","version":"6.3.0","strict":true,
	     "source":{"source":"url","url":"https://github.com/obra/superpowers.git"}},
	    {"name":"elements-of-style","version":"1.0.0",
	     "source":{"source":"url","url":"https://github.com/obra/the-elements-of-style.git"}}
	  ]
	}`)

	m, err := ReadMarketplace(dir)
	if err != nil {
		t.Fatalf("ReadMarketplace: %v", err)
	}
	if m.Name != "superpowers-marketplace" {
		t.Errorf("name = %q", m.Name)
	}
	if len(m.Plugins) != 2 {
		t.Fatalf("plugins = %d, want 2", len(m.Plugins))
	}
	p, ok := FindPlugin(m, "superpowers")
	if !ok {
		t.Fatal("FindPlugin(superpowers) not found")
	}
	if p.Version != "6.3.0" || !p.Strict {
		t.Errorf("plugin = %+v", p)
	}
	if _, ok := FindPlugin(m, "nope"); ok {
		t.Error("FindPlugin found a plugin that is not there")
	}
}

// A marketplace with no name field is addressed by its directory name.
func TestReadMarketplace_NameFallsBackToDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-marketplace")
	writeFile(t, filepath.Join(dir, ManifestDir, MarketplaceName),
		`{"plugins":[{"name":"p","source":"./"}]}`)

	m, err := ReadMarketplace(dir)
	if err != nil {
		t.Fatalf("ReadMarketplace: %v", err)
	}
	if m.Name != "my-marketplace" {
		t.Errorf("name = %q, want my-marketplace", m.Name)
	}
}

func TestReadMarketplace_Errors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		if _, err := ReadMarketplace(t.TempDir()); err == nil {
			t.Fatal("expected an error for a missing marketplace.json")
		}
	})
	t.Run("malformed json", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestDir, MarketplaceName), `{not json`)
		if _, err := ReadMarketplace(dir); err == nil {
			t.Fatal("expected an error for malformed json")
		}
	})
	t.Run("no plugins", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestDir, MarketplaceName), `{"name":"empty","plugins":[]}`)
		if _, err := ReadMarketplace(dir); err == nil {
			t.Fatal("expected an error for a marketplace with no plugins")
		}
	})
}

// A missing plugin.json is not an error: the catalog's name and version stand in.
func TestReadManifest(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, ManifestDir, PluginName),
			`{"name":"superpowers","version":"5.0.7","license":"MIT"}`)
		m, err := ReadManifest(dir)
		if err != nil {
			t.Fatalf("ReadManifest: %v", err)
		}
		if m.Name != "superpowers" || m.Version != "5.0.7" {
			t.Errorf("manifest = %+v", m)
		}
	})
	t.Run("absent", func(t *testing.T) {
		m, err := ReadManifest(t.TempDir())
		if err != nil {
			t.Fatalf("absent manifest should not error, got %v", err)
		}
		if m.Name != "" {
			t.Errorf("manifest = %+v, want zero value", m)
		}
	})
}

func TestSourceIsRemote(t *testing.T) {
	tests := []struct {
		src  Source
		want bool
	}{
		{Source{Kind: SourceRelative}, false},
		{Source{Kind: SourceURL}, true},
		{Source{Kind: SourceGitSubdir}, true},
		{Source{Kind: SourceGitHub}, true},
	}
	for _, tt := range tests {
		if got := tt.src.IsRemote(); got != tt.want {
			t.Errorf("%s.IsRemote() = %v, want %v", tt.src.Kind, got, tt.want)
		}
	}
}
