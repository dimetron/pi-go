package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/palace"
)

func newMemoryModelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "model",
		Short: "Manage the embedding model",
	}

	cmd.AddCommand(newMemoryModelDownloadCmd())
	cmd.AddCommand(newMemoryModelStatusCmd())

	return cmd
}

func newMemoryModelDownloadCmd() *cobra.Command {
	var flagDest string

	cmd := &cobra.Command{
		Use:   "download",
		Short: "Download the embedding model (all-MiniLM-L6-v2)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryModelDownload(flagDest)
		},
	}

	cmd.Flags().StringVar(&flagDest, "dest", "", "Destination directory (default: ~/.pi-go/models/)")

	return cmd
}

func runMemoryModelDownload(dest string) error {
	if dest == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		dest = filepath.Join(home, ".pi-go", "models")
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating model directory: %w", err)
	}

	fmt.Printf("Downloading embedding model to %s ...\n", dest)

	modelPath, err := palace.DownloadModel(dest)
	if err != nil {
		return fmt.Errorf("downloading model: %w", err)
	}

	fmt.Printf("Model downloaded: %s\n", modelPath)
	return nil
}

func newMemoryModelStatusCmd() *cobra.Command {
	var flagPath string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Check embedding model status",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMemoryModelStatus(flagPath)
		},
	}

	cmd.Flags().StringVar(&flagPath, "path", "", "Model directory (default: ~/.pi-go/models/)")

	return cmd
}

func runMemoryModelStatus(modelPath string) error {
	if modelPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		modelPath = filepath.Join(home, ".pi-go", "models")
	}

	// Check for model directory with the expected name.
	modelDir := filepath.Join(modelPath, "sentence-transformers_all-MiniLM-L6-v2")
	info, err := os.Stat(modelDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("Model: not downloaded")
			fmt.Printf("Path:  %s\n", modelDir)
			fmt.Println("\nRun 'pi memory model download' to fetch the embedding model.")
			return nil
		}
		return fmt.Errorf("checking model: %w", err)
	}

	// Compute directory size.
	var totalSize int64
	var fileCount int
	_ = filepath.WalkDir(modelDir, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		totalSize += fi.Size()
		fileCount++
		return nil
	})

	fmt.Println("Model: all-MiniLM-L6-v2")
	fmt.Printf("Path:  %s\n", modelDir)
	fmt.Printf("Size:  %.1f MB (%d files)\n", float64(totalSize)/(1024*1024), fileCount)
	fmt.Printf("Modified: %s\n", info.ModTime().Format("2006-01-02 15:04:05"))

	return nil
}
