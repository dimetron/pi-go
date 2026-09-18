// Tool-call cards for the chat webview: collapsible <details> keyed by
// toolCallId, with live status transitions and real line diffs for file edits.

import type { ToolSnapshot } from "../shared/protocol";
import { lineDiff } from "../shared/diff";

export interface ToolCard {
  root: HTMLDetailsElement;
  update(tool: ToolSnapshot): void;
}

const STATUS_META: Record<ToolSnapshot["status"], { glyph: string; className: string; label: string }> = {
  pending: { glyph: "◔", className: "pending", label: "queued" },
  in_progress: { glyph: "◐", className: "in-progress", label: "running" },
  completed: { glyph: "✓", className: "completed", label: "done" },
  failed: { glyph: "✕", className: "failed", label: "failed" },
};

/** Tool details are user-controlled; status updates must never open raw output. */
export const TOOL_CARD_DEFAULT_OPEN = false;

export function shouldAutoExpandTool(_status: ToolSnapshot["status"]): boolean {
  return false;
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
  const name = document.createElement("span");
  name.className = "tool-name";
  const title = document.createElement("span");
  title.className = "tool-title";
  const status = document.createElement("span");
  status.className = "tool-status";
  summary.append(glyph, name, title, status);
  root.append(summary, body);

  const card: ToolCard = {
    root,
    update(next: ToolSnapshot) {
      const meta = STATUS_META[next.status];
      root.classList.remove("status-pending", "status-in-progress", "status-completed", "status-failed");
      root.classList.add(`status-${meta.className}`);
      glyph.textContent = meta.glyph;
      glyph.title = meta.label;
      name.textContent = next.toolName;
      title.textContent = next.title && next.title !== next.toolName ? next.title : "";
      status.textContent = meta.label;

      body.replaceChildren();
      if (next.inputText) {
        const input = document.createElement("pre");
        input.className = "tool-input";
        input.textContent = next.inputText;
        body.append(input);
      }
      if (next.diff) {
        body.append(diffTable(next.diff, onRevealFile));
      } else if (next.outputText) {
        const output = document.createElement("pre");
        output.className = "tool-output";
        output.textContent = next.outputText;
        body.append(output);
      }
      // Keep the disclosure state under the user's control. Status updates
      // only refresh the compact summary line; raw input/output stays hidden.
    },
  };
  card.update(tool);
  return card;
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
  header.append(file);
  wrap.append(header);

  const table = document.createElement("div");
  table.className = "diff-table";
  for (const line of lineDiff(diff.oldText, diff.newText)) {
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
  wrap.append(table);
  return wrap;
}
