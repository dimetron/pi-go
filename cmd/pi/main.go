package main

import (
	"context"
	"fmt"
	"os"

	"github.com/dimetron/pi-go/internal/cli"
	"github.com/dimetron/pi-go/internal/otel"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	// Flush any pending OTEL traces before exiting.
	_ = otel.Shutdown(context.Background())
}
