package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

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
	barWidth := 32
	fileCount := 0
	chunkCount := 0
	dupCount := 0
	errCount := 0
	spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerIdx := 0
	startTime := time.Now()
	currentPhase := "scan"

	// progressBar generates a Unicode block progress bar.
	progressBar := func(filled, width int) string {
		var b strings.Builder
		b.WriteString("[")
		for i := 0; i < width; i++ {
			if i < filled {
				b.WriteString("█")
			} else {
				b.WriteString("░")
			}
		}
		b.WriteString("]")
		return b.String()
	}

	// formatDuration formats elapsed time as MmSSs or SSs.
	formatDuration := func(d time.Duration) string {
		d = d.Round(time.Second)
		if d >= time.Minute {
			return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
		}
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}

	// Create progress callback that shows live progress with phase awareness.
	progress := func(file string, added, skipped, errors int) {
		if file != "" {
			// File completion callback — show file being processed.
			fileCount++
			chunkCount += added
			dupCount += skipped
			errCount += errors
			spinnerIdx = (spinnerIdx + 1) % len(spinnerFrames)

			// Calculate overall progress (0-100%).
			progressPct := float64(fileCount) / float64(totalFiles) * 100.0
			filled := int(progressPct / 100.0 * float64(barWidth))

			// Show abbreviated filename if too long.
			displayFile := file
			if len(displayFile) > 40 {
				displayFile = "..." + displayFile[len(displayFile)-37:]
			}

			// Erase line and redraw with spinner.
			fmt.Printf("\r %s  %-8s %s  %-38s %3d/%d files, %d chunks  %s\x1b[K",
				spinnerFrames[spinnerIdx], currentPhase,
				progressBar(filled, barWidth), displayFile,
				fileCount, totalFiles, chunkCount,
				formatDuration(time.Since(startTime)))
		} else {
			// Phase progress callback: file="" with percentage in 'added'.
			spinnerIdx = (spinnerIdx + 1) % len(spinnerFrames)
			progressPct := float64(added)
			if progressPct > 100.0 {
				progressPct = 100.0
			}
			filled := int(progressPct / 100.0 * float64(barWidth))

			// Determine stage label from percentage.
			stage := "embed"
			if progressPct >= 70 {
				stage = "insert"
			}
			currentPhase = stage

			// Erase line and redraw.
			fmt.Printf("\r %s  %-8s %s  %-38s %3d/%d files, %d chunks  %s\x1b[K",
				spinnerFrames[spinnerIdx], stage,
				progressBar(filled, barWidth), "",
				fileCount, totalFiles, chunkCount,
				formatDuration(time.Since(startTime)))
		}
	}

	cfg := &palace.MineConfig{Wing: wing, Progress: progress}
	ctx := context.Background()

	dbPath := filepath.Join(absDir, ".pi-go", "palace.db")
	p, err := palace.New(
		palace.WithDBPath(dbPath),
		palace.WithModelPath(defaultPalaceModelPath()),
	)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	var result *palace.MineResult
	if convos {
		result, err = palace.MineConversations(ctx, p, absDir, cfg)
	} else {
		result, err = palace.MineProject(ctx, p, absDir, cfg)
	}

	if err != nil {
		return fmt.Errorf("mining: %w", err)
	}

	// Ensure progress bar shows 100%.
	fmt.Printf("\r ✓  %-8s %s  %-38s %3d/%d files, %d chunks  %s\x1b[K\n",
		"done", progressBar(barWidth, barWidth), "",
		totalFiles, totalFiles, result.Added,
		formatDuration(time.Since(startTime)))

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
