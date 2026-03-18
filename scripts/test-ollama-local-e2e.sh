#!/usr/bin/env bash
set -euo pipefail

# E2E test: run pi with Ollama local qwen3.5 model
# Usage: ./scripts/test-ollama-local-e2e.sh
#
# Uses :cloud suffix for Ollama (Anthropic-compatible API) - works with qwen3.5

PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"

# Run with qwen3.5 - now auto-detected as Ollama model (prefix-based detection)
exec "${PROJECT_DIR}/scripts/test-ollama-cloud-e2e.sh" "qwen3.5" "$@"