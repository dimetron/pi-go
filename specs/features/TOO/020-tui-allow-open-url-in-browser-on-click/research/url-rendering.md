# Research: URL rendering pipeline (chat.go)

## Regexes (chat.go:462-463)

```go
var markdownLinkRe = regexp.MustCompile(`\[([^\]]+)\]\((file://[^)]+|http://[^)]+|https://[^)]+)\)`)
var httpURLRe      = regexp.MustCompile(`https?://[^\s<>()[\]{}"']+`)
```

## Functions

- `hyperlinkURLs(text string) string` (chat.go:473-485): wraps `https?://` matches in OSC 8; trims trailing punctuation, validates via `url.Parse`. Constants chat.go:466-467.
- `hyperlinkRenderedURLs(text string) string` (chat.go:490-496): per-line → `hyperlinkRenderedLine` (chat.go:498-529): strips SGR with `ansi.Strip`, finds URLs in plain text, converts byte offsets to display columns (`lipgloss.Width`, `ansi.Cut`), splices OSC 8 around visible span preserving style bytes.
- Column ranges (`start`, `end`, `rawEnd`) computed then **discarded** after rendering.

## Render pipeline

`RenderMarkdown` (chat.go:557-571): `expandLinks` (flattens `[text](url)` to `"text (url)"`, chat.go:534-552) → glamour → `hyperlinkRenderedURLs`.

Called from `assistantBody` (:808) → `renderAssistantBlock` → `renderMessageBlock` → `renderMessages(running bool) (string, []blockKind)` (chat.go:629).

## Output structure — key constraint

Rendered output is a single Go `string` with embedded ANSI/OSC 8 escapes. Per-message cache: `message.renderCache string` (+key/bool, chat.go:144-146). View calls `clipMessagesToViewport(messagesView, availableHeight, m.chatModel.Scroll)` returning `(visibleMessages, startLine, endLine)` (tui.go:1483).

**No position→URL mapping exists.** The only retained artifacts are: the full rendered string, the viewport start/end line offsets, and `lastFrame` (composed frame saved at tui.go:1597). Any click→URL lookup must be derived from these (e.g., parse OSC 8 sequences out of the target frame line/column) or built fresh during render.

Tests: `terminal_hyperlink_test.go`, `chat_test.go`.
