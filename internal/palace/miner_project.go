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
	cfg = mineConfigFor(absDir, cfg)

	// Phase 1: collect the files worth mining.
	tasks, err := collectMineTasks(absDir, cfg)
	if err != nil {
		return nil, err
	}

	// Phase 2: flatten them into chunks.
	result := &MineResult{}
	allChunks := collectChunks(tasks, cfg, result)
	if len(allChunks) == 0 {
		return result, nil
	}

	// Phase 2b: drop chunks that have not changed since the last run.
	allChunks = dropUnchangedChunks(ctx, palace, cfg.Wing, allChunks, result)
	if len(allChunks) == 0 {
		return result, nil
	}

	// Phase 3: embed what is left.
	embeddings := embedChunks(palace, cfg, allChunks)

	// Phase 4 and 5: build the drawers and persist them.
	drawers := buildDrawers(allChunks, embeddings, result)
	insertDrawers(ctx, palace, cfg, drawers, result)

	return result, nil
}

// mineConfigFor resolves the effective mine config: the caller's, else
// mempalace.yaml, else defaults — with the wing defaulting to the directory
// basename.
func mineConfigFor(absDir string, cfg *MineConfig) *MineConfig {
	if cfg == nil {
		loaded, err := readMempalaceYAML(absDir)
		if err != nil {
			cfg = &MineConfig{Wing: filepath.Base(absDir)}
		} else {
			cfg = loaded
		}
	}
	if cfg.Wing == "" {
		cfg.Wing = filepath.Base(absDir)
	}
	return cfg
}

// fileTask is one source file that survived filtering, already chunked.
type fileTask struct {
	relPath string
	room    string
	chunks  []string
}

// chunkJob is a single chunk of a file, addressed by its position in the wing.
type chunkJob struct {
	wing     string
	room     string
	relPath  string
	chunkIdx int
	content  string
}

// collectMineTasks walks absDir and returns the chunked files worth mining,
// skipping ignored trees, unsupported extensions, binaries and oversized files.
func collectMineTasks(absDir string, cfg *MineConfig) ([]fileTask, error) {
	// Try git first for accurate .gitignore handling (nested files, ** globs,
	// negation, parent .gitignore). Fall back to manual parsing if not a repo.
	ignoredSet := gitIgnoredSet(absDir)
	var gitignorePatterns []string
	if ignoredSet == nil {
		gitignorePatterns = loadGitignore(absDir)
	}

	var tasks []fileTask
	err := filepath.WalkDir(absDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // continue walking
		}
		if d.IsDir() {
			return skipMinedDir(absDir, path, d, ignoredSet)
		}

		relPath, err := filepath.Rel(absDir, path)
		if err != nil {
			return nil
		}
		if !isMineableFile(path, relPath, d, ignoredSet, gitignorePatterns) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("mine: read file", "path", relPath, "error", err)
			return nil
		}
		content := string(data)
		if strings.TrimSpace(content) == "" {
			return nil
		}

		chunks := chunkText(content, defaultChunkSize, defaultChunkOverlap)
		if len(chunks) == 0 {
			return nil
		}
		tasks = append(tasks, fileTask{
			relPath: relPath,
			room:    detectRoom(relPath, cfg.Rooms),
			chunks:  chunks,
		})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("mine: walk: %w", err)
	}
	return tasks, nil
}

// skipMinedDir prunes dot-directories, known build directories, and trees git
// reports as fully ignored. Pruning beats filtering file by file: git reports a
// fully-ignored directory as one entry, and descending into it means embedding
// everything inside.
func skipMinedDir(absDir, path string, d os.DirEntry, ignoredSet map[string]bool) error {
	name := d.Name()
	if name[0] == '.' && name != "." || skipDirNames[name] {
		return filepath.SkipDir
	}
	if rel, err := filepath.Rel(absDir, path); err == nil && rel != "." {
		if isGitIgnoredSet(rel, ignoredSet) {
			return filepath.SkipDir
		}
	}
	return nil
}

