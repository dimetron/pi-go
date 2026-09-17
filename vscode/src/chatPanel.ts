import * as vscode from "vscode";
import type * as acp from "@agentclientprotocol/sdk";
import {
  PiGoAcpClient,
  chunkText,
  thoughtText,
  type SessionEntry,
} from "./acp";
import { TranscriptStore, toolStateOf } from "./transcript";
import { pathsToBlocks, skippedMentionsMarkdown } from "./mentions";
import type {
  CommandInfo,
  HostToWebview,
  ToolSnapshot,
  TurnSnapshot,
  WebviewToHost,
} from "./shared/protocol";

const log = vscode.window.createOutputChannel("pi-go", { log: true });

function errString(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "object" && err !== null && "message" in err) {
    return String((err as { message: unknown }).message);
  }
  return String(err);
}

function getNonce(): string {
  let text = "";
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  for (let i = 0; i < 32; i++) text += chars.charAt(Math.floor(Math.random() * chars.length));
  return text;
}

/**
 * Host bridge for the dedicated chat view (activity bar + secondary sidebar).
 * One instance serves both view ids; live ACP updates stream straight to the
 * webview while the global listener keeps the TranscriptStore authoritative
 * for snapshots, replays, and the native sessions path.
 */
export class ChatPanelProvider implements vscode.WebviewViewProvider, vscode.Disposable {
  private readonly views = new Map<string, vscode.WebviewView>();
  private currentSessionId?: string;
  private inFlight?: vscode.CancellationTokenSource;
  /** True while session/load replay flows through the update listener. */
  private replaying = false;

  constructor(
    private readonly context: vscode.ExtensionContext,
    private readonly client: PiGoAcpClient,
    private readonly store: TranscriptStore,
    private readonly refresh: vscode.EventEmitter<void>,
    private readonly active: Set<string>,
  ) {
    context.subscriptions.push(
      this.client.onSessionUpdate((update) => this.onUpdate(update)),
    );
  }

  resolveWebviewView(
    view: vscode.WebviewView,
    _context: vscode.WebviewViewResolveContext,
    _token: vscode.CancellationToken,
  ): void {
    this.views.set(view.viewType, view);
    view.onDidDispose(() => this.views.delete(view.viewType), null, this.context.subscriptions);

    const extUri = this.context.extensionUri;
    view.webview.options = {
      enableScripts: true,
      localResourceRoots: [
        vscode.Uri.joinPath(extUri, "media"),
        vscode.Uri.joinPath(extUri, "dist"),
      ],
    };
    view.webview.html = this.html(view.webview);
    view.webview.onDidReceiveMessage(
      (message) => this.onMessage(message as WebviewToHost),
      null,
      this.context.subscriptions,
    );
  }

  dispose(): void {
    this.inFlight?.dispose();
  }

  /** Open a persisted session in the chat view (from the sessions tree). */
  async openSession(entry: SessionEntry): Promise<void> {
    if (this.inFlight) return;
    const acpId = entry.sessionId;
    this.currentSessionId = acpId;
    this.postAll({ type: "sessionLoaded", sessionId: acpId, title: entry.title ?? undefined });

    // Replay appends from a clean slate, so repeated opens do not stack turns.
    this.store.register(acpId);
    this.store.reset(acpId);
    this.postAll({ type: "replayStarted", sessionId: acpId });
    this.replaying = true;
    try {
      await this.client.load(entry);
    } catch (err) {
      log.error(`session/load for ${acpId}: ${errString(err)}`);
    }
    this.replaying = false;
    this.sendState();
    this.refresh.fire();
  }

  // -- webview → host ------------------------------------------------------

  private onMessage(message: WebviewToHost): void {
    if (!message || typeof message !== "object") return;
    switch (message.type) {
      case "ready":
        log.debug("chat webview ready");
        this.sendState();
        break;
      case "prompt":
        void this.runPrompt(message.text, message.attachments);
        break;
      case "newSession":
        void this.startSession();
        break;
      case "openSession":
        void this.openSession({ sessionId: message.sessionId, cwd: workspaceCwd() });
        break;
      case "cancel":
        this.inFlight?.cancel();
        break;
      case "revealFile":
        void this.revealFile(message.path);
        break;
      case "requestFilePicker":
        void this.pickFiles();
        break;
      case "draft":
        break;
      default:
        break;
    }
  }

