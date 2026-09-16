import { describe, expect, it, vi } from "vitest";
import vscode from "vscode";
import {
  TranscriptStore,
  firstDiff,
  formatContent,
  formatInput,
  recordUpdate,
  toolStateOf,
  type ToolCallState,
} from "../src/transcript";

function store(): TranscriptStore {
  return new TranscriptStore();
}

const update = (sessionId: string, u: unknown) => ({ sessionId, update: u } as never);

describe("TranscriptStore registry", () => {
  it("register creates a stable uri per acp id", () => {
    const t = store();
    const uri = t.register("s1");
    expect(t.register("s1")).toBe(uri); // idempotent
    expect(uri.scheme).toBe("pi-go-session");
    expect(t.acpIdFor(uri)).toBe("s1");
    expect(t.uriFor("s1")).toBe(uri);
    expect(t.uriFor("nope")).toBeUndefined();
  });

  it("rebind points the resource at a new acp id and resets turns", () => {
    const t = store();
    const uri = t.register("s1");
    t.appendUserTurn("s1", "hello");
    t.rebind(uri, "s2");
    expect(t.acpIdFor(uri)).toBe("s2");
    expect(t.uriFor("s1")).toBeUndefined();
    expect(t.uriFor("s2")).toBe(uri);
    expect(t.snapshot("s2")).toEqual([]); // reset by rebind
  });

  it("reset drops turns and lastMessage bookkeeping", () => {
    const t = store();
    t.appendMessageChunk("s1", "agent", "a", "m1");
    t.reset("s1");
    t.appendMessageChunk("s1", "agent", "b", "m1");
    const turns = t.snapshot("s1");
    expect(turns).toHaveLength(1);
    expect(turns[0]).toEqual({ role: "agent", parts: [{ kind: "text", text: "b" }] });
  });

  it("title truncates long first lines and defaults", () => {
    const t = store();
    expect(t.title("none")).toBe("pi-go session");
    t.appendUserTurn("s1", "\n  \n");
    expect(t.title("s1")).toBe("pi-go session");
    t.appendUserTurn("s2", "short prompt");
    expect(t.title("s2")).toBe("short prompt");
    t.appendUserTurn("s3", "x".repeat(70));
    expect(t.title("s3")).toBe(`${"x".repeat(60)}…`);
    t.appendUserTurn("s4", "first line\nsecond line");
    expect(t.title("s4")).toBe("first line");
  });

  it("snapshot lazily creates empty turn lists", () => {
    const t = store();
    expect(t.snapshot("fresh")).toEqual([]);
    expect(t.snapshot("fresh")).toEqual([]); // same list, no throw
  });
});

