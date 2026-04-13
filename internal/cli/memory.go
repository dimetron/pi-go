package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// defaultPalaceModelPath returns the default embedding model path.
func defaultPalaceModelPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".pi-go", "models", "sentence-transformers_all-MiniLM-L6-v2")
	}
	return ""
}

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
	cmd.AddCommand(newMemorySearchCmd())
	cmd.AddCommand(newMemoryKGCmd())
	cmd.AddCommand(newMemoryWakeUpCmd())
	cmd.AddCommand(newMemoryRecentCmd())

	return cmd
}
