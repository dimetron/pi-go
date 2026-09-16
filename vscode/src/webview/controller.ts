// DOM controller for the chat view. Renders the transcript, manages the
// composer and message routing. Imports nothing from the extension host —
// messages arrive via the router in main.ts.

import type {
  CommandInfo,
  HostToWebview,
  TurnSnapshot,
  ToolSnapshot,
  WebviewToHost,
} from "../shared/protocol";
import { renderMarkdown } from "./markdown";
import { renderMermaidBlocks } from "./mermaid";
import { toolCard, type ToolCard } from "./toolCard";
import { Composer } from "./composer";

export interface ControllerHost {
  post(message: WebviewToHost): void;
  setState(state: { draft?: string; attachments?: string[]; sessionId?: string }): void;
  getState(): { draft?: string; attachments?: string[]; sessionId?: string } | undefined;
}

export class ChatController {
  private currentSessionId?: string;
  private streaming = false;
  private commands: CommandInfo[] = [];
  private cards = new Map<string, ToolCard>();

  private readonly transcript: HTMLElement;
  private readonly header: HTMLElement;
  private readonly headerTitle: HTMLElement;
  private readonly emptyState: HTMLElement;
  private readonly composer: Composer;
  private openTextPart?: HTMLElement;
  private openThoughtPart?: HTMLElement;

  constructor(
    root: HTMLElement,
    private readonly host: ControllerHost,
  ) {
    this.header = document.createElement("div");
    this.header.className = "chat-header";
    this.headerTitle = document.createElement("span");
    this.headerTitle.className = "session-title";
    const newButton = document.createElement("button");
    newButton.className = "header-new";
    newButton.title = "New session";
    newButton.textContent = "+ New";
    newButton.addEventListener("click", () => this.host.post({ type: "newSession" }));
    this.header.append(this.headerTitle, newButton);

    this.transcript = document.createElement("div");
    this.transcript.className = "transcript";

    this.emptyState = document.createElement("div");
    this.emptyState.className = "empty-state";
    const start = document.createElement("button");
    start.textContent = "Start a session";
    start.addEventListener("click", () => this.host.post({ type: "newSession" }));
    const hint = document.createElement("p");
    hint.textContent = "Ask pi-go anything, or start a session and reopen saved ones from the Sessions tree.";
    this.emptyState.append(hint, start);

    const composerHost = document.createElement("div");
    composerHost.className = "composer-host";
    this.composer = new Composer(composerHost, {
      onSend: (text, attachments) =>
        this.host.post({ type: "prompt", sessionId: this.currentSessionId, text, attachments }),
      onCancel: () => {
        if (this.currentSessionId) this.host.post({ type: "cancel", sessionId: this.currentSessionId });
      },
      onDraft: (text) => this.host.setState({ draft: text, sessionId: this.currentSessionId }),
      onRequestFilePicker: () => this.host.post({ type: "requestFilePicker" }),
    });

    root.append(this.header, this.transcript, this.emptyState, composerHost);

    // New tool output / chunks only scroll when the user is at the bottom.
    this.transcript.addEventListener("scroll", () => this.updatePinned(), { passive: true });

    const restored = this.host.getState();
    if (restored?.draft) this.composer.restore(restored.draft);
  }

  ready(): void {
    this.host.post({ type: "ready" });
  }

  focus(): void {
    this.composer.focus();
  }

  // -- message router ------------------------------------------------------

  handle(message: HostToWebview): void {
    switch (message.type) {
      case "state": {
        this.setSession(message.sessionId, message.title);
        this.streaming = message.streaming;
        this.composer.setStreaming(message.streaming);
        this.commands = message.commands;
        this.composer.setCommands(message.commands);
        this.renderTurns(message.turns);
        break;
      }
      case "sessionLoaded": {
        this.setSession(message.sessionId, message.title);
        this.transcript.replaceChildren();
        this.cards.clear();
        this.showLoading();
        break;
      }
      case "replayStarted": {
        this.cards.clear();
        this.transcript.replaceChildren();
        break;
      }
      case "userTurn": {
        if (!this.isCurrent(message.sessionId)) return;
        this.openTextPart = undefined;
        this.openThoughtPart = undefined;
        this.appendUserTurn(message.prompt);
        break;
      }
      case "agentChunk": {
        if (!this.isCurrent(message.sessionId)) return;
        this.openThoughtPart = undefined;
        this.appendStream(message.text, "text");
        break;
      }
      case "thoughtChunk": {
        if (!this.isCurrent(message.sessionId)) return;
        this.openTextPart = undefined;
        this.appendStream(message.text, "thought");
        break;
      }
      case "toolUpdate": {
        if (!this.isCurrent(message.sessionId)) return;
        this.openTextPart = undefined;
        this.openThoughtPart = undefined;
        this.upsertTool(message.tool);
        break;
      }
      case "turnEnd": {
        if (!this.isCurrent(message.sessionId)) return;
        this.streaming = false;
        this.composer.setStreaming(false);
        this.finalizeStreamedParts();
        if (message.error) this.banner(message.error);
        break;
      }
      case "commandsUpdated": {
        this.commands = message.commands;
        this.composer.setCommands(message.commands);
        break;
      }
      case "error": {
        this.banner(message.message);
        break;
      }
      case "notice": {
        this.notice(message.text);
        break;
      }
      case "attachmentsAdded": {
        for (const path of message.paths) this.composer.addAttachment(path);
        this.composer.focus();
        break;
      }
      default:
        break;
    }
  }

  // -- session / layout ----------------------------------------------------

