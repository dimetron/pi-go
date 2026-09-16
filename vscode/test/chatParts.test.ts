import { describe, expect, it } from "vitest";
import {
  availableCommandsMarkdown,
  historyFromTurns,
  noticePartFor,
  thoughtPartFor,
  toolFallbackMarkdown,
  toolPartFor,
  type PartCtors,
} from "../src/chatParts";
import type { ToolCallState, Turn } from "../src/transcript";

/** Fake constructor bundle: records constructor args as plain objects. */
function fakeCtors(): PartCtors {
  return {
    ChatRequestTurn: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
    ChatResponseTurn2: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
    ChatResponseTurn: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
    ChatResponseMarkdownPart: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
    ChatToolInvocationPart: class {
      toolSpecificData: unknown;
      invocationMessage?: string;
      pastTenseMessage?: string;
      isComplete?: boolean;
      isError?: boolean;
      enablePartialUpdate?: boolean;
      constructor(
        public toolName: string,
        public toolCallId: string,
        public errorMessage?: string,
      ) {}
    },
    McpToolInvocationContentData: class {
      constructor(public data: Uint8Array, public mimeType: string) {}
    },
    ChatResponseThinkingProgressPart: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
    ChatResponseWarningPart: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
    ChatResponseInfoPart: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
    ChatCompletionItem: class {
      constructor(...args: unknown[]) {
        Object.assign(this, { args });
      }
    },
  } as unknown as PartCtors;
}

const tool: ToolCallState = {
  toolCallId: "t1",
  toolName: "bash",
  title: "Run ls",
  status: "completed",
  inputText: '{"cmd":"ls"}',
  outputText: "file1",
};

describe("toolPartFor", () => {
  it("builds a live part with partial updates", () => {
    const ctors = fakeCtors();
    const part = toolPartFor(tool, ctors, { live: true }) as Record<string, unknown>;
    expect(part.toolName).toBe("bash");
    expect(part.toolCallId).toBe("t1");
    expect(part.invocationMessage).toBe("Run ls");
    expect(part.pastTenseMessage).toBe("Run ls");
    expect(part.isComplete).toBe(true);
    expect(part.isError).toBeUndefined();
    expect(part.enablePartialUpdate).toBe(true);
    const data = part.toolSpecificData as { input: string; output: unknown[] };
    expect(data.input).toBe('{"cmd":"ls"}');
    expect(data.output).toHaveLength(1);
    const bytes = (data.output[0] as { data: Uint8Array }).data;
    expect(Buffer.from(bytes).toString()).toBe("file1");
  });

  it("pending parts have no pastTenseMessage and no partial updates in history", () => {
    const ctors = fakeCtors();
    const part = toolPartFor({ ...tool, status: "in_progress" }, ctors, { live: false }) as Record<string, unknown>;
    expect(part.pastTenseMessage).toBeUndefined();
    expect(part.isComplete).toBe(false);
    expect(part.enablePartialUpdate).toBeUndefined();
  });

  it("failed parts flag the error and use the title fallback", () => {
    const ctors = fakeCtors();
    const part = toolPartFor({ ...tool, status: "failed", title: "" }, ctors, { live: true }) as Record<string, unknown>;
    expect(part.isError).toBe(true);
    expect(part.invocationMessage).toBe("Failed: ");
    expect(part.errorMessage).toBe("bash"); // ctor arg: title || toolName
    const titled = toolPartFor({ ...tool, status: "failed" }, ctors, { live: true }) as Record<string, unknown>;
    expect(titled.invocationMessage).toBe("Failed: Run ls");
  });

  it("clamps oversized output text", () => {
    const ctors = fakeCtors();
    const part = toolPartFor(
      { ...tool, outputText: "x".repeat(17 * 1024) },
      ctors,
      { live: false },
    ) as Record<string, unknown>;
    const data = part.toolSpecificData as { output: { data: Uint8Array }[] };
    const text = Buffer.from(data.output[0].data).toString();
    expect(text).toContain("…(truncated)");
    expect(text.length).toBeLessThan(17 * 1024);
  });

  it("omits toolSpecificData when there is no input/output", () => {
    const ctors = fakeCtors();
    const part = toolPartFor({ ...tool, inputText: undefined, outputText: undefined }, ctors, { live: false }) as Record<string, unknown>;
    expect(part.toolSpecificData).toBeUndefined();
  });

  it("returns undefined without the ChatToolInvocationPart ctor", () => {
    const ctors = fakeCtors();
    (ctors as unknown as Record<string, unknown>).ChatToolInvocationPart = undefined;
    expect(toolPartFor(tool, ctors, { live: true })).toBeUndefined();
  });
});

