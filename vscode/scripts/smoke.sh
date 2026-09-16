#!/bin/bash
# Smoke-test the pi-go VS Code extension in a real editor.
#
# Launches VS Code (Insiders preferred, stable fallback) with the extension
# loaded and PI_GO_SMOKE=1, waits for the extension's [smoke] self-check in the
# extension-host log (activation → acp-server up → prompt roundtrip → SMOKE-OK),
# then kills the editor and prints a verdict table.
#
# Modes:
#   mock      (default) dev extension + pi-acp-mock — no LLM, no real pi needed
#   dev       dev extension + real `pi` binary from the repo root
#   installed installed VSIX + real pi (packaged into the isolated profile)
#
# Usage: scripts/smoke.sh [mock|dev|installed] [timeout-seconds]

set -u

# Repo root: the script's location if it lives in the repo (vscode/scripts/),
# else the CWD — lets a copied variant (sed-editors, CI) run from vscode/.
if [ -f "$(dirname "$0")/../package.json" ]; then
  REPO="$(cd "$(dirname "$0")/../.." && pwd)"
else
  REPO="$(pwd -P)"
fi
VSC="$REPO/vscode"
MODE="${1:-mock}"
TIMEOUT="${2:-90}"

# --- editor + CLI resolution ------------------------------------------------
CODE_INSIDERS="/Applications/Visual Studio Code - Insiders.app/Contents/Resources/app/bin/code"
CODE_STABLE="/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code"
if [ -x "$CODE_INSIDERS" ]; then CODE_BIN="$CODE_INSIDERS"
elif [ -x "$CODE_STABLE" ]; then CODE_BIN="$CODE_STABLE"
elif command -v code >/dev/null 2>&1; then CODE_BIN="$(command -v code)"
else echo "FATAL: no VS Code CLI found"; exit 2; fi

# --- agent binary -----------------------------------------------------------
case "$MODE" in
  mock)
    MOCK_BIN="/tmp/pi-acp-mock"
    if [ ! -x "$MOCK_BIN" ]; then
      (cd "$REPO" && go build -o "$MOCK_BIN" ./cmd/pi-acp-mock) || { echo "FATAL: mock build failed"; exit 2; }
    fi
    AGENT_CMD="$MOCK_BIN"
    ;;
  dev|installed)
    AGENT_CMD="$REPO/pi"
    [ -x "$AGENT_CMD" ] || AGENT_CMD="$(command -v pi)" || { echo "FATAL: no pi binary"; exit 2; }
    AGENT_CMD="$(cd "$(dirname "$AGENT_CMD")" && pwd)/$(basename "$AGENT_CMD")"
    ;;
esac

WORKDIR="$(mktemp -d /tmp/pi-go-vscode-smoke.XXXXXX)"
LOGDIR="$WORKDIR/profile/logs"
SMOKEFILE="$WORKDIR/smoke-result.log"
mkdir -p "$WORKDIR/profile/User" "$LOGDIR"
: > "$SMOKEFILE"

# Isolated profile: own user-data dir, own (empty) extensions dir, so the
# machine's installed extensions cannot interfere. In installed mode the VSIX
# is installed into the profile's extensions dir first.
EXDIR="$WORKDIR/extensions"
if [ "$MODE" = "installed" ]; then
  VSIX="$HOME/.vscode-ext/pi-go-vscode.vsix"
  [ -f "$VSIX" ] || VSIX="$VSC/pi-go-vscode-$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' "$VSC/package.json" | head -1).vsix"
  [ -f "$VSIX" ] || { echo "FATAL: no VSIX found (run make package first)"; exit 2; }
  "$CODE_BIN" --install-extension "$VSIX" --force \
    --extensions-user-dir "$EXDIR" >/dev/null 2>&1 || { echo "FATAL: VSIX install failed"; exit 2; }
fi