  private setSession(sessionId: string | undefined, title: string | undefined): void {
    this.currentSessionId = sessionId;
    this.headerTitle.textContent = title ?? (sessionId ? sessionId : "");
    this.header.hidden = !sessionId;
    this.emptyState.hidden = !!sessionId;
  }

  private isCurrent(sessionId: string): boolean {
    return sessionId === this.currentSessionId;
  }

  private showLoading(): void {
    const el = document.createElement("div");
    el.className = "loading";
    el.textContent = "Loading session…";
    this.transcript.append(el);
  }

  private renderTurns(turns: readonly TurnSnapshot[]): void {
    this.transcript.replaceChildren();
    this.cards.clear();
    this.openTextPart = undefined;
    this.openThoughtPart = undefined;
    for (const turn of turns) {
      if (turn.role === "user") {
        this.appendUserTurn(turn.prompt);
        continue;
      }
      for (const part of turn.parts) {
        if (part.kind === "text") this.appendAgentTextBlock(markdownBlock(part.text));
        else if (part.kind === "thought") this.appendThoughtBlock(markdownBlock(part.text));
        else this.upsertTool(part.tool);
      }
    }
    this.scrollToBottom(true);
  }

  // -- turn rendering ------------------------------------------------------

  private appendUserTurn(prompt: string): void {
    const turn = document.createElement("div");
    turn.className = "turn user";
    const bubble = document.createElement("div");
    bubble.className = "bubble";
    bubble.textContent = prompt;
    turn.append(bubble);
    this.transcript.append(turn);
    this.scrollToBottom();
  }

  /** Current agent turn, creating one if needed. */
  private agentTurn(): HTMLElement {
    const last = this.transcript.lastElementChild;
    if (last instanceof HTMLElement && last.classList.contains("turn") && last.classList.contains("agent")) {
      return last;
    }
    const turn = document.createElement("div");
    turn.className = "turn agent";
    this.transcript.append(turn);
    return turn;
  }

  private appendStream(text: string, kind: "text" | "thought"): void {
    const turn = this.agentTurn();
    let part =
      kind === "text"
        ? this.openTextPart
        : this.openThoughtPart;
    if (!part) {
      part = this.newStreamPart(kind);
      turn.append(part);
      if (kind === "text") this.openTextPart = part;
      else this.openThoughtPart = part;
    }
    const target = part.querySelector(".stream-body");
    if (target) target.textContent += text;
    this.scrollToBottom();
  }

  private newStreamPart(kind: "text" | "thought"): HTMLElement {
    if (kind === "text") {
      const part = document.createElement("div");
      part.className = "part text streaming";
      part.textContent = "";
      const body = document.createElement("span");
      body.className = "stream-body";
      part.append(body);
      return part;
    }
    const part = document.createElement("details");
    part.className = "part thought streaming";
    part.open = true;
    const summary = document.createElement("summary");
    summary.textContent = "Thinking";
    const body = document.createElement("div");
    body.className = "thought-body stream-body";
    part.append(summary, body);
    return part;
  }

  private finalizeStreamedParts(): void {
    for (const part of this.transcript.querySelectorAll(".part.streaming")) {
      const body = part.querySelector(".stream-body");
      const text = body?.textContent ?? "";
      part.classList.remove("streaming");
      if (part.classList.contains("thought")) {
        part.querySelector(".thought-body")?.replaceChildren();
        const rendered = markdownBlock(text);
        rendered.className = "thought-body md";
        body?.replaceWith(rendered);
        (part as HTMLDetailsElement).open = false;
      } else {
        part.replaceChildren(markdownBlock(text));
      }
    }
    this.openTextPart = undefined;
    this.openThoughtPart = undefined;
    this.scrollToBottom();
  }

  private upsertTool(tool: ToolSnapshot): void {
    const existing = this.cards.get(tool.toolCallId);
    if (existing) {
      existing.update(tool);
      return;
    }
    const card = toolCard(tool, (path) => this.host.post({ type: "revealFile", path }));
    this.cards.set(tool.toolCallId, card);
    this.agentTurn().append(card.root);
    this.scrollToBottom();
  }

  private appendAgentTextBlock(node: HTMLElement): void {
    const part = document.createElement("div");
    part.className = "part text";
    part.append(node);
    this.agentTurn().append(part);
  }

  private appendThoughtBlock(node: HTMLElement): void {
    const part = document.createElement("details");
    part.className = "part thought";
    const summary = document.createElement("summary");
    summary.textContent = "Thinking";
    part.append(summary, node);
    this.agentTurn().append(part);
  }

  private notice(text: string): void {
    const el = document.createElement("div");
    el.className = "notice";
    el.textContent = text;
    this.transcript.append(el);
    this.scrollToBottom();
  }

  private banner(message: string): void {
    const el = document.createElement("div");
    el.className = "banner";
    el.textContent = message;
    this.transcript.append(el);
    this.scrollToBottom();
  }

  // -- scrolling -----------------------------------------------------------

  private pinned = true;

  private updatePinned(): void {
    const el = this.transcript;
    this.pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }

  private scrollToBottom(force = false): void {
    if (!force && !this.pinned) return;
    this.transcript.scrollTop = this.transcript.scrollHeight;
  }
}

/** A static markdown-rendered block; mermaid fences become diagrams. */
function markdownBlock(text: string): HTMLElement {
  const el = document.createElement("div");
  el.className = "md";
  el.innerHTML = renderMarkdown(text);
  // Fire-and-forget: diagrams render when mermaid finishes loading; on
  // failure the source code blocks stay in place.
  void renderMermaidBlocks(el).catch(() => undefined);
  return el;
}