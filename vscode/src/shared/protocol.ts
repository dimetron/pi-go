// Host ↔ webview message protocol for the pi-go chat view.
//
// This module is imported by BOTH esbuild targets (the node extension and the
// browser webview bundle), so it must stay dependency-free: no "vscode", no
// node imports, plain JSON-serializable types only. The Makefile check-types
// step greps for violations.

// ---------------------------------------------------------------------------
// Snapshot types — mirrors of src/transcript.ts's Turn/ToolCallState as plain
// JSON (structurally identical, so the host can postMessage store snapshots
// directly).
// ---------------------------------------------------------------------------

/** A structured file diff on a tool call, rendered as a real line diff. */
export interface ToolDiff {
  path: string;
  oldText: string;
  newText: string;
}

export interface ToolSnapshot {
  toolCallId: string;
  /** Programmatic name when the agent sends one, else derived from the title. */
  toolName: string;
  title: string;
  status: "pending" | "in_progress" | "completed" | "failed";
  inputText?: string;
  outputText?: string;
  diff?: ToolDiff;
}

export type TurnPartSnapshot =
  | { kind: "text"; text: string }
  | { kind: "thought"; text: string }
  | { kind: "tool"; tool: ToolSnapshot };

export type TurnSnapshot =
  | { role: "user"; prompt: string }
  | { role: "agent"; parts: TurnPartSnapshot[] };

/** Slash command advertised via available_commands_update. */
export interface CommandInfo {
  name: string;
  description?: string;
}

// ---------------------------------------------------------------------------
// Host → webview
// ---------------------------------------------------------------------------

export interface StateMessage {
  type: "state";
  /** Undefined → show the welcome screen; the first prompt starts a session. */
  sessionId?: string;
  title?: string;
  turns: TurnSnapshot[];
  commands: CommandInfo[];
  streaming: boolean;
  caps: { embeddedContext: boolean };
}

export interface ReplayStartedMessage {
  type: "replayStarted";
  sessionId: string;
}

export interface UserTurnMessage {
  type: "userTurn";
  sessionId: string;
  prompt: string;
}

export interface AgentChunkMessage {
  type: "agentChunk";
  sessionId: string;
  text: string;
}

export interface ThoughtChunkMessage {
  type: "thoughtChunk";
  sessionId: string;
  text: string;
}

export interface ToolUpdateMessage {
  type: "toolUpdate";
  sessionId: string;
  tool: ToolSnapshot;
}

/** The prompt promise settled (resolved, errored, or cancelled). */
export interface TurnEndMessage {
  type: "turnEnd";
  sessionId: string;
  /** Error text when the turn failed; undefined on success/cancel. */
  error?: string;
  /** Human-readable diagnosis for a failed turn. */
  errorDetail?: string;
  /** Recovery actions for a failed turn. */
  errorSteps?: string[];
}

export interface CommandsUpdatedMessage {
  type: "commandsUpdated";
  sessionId: string;
  commands: CommandInfo[];
}

export interface SessionLoadedMessage {
  type: "sessionLoaded";
  sessionId: string;
  title?: string;
}

export interface ErrorMessage {
  type: "error";
  message: string;
  detail?: string;
  steps?: string[];
}

export interface PingResultMessage {
  type: "pingResult";
  ok: boolean;
  title: string;
  detail: string;
}

/** Non-failure inline notice (e.g. skipped oversized attachments). */
export interface NoticeMessage {
  type: "notice";
  text: string;
}

export interface AttachmentsAddedMessage {
  type: "attachmentsAdded";
  paths: string[];
}

export type HostToWebview =
  | StateMessage
  | ReplayStartedMessage
  | UserTurnMessage
  | AgentChunkMessage
  | ThoughtChunkMessage
  | ToolUpdateMessage
  | TurnEndMessage
  | CommandsUpdatedMessage
  | SessionLoadedMessage
  | ErrorMessage
  | PingResultMessage
  | NoticeMessage
  | AttachmentsAddedMessage;

// ---------------------------------------------------------------------------
// Webview → host
// ---------------------------------------------------------------------------

export interface ReadyMessage {
  type: "ready";
}

export interface PromptMessage {
  type: "prompt";
  /** Omitted → the host creates a new session first. */
  sessionId?: string;
  text: string;
  attachments: string[];
}

export interface NewSessionMessage {
  type: "newSession";
}

export interface OpenSessionMessage {
  type: "openSession";
  sessionId: string;
}

export interface CancelMessage {
  type: "cancel";
  sessionId: string;
}

export interface DraftMessage {
  type: "draft";
  sessionId?: string;
  text: string;
}

export interface RevealFileMessage {
  type: "revealFile";
  path: string;
}

export interface RequestFilePickerMessage {
  type: "requestFilePicker";
}

export interface OpenPiGoSettingsMessage {
  type: "openPiGoSettings";
}

export interface OpenPiGoLogsMessage {
  type: "openPiGoLogs";
}

export interface PingMessage {
  type: "ping";
}

export type WebviewToHost =
  | ReadyMessage
  | { type: "showHistory" }
  | PromptMessage
  | NewSessionMessage
  | OpenSessionMessage
  | CancelMessage
  | DraftMessage
  | RevealFileMessage
  | RequestFilePickerMessage
  | OpenPiGoSettingsMessage
  | OpenPiGoLogsMessage
  | PingMessage;
