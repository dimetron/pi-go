import * as vscode from "vscode";
import type * as acp from "@agentclientprotocol/sdk";
import { SESSION_SCHEME } from "./acp";

// ---------------------------------------------------------------------------
// Turn model — what a session transcript is made of, in arrival order.
// ---------------------------------------------------------------------------

/** Snapshot of one tool call, kept up to date from tool_call/_update events. */
export interface ToolCallState {
  toolCallId: string;
  /** Programmatic name when the agent sends one, else derived from the title. */
  toolName: string;
  title: string;
  status: "pending" | "in_progress" | "completed" | "failed";
  inputText?: string;
  outputText?: string;
}

export type TurnPart =
  | { kind: "text"; text: string }
  | { kind: "thought"; text: string }
  | { kind: "tool"; tool: ToolCallState };

export type Turn =
  | { role: "user"; prompt: string }
  | { role: "agent"; parts: TurnPart[] };

// ---------------------------------------------------------------------------

/**
 * In-memory transcript keyed by ACP session id. Live prompts and session/load
 * replays both write here through the session-update fan-out; the content
 * provider renders history from snapshots.
 */
export class TranscriptStore {
  private readonly sessions = new Map<string, vscode.Uri>();
  private readonly byUri = new Map<string, string>();
  private readonly turns = new Map<string, Turn[]>();
  private readonly lastMessage = new Map<string, string>();
  private seq = 0;

  register(acpId: string): vscode.Uri {
    const existing = this.sessions.get(acpId);
    if (existing) return existing;
    const uri = vscode.Uri.parse(`${SESSION_SCHEME}:local/${Date.now().toString(36)}-${this.seq++}`);
    this.sessions.set(acpId, uri);
    this.byUri.set(uri.toString(), acpId);
    this.turns.set(acpId, []);
    return uri;
  }

  /** Drop the in-memory transcript before a session/load replay, so repeated
   *  resolves of the same session do not stack duplicate turns. */
  reset(acpId: string): void {
    this.turns.set(acpId, []);
    this.lastMessage.delete(acpId);
  }

  acpIdFor(uri: vscode.Uri): string | undefined {
    return this.byUri.get(uri.toString());
  }

  uriFor(acpId: string): vscode.Uri | undefined {
    return this.sessions.get(acpId);
  }

  /** Point an existing session resource at a new ACP id (used by /clear). */
  rebind(uri: vscode.Uri, newAcpId: string): void {
    const old = this.byUri.get(uri.toString());
    if (old) this.sessions.delete(old);
    this.sessions.set(newAcpId, uri);
    this.byUri.set(uri.toString(), newAcpId);
    this.reset(newAcpId);
  }

  snapshot(acpId: string): Turn[] {
    return this.turnList(acpId);
  }

  /** First user prompt line, for the session list label. */
  title(acpId: string): string {
    const first = this.turnList(acpId).find((t) => t.role === "user");
    const line = first?.prompt.trim().split("\n")[0] ?? "";
    if (!line) return "pi-go session";
    return line.length > 60 ? `${line.slice(0, 60)}…` : line;
  }

  // -- writers ------------------------------------------------------------

  appendUserTurn(acpId: string, prompt: string): void {
    this.turnList(acpId).push({ role: "user", prompt });
  }

  appendMessageChunk(acpId: string, role: "user" | "agent", text: string, messageId?: string): void {
    const list = this.turnList(acpId);
    // A new messageId starts a new agent turn; without one, chunks merge on.
    const newMessage = messageId !== undefined && this.lastMessage.get(acpId) !== messageId;
    if (messageId !== undefined) this.lastMessage.set(acpId, messageId);
    const last = list[list.length - 1];
    if (role === "user") {
      if (last?.role === "user" && !newMessage) last.prompt += text;
      else list.push({ role: "user", prompt: text });
      return;
    }
    if (last?.role === "agent" && !newMessage) {
      last.parts.push({ kind: "text", text });
    } else {
      list.push({ role: "agent", parts: [{ kind: "text", text }] });
    }
  }

  appendThought(acpId: string, text: string): void {
    const list = this.turnList(acpId);
    const last = list[list.length - 1];
    const prev =
      last?.role === "agent"
        ? [...last.parts].reverse().find((p): p is Extract<TurnPart, { kind: "thought" }> => p.kind === "thought")
        : undefined;
    if (prev) {
      prev.text += text;
    } else if (last?.role === "agent") {
      last.parts.push({ kind: "thought", text });
    } else {
      list.push({ role: "agent", parts: [{ kind: "thought", text }] });
    }
  }

