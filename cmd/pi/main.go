package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/cli"
	"github.com/dimetron/pi-go/internal/otel"
)

// applyGCDefaults honors GOMEMLIMIT / GOGC if the user already set them and
// otherwise pins soft ceilings that match the measured pprof data on this
// binary (TODO §28): heap in-use oscillates 18–85 MB across a long session, so
// 512 MiB is well above the working set but below the point where the
// scavenger starts sawtoothing (`madvise` was 21% of CPU on a 705 s aged
// capture). The 200 default for GOGC keeps GC pressure off the render hot
// path; a long history is a churn problem, not a retention problem, so trading
// some extra RSS for fewer cycles is the right trade.
//
// Both values are env-overridable: set GOMEMLIMIT=0 to keep the runtime
// default, or GOGC=off to disable the tuning entirely. SetGCPercent returns
// the previous value; we don't act on it.
func applyGCDefaults() {
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(512 << 20) // 512 MiB
	}
	if os.Getenv("GOGC") == "" {
		// 200 doubles the default 100: fewer GC cycles per byte of churn.
		// The aged pprof capture was at ~26 GC/sec at GOGC=100; 200 should
		// halve that without inflating RSS past the GOMEMLIMIT cap above.
		debug.SetGCPercent(200)
	}
}

func main() {
	applyGCDefaults()
	os.Exit(run(os.Stderr, cli.Execute))
}

// run is main's body with the process-exiting and CLI-dispatching parts pulled
// out, so the startup sequence (dotenv → OTEL probe → execute → trace flush) is
// testable without spawning a process or running the real cobra command tree.
// It returns the exit code.
func run(stderr io.Writer, execute func() error) int {
	// Load ~/.pi-go/.env and project .pi-go/.env before any CLI/provider setup
	// reads API keys or OTEL settings from the process environment.
	cli.LoadDotEnv()

	// Test if OTEL collector port is available at startup.
	// If not reachable, disable tracing to prevent connection errors.
	if !isOTELPortAvailable() {
		os.Setenv("OTEL_TRACES_EXPORTER", "none")
	}

	if err := execute(); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	// Flush any pending OTEL traces before exiting.
	_ = otel.Shutdown(context.Background())
	return 0
}

// isOTELPortAvailable checks if the configured OTEL collector port is reachable.
// If the collector is reachable, it returns true to enable OTEL tracing.
// If the collector is NOT reachable, it disables tracing and returns false.
func isOTELPortAvailable() bool {
	// Parse OTEL config to find the collector port
	exporter := os.Getenv("OTEL_TRACES_EXPORTER")
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")

	port := 4317 // default gRPC port
	if endpoint != "" && strings.Contains(endpoint, ":") {
		parts := strings.Split(endpoint, ":")
		if p, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			port = p
		}
	}

	// Check if collector port is reachable
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		// Collector not reachable - disable tracing if not already
		if exporter != "none" && exporter != "" {
			os.Setenv("OTEL_TRACES_EXPORTER", "none")
		}
		return false
	}
	conn.Close()

	// Collector is reachable - enable tracing if not already set
	if exporter == "none" || exporter == "" {
		os.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	}
	return true
}
