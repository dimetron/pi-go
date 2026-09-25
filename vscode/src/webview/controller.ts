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
import { toolCard, type ToolCard, formatDuration } from "./toolCard";
import { Composer } from "./composer";
import { iconButton, mascot } from "./icons";
import { copyButton } from "./clipboard";

export interface ControllerHost {
  post(message: WebviewToHost): void;
  setState(state: { draft?: string; attachments?: string[]; sessionId?: string; welcomeDismissed?: boolean }): void;
  getState(): { draft?: string; attachments?: string[]; sessionId?: string; welcomeDismissed?: boolean } | undefined;
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
    const historyButton = iconButton("history", "Session history", () => this.host.post({ type: "showHistory" }));
    const newButton = iconButton("newChat", "New session", () => this.host.post({ type: "newSession" }));
    const pingButton = iconButton("ping", "Run pi ping", () => this.host.post({ type: "ping" }));
    this.headerTitle.textContent = "Untitled";
    this.header.append(this.headerTitle, historyButton, pingButton, newButton);

    this.transcript = document.createElement("div");
    this.transcript.className = "transcript";
    this.transcript.hidden = true;
    this.transcript.setAttribute("role", "log");
    this.transcript.setAttribute("aria-label", "Conversation");

    this.emptyState = document.createElement("div");
    this.emptyState.className = "empty-state";
    const brand = document.createElement("div");
    brand.className = "welcome-brand";
    const wordmark = document.createElement("h1");
    wordmark.className = "wordmark";
    wordmark.textContent = "pi-go";
    const tagline = document.createElement("p");
    tagline.className = "tagline";
    tagline.textContent = "Your AI coding agent, in the editor";
    const titles = document.createElement("div");
    titles.append(wordmark, tagline);
    brand.append(mascot(root.dataset.mascot), titles);

