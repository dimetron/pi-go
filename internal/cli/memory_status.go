package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

func newMemoryStatusCmd() *cobra.Command {
	var flagDB string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show palace status overview",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryStatus(flagDB)
		},
	}

	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path (default: .pi-go/palace.db)")

	return cmd
}

func runMemoryStatus(dbPath string) error {
	if dbPath == "" {
		dbPath = filepath.Join(".pi-go", "palace.db")
	}

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("No palace database found.")
		fmt.Printf("Looked at: %s\n", dbPath)
		fmt.Println("\nRun 'pi memory init' to create one.")
		return nil
	}

	p, err := palace.New(palace.WithDBPath(dbPath))
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	status, err := p.Status(ctx)
	if err != nil {
		return fmt.Errorf("getting status: %w", err)
	}

	fmt.Println("Palace Status")
	fmt.Println("─────────────")
	fmt.Printf("Drawers:  %d\n", status.DrawerCount)
	fmt.Printf("Wings:    %d\n", status.WingCount)
	fmt.Printf("Rooms:    %d\n", status.RoomCount)

	if status.ModelLoaded {
		fmt.Println("Model:    loaded")
	} else {
		fmt.Println("Model:    not loaded")
	}

	if status.KG != nil {
		fmt.Println()
		fmt.Println("Knowledge Graph")
		fmt.Println("───────────────")
		fmt.Printf("Entities: %d\n", status.KG.EntityCount)
		fmt.Printf("Triples:  %d (%d active)\n", status.KG.TripleCount, status.KG.ActiveTriples)
		if len(status.KG.Predicates) > 0 {
			fmt.Printf("Predicates: %v\n", status.KG.Predicates)
		}
	}

	// Show wing details if there are any.
	if status.WingCount > 0 {
		wings, err := p.ListWings(ctx)
		if err == nil && len(wings) > 0 {
			fmt.Println()
			fmt.Println("Wings")
			fmt.Println("─────")
			for _, w := range wings {
				fmt.Printf("  %-20s  %d drawers, %d rooms\n", w.Wing, w.DrawerCount, w.RoomCount)
			}
		}
	}

	return nil
}
