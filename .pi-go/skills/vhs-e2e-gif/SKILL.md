---
name: vhs-e2e-gif
description: Record a test run, a TUI session, or any terminal command as a GIF with VHS and attach it to a GitHub PR as a release-hosted asset, never a repo commit. Use when asked to record an e2e run, demo a fix on a PR, attach a GIF or screen recording to a pull request, show a test passing visually, or produce a terminal recording of pi-go. Encodes the sandbox trap that makes VHS segfault on the first attempt.
---

# Recording an e2e run as a GIF on a PR

Turn a test run into a short GIF and hang it off the PR, so a reviewer sees the
suite go green without checking out the branch.

## The one trap

**VHS segfaults inside the agent sandbox.** It binds a loopback port for `ttyd`,
the sandbox refuses, and `randomPort()` dereferences the nil listener:

```
panic: runtime error: invalid memory address or nil pointer dereference
main.randomPort()
	github.com/charmbracelet/vhs/tty.go:22
```

That is the sandbox, not a broken tape. **Run every `vhs` invocation with the
sandbox disabled.** Same for `gh` (network) — the upload step needs it too.

Dependencies: `vhs`, `ttyd`, `ffmpeg` (`brew install vhs ttyd ffmpeg`). VHS pulls
in the other two, but check before blaming a tape.

## Workflow

**1. Record.**

```bash
OUT=/tmp/e2e-tool-calls.gif DIR=$PWD \
  scripts/record-gif.sh "go test -tags e2e ./internal/agent/ -run TestE2E -v 2>&1 | grep -E '^(--- |ok|FAIL)'"
```

The script writes the tape, runs VHS, and extracts the **final frame** next to
the GIF as `<name>.last.png`.

**2. Read the last frame before posting.** A GIF is opaque to you — the PNG is
not. Open it and confirm the run actually passed. Posting a recording of a
failing suite as evidence of a passing one is the failure mode this step exists
to prevent.

**3. Attach.**

```bash
CAPTIONS='e2e: tool calls after the fix' \
  scripts/attach-gif-to-pr.sh 246 /tmp/e2e-tool-calls.gif
```

Uploads as a release asset — never a repo commit — and posts, or edits, a single
recording comment on the PR, so a re-record updates that comment in place instead
of adding another.

## Choosing what to record

Record the **narrowest command that proves the claim**, filtered to result lines.
A full `go test ./...` scroll is unreadable at 14pt and the interesting line
scrolls off. These read well:

```bash
go test -tags e2e ./internal/agent/ -run TestE2E -v 2>&1 | grep -E '^(--- |ok|FAIL)'
go test -tags e2e ./... 2>&1 | grep -vE '(no test files)$'
make lint
```

Sizing: 1280x720 at font size 14 fits ~30 result lines and lands around 150 KB.
Go wider only if lines wrap.

## Recording an interactive TUI

A TUI is two input steps, not one: launch the program, then type *into* it. That
is what `READY_RE` and `INPUT` are for.

```bash
OUT=/tmp/pi-tui-tools.gif DIR=$PWD \
  READY_RE='tkn:' INPUT='show me your tools' \
  WAIT_RE='took [0-9]+[.][0-9]+s' WAIT_TIMEOUT=240s \
  HEIGHT=800 FRAMERATE=20 PLAYBACK_SPEED=2 SHRINK=1 \
  scripts/record-gif.sh "./pi"
```

Four things that go wrong, in the order they bite:

1. **The tape's shell starts in the tape file's directory, not yours.** `./pi`
   becomes `No such file or directory` and you get a 60 KB GIF of a bash error.
   `DIR` is the fix — the script always emits the `cd`.
2. **Typing before the TUI has painted drops the keystrokes.** `READY_RE` holds
   until the program's own output proves it is up. For pi-go, `tkn:` (the
   statusline token counter) is a good marker; the banner is not, it renders
   before the deferred init finishes.
3. **A loose completion regex fires mid-render.** `WAIT_RE='took '` matched while
   the answer was still streaming and cut the recording off at "thinking…".
   Require the digits — `took [0-9]+[.][0-9]+s` — so only the finished summary
   line can match.
4. **TUI captures are several MB.** A full-screen repaint at 50fps is the
   worst case for GIF deltas. `SHRINK=1` recompresses (12fps, 1000px, 96 colors)
   and cut 2.9 MB to 1.3 MB with no loss of legibility.

The alternate screen itself is a non-issue — VHS captures it correctly.

Exiting is optional: the recording stops when `WAIT_RE` matches, and VHS kills
the shell. `/exit` or `/quit` quits pi-go's TUI if you want the exit in frame.

## Where the GIF gets hosted

**Never commit a recording to the repo.** A recording is review scaffolding with
a lifetime of days; a committed binary is in history forever and in every clone.
`MODE=release` (the default) attaches the GIFs to a release tag instead — GitHub
renders a release-asset URL as a real inline `<img data-animated-image>`, no camo
proxy, and git history stays clean.

```bash
CAPTIONS='The TUI, after the fix|e2e: 19 tests green' \
  scripts/attach-gif-to-pr.sh 246 /tmp/pi-tui-tools.gif /tmp/e2e-tool-calls.gif
```

`MODE=branch` still exists for a repo with releases disabled. Do not reach for it
here.

### Immutable releases change the rules

pi-go has immutable releases enabled, which constrains the release path in two
ways the script works around:

1. **Assets attach at creation time only.** `gh release create <tag> a.gif b.gif`
   works; a later `gh release upload` on a published release does not:
   ```
   HTTP 422: Cannot upload assets to an immutable release.
   ```
   So pass *every* GIF for a PR in one invocation. Adding one later means a new
   release.
2. **A deleted tag stays burned.** Re-creating a tag that once held an immutable
   release fails with `tag_name was used by an immutable release`, even after
   `gh release delete` and deleting the git tag. The script probes for a free
   `media-pr<N>`, `media-pr<N>-2`, … rather than assuming.

Neither the release-asset URL nor a raw URL renders for signed-out readers on a
**private** repo; the script checks visibility and refuses rather than posting a
broken image.

## Tape gotchas

- The command and `INPUT` are typed into double-quoted tape strings, so neither
  **may contain a double quote**. Use single quotes; the script rejects the rest
  with a clear error rather than emitting a broken tape.
- `Wait+Screen@<timeout> /regex/` ends the take when the result line appears. A
  regex that never matches burns the whole timeout and then fails the recording,
  so match both outcomes (`ok|FAIL`), never just the happy one.
- `Hide` / `Show` around the `cd` keeps the setup out of the frame.
- VHS resets the shell's cwd afterwards; run it from a scratch directory and pass
  `DIR` rather than relying on where you were.

## Scripts

| Script | Does |
|---|---|
| `scripts/record-gif.sh <command...>` | Tape → GIF → last-frame PNG. Knobs: `OUT`, `DIR`, `READY_RE`, `INPUT`, `WAIT_RE`, `WAIT_TIMEOUT`, `SHRINK`, `SHRINK_WIDTH`, `WIDTH`, `HEIGHT`, `FONT_SIZE`, `THEME`, `PADDING`, `FRAMERATE`, `PLAYBACK_SPEED` |
| `scripts/attach-gif-to-pr.sh <pr> <file.gif> [more.gif ...]` | Publish as release assets, post/edit one PR comment. Knobs: `CAPTIONS` (`\|`-separated), `TAG`, `MODE` (`release`/`branch`), `MEDIA_DIR` |

Both are pi-go-agnostic — they take any command and any PR.
