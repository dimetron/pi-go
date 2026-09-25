// Tool-call cards for the chat webview: collapsible <details> keyed by
// toolCallId, with live status transitions and real line diffs for file edits.

import type { ToolSnapshot } from "../shared/protocol";
import { lineDiff, type DiffLine } from "../shared/diff";
import { icon } from "./icons";
import { copyButton } from "./clipboard";

export interface ToolCard {
  root: HTMLDetailsElement;
  update(tool: ToolSnapshot): void;
}

const STATUS_META: Record<ToolSnapshot["status"], { icon: Parameters<typeof icon>[0]; className: string; label: string }> = {
  pending: { icon: "clock", className: "pending", label: "queued" },
  in_progress: { icon: "loader", className: "in-progress", label: "running" },
  completed: { icon: "check", className: "completed", label: "done" },
  failed: { icon: "alert", className: "failed", label: "failed" },
};

/** Per-tool emoji badge, matched on the lowercased tool name. Order matters:
 *  the first rule that matches wins, so specific names (read_image, git-*)
 *  come before the generic ones they contain (read, diff). */
const TOOL_EMOJI: ReadonlyArray<readonly [RegExp, string]> = [
  [/^(ripgrep|grep|rg)$|search|find_in|glob/, "🔍"],
  [/image|screenshot/, "🖼️"],
  [/^git|commit|branch/, "🌿"],
  [/^read|view|cat$/, "📖"],
  [/^edit|replace|patch|apply/, "✏️"],
  [/^write|create_file|save/, "📝"],
  [/^(bash|shell|sh|exec|run|terminal)/, "⚡"],
  [/lsp|diagnos|symbol|hover|definition|reference/, "🧩"],
  [/^(ls|tree|find|list_dir|dir)($|[_-])/, "📂"],
  [/fetch|http|web|url|browse/, "🌐"],
  [/agent|task|delegate/, "🤖"],
  [/memory|remember|recall/, "🧠"],
  [/todo|plan|goal/, "🎯"],
  [/test/, "🧪"],
  [/^mcp/, "🔌"],
  [/stat|metric|usage|cost/, "📊"],
];

export function toolEmoji(toolName: string): string {
  const name = toolName.trim().toLowerCase();
  for (const [pattern, emoji] of TOOL_EMOJI) {
    if (pattern.test(name)) return emoji;
  }
  return "🔧";
}

/** Tool details are user-controlled; status updates must never open raw output. */
export const TOOL_CARD_DEFAULT_OPEN = false;

export function shouldAutoExpandTool(_status: ToolSnapshot["status"]): boolean {
  return false;
}

/** The one deliberate exception to `shouldAutoExpandTool`: the moment a call
 *  first fails, its card opens so the error is visible without a click. It
 *  fires once per card (on the pending/running → failed edge) and never
 *  fights a manual close afterward — see the `userToggled` guard below. */
export function isFreshFailure(status: ToolSnapshot["status"], previousStatus: ToolSnapshot["status"] | undefined): boolean {
  return status === "failed" && previousStatus !== "failed";
}

/** Lines beyond this are hidden behind a "Show more" control. */
const MAX_PREVIEW_LINES = 40;

/** Format a duration for a tool-card status line: "480ms", "2.3s", "1m 04s". */
export function formatDuration(ms: number): string {
  const clamped = Math.max(0, ms);
  if (clamped < 1000) return `${Math.round(clamped)}ms`;
  const seconds = clamped / 1000;
  if (seconds < 60) return `${seconds < 10 ? seconds.toFixed(1) : Math.round(seconds)}s`;
  const minutes = Math.floor(seconds / 60);
  const remainder = Math.round(seconds % 60);
  return `${minutes}m ${remainder.toString().padStart(2, "0")}s`;
}

/** Compose the compact status text: bare label while queued, "label · Ns"
 *  once timing is known (running has an elapsed duration; done/failed a
 *  final one). */
export function toolStatusText(status: ToolSnapshot["status"], label: string, elapsedMs: number | undefined): string {
  if (elapsedMs === undefined) return label;
  // A live "running · 0ms" reads as stuck; show the clock only once it moves.
  if (status === "in_progress" && elapsedMs < 1000) return label;
  return `${label} · ${formatDuration(elapsedMs)}`;
}

