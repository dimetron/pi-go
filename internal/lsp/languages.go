package lsp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// LanguageConfig describes how to start and identify an LSP server for a language.
type LanguageConfig struct {
	Command        string         `json:"command"`
	Args           []string       `json:"args,omitempty"`
	FileExtensions []string       `json:"fileExtensions"`
	RootMarkers    []string       `json:"rootMarkers"`
	InitOptions    map[string]any `json:"initOptions,omitempty"`
	LanguageID     string         `json:"languageId"` // LSP language identifier
}

// DefaultLanguages returns built-in language server configurations.
func DefaultLanguages() map[string]*LanguageConfig {
	return map[string]*LanguageConfig{
		"go": {
			Command:        "gopls",
			FileExtensions: []string{".go"},
			RootMarkers:    []string{"go.mod", "go.sum"},
			LanguageID:     "go",
		},
		"typescript": {
			Command:        "typescript-language-server",
			Args:           []string{"--stdio"},
			FileExtensions: []string{".ts", ".tsx", ".js", ".jsx"},
			RootMarkers:    []string{"tsconfig.json", "package.json"},
			LanguageID:     "typescript",
		},
		"python": {
			Command:        "pyright-langserver",
			Args:           []string{"--stdio"},
			FileExtensions: []string{".py", ".pyi"},
			RootMarkers:    []string{"pyproject.toml", "setup.py", "requirements.txt"},
			LanguageID:     "python",
		},
		"rust": {
			Command:        "rust-analyzer",
			FileExtensions: []string{".rs"},
			RootMarkers:    []string{"Cargo.toml"},
			LanguageID:     "rust",
		},
	}
}

// DetectLanguage returns the language name for a file path based on extension.
// Returns empty string if no language matches.
func DetectLanguage(filePath string, languages map[string]*LanguageConfig) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" {
		return ""
	}
	for name, cfg := range languages {
		if slices.Contains(cfg.FileExtensions, ext) {
			return name
		}
	}
	return ""
}

// FindRoot walks up from filePath looking for any of the marker files/dirs.
// Returns the directory containing the first marker found, or the directory
// of filePath itself if no marker is found.
func FindRoot(filePath string, markers []string) string {
	dir := filepath.Dir(filePath)
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err == nil {
			dir = abs
		}
	}

	for {
		for _, marker := range markers {
			path := filepath.Join(dir, marker)
			if _, err := os.Stat(path); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root; return original directory.
			return filepath.Dir(filePath)
		}
		dir = parent
	}
}
