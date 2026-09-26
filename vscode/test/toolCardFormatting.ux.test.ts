// Pure-logic coverage for the tool-card UX helpers added alongside the
// Pi-Go visual redesign: duration formatting, output line-capping, and the
// one-shot "auto-expand on first failure" rule. DOM assembly (toolCard()
// itself) needs a browser context this node-side suite doesn't provide, so
// only the pure functions are exercised here.

import { describe, expect, it } from "vitest";
import {
  capDiffLines,
  capLines,
  formatDuration,
  isFreshFailure,
  shouldAutoExpandTool,
  toolStatusText,
} from "../src/webview/toolCard";
import type { DiffLine } from "../src/shared/diff";

describe("formatDuration", () => {
  it("renders sub-second durations in milliseconds", () => {
    expect(formatDuration(0)).toBe("0ms");
    expect(formatDuration(480)).toBe("480ms");
  });

  it("renders sub-ten-second durations with one decimal", () => {
    expect(formatDuration(2300)).toBe("2.3s");
  });

  it("renders longer durations rounded to the second", () => {
    expect(formatDuration(45000)).toBe("45s");
  });

  it("renders minute-scale durations as m + zero-padded s", () => {
    expect(formatDuration(64000)).toBe("1m 04s");
  });

  it("clamps negative input to zero instead of going negative", () => {
    expect(formatDuration(-50)).toBe("0ms");
  });
});

describe("toolStatusText", () => {
  it("returns the bare label when there is no timing yet", () => {
    expect(toolStatusText("pending", "queued", undefined)).toBe("queued");
  });

  it("appends the formatted duration once elapsed time is known", () => {
    expect(toolStatusText("in_progress", "running", 2300)).toBe("running · 2.3s");
    expect(toolStatusText("completed", "done", 480)).toBe("done · 480ms");
  });
});

describe("capLines", () => {
  it("returns the text unchanged when under the cap", () => {
    const text = "a\nb\nc";
    expect(capLines(text, 40)).toEqual({ visible: text, hiddenLines: 0 });
  });

  it("caps to the first N lines and reports how many were hidden", () => {
    const lines = Array.from({ length: 50 }, (_, i) => `line ${i}`);
    const { visible, hiddenLines } = capLines(lines.join("\n"), 40);
    expect(visible.split("\n")).toHaveLength(40);
    expect(hiddenLines).toBe(10);
  });
});

describe("capDiffLines", () => {
  const mk = (n: number): DiffLine[] =>
    Array.from({ length: n }, (_, i) => ({ type: "ctx", text: `line ${i}` }));

  it("returns all lines unchanged when under the cap", () => {
    const lines = mk(10);
    expect(capDiffLines(lines, 40)).toEqual({ visible: lines, hiddenLines: 0 });
  });

  it("caps the diff and preserves the true count of what's hidden", () => {
    const lines = mk(250);
    const { visible, hiddenLines } = capDiffLines(lines, 40);
    expect(visible).toHaveLength(40);
    expect(hiddenLines).toBe(210);
  });
});

describe("tool card auto-expand", () => {
  it("shouldAutoExpandTool keeps its original contract: never true", () => {
    // Locked in by the pre-existing "never auto-expands" test — the new
    // auto-open-on-first-failure behavior lives in isFreshFailure instead so
    // that older contract is left intact.
    expect(shouldAutoExpandTool("pending")).toBe(false);
    expect(shouldAutoExpandTool("in_progress")).toBe(false);
    expect(shouldAutoExpandTool("completed")).toBe(false);
    expect(shouldAutoExpandTool("failed")).toBe(false);
  });

  it("isFreshFailure fires only on the transition into failed", () => {
    expect(isFreshFailure("failed", undefined)).toBe(true);
    expect(isFreshFailure("failed", "in_progress")).toBe(true);
    expect(isFreshFailure("failed", "failed")).toBe(false);
    expect(isFreshFailure("completed", undefined)).toBe(false);
    expect(isFreshFailure("in_progress", "pending")).toBe(false);
  });
});
