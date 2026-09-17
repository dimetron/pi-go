import * as vscode from "vscode";
import type { ChatTurnConstructors } from "./types/chatApi";
import { MAX_TOOL_OUTPUT, type Turn, type ToolCallState } from "./transcript";
import type * as acp from "@agentclientprotocol/sdk";

/** Ctor bundle accepted by the mappers: full ChatTurnConstructors. */
export type PartCtors = ChatTurnConstructors;

/**
 * Build a ChatToolInvocationPart for a tool call state. Live pushes carry
 * enablePartialUpdate so repeated pushes with the same toolCallId update the
 * card in place; history pushes are final.
 */
export function toolPartFor(
  tool: ToolCallState,
  ctors: PartCtors,
  opts: { live: boolean },
): unknown | undefined {
  const Ctor = ctors.ChatToolInvocationPart;
  if (!Ctor) return undefined;
  const failed = tool.status === "failed";
  const part = new Ctor(tool.toolName, tool.toolCallId, failed ? tool.title || tool.toolName : undefined);
  const message = failed ? `Failed: ${tool.title}` : tool.title || tool.toolName;
  part.invocationMessage = message;
  if (tool.status !== "pending" && tool.status !== "in_progress") {
    part.pastTenseMessage = message;
  }
  part.isComplete = tool.status === "completed" || tool.status === "failed";
  part.isError = failed || undefined;
  if (opts.live) part.enablePartialUpdate = true;
  if (tool.inputText !== undefined || tool.outputText !== undefined) {
    const Ctor2 = ctors.McpToolInvocationContentData;
    part.toolSpecificData = {
      input: tool.inputText ?? "",
      output: Ctor2
        ? [new Ctor2(Buffer.from(clamp(tool.outputText ?? ""), "utf8"), "text/plain")]
        : [],
    };
  }
  return part;
}

function clamp(text: string | undefined): string {
  const s = text ?? "";
  if (s.length <= MAX_TOOL_OUTPUT) return s;
  return `${s.slice(0, MAX_TOOL_OUTPUT)}\n…(truncated)`;
}

/** Thinking part for a thought chunk; undefined when unsupported. */
export function thoughtPartFor(
  text: string,
  id: string | undefined,
  ctors: PartCtors,
): unknown | undefined {
  const Ctor = ctors.ChatResponseThinkingProgressPart;
  if (!Ctor) return undefined;
  return new Ctor(text, id);
}

/** Info/warning banner part; undefined when unsupported. */
export function noticePartFor(text: string, ctors: PartCtors, warning = false): unknown | undefined {
  const Ctor = warning ? ctors.ChatResponseWarningPart : ctors.ChatResponseInfoPart;
  if (!Ctor) return undefined;
  return new Ctor(text);
}

/**
 * Build ChatRequestTurn / ChatResponseTurn2 history from a transcript
 * snapshot. Tool and thought parts are included when the runtime provides
 * their constructors; otherwise only markdown survives.
 */
export function historyFromTurns(turns: Turn[], ctors: PartCtors): unknown[] {
  const out: unknown[] = [];
  for (const turn of turns) {
    if (turn.role === "user") {
      if (ctors.ChatRequestTurn) {
        out.push(new ctors.ChatRequestTurn(turn.prompt, undefined, [], "pi-go", []));
      }
      continue;
    }
    const parts: unknown[] = [];
    for (const part of turn.parts) {
      switch (part.kind) {
        case "text": {
          if (part.text) parts.push(new ctors.ChatResponseMarkdownPart(part.text));
          break;
        }
        case "thought": {
          const p = thoughtPartFor(part.text, undefined, ctors);
          if (p) {
            parts.push(p);
          } else {
            parts.push(new ctors.ChatResponseMarkdownPart(`>*_thinking…_* ${part.text}`));
          }
          break;
        }
        case "tool": {
          const p = toolPartFor(part.tool, ctors, { live: false });
          if (p) parts.push(p);
          else parts.push(new ctors.ChatResponseMarkdownPart(toolFallbackMarkdown(part.tool)));
          break;
        }
      }
    }
    if (parts.length && ctors.ChatResponseTurn2) {
      out.push(new ctors.ChatResponseTurn2(parts, {}, "pi-go"));
    }
  }
  return out;
}

/** Plain-markdown rendering of a tool call when tool parts are unavailable. */
export function toolFallbackMarkdown(tool: ToolCallState): string {
  const lines = [`**${tool.status === "failed" ? "Tool failed" : "Tool"}:** ${tool.title || tool.toolName}`];
  if (tool.inputText) lines.push("```json\n" + clamp(tool.inputText) + "\n```");
  if (tool.outputText) lines.push("```\n" + clamp(tool.outputText) + "\n```");
  return lines.join("\n\n");
}

/** The agent's advertised slash commands as markdown (for /help). */
export function availableCommandsMarkdown(commands: acp.AvailableCommand[]): string {
  if (commands.length === 0) return "_pi-go has not advertised any commands._";
  const lines = commands.map((c) => `- **/${c.name}**${c.description ? ` — ${c.description}` : ""}`);
  return `Available commands:\n\n${lines.join("\n")}`;
}