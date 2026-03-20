package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectLanguage(t *testing.T) {
	langs := DefaultLanguages()

	tests := []struct {
		path string
		want string
	}{
		{"main.go", "go"},
		{"/some/path/file.go", "go"},
		{"app.ts", "typescript"},
		{"component.tsx", "typescript"},
		{"index.js", "typescript"},
		{"app.jsx", "typescript"},
		{"script.py", "python"},
		{"types.pyi", "python"},
		{"lib.rs", "rust"},
		{"readme.txt", ""},
		{"Makefile", ""},
		{"", ""},
	}

	for _, tt := range tests {
		got := DetectLanguage(tt.path, langs)
		if got != tt.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestDefaultLanguages_AllHaveRequired(t *testing.T) {
	langs := DefaultLanguages()

	expectedLangs := []string{"go", "typescript", "python", "rust"}
	for _, name := range expectedLangs {
		cfg, ok := langs[name]
		if !ok {
			t.Errorf("missing language config for %q", name)
			continue
		}
		if cfg.Command == "" {
			t.Errorf("%s: empty command", name)
		}
		if len(cfg.FileExtensions) == 0 {
			t.Errorf("%s: no file extensions", name)
		}
		if len(cfg.RootMarkers) == 0 {
			t.Errorf("%s: no root markers", name)
		}
		if cfg.LanguageID == "" {
			t.Errorf("%s: empty languageId", name)
		}
	}
}

func TestFindRoot_GoMod(t *testing.T) {
	// Create temp directory structure:
	// root/
	//   go.mod
	//   pkg/
	//     sub/
	//       file.go
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test"), 0o644)
	os.MkdirAll(filepath.Join(root, "pkg", "sub"), 0o755)
	filePath := filepath.Join(root, "pkg", "sub", "file.go")
	os.WriteFile(filePath, []byte("package sub"), 0o644)

	got := FindRoot(filePath, []string{"go.mod"})
	if got != root {
		t.Errorf("FindRoot() = %q, want %q", got, root)
	}
}

func TestFindRoot_PackageJSON(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "package.json"), []byte("{}"), 0o644)
	os.MkdirAll(filepath.Join(root, "src", "components"), 0o755)
	filePath := filepath.Join(root, "src", "components", "app.tsx")
	os.WriteFile(filePath, []byte(""), 0o644)

	got := FindRoot(filePath, []string{"tsconfig.json", "package.json"})
	if got != root {
		t.Errorf("FindRoot() = %q, want %q", got, root)
	}
}

func TestFindRoot_NoMarkerFound(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "file.go")
	os.WriteFile(filePath, []byte("package main"), 0o644)

	got := FindRoot(filePath, []string{"nonexistent.marker"})
	// Should return the directory of the file itself.
	if got != root {
		t.Errorf("FindRoot() = %q, want %q", got, root)
	}
}

func TestFindRoot_MultipleMarkers(t *testing.T) {
	// Verify it stops at the first marker found going up.
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	os.MkdirAll(sub, 0o755)

	// Put Cargo.toml at sub level.
	os.WriteFile(filepath.Join(sub, "Cargo.toml"), []byte(""), 0o644)
	filePath := filepath.Join(sub, "src", "lib.rs")
	os.MkdirAll(filepath.Join(sub, "src"), 0o755)
	os.WriteFile(filePath, []byte(""), 0o644)

	got := FindRoot(filePath, []string{"Cargo.toml"})
	if got != sub {
		t.Errorf("FindRoot() = %q, want %q", got, sub)
	}
}

func TestFindRoot_MarkerAtFileDirectory(t *testing.T) {
	// Marker is in the same directory as the file — should return that directory.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test"), 0o644)
	filePath := filepath.Join(root, "main.go")
	os.WriteFile(filePath, []byte("package main"), 0o644)

	got := FindRoot(filePath, []string{"go.mod"})
	if got != root {
		t.Errorf("FindRoot() = %q, want %q", got, root)
	}
}

func TestFindRoot_FirstMarkerWins(t *testing.T) {
	// When multiple markers are provided and the first one is found, it returns that directory.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte("{}"), 0o644)
	// No package.json — only tsconfig.json exists.
	filePath := filepath.Join(root, "index.ts")
	os.WriteFile(filePath, []byte(""), 0o644)

	got := FindRoot(filePath, []string{"tsconfig.json", "package.json"})
	if got != root {
		t.Errorf("FindRoot() = %q, want %q", got, root)
	}
}

func TestFindRoot_EmptyMarkers(t *testing.T) {
	// With no markers, should return the file's directory.
	root := t.TempDir()
	filePath := filepath.Join(root, "file.go")
	os.WriteFile(filePath, []byte(""), 0o644)

	got := FindRoot(filePath, []string{})
	// Should return the file's directory when no markers specified.
	if got != root {
		t.Errorf("FindRoot() with empty markers = %q, want %q", got, root)
	}
}

func TestFindRoot_DeepNesting(t *testing.T) {
	// go.mod at root, file deeply nested — should walk all the way up.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "go.mod"), []byte("module deep"), 0o644)

	deep := filepath.Join(root, "a", "b", "c", "d", "e")
	os.MkdirAll(deep, 0o755)
	filePath := filepath.Join(deep, "file.go")
	os.WriteFile(filePath, []byte("package e"), 0o644)

	got := FindRoot(filePath, []string{"go.mod"})
	if got != root {
		t.Errorf("FindRoot() = %q, want %q", got, root)
	}
}

func TestFindRoot_DirectoryMarker(t *testing.T) {
	// Markers can also be directories (e.g., .git).
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	os.MkdirAll(gitDir, 0o755)

	sub := filepath.Join(root, "src")
	os.MkdirAll(sub, 0o755)
	filePath := filepath.Join(sub, "main.go")
	os.WriteFile(filePath, []byte(""), 0o644)

	got := FindRoot(filePath, []string{".git"})
	if got != root {
		t.Errorf("FindRoot() = %q, want %q", got, root)
	}
}

func TestDetectLanguage_CaseInsensitiveExtension(t *testing.T) {
	langs := DefaultLanguages()

	// Uppercase extensions should still match via ToLower.
	tests := []struct {
		path string
		want string
	}{
		{"/path/FILE.GO", "go"},
		{"/path/APP.TS", "typescript"},
		{"/path/SCRIPT.PY", "python"},
		{"/path/LIB.RS", "rust"},
	}
	for _, tt := range tests {
		got := DetectLanguage(tt.path, langs)
		if got != tt.want {
			t.Errorf("DetectLanguage(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestDetectLanguage_NoExtension(t *testing.T) {
	langs := DefaultLanguages()
	// Files with no extension should return empty string.
	got := DetectLanguage("Makefile", langs)
	if got != "" {
		t.Errorf("expected empty for no extension, got %q", got)
	}
}
