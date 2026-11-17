---
name: rex-hunk-diff
description: Open a live Hunk diff of the current working tree in a pane beside the agent, driven by the rex CLI, so the user can review changes while you keep working. Use when asked to "show the diff", "open hunk", "put the diff on the left/right", "show me the changes beside you", or to watch a diff update while editing. Encodes the pane-placement sequence and the traps that make the first attempt land in the wrong place.
---

# Live diff beside the agent

Two CLIs, one job: **rex** splits the terminal and owns the layout, **hunk** draws
the diff inside a block. Neither calls the other.

The failure mode this skill exists to prevent: splitting with the default
direction and then discovering the diff landed on the side you did not want, or
re-running the whole sequence and ending up with duplicate panes. Placement is
two commands — split, then move — and `move` is what makes the direction
reliable.

## 1. Confirm both tools and the location

```bash
rex whoami --json | jq -r '.rex_version'    # also proves the server is up
echo "REX_SESSION=$REX_SESSION REX_BLOCK=$REX_BLOCK"
hunk --version
```

`REX_SESSION` and `REX_BLOCK` are set by rex in the terminal it opened. They
identify **your** session and pane — pass them as `-s` and `-b`. If
`REX_SESSION` is empty, run `rex whoami --json` then `rex ls --json` and ask the
user which session to use; do not guess between sessions.

## 2. Know what hunk will be looking at

Run from the tree under review — a worktree, not the primary checkout:

```bash
cd <worktree>
hunk status --json | jq -r '.head.name, .paths[] | .path'   # sanity: right branch
git --no-pager diff --stat
```

A `hunk status` that shows no `paths` means the diff is empty and the pane will
open on nothing. Say so instead of opening it.

## 3. Split, then move into place

Open the pane, then place it. Do **not** try to get the direction right in the
first command:

```bash
# 1. Open hunk in a new pane beside $REX_BLOCK (block id captured for later).
created=$(rex split -s "$REX_SESSION" -b "$REX_BLOCK" --ratio 50 \
  --label "hunk diff" -c "$PWD" --keep-open --focus=false --json -- hunk diff) || exit
hunk_block=$(printf '%s' "$created" | jq -er '.block_ids[0]') || exit

# 2. Place it. DIRECTION is relative to the anchor: right|left|above|below.
rex move -s "$REX_SESSION" "$hunk_block" right "$REX_BLOCK" --ratio 50
```

Why the second command earns its place: `rex split --split=left` is relative to
the *anchor block's* position in its window, so the result depends on where that
block already sits, and a re-split can nest the new pane inside the old split
rather than beside it. `rex move BLOCK DIRECTION ANCHOR` states the final
relationship directly, which is the thing the user actually asked for.

**"Show the diff on the left" = `rex move "$hunk_block" left "$REX_BLOCK"`.**
The agent's own pane is the anchor; the diff goes beside it, not on top of it.

Flags that matter in the split:

| Flag | Why |
|---|---|
| `--keep-open` | Without it the pane closes the moment hunk exits, and the user sees a flash. |
| `--focus=false` | Keeps the user typing where they were. New blocks take focus by default, which steals their cursor. |
| `-c "$PWD"` | The pane inherits the cwd you name, not the shell's. Without it hunk may open in the primary checkout. |
| `--label` | Names the block so later `capture`/`kill`/`move` can address it by something readable. |
| `--json` | The block id you need for `move`. |

## 4. Verify it landed before reporting

```bash
rex capture -s "$REX_SESSION" -b "$hunk_block" --trim | head -20
hunk session list                     # the live session, its path and focus
rex window ls -s "$REX_SESSION" --json | jq -r '.windows[].focused_block_id'
```

`capture` is the only proof: a split that returned JSON has still not told you
what is on the screen. If the capture is empty or shows hunk's "no changes",
the pane opened on the wrong directory — check `-c` before re-running.

To read which file and hunk the user is looking at:

```bash
hunk session review --repo <worktree> --json | jq -r '.review.selectedFile.path'
hunk session context --repo <worktree>
```

## 5. A pane needs room for the sidebar

pi's right sidebar appears only when a terminal is wider than 80 columns. A
pane split 50/50 from a normal window often lands at ~80 and pi silently renders
without it, which looks like a layout bug in pi rather than a narrow pane.

Force the size on pi's block when the pane is too narrow:

```bash
rex api call -s "$REX_SESSION" com.superlogical.terminal.resize \
  "{\"block_id\":\"$pi_block\",\"args\":{\"columns\":160,\"rows\":40}}"
```

`resize` takes `{"block_id":..., "args":{"columns":N,"rows":N}}` as a **single
JSON argument** — passing flags (`block_id=... args=...`) fails with
`accepts between 1 and 2 arg(s), received 3`.

## Traps

| Symptom | Cause | Fix |
|---|---|---|
| Diff pane on the wrong side | `--split` is relative to the anchor's position | Place with `rex move BLOCK right\|left ANCHOR` instead |
| Pane flashes and vanishes | Missing `--keep-open` | Re-run with `--keep-open` |
| User's cursor jumps to the new pane | New blocks take focus | `--focus=false` |
| Duplicate diff panes | Retried a split that had already succeeded | `rex block ls -s "$REX_SESSION" --json` first; the command's result is checked before retrying |
| Hunk shows another checkout's diff | `-c "$PWD"` missing, or cwd is the primary checkout | Run from the worktree; pass `-c "$PWD"` |
| `rex api call ... block_id=... args=...` → `accepts between 1 and 2 arg(s)` | Args must be one JSON object | Pass a single quoted JSON object |
| pi has no sidebar in the pane | Pane is ≤80 columns | Resize the block, or split with a smaller `--ratio` for the diff pane |
| `hunk diff` blocks the shell forever | It is an interactive TUI, by design | Always launch it inside a rex pane, never through `bash` in the agent's own terminal |

## Do not

- **Do not run `hunk diff` in your own shell.** It is a full-screen TUI: it will
  take over the agent's terminal and hang the tool call. It belongs in a rex
  block.
- **Do not kill panes you did not create.** The user may have their editor or a
  server in the same window; remove only the block id you got back.
- **Do not reload the pane's diff by hand after every edit.** hunk watches its
  input — `--watch` on launch, or `hunk session reload` for a live session:
  ```bash
  hunk session reload --repo "$PWD" -- hunk diff
  ```

## Checklist

- [ ] `rex whoami --json` OK and `REX_SESSION` / `REX_BLOCK` set
- [ ] Working from the worktree under review, not the primary checkout
- [ ] `hunk status --json` shows the expected branch and non-empty paths
- [ ] Split with `--keep-open --focus=false -c "$PWD" --json`, block id captured
- [ ] Placed with `rex move` in the direction the user asked for
- [ ] `rex capture` confirms hunk is actually drawing the diff there
- [ ] Pane is wider than 80 columns if pi's sidebar is expected in it
