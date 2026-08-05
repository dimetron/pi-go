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
# dlv: debugging from the IDE.
go install github.com/go-delve/delve/cmd/dlv@latest
# govulncheck is not installed here: `make check-cve` installs it itself.
# grype and goreleaser live in install-deps.sh — they serve the CVE and release
# targets, not the build/test/lint loop this container exists for.

# golangci-lint is installed by the go feature, pinned in devcontainer.json.

echo "==> warm module cache"
go mod download

echo "==> PATH"
# remoteEnv in devcontainer.json covers VS Code terminals and the lifecycle
# commands, but not `docker exec`, an SSH session into a Codespace, or anything
# else that starts a shell directly. Write the same entries into the system
# profile so `pi` — which `make install` puts in $GOPATH/bin — resolves in every
# shell however it was started.
sudo tee /etc/profile.d/10-pi-go-path.sh >/dev/null <<'EOF'
export GOPATH="${GOPATH:-$HOME/go}"
# Prepend only when absent, so re-sourcing does not stack duplicate entries.
for _dir in "$GOPATH/bin" "$HOME/.local/bin" "$HOME/.cargo/bin"; do
	case ":$PATH:" in
	*":$_dir:"*) ;;
	*) PATH="$_dir:$PATH" ;;
	esac
done
unset _dir
export PATH
EOF
sudo chmod 0644 /etc/profile.d/10-pi-go-path.sh
mkdir -p "${GOPATH:-$HOME/go}/bin" "$HOME/.local/bin"

# Interactive non-login shells — what `docker exec -it … zsh` gives you — read
# only the rc files, which do not source /etc/profile.d.
for rc in "$HOME/.bashrc" "$HOME/.zshrc"; do
	[ -f "$rc" ] || continue
	grep -q '10-pi-go-path' "$rc" ||
		printf '\n. /etc/profile.d/10-pi-go-path.sh\n' >>"$rc"
done

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
  make install        # puts `pi` in $GOPATH/bin, already on PATH

  kind create cluster --name pi-go        # docker-in-docker; kubectl + helm ready

This image ships Go only — the full unit suite passes with nothing else on
PATH. Node, Python, Rust, grype and goreleaser install on demand:

  .devcontainer/install-deps.sh                # everything
  .devcontainer/install-deps.sh rust python    # only what you need

Notes
  * Commit signing (gpg.format=ssh via 1Password's op-ssh-sign) does not work
    in the container — op-ssh-sign is a macOS host binary. Commit from the
    host, or configure a plain ssh-ed25519 key here and let VS Code forward
    SSH_AUTH_SOCK.
  * `make build-accel` (ONNX Runtime + CoreML) is macOS-only and intentionally
    not provisioned; the default pure-Go build is what runs here.
  * Ollama runs on the host: use http://host.docker.internal:11434.
EOF
