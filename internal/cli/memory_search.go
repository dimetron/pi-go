package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

func newMemorySearchCmd() *cobra.Command {
	var (
		flagDB    string
		flagWing  string
		flagRoom  string
		flagLimit int
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search palace drawers",
		Long: `Performs semantic search (if the embedding model is loaded) or FTS5 keyword
search against the palace. Results include similarity score, wing, room, and
a content preview.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemorySearch(args[0], flagDB, flagWing, flagRoom, flagLimit)
		},
	}

	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path (default: .pi-go/palace.db)")
	cmd.Flags().StringVar(&flagWing, "wing", "", "Filter by wing")
	cmd.Flags().StringVar(&flagRoom, "room", "", "Filter by room")
	cmd.Flags().IntVar(&flagLimit, "limit", 5, "Maximum results")

	return cmd
}

func runMemorySearch(query, dbPath, wing, room string, limit int) error {
	if dbPath == "" {
		dbPath = filepath.Join(".pi-go", "palace.db")
	}

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	results, err := p.Search(ctx, palace.SearchQuery{
		Query: query,
		Wing:  wing,
		Room:  room,
		Limit: limit,
	})
	if err != nil {
		return fmt.Errorf("search: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No results found.")
		return nil
	}

	fmt.Printf("Found %d result(s):\n\n", len(results))
	for i, r := range results {
		preview := r.Drawer.Content
		if len(preview) > 120 {
			preview = preview[:120] + "..."
		}
		fmt.Printf("%d. [%.2f] %s/%s\n", i+1, r.Similarity, r.Drawer.Wing, r.Drawer.Room)
		fmt.Printf("   %s\n\n", preview)
	}

	return nil
}
