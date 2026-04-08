package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

func newMemoryMineCmd() *cobra.Command {
	var (
		flagWing   string
		flagConvos bool
	)

	cmd := &cobra.Command{
		Use:   "mine <dir>",
		Short: "Mine project files or conversations into the palace",
		Long: `Walks a directory and ingests source files (or conversation files with --convos)
as palace drawers. Room assignment uses mempalace.yaml if present, or falls back
to directory structure. Respects .gitignore.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryMine(args[0], flagWing, flagConvos)
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

	dbPath := filepath.Join(absDir, ".pi-go", "palace.db")

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	cfg := &palace.MineConfig{Wing: wing}
	ctx := context.Background()

	var result *palace.MineResult

	if convos {
		fmt.Printf("Mining conversations from %s...\n", absDir)
		result, err = palace.MineConversations(ctx, p, absDir, cfg)
	} else {
		fmt.Printf("Mining project files from %s...\n", absDir)
		result, err = palace.MineProject(ctx, p, absDir, cfg)
	}

	if err != nil {
		return fmt.Errorf("mining: %w", err)
	}

	fmt.Printf("Mining complete:\n")
	fmt.Printf("  Processed: %d\n", result.Processed)
	fmt.Printf("  Added:     %d\n", result.Added)
	fmt.Printf("  Skipped:   %d (duplicates)\n", result.Skipped)
	fmt.Printf("  Errors:    %d\n", result.Errors)

	return nil
}
