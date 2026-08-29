#!/usr/bin/env bash
# Install the Google Antigravity ACP server used by pi-go's "agy" subagent.
#
# The agy CLI has no ACP mode: Antigravity ships a standalone ACP server binary
# distributed as a platform archive by the Agent Client Protocol registry. This
# script resolves the entry for the current platform, downloads the archive and
# extracts it into ~/.pi-go/acp/agy, which is the first location the adapter
# (internal/acp/client/agy) searches.
#
# The download is large (~300 MB compressed, ~900 MB extracted).
#
# Usage: scripts/install-agy-acp.sh [install-dir]

set -euo pipefail

AGENT_JSON_URL="${AGY_ACP_AGENT_JSON:-https://raw.githubusercontent.com/agentclientprotocol/registry/main/antigravity-acp/agent.json}"
INSTALL_DIR="${1:-$HOME/.pi-go/acp/agy}"

for cmd in curl python3 unzip; do
  command -v "$cmd" >/dev/null 2>&1 || { echo "error: $cmd is required" >&2; exit 1; }
done

# Map uname output onto the registry's platform identifiers (FORMAT.md).
case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *)      echo "error: unsupported OS $(uname -s); install the Windows build manually" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) arch=aarch64 ;;
  x86_64|amd64)  arch=x86_64 ;;
  *)             echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac
platform="$os-$arch"

echo "==> resolving antigravity-acp for $platform"
entry="$(curl -fsSL "$AGENT_JSON_URL")"
read -r archive_url sha256 <<EOF
$(printf '%s' "$entry" | python3 -c '
import json, sys
platform = sys.argv[1]
target = json.load(sys.stdin)["distribution"]["binary"].get(platform)
if target is None:
    sys.exit(f"no binary distribution for {platform}")
print(target["archive"], target.get("sha256", ""))
' "$platform")
EOF

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
archive="$tmp/${archive_url##*/}"

echo "==> downloading $archive_url"
curl -fL --progress-bar -o "$archive" "$archive_url"

if [ -n "$sha256" ]; then
  echo "==> verifying sha256"
  actual="$(python3 -c '
import hashlib, sys
h = hashlib.sha256()
with open(sys.argv[1], "rb") as f:
    for chunk in iter(lambda: f.read(1 << 20), b""):
        h.update(chunk)
print(h.hexdigest())
' "$archive")"
  if [ "$(printf '%s' "$actual" | tr 'A-Z' 'a-z')" != "$(printf '%s' "$sha256" | tr 'A-Z' 'a-z')" ]; then
    echo "error: sha256 mismatch: got $actual, want $sha256" >&2
    exit 1
  fi
fi

echo "==> extracting into $INSTALL_DIR"
mkdir -p "$INSTALL_DIR"
case "$archive" in
  *.zip)             unzip -oq "$archive" -d "$INSTALL_DIR" ;;
  *.tar.gz|*.tgz)    tar -xzf "$archive" -C "$INSTALL_DIR" ;;
  *.tar.bz2|*.tbz2)  tar -xjf "$archive" -C "$INSTALL_DIR" ;;
  *)                 echo "error: unsupported archive format $archive" >&2; exit 1 ;;
esac

# The .par server loads its sibling helper binaries from the same directory, so
# everything the archive contains stays together and must stay executable.
find "$INSTALL_DIR" -maxdepth 1 -type f -name 'agy_acp_server*' -exec chmod +x {} +

server="$INSTALL_DIR/agy_acp_server.par"
[ -f "$server" ] || server="$INSTALL_DIR/agy_acp_server.exe"
if [ ! -f "$server" ]; then
  echo "error: no agy_acp_server binary found under $INSTALL_DIR" >&2
  exit 1
fi

echo "==> installed $server"
echo "    pi-go finds it automatically; override with PI_ACP_AGY_CMD if you move it."
echo
echo "Next: the server does not inherit the agy CLI login. Select an auth method in"
echo "  ~/.gemini/antigravity-acp/settings.json, e.g. {\"auth\": {\"type\": \"oauth-personal\"}},"
echo "then run $server once by hand to complete the one-time browser sign-in."
