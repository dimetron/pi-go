#!/usr/bin/env bash
# Record a command's terminal output as a GIF with VHS, and extract the final
# frame as a PNG so the result can actually be read back.
#
# Usage:
#   record-gif.sh <command...>
#
# Environment:
#   OUT           output GIF path            (default: e2e.gif)
#   DIR           directory to run in        (default: $PWD)
#   WAIT_RE       regex that ends the take   (default: (^|[[:space:]])(ok|FAIL|PASS)[[:space:]])
#   READY_RE      regex that must appear before INPUT is typed (optional; for a
#                 TUI, a statusline marker saying it has finished painting)
#   INPUT         text typed INTO the launched program once READY_RE matches,
#                 followed by Enter — how you drive a TUI rather than a one-shot
#                 command
#   WAIT_TIMEOUT  give up after              (default: 900s)
#   SHRINK        1 = recompress the GIF (TUI captures run several MB)
#   SHRINK_WIDTH  width to scale down to when SHRINK=1  (default: 1000)
#   WIDTH HEIGHT FONT_SIZE THEME PADDING FRAMERATE PLAYBACK_SPEED   cosmetics
#
# The command must not contain a double quote: it is typed into the tape inside
# a double-quoted string so the viewer sees the real command. Use single quotes.
set -euo pipefail

if [ $# -eq 0 ]; then
  echo "usage: record-gif.sh <command...>" >&2
  exit 2
fi

CMD="$*"
case "$CMD$INPUT" in
  *'"'*)
    echo "record-gif.sh: the command contains a double quote, which breaks the tape string." >&2
    echo "               rewrite it with single quotes." >&2
    exit 2
    ;;
esac

OUT=${OUT:-e2e.gif}
DIR=${DIR:-$PWD}
WAIT_RE=${WAIT_RE:-(^|[[:space:]])(ok|FAIL|PASS)[[:space:]]}
WAIT_TIMEOUT=${WAIT_TIMEOUT:-900s}
READY_RE=${READY_RE:-}
INPUT=${INPUT:-}
WIDTH=${WIDTH:-1280}
HEIGHT=${HEIGHT:-720}
FONT_SIZE=${FONT_SIZE:-14}
THEME=${THEME:-Catppuccin Macchiato}
PADDING=${PADDING:-24}
FRAMERATE=${FRAMERATE:-24}
PLAYBACK_SPEED=${PLAYBACK_SPEED:-1}
SHRINK=${SHRINK:-0}
SHRINK_WIDTH=${SHRINK_WIDTH:-1000}

for bin in vhs ttyd ffmpeg; do
  command -v "$bin" >/dev/null 2>&1 || { echo "record-gif.sh: $bin not found (brew install $bin)" >&2; exit 1; }
done

OUT_DIR=$(cd "$(dirname "$OUT")" && pwd)
OUT_BASE=$(basename "$OUT")
OUT="$OUT_DIR/$OUT_BASE"
TAPE=$(mktemp -t vhs-tape.XXXXXX).tape
trap 'rm -f "$TAPE"' EXIT

# The tape's shell starts in the tape file's directory, not yours, so the cd is
# not optional — without it a relative command (./pi) is "No such file".
{
  cat <<EOF
Output "$OUT"

Set Shell "bash"
Set FontSize $FONT_SIZE
Set Width $WIDTH
Set Height $HEIGHT
Set Padding $PADDING
Set Theme "$THEME"
Set TypingSpeed 20ms
Set Framerate $FRAMERATE
Set PlaybackSpeed $PLAYBACK_SPEED

Hide
Type "cd $DIR && clear" Enter
Show

Sleep 1s
Type "$CMD"
Sleep 500ms
Enter
EOF
  # A TUI needs to finish painting before it will accept a keystroke, so hold
  # until its own output says it is up, then type the prompt into it.
  if [ -n "$READY_RE" ]; then
    printf 'Wait+Screen@%s /%s/\nSleep 2s\n' "$WAIT_TIMEOUT" "$READY_RE"
  fi
  if [ -n "$INPUT" ]; then
    printf 'Type "%s"\nSleep 1s\nEnter\n' "$INPUT"
  fi
  cat <<EOF
Wait+Screen@$WAIT_TIMEOUT /$WAIT_RE/
Sleep 4s
EOF
} > "$TAPE"

if [ -n "$INPUT" ]; then
  echo "recording: $CMD  <- \"$INPUT\""
else
  echo "recording: $CMD"
fi
vhs "$TAPE"

# The last frame is the only part a reader can check without playing the GIF,
# so always leave one behind.
FRAME="${OUT%.gif}.last.png"
ffmpeg -y -loglevel error -sseof -2 -i "$OUT" -frames:v 1 "$FRAME"

BYTES=$(wc -c < "$OUT" | tr -d ' ')
if [ "$SHRINK" = "1" ]; then
  TMP_GIF="${OUT%.gif}.shrunk.gif"
  ffmpeg -y -loglevel error -i "$OUT" \
    -vf "fps=12,scale=$SHRINK_WIDTH:-1:flags=lanczos,split[s0][s1];[s0]palettegen=max_colors=96[p];[s1][p]paletteuse=dither=bayer:bayer_scale=3" \
    "$TMP_GIF"
  mv "$TMP_GIF" "$OUT"
  ffmpeg -y -loglevel error -sseof -1 -i "$OUT" -frames:v 1 "$FRAME"
  echo "shrunk:     $(numfmt --to=iec "$BYTES" 2>/dev/null || echo "$BYTES B") -> $(du -h "$OUT" | cut -f1)"
elif [ "$BYTES" -gt 1500000 ]; then
  echo "note:       $(du -h "$OUT" | cut -f1) is large for a PR comment — re-run with SHRINK=1"
fi

echo "gif:        $OUT ($(du -h "$OUT" | cut -f1))"
echo "last frame: $FRAME"