// isMineableFile reports whether a file should be chunked and embedded.
func isMineableFile(path, relPath string, d os.DirEntry, ignoredSet map[string]bool, gitignorePatterns []string) bool {
	// An empty ext means the file has no "." suffix at all (LICENSE, Makefile,
	// a stray binary) — never mine those.
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" || !supportedExtensions[ext] {
		return false
	}

	// A permitted extension is not proof of text: minified blobs and embedded
	// payloads turn up under .json/.txt, and embedding them costs exactly as
	// much as real source while producing a meaningless vector.
	if isBinaryFile(path) {
		return false
	}
	if isGitIgnoredSet(relPath, ignoredSet) || (ignoredSet == nil && isGitignored(relPath, gitignorePatterns)) {
		return false
	}

	// Skip large files (> 512KB).
	info, err := d.Info()
	if err != nil || info.Size() > 512*1024 {
		return false
	}
	return true
}

// collectChunks flattens the per-file chunks into one addressable list,
// reporting scan progress as it goes.
func collectChunks(tasks []fileTask, cfg *MineConfig, result *MineResult) []chunkJob {
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
		if cfg.Progress == nil {
			continue
		}
		// Report progress: file scanning phase (0-30% of total progress).
		cfg.Progress(task.relPath, len(task.chunks), 0, 0)
		// Also report intermediate progress for large file lists.
		if i > 0 && i%50 == 0 {
			cfg.Progress("", 0, 0, 0) // empty = progress update only
		}
	}
	return allChunks
}

// dropUnchangedChunks removes chunks whose stored content hash still matches.
//
// This must happen *before* embedding: embedding is ~80% of a mining run's CPU,
// so re-embedding a chunk only to discover it is identical is the single most
// expensive thing the miner can do. A drawer's ID is derived from (source_file,
// chunk_index) and says nothing about content, so the stored content hash is the
// only way to tell.
//
// A store lookup failure is not fatal — fall back to embedding everything,
// which is merely the old behavior.
func dropUnchangedChunks(ctx context.Context, palace *Palace, wing string, allChunks []chunkJob, result *MineResult) []chunkJob {
	existing, err := palace.store.DrawerHashes(ctx, wing)
	if err != nil {
		slog.Warn("mine: could not load content hashes; re-embedding everything", "error", err)
		return allChunks
	}
	if len(existing) == 0 {
		return allChunks
	}

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
	return kept
}

// embedChunks embeds every chunk, several batches at a time, and returns the
// vectors indexed in lockstep with allChunks. It returns nil when the palace
// has no embedder.
//
// The result is indexed rather than appended: building it with append() and
// only on success meant a single failed batch shifted every later chunk onto
// someone else's vector — silently, since the caller just reads embeddings[i].
// Writing results at their own index cannot drift, so a failed batch leaves
// nils and those chunks are stored without an embedding rather than a wrong one.
func embedChunks(palace *Palace, cfg *MineConfig, allChunks []chunkJob) [][]float32 {
	if palace.embedder == nil {
		return nil
	}

	texts := make([]string, len(allChunks))
	for i, c := range allChunks {
		texts[i] = c.content
	}
	embeddings := make([][]float32, len(texts))

	workers := embedWorkers(palace.config.ModelPath)
	embs, closeExtra := embedderPool(palace, workers)
	defer closeExtra()

	// Batch size is a property of the backend, not a global: 8 exists only
	// because gomlx's simplego balloons in memory above it, which is irrelevant
	// to Ollama.
	batch := batchSizeFor(palace.embedder)
	slog.Info("mine: embedding chunks",
		"count", len(texts), "workers", len(embs), "batch", batch, "backend", embedderName(palace.embedder))

	type batchJob struct{ start, end int }
	jobs := make(chan batchJob)

	var done atomic.Int64
	var wg sync.WaitGroup
	for _, e := range embs {
		wg.Go(func() {
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
		})
	}

	for i := 0; i < len(texts); i += batch {
		jobs <- batchJob{start: i, end: min(i+batch, len(texts))}
	}
	close(jobs)
	wg.Wait()

	if cfg.Phase != nil {
		cfg.Phase("embed", "", len(texts), len(texts))
	}
	return embeddings
}

