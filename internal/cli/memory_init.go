package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

func newMemoryInitCmd() *cobra.Command {
	var flagWing string

	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Initialize a palace database for a project",
		Long: `Creates a palace SQLite database and generates a mempalace.yaml template
with room candidates derived from directory structure.

If no directory is given, the current directory is used.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) > 0 {
				dir = args[0]
			}
			return runMemoryInit(dir, flagWing)
		},
	}

	cmd.Flags().StringVar(&flagWing, "wing", "", "Wing name (default: directory basename)")

	return cmd
}

func runMemoryInit(dir, wing string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving directory: %w", err)
	}

	if wing == "" {
		wing = filepath.Base(absDir)
	}

	dbPath := filepath.Join(absDir, ".pi-go", "palace.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("creating palace directory: %w", err)
	}

	// Open the palace to run migrations and verify the DB is usable.
	p, err := palace.New(
		palace.WithDBPath(dbPath),
		palace.WithModelPath(defaultPalaceModelPath()),
	)
	if err != nil {
		return fmt.Errorf("initializing palace: %w", err)
	}
	defer p.Close()

	// Scan directory for room candidates (immediate subdirectories).
	rooms := scanRoomCandidates(absDir)

	// Generate mempalace.yaml template.
	yamlPath := filepath.Join(absDir, "mempalace.yaml")
	if _, err := os.Stat(yamlPath); err == nil {
		fmt.Printf("mempalace.yaml already exists at %s\n", yamlPath)
	} else {
		if err := writeMempalaceYAML(yamlPath, wing, rooms); err != nil {
			return fmt.Errorf("writing mempalace.yaml: %w", err)
		}
		fmt.Printf("Created %s\n", yamlPath)
	}

	fmt.Printf("Palace initialized:\n")
	fmt.Printf("  Database: %s\n", dbPath)
	fmt.Printf("  Wing:     %s\n", wing)
	fmt.Printf("  Rooms:    %d candidates detected\n", len(rooms))

	return nil
}

// scanRoomCandidates returns immediate subdirectory names, skipping hidden
// and common non-source directories.
func scanRoomCandidates(dir string) []string {
	skipDirs := map[string]bool{
		"node_modules": true,
		"vendor":       true,
		".git":         true,
		".pi-go":       true,
		"__pycache__":  true,
		"dist":         true,
		"build":        true,
		".idea":        true,
		".vscode":      true,
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var rooms []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name[0] == '.' || skipDirs[name] {
			continue
		}
		rooms = append(rooms, name)
	}
	return rooms
}

func writeMempalaceYAML(path, wing string, rooms []string) error {
	var content string
	content += "# MemPalace configuration\n"
	content += "# See: pi memory mine --help\n\n"
	content += fmt.Sprintf("wing: %s\n\n", wing)
	content += "rooms:\n"
	for _, r := range rooms {
		content += fmt.Sprintf("  - name: %s\n    patterns:\n      - \"%s/**\"\n", r, r)
	}
	if len(rooms) == 0 {
		content += "  # No subdirectories detected. Add rooms manually:\n"
		content += "  # - name: example\n"
		content += "  #   patterns:\n"
		content += "  #     - \"example/**\"\n"
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
