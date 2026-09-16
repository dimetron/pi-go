import { describe, expect, it } from "vitest";
import { chatParts, isProposedApiReady } from "../src/types/chatApi";

describe("chatParts", () => {
  it("resolves the proposed chat ctors off the vscode namespace", () => {
    const ctors = chatParts();
    // The shared vscode mock exports all of them as classes.
    expect(ctors.ChatRequestTurn).toBeTypeOf("function");
    expect(ctors.ChatResponseTurn2).toBeTypeOf("function");
    expect(ctors.ChatResponseMarkdownPart).toBeTypeOf("function");
    expect(ctors.ChatToolInvocationPart).toBeTypeOf("function");
    expect(ctors.McpToolInvocationContentData).toBeTypeOf("function");
    expect(ctors.ChatResponseThinkingProgressPart).toBeTypeOf("function");
    expect(ctors.ChatResponseWarningPart).toBeTypeOf("function");
    expect(ctors.ChatResponseInfoPart).toBeTypeOf("function");
    expect(ctors.ChatCompletionItem).toBeTypeOf("function");
  });

  it("isProposedApiReady is true when the ctors exist", () => {
    expect(isProposedApiReady()).toBe(true);
  });
});