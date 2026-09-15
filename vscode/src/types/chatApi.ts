import * as vscode from "vscode";

/**
 * Structural types for proposed VS Code chat APIs (1.137). These are not in
 * the public @types/vscode index.d.ts, so we declare the shapes we use and
 * resolve the runtime constructors dynamically from the `vscode` module.
 *
 * Verified against microsoft/vscode tag 1.137.0:
 * - vscode.proposed.chatSessionsProvider.d.ts
 * - vscode.proposed.chatParticipantAdditions.d.ts
 * - vscode.proposed.chatParticipantPrivate.d.ts
 *
 * The extension host's converters dispatch history/live parts by instanceof,
 * so parts must be built from the real `vscode` module classes, never
 * structural lookalikes. Every accessor here guards with typeof checks and
 * callers must degrade to plain markdown when a constructor is missing.
 */

// ---------------------------------------------------------------------------
// chatSessionsProvider — session item/content providers
// ---------------------------------------------------------------------------

export interface ChatSessionItemDto {
  resource: vscode.Uri;
  label: string;
  description?: string | vscode.MarkdownString;
  /** ChatSessionStatus: 0 failed, 1 completed, 2 in-progress, 3 needs-input */
  status?: number;
  archived?: boolean;
  tooltip?: string | vscode.MarkdownString;
  timing?: {
    created?: number;
    lastRequestStarted?: number;
    lastRequestEnded?: number;
  };
}

export interface ChatSessionDto {
  title?: string;
  history: unknown[];
  activeResponseCallback?: unknown;
  requestHandler: vscode.ChatRequestHandler | undefined;
  options?: Record<string, unknown>;
}

export interface ChatSessionItemProviderDto {
  readonly onDidChangeChatSessionItems: vscode.Event<void>;
  provideChatSessionItems(
    token: vscode.CancellationToken,
  ): vscode.ProviderResult<ChatSessionItemDto[]>;
  resolveChatSessionItem?(
    item: ChatSessionItemDto,
    token: vscode.CancellationToken,
  ): vscode.ProviderResult<ChatSessionItemDto>;
}

export interface ChatSessionContentProviderDto {
  provideChatSessionContent(
    resource: vscode.Uri,
    token: vscode.CancellationToken,
    context: unknown,
  ): vscode.ProviderResult<ChatSessionDto>;
}

export interface ChatNamespaceWithSessions {
  registerChatSessionItemProvider(
    chatSessionType: string,
    provider: ChatSessionItemProviderDto,
  ): vscode.Disposable;
  /** Keyed by the session resource's URI scheme, not the chat session type. */
  registerChatSessionContentProvider(
    scheme: string,
    provider: ChatSessionContentProviderDto,
    defaultChatParticipant: vscode.ChatParticipant,
    capabilities?: { supportsInterruptions?: boolean },
  ): vscode.Disposable;
}

// ---------------------------------------------------------------------------
// chatParticipantAdditions / chatParticipantPrivate — runtime constructors
// ---------------------------------------------------------------------------

/** ChatMcpToolInvocationData shape — input/output tool card. */
export interface ToolResultData {
  input: string;
  output: unknown[];
}

/** ChatToolInvocationPart, structurally. */
export interface ToolPartLike {
  toolName: string;
  toolCallId: string;
  isError?: boolean;
  invocationMessage?: string | vscode.MarkdownString;
  pastTenseMessage?: string | vscode.MarkdownString;
  isComplete?: boolean;
  toolSpecificData?: ToolResultData;
  enablePartialUpdate?: boolean;
}

export interface ChatTurnConstructors {
  /** (prompt, command, references, participant, toolReferences, ...) */
  ChatRequestTurn: new (
    prompt: string,
    command: string | undefined,
    references: unknown[],
    participant: string,
    toolReferences?: unknown[],
    ...rest: unknown[]
  ) => unknown;
  /** (response, result, participant) — chatParticipantPrivate */
  ChatResponseTurn2: new (
    response: unknown[],
    result: object,
    participant: string,
  ) => unknown;
  /** Legacy public-API response turn, (response, result, participant, command?) */
  ChatResponseTurn: new (
    response: unknown[],
    result: object,
    participant: string,
    command?: string,
  ) => unknown;
  ChatResponseMarkdownPart: new (value: string | vscode.MarkdownString) => unknown;
  /** (toolName, toolCallId, errorMessage?) */
  ChatToolInvocationPart: new (toolName: string, toolCallId: string, errorMessage?: string) => ToolPartLike;
  /** (data: Uint8Array, mimeType: string) */
  McpToolInvocationContentData: new (data: Uint8Array, mimeType: string) => unknown;
  /** (value: string | string[], id?, metadata?, task?) */
  ChatResponseThinkingProgressPart: new (
    value: string | string[],
    id?: string,
    metadata?: Record<string, unknown>,
  ) => unknown;
  ChatResponseWarningPart: new (value: string | vscode.MarkdownString) => unknown;
  ChatResponseInfoPart: new (value: string | vscode.MarkdownString) => unknown;
}

const WANTED: ReadonlyArray<keyof ChatTurnConstructors> = [
  "ChatRequestTurn",
  "ChatResponseTurn2",
  "ChatResponseTurn",
  "ChatResponseMarkdownPart",
  "ChatToolInvocationPart",
  "McpToolInvocationContentData",
  "ChatResponseThinkingProgressPart",
  "ChatResponseWarningPart",
  "ChatResponseInfoPart",
];

/** Constructors the ext host exports at runtime; missing entries mean the
 *  corresponding rendering falls back to plain markdown. */
export function chatParts(): Partial<ChatTurnConstructors> {
  const ns = vscode as unknown as Record<string, unknown>;
  const out: Record<string, unknown> = {};
  for (const name of WANTED) {
    const ctor = ns[name];
    if (typeof ctor === "function") {
      out[name] = ctor;
    }
  }
  return out as Partial<ChatTurnConstructors>;
}

/** True when the constructors needed for tool/thought rendering all exist. */
export function isProposedApiReady(): boolean {
  const ctors = chatParts();
  return WANTED.every((name) => name in ctors);
}