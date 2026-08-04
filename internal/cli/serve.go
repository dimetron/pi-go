package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
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
	flagServeModel          string
	flagServeURL            string
	flagServeHeaders        []string
	flagServeInsecure       bool
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

	cmd.Flags().StringVar(&flagServeAddr, "addr", webserver.DefaultAddr, "Listen address for the web server")
	cmd.Flags().StringVar(&flagServeProject, "project", "", "Default project path (default: current directory)")
	cmd.Flags().DurationVar(&flagServePairingTimeout, "pairing-timeout", 5*time.Minute, "Pairing code expiry time")
	cmd.Flags().StringVar(&flagServeModel, "model", "", "LLM model to use for the web terminal (e.g. claude-sonnet-4-6, gpt-4o, gemini-2.5-pro)")
	cmd.Flags().StringVar(&flagServeURL, "url", "", "LLM API base URL to use for the web terminal")
	cmd.Flags().StringArrayVar(&flagServeHeaders, "header", nil, "Extra HTTP header for LLM requests (key=value, repeatable)")
	cmd.Flags().BoolVar(&flagServeInsecure, "insecure", false, "Skip TLS certificate verification for LLM API calls")

	return cmd
}

func runServe(cmd *cobra.Command, args []string) error {
	for _, h := range flagServeHeaders {
		if !strings.Contains(h, "=") {
			return fmt.Errorf("invalid --header %q: expected key=value", h)
		}
	}

	project := flagServeProject
	if project == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
		project = cwd
	}

	// Open serve.log for streaming structured logs.
	logFile, err := os.OpenFile("serve.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening serve.log: %w", err)
	}
	defer logFile.Close()

	logger := slog.New(slog.NewJSONHandler(
		io.MultiWriter(os.Stderr, logFile),
		&slog.HandlerOptions{Level: slog.LevelInfo},
	))

	cfg := webserver.Config{
		Addr:           flagServeAddr,
		PairingTimeout: flagServePairingTimeout,
		Project:        project,
		Model:          flagServeModel,
		BaseURL:        flagServeURL,
		Headers:        flagServeHeaders,
		Insecure:       flagServeInsecure,
		Logger:         logger,
	}

	server := webserver.NewServerV2(cfg)

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Start server in goroutine
	if err := server.Start(); err != nil {
		return fmt.Errorf("starting server: %w", err)
	}
	code, _, err := server.BootstrapPair(project)
	if err != nil {
		return fmt.Errorf("creating initial pair code: %w", err)
	}

	fmt.Printf("Pi-Go web server started at http://%s\n", browsableAddr(server.Addr()))
	fmt.Printf("Project: %s\n", project)
	if flagServeModel != "" {
		fmt.Printf("Model: %s\n", flagServeModel)
	}
	if flagServeURL != "" {
		fmt.Printf("URL: %s\n", flagServeURL)
	}
	for _, h := range flagServeHeaders {
		fmt.Printf("Header: %s\n", h)
	}
	if flagServeInsecure {
		fmt.Println("TLS verification: disabled (--insecure)")
	}
	fmt.Printf("Pairing timeout: %s\n", flagServePairingTimeout)
	fmt.Printf("Pair code: %s\n", code)
	fmt.Println("Open /pair in your browser, then enter this pair code in mobile app.")
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

// browsableAddr turns a listen address into one a browser can open. Wildcard
// binds (":8765", "0.0.0.0:8765", "[::]:8765") are printed as localhost so the
// URL in the terminal is clickable.
func browsableAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return net.JoinHostPort(host, port)
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
