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

	"github.com/dimetron/pi-go/internal/config"
	"github.com/dimetron/pi-go/internal/voicegemini"
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
	flagServeVoice          bool
	flagServeVoiceModel     string
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
	cmd.Flags().BoolVar(&flagServeVoice, "voice", false, "Talk to the coding agent from the browser: a Gemini Live session that types prompts into this project's pi terminal and reads its output back (needs GEMINI_API_KEY, from the environment or .env)")
	cmd.Flags().StringVar(&flagServeVoiceModel, "voice-model", "", "Gemini Live model for voice (default "+voicegemini.DefaultModel+"; also GEMINI_LIVE_MODEL)")

	return cmd
}

func runServe(cmd *cobra.Command, args []string) error {
	if err := validateServeHeaders(); err != nil {
		return err
	}

	project, err := resolveServeProject()
	if err != nil {
		return err
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

	// Voice is verified before the listener opens: a wrong key or a non-Live
	// model becomes a startup error the operator reads in this terminal,
	// rather than a dead microphone the user discovers mid-sentence.
	if flagServeVoice {
		if err := enableServeVoice(cmd.Context(), server); err != nil {
			return err
		}
	}

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

	printServeBanner(os.Stdout, server.Addr(), project, code)

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

// validateServeHeaders rejects a --header that is not key=value, before the
// listener opens.
func validateServeHeaders() error {
	for _, h := range flagServeHeaders {
		if !strings.Contains(h, "=") {
			return fmt.Errorf("invalid --header %q: expected key=value", h)
		}
	}
	return nil
}

// resolveServeProject is the project directory to serve: --project, or the
// current directory when the flag is unset.
func resolveServeProject() (string, error) {
	if flagServeProject != "" {
		return flagServeProject, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting current directory: %w", err)
	}
	return cwd, nil
}

// printServeBanner writes the startup summary: where to reach the server, what
// it is serving, and the pair code needed to get in. Optional lines are printed
// only for the options actually in effect.
func printServeBanner(w io.Writer, addr, project, code string) {
	fmt.Fprintf(w, "Pi-Go web server started at http://%s\n", browsableAddr(addr))
	fmt.Fprintf(w, "Project: %s\n", project)
	if flagServeModel != "" {
		fmt.Fprintf(w, "Model: %s\n", flagServeModel)
	}
	if flagServeURL != "" {
		fmt.Fprintf(w, "URL: %s\n", flagServeURL)
	}
	for _, h := range flagServeHeaders {
		fmt.Fprintf(w, "Header: %s\n", h)
	}
	if flagServeInsecure {
		fmt.Fprintln(w, "TLS verification: disabled (--insecure)")
	}
	if flagServeVoice {
		fmt.Fprintf(w, "Voice: enabled (%s) — speech drives the pi session in this project\n", serveVoiceModel())
	}
	fmt.Fprintf(w, "Pairing timeout: %s\n", flagServePairingTimeout)
	fmt.Fprintf(w, "Pair code: %s\n", code)
	fmt.Fprintln(w, "Open /pair in your browser, then enter this pair code in mobile app.")
	fmt.Fprintln(w, "\nPress Ctrl+C to stop the server")
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

// serveProjectDir is the directory `pi serve` is serving, which is where a
// project-local .env lives. It repeats the resolution runServe does because the
// voice key is looked up before the server is built.
func serveProjectDir() string {
	if flagServeProject != "" {
		return flagServeProject
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// serveVoiceModel resolves the Live model from the flag, the environment, and
// the package default, in that order. The flag wins so a single run can try a
// new model without editing the environment.
func serveVoiceModel() string {
	if flagServeVoiceModel != "" {
		return flagServeVoiceModel
	}
	if m, _ := config.LookupEnvFrom(serveProjectDir(), "GEMINI_LIVE_MODEL"); m != "" {
		return m
	}
	return voicegemini.DefaultModel
}

// enableServeVoice turns on the browser voice session, failing with an
// actionable message when the key is absent.
//
// The key is resolved through config.LookupEnvFrom rather than os.Getenv so
// that the file `pi login` writes it to counts. Requiring an export for voice
// alone — when every other pi command finds the same key in ~/.pi-go/.env —
// reads as "voice is broken", and the operator has no way to tell an unset key
// from an unread one.
func enableServeVoice(ctx context.Context, server *webserver.ServerV2) error {
	key, source := config.LookupEnvFrom(serveProjectDir(), "GEMINI_API_KEY", "GOOGLE_API_KEY")
	if key == "" {
		return fmt.Errorf("--voice needs GEMINI_API_KEY: export it, or put it in .pi-go/.env, .env, or ~/.pi-go/.env")
	}
	fmt.Printf("Voice: GEMINI_API_KEY from %s\n", source)

	if ctx == nil {
		ctx = context.Background()
	}
	// Verification is a single models round-trip; bound it so a hung network
	// cannot stall startup indefinitely.
	vctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := server.EnableVoice(vctx, key, voicegemini.WithModel(serveVoiceModel())); err != nil {
		return fmt.Errorf("enabling voice: %w", err)
	}
	return nil
}
