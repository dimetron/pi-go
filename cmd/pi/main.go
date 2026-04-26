package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/dimetron/pi-go/internal/cli"
	"github.com/dimetron/pi-go/internal/otel"
)

func main() {
	// Test if OTEL collector port is available at startup.
	// If not reachable, disable tracing to prevent connection errors.
	if !isOTELPortAvailable() {
		os.Setenv("OTEL_TRACES_EXPORTER", "none")
	}

	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	// Flush any pending OTEL traces before exiting.
	_ = otel.Shutdown(context.Background())
}

// isOTELPortAvailable checks if the configured OTEL collector port is reachable.
func isOTELPortAvailable() bool {
	exporter := os.Getenv("OTEL_TRACES_EXPORTER")
	if exporter == "none" || exporter == "" {
		return false
	}

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	port := 4317
	if strings.Contains(endpoint, ":") {
		if p, err := strconv.Atoi(strings.Split(endpoint, ":")[1]); err == nil {
			port = p
		}
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
