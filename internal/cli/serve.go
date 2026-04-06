package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimetron/pi-go/internal/webserver"
)

var (
	flagServeAddr           string
	flagServeProject        string
	flagServePairingTimeout time.Duration
)

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start web server for remote terminal access",
		Long: `Start a web server that exposes a terminal running the pi-go agent via a browser.
Users authenticate by scanning a QR code or entering a pair code in the pi-go mobile app.
Each browser tab gets its own isolated agent session.`,
		RunE: runServe,
	}

	cmd.Flags().StringVar(&flagServeAddr, "addr", ":8080", "Listen address for the web server")
	cmd.Flags().StringVar(&flagServeProject, "project", "", "Default project path (default: current directory)")
	cmd.Flags().DurationVar(&flagServePairingTimeout, "pairing-timeout", 5*time.Minute, "Pairing code expiry time")

	return cmd
}

func runServe(cmd *cobra.Command, args []string) error {
	// Determine project path
	project := flagServeProject
	if project == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		project = cwd
	}

	// Resolve static files directory
	staticDir, err := findStaticDir()
	if err != nil {
		return fmt.Errorf("finding static directory: %w", err)
	}

	// Create server configuration
	cfg := webserver.Config{
		Addr:           flagServeAddr,
		PairingTimeout: flagServePairingTimeout,
		StaticDir:      staticDir,
	}

	// Create server
	server := webserver.NewServerV2(cfg)

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	if err := server.Start(); err != nil {
		return fmt.Errorf("starting server: %w", err)
	}

	fmt.Printf("Pi-Go web server started at http://%s\n", flagServeAddr)
	fmt.Printf("Project: %s\n", project)
	fmt.Printf("Pairing timeout: %s\n", flagServePairingTimeout)
	fmt.Println("\nPress Ctrl+C to stop the server")

	// Wait for shutdown signal
	<-sigChan
	fmt.Println("\nShutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down server: %w", err)
	}

	fmt.Println("Server stopped")
	return nil
}

// findStaticDir finds the static directory relative to the executable.
func findStaticDir() (string, error) {
	// Try executable directory first
	exe, err := os.Executable()
	if err == nil {
		staticDir := filepath.Join(filepath.Dir(exe), "internal", "webserver", "static")
		if _, err := os.Stat(staticDir); err == nil {
			return filepath.Dir(exe), nil
		}
	}

	// Try current working directory
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	staticDir := filepath.Join(cwd, "internal", "webserver", "static")
	if _, err := os.Stat(staticDir); err == nil {
		return cwd, nil
	}

	// Fallback to cwd
	return cwd, nil
}

// GetServePairingManager returns the pairing manager from the server for mobile app approval.
// This is used by the mobile app integration.
func GetServePairingManager(server *webserver.ServerV2) *webserver.PairingManager {
	return server.PairingManager()
}

// ParsePairingCode parses a pairing code from user input.
// It handles both plain codes and "code:token" format.
func ParsePairingCode(input string) (code, token string, err error) {
	input = strings.TrimSpace(input)
	parts := strings.Split(input, ":")
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	// Assume it's just a code
	return input, "", nil
}