/** How often a running card refreshes its elapsed time. */
const RUNNING_TICK_MS = 1000;

interface CapResult {
  visible: string;
  hiddenLines: number;
}

/** Cap text to its first `max` lines; the rest stays behind "Show more". */
export function capLines(text: string, max = MAX_PREVIEW_LINES): CapResult {
  const lines = text.split("\n");
  if (lines.length <= max) return { visible: text, hiddenLines: 0 };
  return { visible: lines.slice(0, max).join("\n"), hiddenLines: lines.length - max };
}

/** Cap a diff's line list the same way, keeping the true count for the label. */
export function capDiffLines(lines: readonly DiffLine[], max = MAX_PREVIEW_LINES): { visible: DiffLine[]; hiddenLines: number } {
  if (lines.length <= max) return { visible: lines.slice(), hiddenLines: 0 };
  return { visible: lines.slice(0, max), hiddenLines: lines.length - max };
}

/** Create (or refresh) the card for one tool call. */
export function toolCard(tool: ToolSnapshot, onRevealFile: (path: string) => void): ToolCard {
  const root = document.createElement("details");
  root.className = "tool-card";
  root.open = TOOL_CARD_DEFAULT_OPEN;
  const body = document.createElement("div");
  body.className = "tool-body";

  const summary = document.createElement("summary");
  const glyph = document.createElement("span");
  glyph.className = "tool-glyph";
  const badge = document.createElement("span");
  badge.className = "tool-emoji";
  badge.setAttribute("aria-hidden", "true");
  const name = document.createElement("span");
  name.className = "tool-name";
  const title = document.createElement("span");
  title.className = "tool-title";
  const status = document.createElement("span");
  status.className = "tool-status";
  summary.append(glyph, badge, name, title, status);
  root.append(summary, body);

  // Native <details> only unmounts the body on close, so an opening
  // transition is what's animatable: measure the target height and let CSS
  // ease into it, then drop the inline styles once the transition settles.
  root.addEventListener("toggle", () => {
    if (!root.open) return;
    const target = body.scrollHeight;
    body.style.maxHeight = "0px";
    body.style.overflow = "hidden";
    void body.offsetHeight; // force reflow so the 0px start is registered
    body.style.transition = "max-height var(--pg-dur) var(--pg-ease)";
    body.style.maxHeight = `${target}px`;
    const onEnd = (ev: TransitionEvent) => {
      if (ev.propertyName !== "max-height" || ev.target !== body) return;
      body.style.maxHeight = "";
      body.style.overflow = "";
      body.style.transition = "";
      body.removeEventListener("transitionend", onEnd);
    };
    body.addEventListener("transitionend", onEnd);
  });

  let previousStatus: ToolSnapshot["status"] | undefined;
  let startedAt: number | undefined;
  let finishedAt: number | undefined;
  let userToggled = false;
  root.addEventListener("toggle", () => {
    userToggled = true;
  });

  // Status updates only arrive on transitions, so a running card ticks its
  // own clock. The timer stops when the tool settles or the card leaves the
  // DOM (session switch), so a dropped card never leaks an interval.
  let ticker: ReturnType<typeof setInterval> | undefined;
  const stopTicker = () => {
    if (ticker !== undefined) clearInterval(ticker);
    ticker = undefined;
  };
  const startTicker = () => {
    if (ticker !== undefined) return;
    ticker = setInterval(() => {
      if (!root.isConnected || startedAt === undefined) {
        stopTicker();
        return;
      }
      status.textContent = toolStatusText("in_progress", STATUS_META.in_progress.label, Date.now() - startedAt);
    }, RUNNING_TICK_MS);
  };

  const card: ToolCard = {
    root,
    update(next: ToolSnapshot) {
      const meta = STATUS_META[next.status];
      const now = Date.now();
      if (next.status === "in_progress" && startedAt === undefined) startedAt = now;
      if ((next.status === "completed" || next.status === "failed") && finishedAt === undefined) {
        finishedAt = now;
      }
      const elapsedMs =
        finishedAt !== undefined && startedAt !== undefined
          ? finishedAt - startedAt
          : startedAt !== undefined && next.status === "in_progress"
            ? now - startedAt
            : undefined;

      root.classList.remove("status-pending", "status-in-progress", "status-completed", "status-failed");
      root.classList.add(`status-${meta.className}`);
      glyph.replaceChildren(icon(meta.icon));
      glyph.title = meta.label;
      badge.textContent = toolEmoji(next.toolName);
      name.textContent = next.toolName;
      const titleText = next.title && next.title !== next.toolName ? next.title : "";
      title.textContent = titleText;
      title.title = titleText;
      status.textContent = toolStatusText(next.status, meta.label, elapsedMs);
      if (next.status === "in_progress") startTicker();
      else stopTicker();

      // A fresh failure opens the card once so the error is on screen
      // without a click; a manual close afterward is respected.
      if (!userToggled && isFreshFailure(next.status, previousStatus)) {
        root.open = true;
      }
      previousStatus = next.status;

      body.replaceChildren();
      if (next.inputText) {
        body.append(outputBlock("Input", next.inputText, "input"));
      }
      if (next.diff) {
        body.append(diffTable(next.diff, onRevealFile));
      } else if (next.outputText) {
        body.append(outputBlock("Output", next.outputText, "output"));
      }
    },
  };
  card.update(tool);
  return card;
}