  private async revealFile(path: string): Promise<void> {
    try {
      await vscode.window.showTextDocument(vscode.Uri.file(path), { preview: true });
    } catch (err) {
      void vscode.window.showErrorMessage(`pi-go: cannot open ${path} — ${errString(err)}`);
    }
  }

  private async pickFiles(): Promise<void> {
    const uris = await vscode.window.showOpenDialog({
      canSelectMany: true,
      openLabel: "Attach to prompt",
    });
    if (uris?.length) {
      this.postAll({ type: "attachmentsAdded", paths: uris.map((u) => u.fsPath) });
    }
  }

  // -- prompts ---------------------------------------------------------------

  /** Command entry point for the sessions tree's "+" button. */
  startNewSession(): void {
    void this.startSession();
  }

  /** Command entry point for the paperclip/@ button on the view title. */
  attachFiles(): void {
    void this.pickFiles();
  }

  private async startSession(): Promise<SessionEntry | undefined> {
    try {
      const entry = await this.client.newSession();
      this.store.register(entry.sessionId);
      this.currentSessionId = entry.sessionId;
      this.postAll({ type: "sessionLoaded", sessionId: entry.sessionId });
      this.sendState();
      this.refresh.fire();
      return entry;
    } catch (err) {
      this.postAll({ type: "error", message: errString(err) });
      return undefined;
    }
  }

  private async runPrompt(text: string, attachments: string[]): Promise<void> {
    if (this.inFlight) {
      this.postAll({ type: "notice", text: "A prompt is already running — stop it first." });
      return;
    }
    let sessionId = this.currentSessionId;
    if (!sessionId) {
      const entry = await this.startSession();
      sessionId = entry?.sessionId;
      if (!sessionId) return;
    }

    const cts = new vscode.CancellationTokenSource();
    this.inFlight = cts;
    this.active.add(sessionId);
    this.updateBadges();

    try {
      if (text === "/help") {
        this.postAll({ type: "userTurn", sessionId, prompt: text });
        this.postAll({ type: "agentChunk", sessionId, text: this.helpText(sessionId) });
        this.postAll({ type: "turnEnd", sessionId });
        return;
      }
      if (text === "/clear") {
        const entry = await this.client.newSession();
        const uri = this.store.uriFor(sessionId);
        if (uri) this.store.rebind(uri, entry.sessionId);
        else this.store.register(entry.sessionId);
        this.currentSessionId = entry.sessionId;
        this.postAll({ type: "sessionLoaded", sessionId: entry.sessionId });
        this.postAll({
          type: "notice",
          text: "Started a fresh session. The previous transcript is still on disk — reopen it from the Sessions tree.",
        });
        this.sendState();
        this.refresh.fire();
        return;
      }
      if (text.startsWith("/")) {
        this.postAll({
          type: "notice",
          text: "Slash commands are forwarded to pi-go as plain text in VS Code.",
        });
      }

      const mentions = await pathsToBlocks(attachments, cts.token);
      const skipNotice = skippedMentionsMarkdown(mentions.skipped);
      if (skipNotice) this.postAll({ type: "notice", text: skipNotice });

      const blocks: Array<{ type: "text"; text: string } | (typeof mentions.blocks)[number]> = [
        { type: "text", text },
      ];
      if (this.client.capabilities?.embeddedContext && mentions.blocks.length) {
        blocks.push(...mentions.blocks);
      }

      this.store.appendUserTurn(sessionId, text);
      this.postAll({ type: "userTurn", sessionId, prompt: text });
      await this.client.prompt(sessionId, blocks, cts.token);
      if (cts.token.isCancellationRequested) {
        this.postAll({ type: "notice", text: "(cancelled)" });
      } else if (!this.store.snapshot(sessionId).some((t) => t.role === "agent")) {
        this.postAll({ type: "agentChunk", sessionId, text: "_(no response)_" });
      }
      this.postAll({ type: "turnEnd", sessionId });
    } catch (err) {
      const msg = errString(err);
      log.error(`prompt failed: ${msg}`);
      this.postAll({ type: "turnEnd", sessionId, error: msg });
    } finally {
      this.inFlight.dispose();
      this.inFlight = undefined;
      this.active.delete(sessionId);
      this.updateBadges();
      this.refresh.fire();
    }
  }