    const learn = document.createElement("section");
    learn.className = "learn-card";
    learn.hidden = this.host.getState()?.welcomeDismissed === true;
    const learnHeader = document.createElement("div");
    learnHeader.className = "learn-header";
    const learnTitle = document.createElement("h2");
    learnTitle.textContent = "Get started";
    const dismiss = iconButton("close", "Dismiss getting started", () => {
      learn.hidden = true;
      this.host.setState({ welcomeDismissed: true });
    });
    learnHeader.append(learnTitle, dismiss);
    const lessons = document.createElement("div");
    lessons.className = "learn-lessons";
    for (const [label, prompt] of [
      ["Ask Pi-Go to write code", "Help me build a new feature in this project. "],
      ["Explore your codebase", "Explain the architecture of this repository."],
      ["Find and fix a bug", "Help me investigate a bug in this project. "],
      ["Plan a change before editing", "Help me plan a change. Explore the code and propose an approach before editing. "],
    ]) {
      const lesson = document.createElement("button");
      lesson.className = "learn-lesson";
      const mark = document.createElement("span");
      mark.className = "lesson-mark";
      mark.setAttribute("aria-hidden", "true");
      const text = document.createElement("span");
      text.textContent = label;
      lesson.append(mark, text);
      lesson.addEventListener("click", () => {
        this.composer.restore(prompt);
        this.host.setState({ draft: prompt });
        this.composer.focus();
      });
      lessons.append(lesson);
    }
    learn.append(learnHeader, lessons);
    this.emptyState.append(brand, learn);

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
        this.streaming = true;
        this.composer.setStreaming(true);
        if (this.headerTitle.textContent === "Untitled") this.headerTitle.textContent = message.prompt;
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
        if (message.error) this.banner(message.error, message.errorDetail, message.errorSteps);
        break;
      }
      case "commandsUpdated": {
        this.commands = message.commands;
        this.composer.setCommands(message.commands);
        break;
      }
      case "error": {
        this.banner(message.message, message.detail, message.steps);
        break;
      }
      case "pingResult": {
        this.appendPingResult(message.ok, message.title, message.detail);
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
    this.headerTitle.textContent = title && title !== "New session" ? title : "Untitled";
    this.headerTitle.title = this.headerTitle.textContent;
  }

  private isCurrent(sessionId: string): boolean {
    return sessionId === this.currentSessionId;
  }

  private showTranscript(): void {
    this.emptyState.hidden = true;
    this.transcript.hidden = false;
  }

  private showLoading(): void {
    const el = document.createElement("div");
    el.className = "loading";
    el.textContent = "Loading session…";
    this.showTranscript();
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
    this.emptyState.hidden = turns.length > 0;
    this.transcript.hidden = turns.length === 0;
    this.scrollToBottom(true);
  }

  // -- turn rendering ------------------------------------------------------

  private appendUserTurn(prompt: string): void {
    this.showTranscript();
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
    this.showTranscript();
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
    // Thinking can be very verbose. Keep the disclosure collapsed while the
    // model streams; the complete thought remains available on demand.
    part.open = false;
    part.dataset.startedAt = String(Date.now());
    const summary = document.createElement("summary");
    summary.className = "thought-summary";
    const pulse = document.createElement("span");
    pulse.className = "thought-pulse";
    pulse.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.className = "thought-label";
    label.textContent = "Thinking";
    const status = document.createElement("span");
    status.className = "thought-status";
    summary.append(pulse, label, status);
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
        part.querySelector(".thought-pulse")?.remove();
        const label = part.querySelector(".thought-label");
        if (label) label.textContent = "Thought";
        const status = part.querySelector(".thought-status");
        if (status) status.textContent = thoughtDurationLabel((part as HTMLElement).dataset.startedAt);
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
    summary.className = "thought-summary";
    const label = document.createElement("span");
    label.className = "thought-label";
    // Replayed from history: it already ran, and no timing survives a
    // reload, so state it as a fact rather than a live "Thinking…".
    label.textContent = "Thought";
    summary.append(label);
    part.append(summary, node);
    this.agentTurn().append(part);
  }

  private notice(text: string): void {
    const el = document.createElement("div");
    el.className = "notice";
    el.textContent = text;
    this.showTranscript();
    this.transcript.append(el);
    this.scrollToBottom();
  }

  private appendPingResult(ok: boolean, title: string, detail: string): void {
    const el = document.createElement("section");
    el.className = `ping-result${ok ? "" : " failed"}`;
    el.setAttribute("role", ok ? "status" : "alert");
    const heading = document.createElement("strong");
    heading.className = "ping-title";
    heading.textContent = title;
    const body = document.createElement("pre");
    body.className = "ping-detail";
    body.textContent = detail;
    el.append(heading, body);
    this.showTranscript();
    this.transcript.append(el);
    this.scrollToBottom();
  }

  private banner(message: string, detail?: string, steps?: readonly string[]): void {
    const el = document.createElement("section");
    el.className = "error-card";
    el.setAttribute("role", "alert");

    const title = document.createElement("div");
    title.className = "error-title";
    const mark = document.createElement("span");
    mark.className = "error-mark";
    mark.textContent = "!";
    mark.setAttribute("aria-hidden", "true");
    const heading = document.createElement("strong");
    heading.textContent = message;
    title.append(mark, heading);
    el.append(title);

    const body = detail ?? message;
    if (body) {
      const detailEl = document.createElement("pre");
      detailEl.className = "error-detail";
      detailEl.textContent = body;
      el.append(detailEl);
    }

    if (steps?.length) {
      const helpTitle = document.createElement("div");
      helpTitle.className = "error-help-title";
      helpTitle.textContent = "What to do";
      const list = document.createElement("ul");
      list.className = "error-steps";
      for (const step of steps) {
        const item = document.createElement("li");
        item.textContent = step;
        list.append(item);
      }
      el.append(helpTitle, list);
    }

    const actions = document.createElement("div");
    actions.className = "error-actions";
    const settings = document.createElement("button");
    settings.className = "error-action";
    settings.type = "button";
    settings.textContent = "Open Pi-Go settings";
    settings.addEventListener("click", () => this.host.post({ type: "openPiGoSettings" }));
    const logs = document.createElement("button");
    logs.className = "error-action secondary";
    logs.type = "button";
    logs.textContent = "Open output log";
    logs.addEventListener("click", () => this.host.post({ type: "openPiGoLogs" }));
    actions.append(settings, logs);
    el.append(actions);

    this.showTranscript();
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
  decorateCodeBlocks(el);
  // Fire-and-forget: diagrams render when mermaid finishes loading; on
  // failure the source code blocks stay in place.
  void renderMermaidBlocks(el).catch(() => undefined);
  return el;
}

/** "Thought" alone once elapsed time is negligible; "for Ns" past 1s, so a
 *  near-instant thought doesn't get a distracting "for 200ms". */
function thoughtDurationLabel(startedAt: string | undefined): string {
  const started = Number(startedAt);
  if (!Number.isFinite(started)) return "";
  const elapsed = Date.now() - started;
  return elapsed >= 1000 ? `for ${formatDuration(elapsed)}` : "";
}

/** Wrap fenced code blocks with a header bar (language label + copy button).
 *  Mermaid fences are left untouched — renderMermaidBlocks replaces them
 *  with a diagram right after this runs. */
function decorateCodeBlocks(root: HTMLElement): void {
  for (const code of Array.from(root.querySelectorAll("pre > code[class*='language-']"))) {
    if (code.classList.contains("language-mermaid")) continue;
    const pre = code.parentElement;
    if (!(pre instanceof HTMLElement)) continue;
    const lang = code.className.match(/language-(\S+)/)?.[1] ?? "text";

    const wrapper = document.createElement("div");
    wrapper.className = "code-block";
    pre.replaceWith(wrapper);

    const header = document.createElement("div");
    header.className = "code-block-header";
    const label = document.createElement("span");
    label.className = "code-block-lang";
    label.textContent = lang;
    header.append(label, copyButton("Copy code", () => code.textContent ?? ""));

    wrapper.append(header, pre);
  }
}