# Point the extension at the agent under test.
cat > "$WORKDIR/profile/User/settings.json" <<EOF
{ "pi-go.command": "$AGENT_CMD", "pi-go.args": ["acp-server"] }
EOF

EDITOR_PID=""
kill_editor() {
  [ -n "$EDITOR_PID" ] && kill "$EDITOR_PID" 2>/dev/null
  sleep 0.5
  pkill -f "pi-go-vscode-smoke" 2>/dev/null
  true
}
trap 'kill_editor' EXIT

echo "== smoke: mode=$MODE agent=$AGENT_CMD"
echo "== workdir=$WORKDIR"

# --- launch -----------------------------------------------------------------
# Isolation comes from the empty --extensions-user-dir (machine extensions are
# simply not present); do NOT pass --disable-extensions — it suppresses dev
# extensions' activation in current Insiders builds. --enable-proposed-api
# exercises the native-sessions path; the guarded proposed-API setup degrades
# to the chat view when the gate rejects it.
FLAGS=(
  --user-data-dir "$WORKDIR/profile"
  --extensions-user-dir "$EXDIR"
  --disable-workspace-trust
  --disable-gpu
  --skip-release-notes
  --skip-welcome
  --disable-updates
  --enable-proposed-api pi-go.pi-go-vscode
)
if [ "$MODE" != "installed" ]; then
  FLAGS+=(--extensionDevelopmentPath="$VSC")
fi

PI_GO_SMOKE=1 PI_GO_SMOKE_FILE="$SMOKEFILE" "$CODE_BIN" "${FLAGS[@]}" "$WORKDIR" >/dev/null 2>&1 &
EDITOR_PID=$!

# --- wait for the verdict ---------------------------------------------------
deadline=$(( $(date +%s) + TIMEOUT ))
verdict=""
while [ "$(date +%s)" -lt "$deadline" ]; do
  if grep -q "SMOKE-OK" "$SMOKEFILE" 2>/dev/null; then verdict=ok; break; fi
  if grep -q "SMOKE-FAIL" "$SMOKEFILE" 2>/dev/null; then verdict=fail; break; fi
  sleep 1
done

# --- report -----------------------------------------------------------------
EXTHOST_LOG="$(find "$LOGDIR" -name exthost.log 2>/dev/null | head -1)"
echo
if [ "$verdict" = "ok" ]; then
  echo "┌──────────────────────────────────────────┬──────────┐"
  echo "│                  Check                   │   Now    │"
  echo "├──────────────────────────────────────────┼──────────┤"
  echo "│ extension activated                      │ ✅ pass  │"
  echo "├──────────────────────────────────────────┼──────────┤"
  echo "│ acp-server up ($MODE agent)              │ ✅ pass  │"
  echo "├──────────────────────────────────────────┼──────────┤"
  echo "│ prompt roundtrip                         │ ✅ pass  │"
  echo "└──────────────────────────────────────────┴──────────┘"
  sed 's/^\[smoke\] /  /' "$SMOKEFILE" 2>/dev/null | tail -5
  echo "SMOKE PASS ($MODE)"
  exit 0
else
  echo "┌──────────────────────────────────────────┬──────────┐"
  echo "│                  Check                   │   Now    │"
  echo "├──────────────────────────────────────────┼──────────┤"
  echo "│ smoke verdict                            │ ❌ fail  │"
  echo "└──────────────────────────────────────────┴──────────┘"
  if [ -s "$SMOKEFILE" ]; then
    echo "== smoke self-check =="
    sed 's/^\[smoke\] /  /' "$SMOKEFILE"
  fi
  [ -n "$EXTHOST_LOG" ] && { echo "== exthost.log (pi-go/error lines) =="; grep -i "pi-go\|error" "$EXTHOST_LOG" | grep -v Deprecation | tail -15; }
  [ -z "$EXTHOST_LOG" ] && echo "no exthost.log found — editor may have failed to start (check $LOGDIR)"
  echo "SMOKE FAIL ($MODE)"
  exit 1
fi