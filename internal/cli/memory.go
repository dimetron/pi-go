package cli

import "github.com/spf13/cobra"

func newMemoryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "memory",
		Short: "Manage the MemPalace memory system",
		Long: `Commands for managing the MemPalace memory system: download embedding models,
initialize palace databases, and view palace status.`,
	}

	cmd.AddCommand(newMemoryModelCmd())
	cmd.AddCommand(newMemoryInitCmd())
	cmd.AddCommand(newMemoryStatusCmd())
	cmd.AddCommand(newMemoryMineCmd())

	return cmd
}
