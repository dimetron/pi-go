package palace

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// embedBatchSize is how many chunks are embedded per call.
	//
	// Counter-intuitively a *smaller* batch is both faster and far cheaper in
	// memory here. A bigger batch means bigger intermediate tensors, and gomlx's
	// simplego backend recycles those through a sync.Pool that Go drains on every
	// GC — so under churn the large buffers get re-allocated rather than reused,
	// and peak RSS balloons. Nothing about the work is more efficient in bulk:
	// the backend is compute-bound on per-token matmuls either way.
	//
	// Measured on an M2 Max mining this repo (17,731 chunks, fp32 model):
	//
	//	batch  chunks/sec   peak RSS   full run
	//	  32       4.3       4748 MB    69 min
	//	  16       4.8       1665 MB    62 min
	//	   8       6.9       1808 MB    43 min   <- chosen
	//	   4       5.3       1686 MB    55 min
	//
	// 32 was the original value: the slowest of the four *and* 2.6x the memory.
	embedBatchSize = 8
)

// embedWorkers decides how many chunks are embedded in parallel.
//
// gomlx's intra-op parallelism does not scale: with a single embedder the miner
// used only ~25% of the CPU, with roughly a quarter of all samples burnt in the
// Go scheduler (usleep, futex wait/signal, work-stealing) rather than in matmuls.
// Running several embedders concurrently reclaims the idle cores.
//
// Measured on an M2 Max (12 cores), fp32 model, batch of 8:
//
//	workers  chunks/sec
//	   1        7.1
//	   2       11.4
//	   4       14.1   <- chosen
//	   6       10.5   (contention; slower again)
//
// Each worker holds its own model instance, so this trades memory for speed;
// four is where the curve turns over.
func embedWorkers(modelPath string) int {
	if modelPath == "" {
		return 1
	}
	return min(maxEmbedSessions, max(1, runtime.NumCPU()/3))
}

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
			// Prune whole ignored trees rather than rediscovering them file by
			// file: git reports a fully-ignored directory as one entry, and
			// descending into it means embedding everything inside.
			if rel, relErr := filepath.Rel(absDir, path); relErr == nil && rel != "." {
				if isGitIgnoredSet(rel, ignoredSet) {
					return filepath.SkipDir
				}
			}
			return nil
		}

		// Check file extension. An empty ext means the file has no "." suffix at
		// all (LICENSE, Makefile, a stray binary) — never mine those.
		ext := strings.ToLower(filepath.Ext(path))
		if ext == "" || !supportedExtensions[ext] {
			return nil
		}

		// A permitted extension is not proof of text: minified blobs and embedded
		// payloads turn up under .json/.txt, and embedding them costs exactly as
		// much as real source while producing a meaningless vector.
		if isBinaryFile(path) {
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

	// Phase 2b: Drop chunks whose content is unchanged since the last run.
	//
	// This must happen *before* embedding: embedding is ~80% of a mining run's
	// CPU, so re-embedding a chunk only to discover it is identical is the single
	// most expensive thing the miner can do. A drawer's ID is derived from
	// (source_file, chunk_index) and says nothing about content, so the stored
	// content hash is the only way to tell.
	//
	// A store lookup failure is not fatal — fall back to embedding everything,
	// which is merely the old behavior.
	if existing, hashErr := palace.store.DrawerHashes(ctx, cfg.Wing); hashErr != nil {
		slog.Warn("mine: could not load content hashes; re-embedding everything", "error", hashErr)
	} else if len(existing) > 0 {
		kept := allChunks[:0]
		for _, c := range allChunks {
			id := GenerateDrawerID(c.wing, c.room, c.relPath, c.chunkIdx, c.content)
			if prev, ok := existing[id]; ok && prev != "" && prev == HashContent(c.content) {
				result.Skipped++
				continue
			}
			kept = append(kept, c)
		}
		if skipped := len(allChunks) - len(kept); skipped > 0 {
			slog.Info("mine: skipping unchanged chunks", "skipped", skipped, "to_embed", len(kept))
		}
		allChunks = kept
	}

	if len(allChunks) == 0 {
		return result, nil
	}

	// Phase 3: Embed all chunks, several batches at a time.
	//
	// embeddings is indexed in lockstep with allChunks. It used to be built with
	// append() and only on success, so a single failed batch shifted every later
	// chunk onto someone else's vector — silently, since Phase 4 just reads
	// embeddings[i]. Writing results at their own index cannot drift: a failed
	// batch leaves nils, and those chunks are stored without an embedding rather
	// than with the wrong one.
	var embeddings [][]float32
	if palace.embedder != nil {
		texts := make([]string, len(allChunks))
		for i, c := range allChunks {
			texts[i] = c.content
		}
		embeddings = make([][]float32, len(texts))

		workers := embedWorkers(palace.config.ModelPath)
		slog.Info("mine: embedding chunks", "count", len(texts), "workers", workers, "batch", embedBatchSize)

		// Each worker owns an embedder: hugot pipelines are not safe to call
		// concurrently, and one shared embedder is what limited the run to ~25%
		// CPU. Worker 0 reuses the palace's own embedder so the common
		// single-worker case allocates nothing extra.
		embs := make([]*Embedder, 0, workers)
		embs = append(embs, palace.embedder)
		for len(embs) < workers {
			e, err := NewEmbedder(palace.config.ModelPath)
			if err != nil {
				slog.Warn("mine: extra embedder failed; continuing with fewer workers",
					"have", len(embs), "want", workers, "error", err)
				break
			}
			embs = append(embs, e)
			defer e.Close()
		}

		type batchJob struct{ start, end int }
		jobs := make(chan batchJob)

		var done atomic.Int64
		var wg sync.WaitGroup
		for _, e := range embs {
			wg.Add(1)
			go func(e *Embedder) {
				defer wg.Done()
				for j := range jobs {
					out, err := e.Embed(texts[j.start:j.end])
					if err != nil {
						slog.Warn("mine: embedding batch failed", "error", err)
						// Leave nils: better no vector than a misaligned one.
						continue
					}
					copy(embeddings[j.start:j.end], out)

					n := done.Add(int64(j.end - j.start))
					// Announce progress as batches land. Embedding is by far the
					// slowest phase and works on chunks rather than files, so
					// without this the UI sat blank for minutes and looked hung.
					//
					// Only Phase reports here. The old Progress("") call also drew a
					// line, and because both redraw with \r the blank one landed last
					// and overwrote this one — leaving a frozen "829/832 files" on
					// screen for the entire embed, which is what made a working run
					// look wedged.
					if cfg.Phase != nil {
						cfg.Phase("embed", allChunks[j.start].relPath, int(n), len(texts))
					}
				}
			}(e)
		}

		for i := 0; i < len(texts); i += embedBatchSize {
			jobs <- batchJob{start: i, end: min(i+embedBatchSize, len(texts))}
		}
		close(jobs)
		wg.Wait()

		if cfg.Phase != nil {
			cfg.Phase("embed", "", len(texts), len(texts))
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
			// Recorded so the next run can recognize this chunk as unchanged and
			// skip re-embedding it.
			ContentHash: HashContent(chunk.content),
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
