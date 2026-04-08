package palace

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultChunkSize    = 1500
	defaultChunkOverlap = 200
)

// MineProject walks a directory, chunks source files, and inserts them as
// drawers into the palace. It respects .gitignore and mempalace.yaml room
// definitions. The wing defaults to the directory basename if not set in cfg.
func MineProject(ctx context.Context, palace *Palace, dir string, cfg *MineConfig) (*MineResult, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("mine: resolve dir: %w", err)
	}

	if cfg == nil {
		// Try to load from mempalace.yaml, fall back to defaults.
		loaded, loadErr := readMempalaceYAML(absDir)
		if loadErr != nil {
			cfg = &MineConfig{Wing: filepath.Base(absDir)}
		} else {
			cfg = loaded
		}
	}

	if cfg.Wing == "" {
		cfg.Wing = filepath.Base(absDir)
	}

	gitignorePatterns := loadGitignore(absDir)

	result := &MineResult{}

	err = filepath.WalkDir(absDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			result.Errors++
			return nil // continue walking
		}

		// Skip directories.
		if d.IsDir() {
			name := d.Name()
			if name[0] == '.' && name != "." || skipDirNames[name] {
				return filepath.SkipDir
			}
			return nil
		}

		// Check file extension.
		ext := strings.ToLower(filepath.Ext(path))
		if !supportedExtensions[ext] {
			return nil
		}

		// Check gitignore.
		relPath, err := filepath.Rel(absDir, path)
		if err != nil {
			result.Errors++
			return nil
		}
		if isGitignored(relPath, gitignorePatterns) {
			return nil
		}

		// Skip large files (> 512KB).
		info, err := d.Info()
		if err != nil {
			result.Errors++
			return nil
		}
		if info.Size() > 512*1024 {
			return nil
		}

		// Read and chunk the file.
		data, err := os.ReadFile(path)
		if err != nil {
			result.Errors++
			slog.Warn("mine: read file", "path", relPath, "error", err)
			return nil
		}

		content := string(data)
		if strings.TrimSpace(content) == "" {
			return nil
		}

		room := detectRoom(relPath, cfg.Rooms)
		chunks := chunkText(content, defaultChunkSize, defaultChunkOverlap)

		for i, chunk := range chunks {
			result.Processed++

			_, addErr := palace.AddDrawer(ctx, DrawerInput{
				Wing:       cfg.Wing,
				Room:       room,
				Content:    chunk,
				SourceFile: relPath,
				ChunkIndex: i,
				AddedBy:    "miner:project",
				Importance: 3,
			})
			if addErr != nil {
				var dupErr *DuplicateError
				if errors.As(addErr, &dupErr) {
					result.Skipped++
				} else {
					result.Errors++
					slog.Warn("mine: add drawer", "file", relPath, "chunk", i, "error", addErr)
				}
				continue
			}
			result.Added++
		}

		return nil
	})

	if err != nil {
		return result, fmt.Errorf("mine: walk: %w", err)
	}

	return result, nil
}
