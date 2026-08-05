#!/usr/bin/env bash
# Provision the pi-go dev container. Run once by onCreateCommand.
#
# Everything here is either a tool the Makefile / CI invokes, or a language
# server that internal/lsp starts by name. Versions that CI pins are pinned
# here too; the rest track latest deliberately.
set -euo pipefail

# Cache volumes are created root-owned; the Go toolchain needs them writable.
sudo chown -R "$(id -u):$(id -g)" "${GOPATH:-$HOME/go}" "$HOME/.cache/go-build" 2>/dev/null || true

echo "==> apt packages"
sudo apt-get update -y
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
  ripgrep `# internal/tools shells out to rg` \
  jq \
  build-essential `# cgo, and the opt-in ORT accelerated build` \
  ca-certificates
sudo rm -rf /var/lib/apt/lists/*

echo "==> Go tools"
# gopls: the Go language server internal/lsp starts, and the VS Code Go extension.
go install golang.org/x/tools/gopls@latest
# govulncheck: `make check-cve`.
go install golang.org/x/vuln/cmd/govulncheck@latest
# dlv: debugging from the IDE.
go install github.com/go-delve/delve/cmd/dlv@latest
# goreleaser: dry-runs of .goreleaser.yaml (`goreleaser release --snapshot --clean`).
go install github.com/goreleaser/goreleaser/v2@latest

# ci.yml asks the action for "v2.11", which resolves to the newest v2.11.x —
# so pin the patch here rather than v2.11.0, or the container lints with an
# older ruleset than CI does.
echo "==> golangci-lint (pinned to the v2.11 line used by .github/workflows/ci.yml)"
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh |
  sudo sh -s -- -b /usr/local/bin v2.11.4

echo "==> grype (make check-cve)"
curl -sSfL https://raw.githubusercontent.com/anchore/grype/main/install.sh |
  sudo sh -s -- -b /usr/local/bin

echo "==> language servers"
# typescript: installHint in internal/lsp/manager.go.
npm install -g typescript typescript-language-server
# rust: `rustup component add rust-analyzer`, same hint.
rustup component add rust-analyzer clippy rustfmt
# python: internal/lsp runs `uvx ty server`; warm the tool cache so the first
# LSP start is not a cold download.
uv tool install ty || true

echo "==> warm module cache"
go mod download

echo "==> git"
# CONTRIBUTING.md ships a prepare-commit-msg hook under .githooks/.
git config --local core.hooksPath .githooks || true
# The repo is bind-mounted from the host, so ownership differs from the
# container user; without this every git command in the workspace errors.
git config --global --add safe.directory "$(pwd)" || true

cat <<'EOF'

pi-go dev container ready.

  make build          make lint          make test
  make test-all       make check-cve     make test-coverage

Notes
  * Commit signing (gpg.format=ssh via 1Password's op-ssh-sign) does not work
    in the container — op-ssh-sign is a macOS host binary. Commit from the
    host, or configure a plain ssh-ed25519 key here and let VS Code forward
    SSH_AUTH_SOCK.
  * `make build-accel` (ONNX Runtime + CoreML) is macOS-only and intentionally
    not provisioned; the default pure-Go build is what runs here.
  * Ollama runs on the host: use http://host.docker.internal:11434.
EOF