// embedderPool returns one embedder per worker plus a closer for the extras.
//
// Each worker owns an embedder: hugot pipelines are not safe to call
// concurrently, and one shared embedder is what limited the run to ~25% CPU.
// Worker 0 reuses the palace's own embedder so the common single-worker case
// allocates nothing extra, and is therefore not closed here.
func embedderPool(palace *Palace, workers int) ([]Embedder, func()) {
	// Ollama is a network client, safe to share across goroutines, and it batches
	// server-side — so extra instances buy nothing. Worse, the fallback below
	// would build *local* embedders alongside it, and the two models produce
	// different dimensions (768 vs 384). Mixing them writes vectors of two shapes
	// into one wing, where CosineSimilarity silently returns 0 for every
	// mismatched pair and search quietly stops working.
	if _, ok := palace.embedder.(*ollamaEmbedder); ok {
		return []Embedder{palace.embedder}, func() {}
	}

	embs := make([]Embedder, 0, workers)
	embs = append(embs, palace.embedder)
	for len(embs) < workers {
		e, err := NewEmbedder(palace.config.ModelPath)
		if err != nil {
			slog.Warn("mine: extra embedder failed; continuing with fewer workers",
				"have", len(embs), "want", workers, "error", err)
			break
		}
		embs = append(embs, e)
	}

	extras := embs[1:]
	return embs, func() {
		for _, e := range extras {
			e.Close()
		}
	}
}

// buildDrawers turns chunks and their vectors into drawers, dropping any
// duplicate ID produced within the same run.
func buildDrawers(allChunks []chunkJob, embeddings [][]float32, result *MineResult) []*Drawer {
	now := time.Now().UTC()
	drawers := make([]*Drawer, 0, len(allChunks))
	seenIDs := make(map[string]bool)

	for i, chunk := range allChunks {
		var embedding []float32
		if i < len(embeddings) {
			embedding = embeddings[i]
		}

		id := GenerateDrawerID(chunk.wing, chunk.room, chunk.relPath, chunk.chunkIdx, chunk.content)
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
	return drawers
}

// insertDrawers persists the drawers in one transaction, falling back to
// individual inserts so one bad row cannot lose the whole batch.
func insertDrawers(ctx context.Context, palace *Palace, cfg *MineConfig, drawers []*Drawer, result *MineResult) {
	if len(drawers) == 0 {
		return
	}

	inserted, err := palace.store.BatchInsertDrawers(ctx, drawers)
	if err == nil {
		result.Added = inserted
		if cfg.Progress != nil {
			cfg.Progress("", 100, 0, 0)
		}
		return
	}

	slog.Warn("mine: batch insert failed, falling back to individual inserts", "error", err)
	for i, drawer := range drawers {
		if insErr := palace.store.InsertDrawer(ctx, drawer); insErr != nil {
			result.Errors++
		} else {
			result.Added++
		}
		// Report insert progress (70-100% of total).
		if cfg.Progress != nil {
			cfg.Progress("", 70+(100-70)*i/len(drawers), 0, 0)
		}
	}
}

// batchSizeFor reports how many texts to submit per Embed call for e.
func batchSizeFor(e Embedder) int {
	if _, ok := e.(*ollamaEmbedder); ok {
		return ollamaEmbedBatchSize
	}
	return embedBatchSize
}

// embedderName identifies the backend for logs.
func embedderName(e Embedder) string {
	if o, ok := e.(*ollamaEmbedder); ok {
		return "ollama/" + o.model
	}
	return backendName
}