describe("thoughtPartFor / noticePartFor", () => {
  it("builds thought and notice parts from the right ctors", () => {
    const ctors = fakeCtors();
    expect(thoughtPartFor("thinking", "id1", ctors)).toMatchObject({ args: ["thinking", "id1"] });
    expect((noticePartFor("info", ctors) as { constructor: { name: string } }).constructor.name).toBe(
      "ChatResponseInfoPart",
    );
    expect((noticePartFor("warn", ctors, true) as { constructor: { name: string } }).constructor.name).toBe(
      "ChatResponseWarningPart",
    );
  });

  it("returns undefined when the ctor is missing", () => {
    const ctors = fakeCtors();
    (ctors as unknown as Record<string, unknown>).ChatResponseThinkingProgressPart = undefined;
    (ctors as unknown as Record<string, unknown>).ChatResponseInfoPart = undefined;
    (ctors as unknown as Record<string, unknown>).ChatResponseWarningPart = undefined;
    expect(thoughtPartFor("t", undefined, ctors)).toBeUndefined();
    expect(noticePartFor("i", ctors)).toBeUndefined();
    expect(noticePartFor("w", ctors, true)).toBeUndefined();
  });
});

describe("historyFromTurns", () => {
  it("maps user turns, agent text, thoughts, and tools", () => {
    const ctors = fakeCtors();
    const turns: Turn[] = [
      { role: "user", prompt: "hi" },
      {
        role: "agent",
        parts: [
          { kind: "thought", text: "hmm" },
          { kind: "text", text: "answer" },
          { kind: "tool", tool },
        ],
      },
    ];
    const history = historyFromTurns(turns, ctors) as InstanceType<PartCtors["ChatRequestTurn"]>[];
    expect(history).toHaveLength(2);
    const agentParts = (history[1] as { args: [unknown[]] }).args[0] as { args?: unknown[]; constructor: { name: string } }[];
    expect(agentParts[0].constructor.name).toBe("ChatResponseThinkingProgressPart");
    expect(agentParts[1].constructor.name).toBe("ChatResponseMarkdownPart");
    expect(agentParts[2].constructor.name).toBe("ChatToolInvocationPart");
  });

  it("falls back to markdown when thought/tool ctors are missing", () => {
    const ctors = fakeCtors();
    (ctors as unknown as Record<string, unknown>).ChatResponseThinkingProgressPart = undefined;
    (ctors as unknown as Record<string, unknown>).ChatToolInvocationPart = undefined;
    const turns: Turn[] = [
      {
        role: "agent",
        parts: [
          { kind: "thought", text: "hmm" },
          { kind: "tool", tool },
        ],
      },
    ];
    const history = historyFromTurns(turns, ctors) as { args: [unknown[]] }[];
    const parts = history[0].args[0] as { args: [string] }[];
    expect((parts[0] as unknown as { args: [string] }).args[0]).toContain("_thinking…_");
    expect((parts[1] as unknown as { args: [string] }).args[0]).toContain("**Tool:**");
  });

  it("drops agent turns with no renderable parts and skips empty text", () => {
    const ctors = fakeCtors();
    const turns: Turn[] = [
      { role: "agent", parts: [{ kind: "text", text: "" }, { kind: "text", text: "ok" }] },
    ];
    const history = historyFromTurns(turns, ctors) as { args: [unknown[]] }[];
    expect(history).toHaveLength(1);
    expect((history[0].args[0] as { args: [string] }[])).toHaveLength(1);
  });

  it("skips ChatRequestTurn when its ctor is missing", () => {
    const ctors = fakeCtors();
    (ctors as unknown as Record<string, unknown>).ChatRequestTurn = undefined;
    const history = historyFromTurns([{ role: "user", prompt: "hi" }], ctors);
    expect(history).toEqual([]);
  });

  it("skips ChatResponseTurn2 when its ctor is missing", () => {
    const ctors = fakeCtors();
    (ctors as unknown as Record<string, unknown>).ChatResponseTurn2 = undefined;
    const history = historyFromTurns(
      [{ role: "agent", parts: [{ kind: "text", text: "x" }] }],
      ctors,
    );
    expect(history).toEqual([]);
  });
});

describe("toolFallbackMarkdown / availableCommandsMarkdown", () => {
  it("renders status, input, and output", () => {
    expect(toolFallbackMarkdown(tool)).toBe(
      '**Tool:** Run ls\n\n```json\n{"cmd":"ls"}\n```\n\n```\nfile1\n```',
    );
    expect(toolFallbackMarkdown({ ...tool, status: "failed", title: "" })).toContain("**Tool failed:** bash");
    const long = toolFallbackMarkdown({ ...tool, inputText: "y".repeat(17 * 1024) });
    expect(long).toContain("…(truncated)");
  });

  it("renders available commands", () => {
    expect(availableCommandsMarkdown([])).toContain("has not advertised");
    const md = availableCommandsMarkdown([
      { name: "plan", description: "Plan it" },
      { name: "review" },
    ]);
    expect(md).toContain("**/plan** — Plan it");
    expect(md).toContain("**/plan**");
    expect(md).toContain("**/review**");
  });
});