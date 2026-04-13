package cli

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

func newMemoryWakeUpCmd() *cobra.Command {
	var (
		flagDB   string
		flagWing string
	)

	cmd := &cobra.Command{
		Use:   "wake-up",
		Short: "Print L0+L1 palace context to stdout",
		Long: `Outputs the combined identity (L0) and essential story (L1) context that
would be injected into an agent's system prompt. Useful for debugging or
piping into other tools.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryWakeUp(flagDB, flagWing)
		},
	}

	cmd.Flags().StringVar(&flagDB, "db", "", "Palace database path (default: .pi-go/palace.db)")
	cmd.Flags().StringVar(&flagWing, "wing", "", "Filter essential story to a specific wing")

	return cmd
}

func runMemoryWakeUp(dbPath, wing string) error {
	if dbPath == "" {
		dbPath = filepath.Join(".pi-go", "palace.db")
	}

	p, err := palace.New(
		palace.WithDBPath(dbPath),
		palace.WithModelPath(defaultPalaceModelPath()),
	)
	if err != nil {
		return fmt.Errorf("opening palace: %w", err)
	}
	defer p.Close()

	ctx := context.Background()
	text, err := p.WakeUp(ctx, wing)
	if err != nil {
		return fmt.Errorf("wake-up: %w", err)
	}

	if text == "" {
		fmt.Println("No palace context available.")
		fmt.Println("Add drawers with 'pi memory mine' or via agent tools.")
		return nil
	}

	fmt.Print(text)
	return nil
}
