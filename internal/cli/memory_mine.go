package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/palace"
)

// scanFiles returns a list of all files that would be mined, respecting
// .gitignore, file size limits, and supported extensions.
func scanFiles(dir string, convos bool) ([]string, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("resolving directory: %w", err)
	}

	// Try git first for accurate .gitignore handling (nested files, ** globs,
	// negation, parent .gitignore). Fall back to manual parsing if not a repo.
	ignoredSet := palace.GitIgnoredSet(absDir)
	var gitignorePatterns map[string]bool
	if ignoredSet == nil {
		gitignorePatterns = loadGitignore(absDir)
	}

	var files []string
	err = filepath.WalkDir(absDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			if (name[0] == '.' && name != ".") || skipDirNames[name] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, err := filepath.Rel(absDir, path)
		if err != nil {
			return nil
		}

		if palace.IsGitIgnoredSet(relPath, ignoredSet) || (ignoredSet == nil && isGitignored(relPath, gitignorePatterns)) {
			return nil
		}

		if convos {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".jsonl" || ext == ".txt" || ext == ".md" {
				files = append(files, relPath)
			}
		} else {
			ext := strings.ToLower(filepath.Ext(path))
			if !supportedExtensions[ext] {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.Size() > 512*1024 {
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil || strings.TrimSpace(string(data)) == "" {
				return nil
			}

			files = append(files, relPath)
		}
		return nil
	})

	return files, err
}

// loadGitignore loads .gitignore patterns from the given directory.
func loadGitignore(dir string) map[string]bool {
	patterns := make(map[string]bool)
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return patterns
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns[line] = true
		}
	}
	return patterns
}

// skipDirNames contains directory names to skip during mining.
var skipDirNames = map[string]bool{
	"node_modules": true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
	"build":        true,
	".git":         true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".next":        true,
}

// supportedExtensions lists file extensions to mine for project files.
var supportedExtensions = map[string]bool{
	".go":    true,
	".py":    true,
	".js":    true,
	".ts":    true,
	".tsx":   true,
	".jsx":   true,
	".java":  true,
	".c":     true,
	".cpp":   true,
	".h":     true,
	".hpp":   true,
	".rs":    true,
	".rb":    true,
	".php":   true,
	".swift": true,
	".kt":    true,
	".scala": true,
	".md":    true,
	".txt":   true,
	".json":  true,
	".yaml":  true,
	".yml":   true,
	".toml":  true,
	".xml":   true,
	".html":  true,
	".css":   true,
	".scss":  true,
	".sql":   true,
	".sh":    true,
	".bash":  true,
	".zsh":   true,
	".fish":  true,
}

// isGitignored returns true if the path matches any gitignore pattern.
// This is a simplified check for common patterns.
func isGitignored(relPath string, patterns map[string]bool) bool {
	parts := strings.Split(relPath, string(filepath.Separator))
	for _, part := range parts {
		if patterns[part] {
			return true
		}
		if patterns["*/"+part] {
			return true
		}
	}
	return false
}

