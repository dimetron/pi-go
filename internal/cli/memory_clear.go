package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newMemoryClearCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "clear [dir]",
		Short: "Clear project memory databases",
		Long: `Removes the project's episodic memory and MemPalace databases.

The configuration file, embedding models, and project files are left untouched.
By default, asks for confirmation. Use --force to skip the prompt.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runMemoryClear(dir, force, cmd.InOrStdin(), cmd.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation")
	return cmd
}

func runMemoryClear(dir string, force bool, in io.Reader, out io.Writer) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving directory: %w", err)
	}

	paths := []string{
		filepath.Join(absDir, ".pi-go", "memory", "claude-mem.db"),
		filepath.Join(absDir, ".pi-go", "palace.db"),
	}
	var existing []string
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			existing = append(existing, path)
		}
	}
	if len(existing) == 0 {
		fmt.Fprintln(out, "No project memory databases found.")
		return nil
	}

	if !force {
		fmt.Fprintf(out, "This will permanently clear %d memory database(s) under %s. Continue? [y/N] ", len(existing), absDir)
		answer, err := bufio.NewReader(in).ReadString('\n')
		if err != nil && len(answer) == 0 {
			return fmt.Errorf("reading confirmation: %w", err)
		}
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(out, "Memory clear canceled.")
			return nil
		}
	}

	for _, path := range existing {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing %s: %w", path+suffix, err)
			}
		}
		fmt.Fprintf(out, "Cleared %s\n", path)
	}
	return nil
}