  /** Insert or refresh a tool call by id, in the turn where it first appeared. */
  upsertToolCall(acpId: string, state: ToolCallState): void {
    for (const turn of this.turnList(acpId)) {
      if (turn.role !== "agent") continue;
      const idx = turn.parts.findIndex((p) => p.kind === "tool" && p.tool.toolCallId === state.toolCallId);
      if (idx >= 0) {
        turn.parts[idx] = { kind: "tool", tool: state };
        return;
      }
    }
    const list = this.turnList(acpId);
    const last = list[list.length - 1];
    if (last?.role === "agent") last.parts.push({ kind: "tool", tool: state });
    else list.push({ role: "agent", parts: [{ kind: "tool", tool: state }] });
  }

  private turnList(acpId: string): Turn[] {
    let t = this.turns.get(acpId);
    if (!t) {
      t = [];
      this.turns.set(acpId, t);
    }
    return t;
  }
}

// ---------------------------------------------------------------------------
// ACP update → store writers. Shared by the live prompt path and replays.
// ---------------------------------------------------------------------------

/**
 * Fold one session/update notification into the transcript. Returns true when
 * something was recorded (callers refresh the session list on that).
 */
export function recordUpdate(store: TranscriptStore, update: acp.SessionNotification): boolean {
  const u = update.update;
  if (!u) return false;
  switch (u.sessionUpdate) {
    case "user_message_chunk":
    case "agent_message_chunk": {
      const block = u.content;
      if (!block || block.type !== "text" || !block.text) return false;
      store.appendMessageChunk(
        update.sessionId,
        u.sessionUpdate === "user_message_chunk" ? "user" : "agent",
        block.text,
        u.messageId ?? undefined,
      );
      return true;
    }
    case "agent_thought_chunk": {
      const block = u.content;
      if (!block || block.type !== "text" || !block.text) return false;
      store.appendThought(update.sessionId, block.text);
      return true;
    }
    case "tool_call":
    case "tool_call_update": {
      store.upsertToolCall(update.sessionId, toolStateOf(u));
      return true;
    }
    default:
      return false;
  }
}

/** Merge a tool_call / tool_call_update payload into a ToolCallState. */
export function toolStateOf(
  u: Extract<acp.SessionUpdate, { sessionUpdate: "tool_call" | "tool_call_update" }>,
): ToolCallState {
  return {
    toolCallId: u.toolCallId,
    toolName: u.name ?? deriveName(u.title),
    title: u.title ?? "",
    status: u.status ?? "pending",
    inputText: formatInput(u.rawInput),
    outputText: formatContent(u.content),
  };
}

function deriveName(title: string | null | undefined): string {
  const word = (title ?? "tool").split(/\s+/)[0] ?? "tool";
  const name = word.length > 40 ? `${word.slice(0, 40)}…` : word;
  return name || "tool";
}

/** Raw tool input as a short display string. */
export function formatInput(rawInput: unknown): string | undefined {
  if (rawInput === undefined || rawInput === null) return undefined;
  try {
    if (typeof rawInput === "string") return rawInput;
    return JSON.stringify(rawInput, null, 2);
  } catch {
    return String(rawInput);
  }
}

/** Tool content blocks → display text. Text blocks join; diffs and terminal
 *  output render as fenced snippets; images/audio are skipped. */
export function formatContent(content: acp.ToolCallContent[] | null | undefined): string | undefined {
  if (!content || content.length === 0) return undefined;
  const out: string[] = [];
  for (const c of content) {
    if (c.type === "content") {
      if (c.content?.type === "text") out.push(c.content.text);
      // image/audio blocks: skipped — pi-go does not send them as tool output
    } else if (c.type === "diff") {
      const body = diffBody(c.oldText, c.newText);
      out.push(`\`\`\`diff\n--- ${c.path}\n+++ ${c.path}\n${body}\n\`\`\``);
    } else if (c.type === "terminal") {
      // Terminal blocks reference a terminal by id; there is no inline output
      // to show (pi-go never sends them).
      out.push("_(terminal output)_");
    }
  }
  const text = out.join("\n").trim();
  return text || undefined;
}

function diffBody(oldText: string | null | undefined, newText: string | null | undefined): string {
  const oldLines = (oldText ?? "").split("\n");
  const newLines = (newText ?? "").split("\n");
  const lines: string[] = [];
  for (const l of oldLines) if (l !== undefined) lines.push(`-${l}`);
  for (const l of newLines) if (l !== undefined) lines.push(`+${l}`);
  return lines.join("\n");
}