func newMemoryMineCmd() *cobra.Command {
	var (
		flagWing   string
		flagConvos bool
	)

	cmd := &cobra.Command{
		Use:   "mine [dir]",
		Short: "Mine project files or conversations into the palace",
		Long: `Walks a directory and ingests source files (or conversation files with --convos)
as palace drawers. Room assignment uses mempalace.yaml if present, or falls back
to directory structure. Respects .gitignore. Defaults to the current directory.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			return runMemoryMine(dir, flagWing, flagConvos)
		},
	}

	cmd.Flags().StringVar(&flagWing, "wing", "", "Wing name (default: directory basename)")
	cmd.Flags().BoolVar(&flagConvos, "convos", false, "Mine conversation files (JSONL, text) instead of source files")

	return cmd
}

func runMemoryMine(dir, wing string, convos bool) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving directory: %w", err)
	}

	// Resolve the wing here rather than leaving it to MineProject, which applies
	// the same default internally (miner_project.go). Without this the banner
	// below printed an empty "Wing:" while the run actually indexed into the
	// directory-basename wing — the one line whose job is to say which wing was
	// touched was the one line that did not know.
	if wing == "" {
		wing = filepath.Base(absDir)
	}

	// Phase 1: Scan and list all files first.
	fmt.Printf("Scanning %s...\n", absDir)
	files, err := scanFiles(absDir, convos)
	if err != nil {
		return fmt.Errorf("scanning files: %w", err)
	}

	if len(files) == 0 {
		fmt.Println("No files to mine.")
		return nil
	}

	// Print file list.
	fmt.Printf("\nFiles (%d total):\n", len(files))
	for i, f := range files {
		fmt.Printf("  [%2d] %s\n", i+1, f)
	}
	fmt.Println()

	// Phase 2: Mine files with live visual progress.
	totalFiles := len(files)
	fileCount := 0
	chunkCount := 0
	dupCount := 0
	errCount := 0
	startTime := time.Now()

	// All progress rendering funnels through prog, which owns the terminal line,
	// the spinner, the ETA baseline and the width budget. Both callbacks below are
	// invoked concurrently from the embed worker pool, and internal/palace logs
	// warnings from those same goroutines — so the counters and the line need one
	// lock between them. prog provides it.
	prog := newMineProgress(os.Stdout)
	restoreLogs := prog.captureLogs()
	defer restoreLogs()

	// The heartbeat keeps the spinner and the elapsed clock moving even while a
	// phase blocks with nothing to report, which is what makes a slow step
	// distinguishable from a hung one.
	prog.startHeartbeat()
	defer prog.stopHeartbeat()

	// Progress reports file-level work. Two variants share this callback:
	// a named file means one file finished; an empty file name is a
	// percentage-only tick, and it is the *only* signal insertDrawers emits —
	// dropping it left the whole insert phase silent.
	progress := func(file string, pctOrAdded, skipped, errors int) {
		if file == "" {
			prog.show("insert", "", pctOrAdded, 100, "%")
			return
		}
		prog.do(func() string {
			fileCount++
			chunkCount += pctOrAdded
			dupCount += skipped
			errCount += errors
			return ""
		})
		prog.show("scan", file, fileCount, totalFiles, "files")
	}

	// Phase reports chunk-level work within a stage (embed, insert), which is
	// where the slow part of a run actually is.
	phase := func(stage, item string, done, total int) {
		prog.show(stage, item, done, total, "chunks")
	}

	cfg := &palace.MineConfig{Wing: wing, Progress: progress, Phase: phase}
	ctx := context.Background()

	dbPath := filepath.Join(absDir, ".pi-go", "palace.db")
	modelPath := defaultPalaceModelPath()

	palaceCfg := palace.DefaultConfig()
	palaceCfg.DBPath = dbPath
	palaceCfg.ModelPath = modelPath
	if userCfg, err := config.Load(); err == nil && userCfg.Palace != nil {
		if userCfg.Palace.OllamaURL != "" {
			palaceCfg.OllamaURL = userCfg.Palace.OllamaURL
		}
		if userCfg.Palace.OllamaModel != "" {
			palaceCfg.OllamaModel = userCfg.Palace.OllamaModel
		}
		if userCfg.Palace.LocalEmbedder {
			palaceCfg.UseOllama = false
		}
	}

	// Mining without an embedder produces drawers with no vectors, which look
	// fine until every semantic search silently returns nothing. Refuse up front
	// rather than spend minutes indexing into a broken state.
	if err := palace.EmbedderAvailability(palaceCfg); err != nil {
		return ollamaSetupError(palaceCfg, err)
	}

	// Name the database and model up front. Mining writes to a per-project DB and
	// loads a model from a shared cache, and neither location is obvious from the
	// command line — so a run that appeared to do nothing (or that re-embedded
	// everything) gave no clue which store it had actually touched.
	fmt.Printf("Palace DB: %s\n", dbPath)
	if palaceCfg.UseOllama {
		fmt.Printf("Embedder:  ollama %s (%s)\n", palaceCfg.OllamaModel, palaceCfg.OllamaURL)
	} else {
		fmt.Printf("Embedder:  in-process %s\n", modelPath)
	}
	fmt.Printf("Wing:      %s\n\n", wing)

	// Auto-init: create the palace directory and fetch the model if needed, so
	// `pi memory mine` works on a fresh checkout without a separate `memory init`
	// / `memory model download` step.
	//
	// ModelReady checks for the fp32 weights specifically, not just for the
	// directory. That matters for repair as much as for first use: installs made
	// before the fp32 switch hold only model_qint8_arm64.onnx, which loads fine
	// but runs ~3x slower on the pure-Go backend, so a directory-exists check
	// would leave them on the slow model indefinitely.
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("creating palace directory: %w", err)
	}
	if !palace.ModelReady(modelPath) {
		fmt.Printf("Embedding model not found (or is the older quantized build) — downloading %s ...\n",
			palace.DetectPlatformOnnxFile())
		dest := filepath.Dir(modelPath)
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("creating model directory: %w", err)
		}
		if _, err := palace.DownloadModel(dest, palace.DetectPlatformOnnxFile()); err != nil {
			return fmt.Errorf("downloading embedding model: %w", err)
		}
		fmt.Printf("Model ready: %s\n\n", modelPath)
	}

	palaceOpts := []palace.Option{
		palace.WithDBPath(dbPath),
		palace.WithModelPath(modelPath),
	}
	if palaceCfg.UseOllama {
		palaceOpts = append(palaceOpts, palace.WithOllamaEmbedder(palaceCfg.OllamaURL, palaceCfg.OllamaModel))
	} else {
		palaceOpts = append(palaceOpts, palace.WithLocalEmbedder())
	}
	// Opening the palace loads the embedding model and runs any schema
	// migrations, which on a cold start is seconds of silence right after the
	// banner — the exact point the run looked wedged.
	prog.status("open", "opening palace, loading embedder")
	p, err := palace.New(palaceOpts...)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	prog.status("scan", "collecting files")

	var result *palace.MineResult
	if convos {
		result, err = palace.MineConversations(ctx, p, absDir, cfg)
	} else {
		result, err = palace.MineProject(ctx, p, absDir, cfg)
	}

	if err != nil {
		return fmt.Errorf("mining: %w", err)
	}

	// Ensure the bar shows 100%, and close the live region so the stats below
	// start on their own line.
	prog.finish(fmt.Sprintf("✓ %-6s [%s] 100%%  %d/%d files, %d chunks  %s",
		"done", renderBar(100, barCells),
		totalFiles, totalFiles, result.Added,
		formatETA(time.Since(startTime))))

	// Show final mining stats.
	fmt.Println()
	fmt.Println("Mining complete:")
	fmt.Printf("  Processed: %d\n", result.Processed)
	fmt.Printf("  Added:     %d\n", result.Added)
	fmt.Printf("  Skipped:   %d (duplicates)\n", result.Skipped)
	fmt.Printf("  Errors:    %d\n", result.Errors)

	// Show palace status after mining.
	status, err := p.Status(ctx)
	if err == nil {
		fmt.Println()
		fmt.Println("Palace status:")
		fmt.Printf("  Drawers: %d\n", status.DrawerCount)
		fmt.Printf("  Wings:   %d\n", status.WingCount)
		fmt.Printf("  Rooms:   %d\n", status.RoomCount)
	}

	return nil
}

// ollamaSetupError turns an embedder-availability failure into instructions.
//
// Mining is the one command that cannot degrade: an unembedded drawer is
// invisible to semantic search forever after, and the failure is silent at
// query time. The two ways this goes wrong — daemon down, model not pulled —
// have different fixes, so the message names both rather than dumping the
// underlying error and leaving the user to guess.
func ollamaSetupError(cfg palace.PalaceConfig, cause error) error {
	if !errors.Is(cause, palace.ErrOllamaUnavailable) {
		return fmt.Errorf("embedding backend unavailable: %w", cause)
	}

	fmt.Fprintf(os.Stderr, `
ollama is required for "pi memory mine" but is not available.

  cause: %v

  Fix one of the following, then re-run:

    1. Start the daemon:   ollama serve
    2. Pull the model:     ollama pull %s

  The daemon is expected at %s; set palace.ollama_url to change it.

`, cause, cfg.OllamaModel, cfg.OllamaURL)

	return fmt.Errorf("embedding backend unavailable: %w", cause)
}