describe("TranscriptStore writers", () => {
  it("appendUserTurn pushes a user turn", () => {
    const t = store();
    t.appendUserTurn("s1", "hi");
    expect(t.snapshot("s1")).toEqual([{ role: "user", prompt: "hi" }]);
  });

  it("appendMessageChunk merges consecutive chunks of the same turn", () => {
    const t = store();
    // Without a message id, chunks append to the same agent turn as separate
    // text parts (merge-on-turn, not merge-into-one-string).
    t.appendMessageChunk("s1", "agent", "hel", undefined);
    t.appendMessageChunk("s1", "agent", "lo", undefined);
    let turns = t.snapshot("s1");
    expect(turns).toHaveLength(1);
    expect((turns[0] as { parts: { text: string }[] }).parts.map((p) => p.text)).toEqual(["hel", "lo"]);

    // a first message id starts a new agent turn (no previous id on record)
    t.appendMessageChunk("s1", "agent", "!", "m1");
    turns = t.snapshot("s1");
    expect(turns).toHaveLength(2);
    // same message id → chunks merge into that turn
    t.appendMessageChunk("s1", "agent", "?", "m1");
    turns = t.snapshot("s1");
    expect(turns).toHaveLength(2);
    expect((turns[1] as { parts: { text: string }[] }).parts.map((p) => p.text)).toEqual(["!", "?"]);

    // a new message id starts another agent turn
    t.appendMessageChunk("s1", "agent", "next", "m2");
    turns = t.snapshot("s1");
    expect(turns).toHaveLength(3);

    // user chunks merge into user turns; a new message id splits them
    t.appendMessageChunk("s1", "user", "q1", undefined);
    t.appendMessageChunk("s1", "user", " more", undefined);
    turns = t.snapshot("s1");
    expect(turns.at(-1)).toEqual({ role: "user", prompt: "q1 more" });
    t.appendMessageChunk("s1", "user", "q2", "um1");
    turns = t.snapshot("s1");
    expect(turns.at(-1)).toEqual({ role: "user", prompt: "q2" });
  });

  it("appendThought merges into the latest thought, same turn, or starts a turn", () => {
    const t = store();
    t.appendThought("s1", "think");
    t.appendThought("s1", "ing"); // merge into the agent turn's thought part
    let turns = t.snapshot("s1");
    expect(turns).toHaveLength(1);
    expect((turns[0] as { parts: { kind: string; text: string }[] }).parts[0]).toEqual({
      kind: "thought",
      text: "thinking",
    });

    // thought after text: appended to the existing agent turn
    t.appendMessageChunk("s1", "agent", "answer", undefined);
    t.appendThought("s1", " more");
    turns = t.snapshot("s1");
    const parts = (turns[0] as { parts: { kind: string; text: string }[] }).parts;
    expect(parts).toHaveLength(2);
    // the " more" merged into the *earlier* thought part, text part untouched
    expect(parts[0]).toEqual({ kind: "thought", text: "thinking more" });
    expect(parts[1]).toEqual({ kind: "text", text: "answer" });

    // fresh store: thought after a user turn starts a new agent turn
    t.appendUserTurn("s2", "q");
    t.appendThought("s2", "hmm");
    const s2 = t.snapshot("s2");
    expect(s2).toHaveLength(2);
    expect(s2[1]).toEqual({ role: "agent", parts: [{ kind: "thought", text: "hmm" }] });
  });

  it("upsertToolCall updates in place and appends otherwise", () => {
    const t = store();
    const base: ToolCallState = {
      toolCallId: "t1",
      toolName: "read",
      title: "Read file",
      status: "pending",
    };
    t.upsertToolCall("s1", base);
    t.upsertToolCall("s1", { ...base, status: "completed", outputText: "done" });
    let turns = t.snapshot("s1");
    expect(turns).toHaveLength(1);
    const parts = (turns[0] as { parts: { kind: string; tool: ToolCallState }[] }).parts;
    expect(parts).toHaveLength(1);
    expect(parts[0].tool.status).toBe("completed");

    // a new tool id in the same agent turn is appended
    t.upsertToolCall("s1", { ...base, toolCallId: "t2" });
    turns = t.snapshot("s1");
    expect((turns[0] as { parts: unknown[] }).parts).toHaveLength(2);

    // an update for t1 lands in the turn where it first appeared
    t.appendUserTurn("s1", "next question");
    t.upsertToolCall("s1", { ...base, status: "failed" });
    turns = t.snapshot("s1");
    expect((turns[0] as { parts: unknown[] }).parts[0]).toMatchObject({ tool: { status: "failed" } });

    // a tool call with no agent turn yet creates one
    t.upsertToolCall("s2", base);
    expect(t.snapshot("s2")).toEqual([{ role: "agent", parts: [{ kind: "tool", tool: base }] }]);
  });
});

