#!/usr/bin/env bash
# Install the optional toolchains that the pi-go container deliberately omits.
#
# The container ships Go and nothing else, because nothing else is needed:
# `make build`, `make test` and `make lint` all pass with only Go on PATH. The
# LSP tests that would need a language server skip themselves when it is absent
# (internal/lsp/python_lsp_test.go, internal/lsp/ruff_integration_test.go), and
# the TypeScript tests never spawn a server at all.
#
# So these belong here rather than in the image: they are for working ON
# internal/lsp against real language servers, and for the release and CVE
# targets. In the image they cost roughly two extra minutes per rebuild —
# python alone compiles CPython from source.
#
#   .devcontainer/install-deps.sh               # everything
#   .devcontainer/install-deps.sh rust python   # only those
#
# Components: node, python, rust, security, release
#
# Re-running is safe: each component is skipped when already present.
set -euo pipefail

CARGO_BIN="$HOME/.cargo/bin"
LOCAL_BIN="$HOME/.local/bin"

have() { command -v "$1" >/dev/null 2>&1; }

# --- components ---

# node: internal/lsp starts `typescript-language-server --stdio`; the
# installHint in internal/lsp/manager.go names this exact package pair.
install_node() {
	if have typescript-language-server; then
		echo "==> node: already present, skipping"
		return
	fi
	if ! have node; then
		echo "==> node: installing LTS from NodeSource"
		curl -fsSL https://deb.nodesource.com/setup_lts.x | sudo -E bash -
		sudo apt-get install -y --no-install-recommends nodejs
	fi
	echo "==> node: installing typescript-language-server"
	sudo npm install -g typescript typescript-language-server
}

# python: internal/lsp starts `uvx ty server`, and hack/test/lsp/python is a uv
# project. uv supplies its own Python, so nothing here builds CPython or needs
# the system interpreter.
install_python() {
	if ! have uv; then
		echo "==> python: installing uv"
		curl -LsSf https://astral.sh/uv/install.sh | sh
	fi
	export PATH="$LOCAL_BIN:$PATH"
	echo "==> python: installing a managed interpreter"
	uv python install 3.12
	# Warms the tool cache so the first LSP start is not a cold download.
	echo "==> python: installing ty"
	uv tool install ty || true
}

# rust: internal/lsp starts `rust-analyzer`; hack/test/lsp/rust is a cargo crate.
install_rust() {
	if have rust-analyzer; then
		echo "==> rust: already present, skipping"
		return
	fi
	if ! have rustup; then
		echo "==> rust: installing rustup (minimal profile)"
		curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs |
			sh -s -- -y --profile minimal --no-modify-path
	fi
	export PATH="$CARGO_BIN:$PATH"
	echo "==> rust: adding rust-analyzer, clippy, rustfmt"
	rustup component add rust-analyzer clippy rustfmt
}

# security: `make check-cve`. govulncheck is not installed here because that
# target installs it itself.
install_security() {
	if have grype; then
		echo "==> security: grype already present, skipping"
		return
	fi
	echo "==> security: installing grype"
	curl -sSfL https://raw.githubusercontent.com/anchore/grype/main/install.sh |
		sudo sh -s -- -b /usr/local/bin
}

# release: dry-runs of .goreleaser.yaml (`goreleaser release --snapshot --clean`).
install_release() {
	if have goreleaser; then
		echo "==> release: goreleaser already present, skipping"
		return
	fi
	echo "==> release: installing goreleaser"
	go install github.com/goreleaser/goreleaser/v2@latest
}

# --- dispatch ---

components=("$@")
if [ ${#components[@]} -eq 0 ]; then
	components=(node python rust security release)
fi

for component in "${components[@]}"; do
	case "$component" in
	node) install_node ;;
	python) install_python ;;
	rust) install_rust ;;
	security) install_security ;;
	release) install_release ;;
	*)
		echo "unknown component: $component" >&2
		echo "expected one or more of: node python rust security release" >&2
		exit 2
		;;
	esac
done

cat <<'EOF'

Done. Open a new shell (or `. /etc/profile.d/10-pi-go-path.sh`) so the new
directories are on PATH.

EOF
