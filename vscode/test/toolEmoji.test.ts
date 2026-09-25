import { describe, expect, it } from "vitest";
import { toolEmoji } from "../src/webview/toolCard";

describe("tool emoji badge", () => {
  it.each([
    ["ripgrep", "🔍"],
    ["grep", "🔍"],
    ["Grep", "🔍"],
    ["read", "📖"],
    ["Read", "📖"],
    ["read_image", "🖼️"],
    ["edit", "✏️"],
    ["write", "📝"],
    ["bash", "⚡"],
    ["ls", "📂"],
    ["tree", "📂"],
    ["find", "📂"],
    ["git-file-diff", "🌿"],
    ["git-overview", "🌿"],
    ["web_fetch", "🌐"],
    ["subagent", "🤖"],
    ["memory_search", "🔍"],
    ["memory_store", "🧠"],
    ["lsp_diagnostics", "🧩"],
    ["mcp__linear__list_issues", "🔌"],
  ])("maps %s to %s", (name, emoji) => {
    expect(toolEmoji(name)).toBe(emoji);
  });

  it("falls back to a wrench for unknown tools", () => {
    expect(toolEmoji("frobnicate")).toBe("🔧");
    expect(toolEmoji("")).toBe("🔧");
  });
});
