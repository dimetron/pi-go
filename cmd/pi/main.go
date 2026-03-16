package main

import (
	"fmt"
	"os"

	"github.com/dimetron/pi-go/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