/** A labeled, copyable, line-capped block for raw tool input/output. */
function outputBlock(label: string, text: string, variant: "input" | "output"): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = `tool-block tool-${variant}-block`;

  const header = document.createElement("div");
  header.className = "tool-block-header";
  const eyebrow = document.createElement("span");
  eyebrow.className = "tool-block-label";
  eyebrow.textContent = label;
  header.append(eyebrow, copyButton(`Copy ${label.toLowerCase()}`, () => text));
  wrap.append(header);

  const { visible, hiddenLines } = capLines(text);
  const pre = document.createElement("pre");
  pre.className = variant === "input" ? "tool-input" : "tool-output";
  pre.textContent = visible;
  wrap.append(pre);

  if (hiddenLines > 0) {
    const more = document.createElement("button");
    more.type = "button";
    more.className = "tool-block-more";
    more.textContent = `Show ${hiddenLines} more line${hiddenLines === 1 ? "" : "s"}`;
    more.addEventListener("click", () => {
      pre.textContent = text;
      more.remove();
    });
    wrap.append(more);
  }
  return wrap;
}

function diffTable(
  diff: { path: string; oldText: string; newText: string },
  onRevealFile: (path: string) => void,
): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "tool-diff";

  const header = document.createElement("div");
  header.className = "diff-header";
  const file = document.createElement("button");
  file.className = "diff-path";
  file.textContent = diff.path;
  file.title = "Open file";
  file.addEventListener("click", () => onRevealFile(diff.path));
  header.append(file, copyButton("Copy new file contents", () => diff.newText));
  wrap.append(header);

  const lines = lineDiff(diff.oldText, diff.newText);
  const { visible, hiddenLines } = capDiffLines(lines);

  const table = document.createElement("div");
  table.className = "diff-table";
  renderDiffRows(table, visible);
  wrap.append(table);

  if (hiddenLines > 0) {
    const more = document.createElement("button");
    more.type = "button";
    more.className = "tool-block-more";
    more.textContent = `Show ${hiddenLines} more line${hiddenLines === 1 ? "" : "s"}`;
    more.addEventListener("click", () => {
      renderDiffRows(table, lines);
      more.remove();
    });
    wrap.append(more);
  }
  return wrap;
}

function renderDiffRows(table: HTMLElement, lines: readonly DiffLine[]): void {
  table.replaceChildren();
  for (const line of lines) {
    const row = document.createElement("div");
    row.className = `diff-line ${line.type}`;
    const sign = document.createElement("span");
    sign.className = "diff-sign";
    sign.textContent = line.type === "add" ? "+" : line.type === "del" ? "−" : " ";
    const text = document.createElement("span");
    text.className = "diff-text";
    text.textContent = line.text;
    row.append(sign, text);
    table.append(row);
  }
}