describe("recordUpdate", () => {
  it("records message chunks and returns true", () => {
    const t = store();
    expect(recordUpdate(t, update("s1", {
      sessionUpdate: "agent_message_chunk",
      content: { type: "text", text: "a" },
      messageId: "m1",
    } as never))).toBe(true);
    expect(recordUpdate(t, update("s1", {
      sessionUpdate: "user_message_chunk",
      content: { type: "text", text: "u" },
    } as never))).toBe(true);
    const turns = t.snapshot("s1");
    expect(turns).toEqual([
      { role: "agent", parts: [{ kind: "text", text: "a" }] },
      { role: "user", prompt: "u" },
    ]);
  });

  it("no-ops for empty/non-text chunks and unknown kinds", () => {
    const t = store();
    expect(recordUpdate(t, update("s1", undefined as never))).toBe(false);
    expect(recordUpdate(t, update("s1", {
      sessionUpdate: "agent_message_chunk",
      content: { type: "image", data: "x" },
    } as never))).toBe(false);
    expect(recordUpdate(t, update("s1", {
      sessionUpdate: "agent_message_chunk",
      content: { type: "text", text: "" },
    } as never))).toBe(false);
    expect(recordUpdate(t, update("s1", {
      sessionUpdate: "agent_thought_chunk",
      content: undefined,
    } as never))).toBe(false);
    expect(recordUpdate(t, update("s1", {
      sessionUpdate: "something_else",
    } as never))).toBe(false);
    expect(t.snapshot("s1")).toEqual([]);
  });

  it("records thought and tool updates", () => {
    const t = store();
    recordUpdate(t, update("s1", {
      sessionUpdate: "agent_thought_chunk",
      content: { type: "text", text: "hmm" },
    } as never));
    recordUpdate(t, update("s1", {
      sessionUpdate: "tool_call",
      toolCallId: "t1",
      name: "bash",
      status: "in_progress",
      rawInput: { cmd: "ls" },
    } as never));
    const parts = (t.snapshot("s1")[0] as { parts: { kind: string; tool?: ToolCallState }[] }).parts;
    expect(parts[0]).toEqual({ kind: "thought", text: "hmm" });
    expect(parts[1].tool).toMatchObject({ toolCallId: "t1", toolName: "bash", inputText: "{\n  \"cmd\": \"ls\"\n}" });
  });
});

describe("toolStateOf / formatting helpers", () => {
  it("toolStateOf fills defaults and derives names", () => {
    expect(toolStateOf({
      sessionUpdate: "tool_call",
      toolCallId: "t1",
    } as never)).toEqual({
      toolCallId: "t1",
      toolName: "tool",
      title: "",
      status: "pending",
      inputText: undefined,
      outputText: undefined,
      diff: undefined,
    });
    // First word only, truncated at 40 chars when overlong.
    const derived = toolStateOf({
      sessionUpdate: "tool_call_update",
      toolCallId: "t2",
      title: `${"y".repeat(50)} rest`,
    } as never);
    expect(derived.toolName).toBe(`${"y".repeat(40)}…`);
    expect(derived.toolName.length).toBe(41);
    expect(derived.status).toBe("pending");
  });

  it("formatInput handles strings, objects, and nulls", () => {
    expect(formatInput(undefined)).toBeUndefined();
    expect(formatInput(null)).toBeUndefined();
    expect(formatInput("raw")).toBe("raw");
    expect(formatInput({ a: 1 })).toBe("{\n  \"a\": 1\n}");
    const cyclic: Record<string, unknown> = {};
    cyclic.self = cyclic;
    expect(formatInput(cyclic)).toBe(String(cyclic));
  });

  it("formatContent joins text, diffs, and terminal blocks", () => {
    expect(formatContent(undefined)).toBeUndefined();
    expect(formatContent([])).toBeUndefined();
    expect(
      formatContent([{ type: "content", content: { type: "text", text: "line1" } } as never,
        { type: "content", content: { type: "text", text: "line2" } } as never]),
    ).toBe("line1\nline2");
    const diffOnly = formatContent([
      { type: "diff", path: "a.txt", oldText: "old\n", newText: "new" } as never,
    ]);
    expect(diffOnly).toBe("```diff\n--- a.txt\n+++ a.txt\n-old\n-\n+new\n```");
    expect(formatContent([{ type: "terminal", terminalId: "t1" } as never])).toBe("_(terminal output)_");
    expect(formatContent([{ type: "content", content: { type: "image" } } as never])).toBeUndefined();
  });

  it("firstDiff extracts the first diff block", () => {
    expect(firstDiff(undefined)).toBeUndefined();
    expect(firstDiff([{ type: "content", content: { type: "text", text: "t" } } as never])).toBeUndefined();
    expect(
      firstDiff([
        { type: "content", content: { type: "text", text: "t" } } as never,
        { type: "diff", path: "f.ts", oldText: null, newText: null } as never,
        { type: "diff", path: "g.ts", oldText: "a", newText: "b" } as never,
      ]),
    ).toEqual({ path: "f.ts", oldText: "", newText: "" });
  });

  it("uses the mocked Uri.parse for register", () => {
    vi.spyOn(vscode.Uri, "parse");
    const t = store();
    t.register("s9");
    expect(vi.mocked(vscode.Uri.parse).mock.calls[0]?.[0]).toContain("pi-go-session:local/");
  });
});