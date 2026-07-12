package palace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// Batch size for embedding multiple chunks at once.
	embedBatchSize = 32
)

// MineProject walks a directory, chunks source files, and inserts them as
// drawers into the palace. It respects .gitignore and mempalace.yaml room
// definitions. The wing defaults to the directory basename if not set in cfg.
//
// This implementation uses batch embedding and single-pass collection
// for significantly faster mining.
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

	// Try git first for accurate .gitignore handling (nested files, ** globs,
	// negation, parent .gitignore). Fall back to manual parsing if not a repo.
	ignoredSet := gitIgnoredSet(absDir)
	var gitignorePatterns []string
	if ignoredSet == nil {
		gitignorePatterns = loadGitignore(absDir)
	}

	// Phase 1: Collect all files to process.
	type fileTask struct {
		path    string
		relPath string
		room    string
		chunks  []string
	}

	var tasks []fileTask

	err = filepath.WalkDir(absDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
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
			return nil
		}
		if isGitIgnoredSet(relPath, ignoredSet) || (ignoredSet == nil && isGitignored(relPath, gitignorePatterns)) {
			return nil
		}

		// Skip large files (> 512KB).
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > 512*1024 {
			return nil
		}

		// Read and chunk the file.
		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("mine: read file", "path", relPath, "error", err)
			return nil
		}

		content := string(data)
		if strings.TrimSpace(content) == "" {
			return nil
		}

		room := detectRoom(relPath, cfg.Rooms)
		chunks := chunkText(content, defaultChunkSize, defaultChunkOverlap)
		if len(chunks) == 0 {
			return nil
		}

		tasks = append(tasks, fileTask{
			path:    path,
			relPath: relPath,
			room:    room,
			chunks:  chunks,
		})
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("mine: walk: %w", err)
	}

	result := &MineResult{}

	// Phase 2: Collect all chunks from all files.
	type chunkJob struct {
		wing     string
		room     string
		relPath  string
		chunkIdx int
		content  string
	}

	var allChunks []chunkJob
	for i, task := range tasks {
		for j, chunk := range task.chunks {
			allChunks = append(allChunks, chunkJob{
				wing:     cfg.Wing,
				room:     task.room,
				relPath:  task.relPath,
				chunkIdx: j,
				content:  chunk,
			})
		}
		result.Processed += len(task.chunks)
		// Report progress: file scanning phase (0-30% of total progress).
		if cfg.Progress != nil {
			cfg.Progress(task.relPath, len(task.chunks), 0, 0)
		}
		// Also report intermediate progress for large file lists.
		if cfg.Progress != nil && i > 0 && i%50 == 0 {
			cfg.Progress("", 0, 0, 0) // empty = progress update only
		}
	}

	if len(allChunks) == 0 {
		return result, nil
	}

	// Phase 3: Batch embed all chunks.
	var embeddings [][]float32
	if palace.embedder != nil {
		// Embed in batches.
		texts := make([]string, len(allChunks))
		for i, c := range allChunks {
			texts[i] = c.content
		}

		slog.Info("mine: embedding chunks", "count", len(texts))
		totalBatches := (len(texts) + embedBatchSize - 1) / embedBatchSize
		for i := 0; i < len(texts); i += embedBatchSize {
			end := i + embedBatchSize
			if end > len(texts) {
				end = len(texts)
			}
			batch, err := palace.embedder.Embed(texts[i:end])
			if err != nil {
				slog.Warn("mine: embedding batch failed", "error", err)
			} else {
				embeddings = append(embeddings, batch...)
			}
			// Report embedding progress (30-70% of total).
			if cfg.Progress != nil {
				batchIdx := i / embedBatchSize
				pct := 30 + (70-30)*batchIdx/totalBatches
				cfg.Progress("", pct, 0, 0)
			}
		}
	}

	// Phase 4: Build drawer slice.
	now := time.Now().UTC()
	drawers := make([]*Drawer, 0, len(allChunks))
	seenIDs := make(map[string]bool)

	for i, chunk := range allChunks {
		var embedding []float32
		if i < len(embeddings) {
			embedding = embeddings[i]
		}

		id := GenerateDrawerID(chunk.wing, chunk.room, chunk.relPath, chunk.chunkIdx, chunk.content)

		// Skip if we already have this ID (from same file).
		if seenIDs[id] {
			result.Skipped++
			continue
		}
		seenIDs[id] = true

		drawers = append(drawers, &Drawer{
			ID:         id,
			Wing:       chunk.wing,
			Room:       chunk.room,
			Content:    chunk.content,
			SourceFile: chunk.relPath,
			ChunkIndex: chunk.chunkIdx,
			AddedBy:    "miner:project",
			Importance: 3,
			Embedding:  embedding,
			CreatedAt:  now,
		})
	}

	// Phase 5: Batch insert with transaction.
	if len(drawers) > 0 {
		inserted, err := palace.store.BatchInsertDrawers(ctx, drawers)
		if err != nil {
			slog.Warn("mine: batch insert failed, falling back to individual inserts", "error", err)
			// Fall back to individual inserts.
			for i, drawer := range drawers {
				if err := palace.store.InsertDrawer(ctx, drawer); err != nil {
					result.Errors++
				} else {
					result.Added++
				}
				// Report insert progress (70-100% of total).
				if cfg.Progress != nil {
					pct := 70 + (100-70)*i/len(drawers)
					cfg.Progress("", pct, 0, 0)
				}
			}
		} else {
			result.Added = inserted
			// Report completion.
			if cfg.Progress != nil {
				cfg.Progress("", 100, 0, 0)
			}
		}
	}

	return result, nil
}
