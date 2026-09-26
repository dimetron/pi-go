# pi-go VS Code extension

This folder contains the VS Code integration between VS Code and pi-go using the
[Agent Client Protocol](https://agentclientprotocol.com/). The extension starts
`pi acp-server`, drives it over stdio, and surfaces pi-go as a **native agent in
VS Code's Chat/Agent Sessions UI** via the `chatSessionsProvider` proposed API
(VS Code 1.137).

## Native agent sessions

`pi-go` appears:

- in the Chat view's session list (Agent Sessions) with persisted pi-go
  transcripts (via ACP `session/list`),
- in the new-session picker as a dedicated **pi-go** session type, and
- as a `@pi-go` chat participant for quick prompts.

Prompts typed into a pi-go session editor are routed through the session
`requestHandler` to ACP `session/prompt`:

- streamed `agent_message_chunk` updates render as markdown,
- `tool_call` / `tool_call_update` render as live collapsible tool cards that
  resolve in place (with input/output sections, diff snippets, and error
  styling for failed calls), and reappear in replayed history,
- `agent_thought_chunk` renders as a thinking part, dimmed in history,
- `@file` mentions become ACP embedded resources (text files only — 256 KiB
  per file, 10 files, 1 MiB per prompt; larger or binary files are skipped
  with a warning banner),
- cancellation maps to ACP `session/cancel`.

A stub language model (`pi-go` vendor, `pi-go agent` model) is registered so
the sessions editor can resolve `request.model` without a Copilot sign-in.
Prompts never generate through it — the pi-go agent answers.

## Slash commands

pi-go dispatches slash commands only in its TUI today, so in VS Code sessions:

- `/clear` starts a fresh pi-go session bound to the same editor (the old
  transcript stays on disk and remains openable from the sessions list),
- `/help` lists the commands pi-go advertised (`available_commands_update`),
- any other `/command` is forwarded to the model as plain text, with an info
  notice. `/`-autocomplete is wired via `participantVariableProvider` where
  the sessions editor consults it.

Follow-up (pi-go side): dispatch slash commands in the ACP prompt handler; the
advertisement already exists.

## Dedicated Pi-Go chat

In addition to the native agent sessions above, the extension contributes a
**pi-go** view container for session history and one **Pi-Go** chat in the
secondary sidebar — the focused chat pattern Claude Code and Codex use:

- **Chat** — a webview chat: streamed replies, collapsible thinking blocks,
  live tool cards with real line diffs for file edits, fenced ```mermaid
  fences rendered as diagrams (the library loads on demand from cdnjs;
  offline, the source code block stays), a slash-command popup fed by
  `available_commands_update`, `@` file attachments (same size caps as
  the native path), a stop button (ACP `session/cancel`), and a badge with the
  number of running prompts.
- **Sessions** — a tree of persisted pi-go sessions (`session/list`), newest
  first. A single click loads the session and replays its transcript into the
  chat view; the running-prompt count shows as the view badge.

The chat works without `--enable-proposed-api` and keeps its composer draft and
transcript when the sidebar is hidden. The first prompt creates a session, so
there is no separate start screen or duplicate chat surface.

The **Get Started with Pi-Go** walkthrough is available from the Welcome page,
the Command Palette as `pi-go: Open Getting Started`, or the extension's
walkthrough entry. It covers configuration, chat, sessions, and attachments.

Keybinding: **cmd+alt+u** (ctrl+alt+u elsewhere) focuses the chat view.

The chat follows whatever color theme is active and layers the
[Pi-Go Design System](https://claude.ai/design/p/7009407c-e035-4157-bc52-4d4544f87be9)
on top: neon cyan / magenta accents, sharp 2px/4px corners, gradient
dividers, and one emoji badge per tool call (🔍 search, 📖 read, ✏️ edit,
⚡ shell…). Light themes get deeper inks; high-contrast themes get the theme's
own colors only.

## Color themes

Two workbench themes in the Pi-Go palette — pick one with
**Preferences: Color Theme**:

| Theme | Type | Look |
|---|---|---|
| **Pi-Go Neon** | dark | `#0a0a12` deep space, cyan `#00f0ff` accent, magenta selection, neon syntax |
| **Pi-Go Daylight** | light | `#f7f8fc` page, the same hues as deeper inks that clear WCAG AA |

Both are compiled with [Catppuccin for VS Code](https://github.com/catppuccin/vscode)
(MIT, © 2021 Catppuccin): its generator derives ~565 workbench colors, the
TextMate rules and the semantic-token rules from a 26-color palette, and
`scripts/themes.mjs` feeds it the Pi-Go palettes plus a few brand signatures
(cyan cursor and tab borders, magenta selection, Pi-Go terminal colors). The
generated JSON in `themes/` is committed; after changing a palette run:

```bash
bun run themes
```

`test/themes.test.ts` fails when the committed JSON drifts from the generator,
and checks contrast: editor text ≥ 7:1, keywords / functions / strings /
numbers ≥ 4.5:1, comments ≥ 3:1.

## Launching with the proposed API

`chatSessionsProvider` is a proposed API. On stable VS Code you must grant it:

```sh
# CLI
code --enable-proposed-api pi-go.pi-go-vscode [folder]

# macOS app binary
/Applications/Visual\ Studio\ Code.app/Contents/MacOS/Code \
  --enable-proposed-api pi-go.pi-go-vscode ~/p6s/pi-dev/pi-go
```

Without the flag the extension still activates and the dedicated Pi-Go chat
view remains available, but native agent sessions are disabled and a warning is
logged in the **pi-go** output channel.

## Build & install

```sh
cd vscode
make install      # bun compile → vsce package → code --install-extension
```

The local VSIX is written to `~/.vscode-ext/pi-go-vscode.vsix` (override with
`OUTDIR=…`), so branches and worktrees never hold build artifacts. The `install`
target detects the VS Code CLI: PATH `code` first, falling back to the binary
inside the Insiders or stable app bundle (paths with spaces are quoted).

or step by step: `bun install`, `bun run compile`, then
`npx @vscode/vsce package --no-dependencies -o ~/.vscode-ext/pi-go-vscode.vsix`
and `code --install-extension ~/.vscode-ext/pi-go-vscode.vsix --force`.

## Settings

```json
{
  "pi-go.command": "/absolute/path/to/pi",   // or the pi-acp-mock binary
  "pi-go.args": ["acp-server"]
}
```

## Architecture

- `src/acp.ts` — ACP stdio client: spawn, initialize (capability cache),
  `session/new`, `session/list`, `session/load`, `session/prompt`,
  `session/cancel`, respawn-once retry, spawn-failure event; fans
  `session/update` notifications out to listeners and captures
  `available_commands_update` per session.
- `src/extension.ts` — native session plumbing: item provider (sessions list),
  content provider (transcript + `requestHandler` bridge, slash-command
  interception, autocomplete), chat participant, language-model stub, and the
  fallback quick-prompt command.
- `src/transcript.ts` — turn model (user prompt / agent parts: text, thought,
  tool call) and the ACP→store writers shared by live prompts and replays.
- `src/chatPanel.ts` — host bridge for the dedicated chat tab: webview HTML
  (CSP, nonce), host↔webview message routing, prompt runs with cancellation,
  session replay, attachments, badges.
- `src/sessionsTree.ts` — sessions tree provider for the activity-bar
  container, with the running-prompt badge.
- `src/webview/` — the webview bundle (vanilla TS, DOM only): controller,
  composer (slash popup + attachment chips), markdown (marked + DOMPurify),
  tool cards with LCS line diffs, entry point.
- `src/shared/` — the host↔webview message protocol and the line-diff helper;
  imported by both esbuild targets and never by "vscode".
- `src/chatParts.ts` — ACP update → proposed-API chat part mappers (tool cards,
  thinking parts, notices) shared by the live stream and history.
- `src/mentions.ts` — @file references → ACP embedded resource blocks.
- `src/types/chatApi.ts` — structural DTOs for the proposed chat APIs and the
  runtime constructor registry (`chatParts()`).

The `cmd/pi-acp-mock` binary in the repo root is a stand-in agent for testing
without a model: `PI_MOCK_TOOLS=1 PI_MOCK_THOUGHTS=1 PI_MOCK_COMMANDS=1
PI_MOCK_ECHO_RESOURCE=1 PI_MOCK_RESPONSE="Hello {{prompt}}" ./pi-acp-mock`.
Point `pi-go.command` at it to drive the extension end-to-end.

## Known limits

- The transcript store is in-memory per window; the persisted-session list
  comes from pi-go's own on-disk store via `session/list`, so reopening a
  session replays through `session/load`.
- Sub-agent nested tool cards are flattened into their parent's output text.
- `registerChatSessionItemProvider` is deprecated upstream in favor of
  `createChatSessionItemController`; migrating is a follow-up.
- The extension does not expose VS Code filesystem/terminal callbacks to the
  ACP agent (pi-go runs its own tools, and never sends
  `session/request_permission` — its permission policy auto-approves).
