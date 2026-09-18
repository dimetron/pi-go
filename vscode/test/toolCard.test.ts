import { describe, expect, it } from "vitest";
import { TOOL_CARD_DEFAULT_OPEN, shouldAutoExpandTool } from "../src/webview/toolCard";

describe("tool card disclosure", () => {
  it("starts collapsed so tool output does not flood the transcript", () => {
    expect(TOOL_CARD_DEFAULT_OPEN).toBe(false);
  });

  it("never auto-expands for a status update", () => {
    expect(shouldAutoExpandTool("pending")).toBe(false);
    expect(shouldAutoExpandTool("in_progress")).toBe(false);
    expect(shouldAutoExpandTool("completed")).toBe(false);
    expect(shouldAutoExpandTool("failed")).toBe(false);
  });
});
