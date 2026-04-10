# Requirements — nanocoder-tui

## Q&A Record

### Q1: Feature Selection

From the nanocoder patterns I identified, which ones are you most interested in adopting for pi-go? Here's the shortlist ranked by reuse potential:

**High potential:**
1. Theme system (JSON-loaded, runtime switching)
2. Responsive terminal layout (narrow/normal/wide breakpoints)
3. Tool confirmation with formatter previews
4. File autocomplete via @mentions
5. Context window usage percentage display
6. Bash streaming progress with live output + token counting

**Medium potential:**
7. Compact/expanded tool output toggle (Ctrl+O)
8. Development mode indicator (normal/plan/dev)
9. Prompt history with full state preservation (not just text)

Which of these do you want in scope? (numbers, "all", or describe your priorities)

**A1:** All 9 features are in scope.

---

### Q2: Scope — New Project or Enhance pi-go?

Is nanocoder-tui intended as:

- **A)** Enhancements to the existing pi-go TUI (add these features into `internal/tui/tui.go` and related files)
- **B)** A separate standalone TUI project (new repo/module, potentially reusable beyond pi-go)
- **C)** Something else?

**A2:** A — Enhancements to the existing pi-go TUI.

---

### Q3: Current TUI Gaps

pi-go's TUI currently uses Bubble Tea v2 with lipgloss styling, glamour for markdown, and chroma for syntax highlighting. It has a monolithic `tui.go` with the Model struct handling everything.

Before I design, should we also **refactor the TUI into smaller components** (e.g., separate input, chat history, status bar, tool display modules) as part of this work? Or keep the current single-file structure and bolt features on top?

- **A)** Refactor into components first, then add features (cleaner, but bigger scope)
- **B)** Add features to existing structure, refactor later if needed (faster delivery)
- **C)** Minimal refactor — extract only what's needed for the new features

**A3:** A — Full refactor into components first, then add features.

---

### Q4: Theme System Design

Nanocoder uses 39 themes loaded from a JSON file with these color roles: `primary`, `secondary`, `success`, `error`, `warning`, `info`, `tool`, `text`, `base`.

For pi-go's theme system:

- **A)** Match nanocoder's color roles and ship the same 39 themes (max compatibility — could even share the JSON)
- **B)** Define our own color roles tailored to pi-go's UI needs (e.g., add `thinking`, `subagent`, `diff-add`, `diff-remove`) and create a smaller curated set of themes
- **C)** Start with B but make the format compatible so nanocoder themes can be imported

**A4:** A — Match nanocoder's color roles and ship the same 39 themes.

---

### Q5: File @Mentions Behavior

Nanocoder's @mentions inject the full file content into the prompt as a hidden placeholder (user sees `[@filename]`, LLM gets the file content).

For pi-go, how should @mentions work?

- **A)** Same as nanocoder — inject full file content into the prompt context
- **B)** Inject file content but with token-aware truncation (respect context budget)
- **C)** Just attach the file path as a reference and let the agent decide whether to read it

**A5:** C — File path reference only, agent decides whether to read.

---

### Q6: Tool Confirmation UX

Nanocoder shows a formatted preview of what a tool will do (e.g., diff preview for file edits, command for bash) and asks Yes/No via a select input.

pi-go currently uses permission-based auto-approval. Should the tool confirmation feature:

- **A)** Replace the current permission system — all tools go through visual confirmation
- **B)** Add visual confirmation as an additional mode (e.g., a "cautious" mode toggled by user preference), keeping auto-approval as default
- **C)** Only add formatter previews for display — keep the existing approval flow unchanged

**A6:** C — Add formatter previews for tool display only, keep existing approval flow.

---

### Q7: Context Window Display

Nanocoder tracks token usage per message and shows a percentage in the status area, with a warning at 60%.

For pi-go, where should the context window percentage be displayed?

- **A)** In the status bar / input prompt area (always visible)
- **B)** In a header/footer bar similar to vim status lines
- **C)** Both — brief indicator in the input area, detailed breakdown available via `/status` command

**A7:** A — Always visible in the status bar / input prompt area.

---

### Q8: Development Modes

Nanocoder has three modes (normal/plan/development) that affect tool auto-approval behavior. pi-go already has a plan flow (`/plan`).

Should the development mode indicator:

- **A)** Map to pi-go's existing modes (normal chat vs plan mode) and just add the visual indicator
- **B)** Introduce a new "development mode" concept that also changes agent behavior (e.g., more aggressive tool use, auto-commit)
- **C)** Skip the mode concept entirely — just add the visual indicator showing the current state (idle/thinking/executing tools/subagent running)

**A8:** A — Map to existing pi-go modes and add visual indicator.

---

### Q9: Prompt History

Nanocoder preserves full InputState (display value + placeholder content for @mentions) in history, not just the raw text string.

pi-go currently has basic prompt history. Should the enhanced history:

- **A)** Preserve full state including @mentions/file references — up/down arrows restore the exact input as it was
- **B)** Keep text-only history but add persistent storage across sessions (like shell history)
- **C)** Both — full state preservation + cross-session persistence

**A9:** A — Full state preservation including @mentions/file references.

---

### Q10: Responsive Layout Breakpoints

Nanocoder uses three breakpoints (narrow/normal/wide) that change box styles, label lengths, and layout.

pi-go's Bubble Tea TUI gets terminal size via `tea.WindowSizeMsg`. What should change across breakpoints?

- **A)** Minimal — just truncate paths and shrink status info on narrow terminals
- **B)** Full adaptive — different layouts per breakpoint (e.g., narrow hides tool previews, stacks elements vertically, uses abbreviated labels)
- **C)** Same as B, but also support a "compact" mode the user can force regardless of terminal width

**A10:** A — Minimal responsive: truncate paths and shrink status on narrow terminals.