  private helpText(sessionId: string): string {
    const commands = this.client.availableCommands(sessionId);
    if (commands.length === 0) {
      return "_This agent has not advertised any slash commands._";
    }
    const lines = commands.map((c) => `- **/${c.name}** — ${c.description ?? ""}`);
    return `Available commands:\n\n${lines.join("\n")}\n\n_Slash commands are dispatched only in pi-go's TUI today; in VS Code they are forwarded to the model as text (with \`/clear\` and \`/help\` handled by the extension)._`;
  }

  // -- live updates ----------------------------------------------------------

  private onUpdate(update: acp.SessionNotification): void {
    const u = update.update;
    if (u?.sessionUpdate === "available_commands_update") {
      const commands: CommandInfo[] = u.availableCommands.map((c) => ({
        name: c.name,
        description: c.description,
      }));
      this.postAll({ type: "commandsUpdated", sessionId: update.sessionId, commands });
      return;
    }
    // Replays are delivered as a snapshot afterwards; live chunks only.
    if (this.replaying || update.sessionId !== this.currentSessionId) return;

    if (u?.sessionUpdate === "agent_message_chunk") {
      const text = chunkText(update);
      if (text) this.postAll({ type: "agentChunk", sessionId: update.sessionId, text });
      return;
    }
    if (u?.sessionUpdate === "agent_thought_chunk") {
      const text = thoughtText(update);
      if (text) this.postAll({ type: "thoughtChunk", sessionId: update.sessionId, text });
      return;
    }
    if (u?.sessionUpdate === "tool_call" || u?.sessionUpdate === "tool_call_update") {
      // Merge against the recorded state: updates carry only changed fields,
      // so a status-only terminal update must not wipe the name/title/input
      // the start event provided.
      const tool = toolStateOf(u, this.store.toolCall(update.sessionId, u.toolCallId));
      this.postAll({ type: "toolUpdate", sessionId: update.sessionId, tool });
      return;
    }
  }

  // -- snapshots ---------------------------------------------------------------

  private sendState(): void {
    const sessionId = this.currentSessionId;
    const turns = sessionId ? this.store.snapshot(sessionId) : [];
    const commands = sessionId
      ? this.client.availableCommands(sessionId).map((c) => ({ name: c.name, description: c.description }))
      : [];
    this.postAll({
      type: "state",
      sessionId,
      title: sessionId ? this.store.title(sessionId) : undefined,
      turns: turns as unknown as TurnSnapshot[],
      commands,
      streaming: this.active.has(sessionId ?? ""),
      caps: { embeddedContext: this.client.capabilities?.embeddedContext === true },
    });
  }

  // -- plumbing -----------------------------------------------------------------

  private postAll(message: HostToWebview): void {
    for (const view of this.views.values()) {
      void view.webview.postMessage(message);
    }
  }

  private updateBadges(): void {
    const value = this.active.size;
    for (const view of this.views.values()) {
      view.badge =
        value > 0
          ? { value, tooltip: value === 1 ? "1 prompt running" : `${value} prompts running` }
          : undefined;
    }
  }

  private html(webview: vscode.Webview): string {
    const extUri = this.context.extensionUri;
    const styles = webview.asWebviewUri(vscode.Uri.joinPath(extUri, "media", "chat.css"));
    const script = webview.asWebviewUri(vscode.Uri.joinPath(extUri, "dist", "webview.js"));
    const nonce = getNonce();
    const csp = [
      "default-src 'none'",
      `img-src ${webview.cspSource} data:`,
      `style-src ${webview.cspSource} 'unsafe-inline'`,
      `font-src ${webview.cspSource} data:`,
      // cdnjs serves the lazy-loaded mermaid bundle (diagram rendering).
      `script-src 'nonce-${nonce}' https://cdnjs.cloudflare.com`,
    ].join("; ");
    return `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<link rel="stylesheet" href="${styles}">
<title>pi-go</title>
</head>
<body>
<script nonce="${nonce}" src="${script}"></script>
</body>
</html>`;
  }
}

function workspaceCwd(): string {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? process.cwd();
}