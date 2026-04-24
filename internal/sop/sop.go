package sop

import (
	"log/slog"
	"os"
	"path/filepath"
)

// userHomeDir is a testable alias for os.UserHomeDir.
var userHomeDir = os.UserHomeDir

// LoadPDD returns the PDD SOP instruction text.
// Resolution order: project .pi-go/sops/pdd.md → global ~/.pi-go/sops/pdd.md → embedded default.
func LoadPDD(workDir string) (string, error) {
	// Try project-level override
	projectPath := filepath.Join(workDir, ".pi-go", "sops", "pdd.md")
	if content, err := os.ReadFile(projectPath); err == nil {
		slog.Debug("loaded PDD SOP from project override", "path", projectPath)
		return string(content), nil
	}

	// Try global override
	home, err := userHomeDir()
	if err == nil {
		globalPath := filepath.Join(home, ".pi-go", "sops", "pdd.md")
		if content, err := os.ReadFile(globalPath); err == nil {
			slog.Debug("loaded PDD SOP from global override", "path", globalPath)
			return string(content), nil
		}
	}

	// Fall back to embedded default
	slog.Debug("using embedded default PDD SOP")
	return DefaultPDDSOP, nil
}

// LoadPDDWithHome is a testable version of LoadPDD that accepts an explicit home directory.
func LoadPDDWithHome(workDir, homeDir string) (string, error) {
	// Try project-level override
	projectPath := filepath.Join(workDir, ".pi-go", "sops", "pdd.md")
	if content, err := os.ReadFile(projectPath); err == nil {
		slog.Debug("loaded PDD SOP from project override", "path", projectPath)
		return string(content), nil
	}

	// Try global override
	if homeDir != "" {
		globalPath := filepath.Join(homeDir, ".pi-go", "sops", "pdd.md")
		if content, err := os.ReadFile(globalPath); err == nil {
			slog.Debug("loaded PDD SOP from global override", "path", globalPath)
			return string(content), nil
		}
	}

	// Fall back to embedded default
	slog.Debug("using embedded default PDD SOP")
	return DefaultPDDSOP, nil
}
