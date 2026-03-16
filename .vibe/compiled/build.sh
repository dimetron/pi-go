#!/bin/bash
set -euo pipefail

# Preflight checks
if ! command -v go > /dev/null 2>&1; then
  echo "error: go is required but not installed"
  exit 2
fi

# Build the binary named pi-go from the module root
go build -o pi-go ./cmd/pi