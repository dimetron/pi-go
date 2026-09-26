// DOM controller for the chat view. Renders the transcript, manages the
// composer and message routing. Imports nothing from the extension host —
// messages arrive via the router in main.ts.
//
// Sessions are tabs: each open session owns its own transcript element,
// scroll pin, streaming parts, tool cards, commands and draft. The host is
// the source of truth for the tab set (tabs messages reconcile the strip);
// per-session messages (chunks, turns) route by sessionId.

import type {
  CommandInfo,
  HostToWebview,
  TabInfo,
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

/** One open session tab: its DOM, streaming state, and composer draft. */
interface TabState {
  readonly sessionId: string;
  title?: string;
  streaming: boolean;
  commands: CommandInfo[];
  /** The transcript element; kept in the DOM (hidden) so scroll survives switches. */
  readonly transcript: HTMLElement;
  readonly cards: Map<string, ToolCard>;
  readonly button: HTMLButtonElement;
  openTextPart?: HTMLElement;
  openThoughtPart?: HTMLElement;
  pinned: boolean;
  loading: boolean;
  draft?: string;
}

export class ChatController {
  private readonly tabs = new Map<string, TabState>();
  private activeId?: string;
  private readonly header: HTMLElement;
  private readonly headerTitle: HTMLElement;
  private readonly tabStrip: HTMLElement;
  private readonly viewStack: HTMLElement;
  private readonly emptyState: HTMLElement;
  private readonly composer: Composer;

  constructor(
    root: HTMLElement,
    private readonly host: ControllerHost,
  ) {
    this.header = document.createElement("div");
    this.header.className = "chat-header";
    this.headerTitle = document.createElement("span");
    this.headerTitle.className = "session-title";
    const historyButton = iconButton("history", "Session history", () => this.host.post({ type: "showHistory" }));
    const pingButton = iconButton("ping", "Run pi ping", () => this.host.post({ type: "ping" }));
    const newButton = iconButton("newChat", "New session", () => this.host.post({ type: "newSession" }));
    this.headerTitle.textContent = "Untitled";
    this.header.append(this.headerTitle, historyButton, pingButton, newButton);

    this.tabStrip = document.createElement("div");
    this.tabStrip.className = "tab-strip";
    this.tabStrip.hidden = true;

    this.viewStack = document.createElement("div");
    this.viewStack.className = "view-stack";

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
        this.host.post({ type: "prompt", sessionId: this.activeId, text, attachments }),
      onCancel: () => {
        if (this.activeId) this.host.post({ type: "cancel", sessionId: this.activeId });
      },
      onDraft: (text) => this.host.setState({ draft: text, sessionId: this.activeId }),
      onRequestFilePicker: () => this.host.post({ type: "requestFilePicker" }),
    });

    root.append(this.header, this.tabStrip, this.viewStack, this.emptyState, composerHost);

    // New tool output / chunks only scroll when the user is at the bottom.
    // Scroll events do not bubble, but they do capture, so the stack listens
    // on behalf of every tab transcript inside it.
    this.viewStack.addEventListener(
      "scroll",
      () => this.updatePinned(),
      { passive: true, capture: true },
    );

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
        const tab = message.sessionId ? this.ensureTab(message.sessionId) : undefined;
        if (tab) {
          tab.commands = message.commands;
          tab.streaming = message.streaming;
          if (message.title) tab.title = message.title;
          // A streaming tab's live DOM already holds every chunk (it hears the
          // same notifications the store does); re-rendering the snapshot
          // would split the streaming part into static + live halves.
          if (!(tab.streaming && tab.transcript.children.length > 0)) {
            this.renderTurns(tab, message.turns);
          }
          this.setTabLabel(tab);
        }
        this.showTab(message.sessionId);
        this.composer.setStreaming(tab?.streaming === true);
        this.composer.setCommands(tab?.commands ?? []);
        break;
      }
      case "tabs": {
        this.reconcileTabs(message.tabs, message.activeSessionId);
        break;
      }
      case "sessionLoaded": {
        const tab = this.ensureTab(message.sessionId);
        if (message.title) tab.title = message.title;
        this.setTabLabel(tab);
        tab.cards.clear();
        tab.openTextPart = undefined;
        tab.openThoughtPart = undefined;
        tab.loading = true;
        this.clearTranscript(tab, "Loading session…");
        break;
      }
      case "replayStarted": {
        const tab = this.tabs.get(message.sessionId);
        if (!tab) return;
        tab.cards.clear();
        tab.openTextPart = undefined;
        tab.openThoughtPart = undefined;
        tab.loading = false;
        this.clearTranscript(tab);
        break;
      }
      case "userTurn": {
        const tab = this.tabs.get(message.sessionId);
        if (!tab) return;
        tab.openTextPart = undefined;
        tab.openThoughtPart = undefined;
        tab.streaming = true;
        if (tab.sessionId === this.activeId) this.composer.setStreaming(true);
        if (!tab.title || tab.title === "Untitled") {
          tab.title = message.prompt;
          this.setTabLabel(tab);
          if (tab.sessionId === this.activeId) {
            this.headerTitle.textContent = tab.title;
            this.headerTitle.title = tab.title;
          }
        }
        this.appendUserTurn(tab, message.prompt);
        break;
      }
      case "agentChunk": {
        const tab = this.tabs.get(message.sessionId);
        if (!tab) return;
        tab.openThoughtPart = undefined;
        this.appendStream(tab, message.text, "text");
        break;
      }
      case "thoughtChunk": {
        const tab = this.tabs.get(message.sessionId);
        if (!tab) return;
        tab.openTextPart = undefined;
        this.appendStream(tab, message.text, "thought");
        break;
      }
      case "toolUpdate": {
        const tab = this.tabs.get(message.sessionId);
        if (!tab) return;
        tab.openTextPart = undefined;
        tab.openThoughtPart = undefined;
        this.upsertTool(tab, message.tool);
        break;
      }
      case "turnEnd": {
        const tab = this.tabs.get(message.sessionId);
        if (!tab) return;
        tab.streaming = false;
        if (tab.sessionId === this.activeId) this.composer.setStreaming(false);
        this.finalizeStreamedParts(tab);
        if (message.error) this.banner(message.error, message.errorDetail, message.errorSteps);
        break;
      }
      case "commandsUpdated": {
        const tab = this.tabs.get(message.sessionId);
        if (!tab) return;
        tab.commands = message.commands;
        if (tab.sessionId === this.activeId) this.composer.setCommands(message.commands);
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
        const tab = (message.sessionId ? this.tabs.get(message.sessionId) : undefined)
          ?? (this.activeId ? this.tabs.get(this.activeId) : undefined);
        if (tab) this.notice(tab, message.text);
        else this.standaloneNotice(message.text);
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

  // -- tabs ------------------------------------------------------------------

  /** Create the tab state (DOM + strip button) if it is not open yet. */
  private ensureTab(sessionId: string): TabState {
    const existing = this.tabs.get(sessionId);
    if (existing) return existing;

    const transcript = document.createElement("div");
    transcript.className = "transcript";
    transcript.hidden = true;
    transcript.setAttribute("role", "log");
    transcript.setAttribute("aria-label", "Conversation");

    const button = document.createElement("button");
    button.className = "tab";
    button.type = "button";
    const label = document.createElement("span");
    label.className = "tab-label";
    const dot = document.createElement("span");
    dot.className = "tab-dot";
    dot.hidden = true;
    dot.setAttribute("aria-hidden", "true");
    const close = document.createElement("span");
    close.className = "tab-close";
    close.setAttribute("aria-hidden", "true");
    close.textContent = "×";
    button.append(dot, label, close);
    button.addEventListener("click", (e) => {
      if (close.contains(e.target as Node)) {
        this.host.post({ type: "closeTab", sessionId });
        return;
      }
      this.host.post({ type: "activateTab", sessionId });
    });

    const tab: TabState = {
      sessionId,
      streaming: false,
      commands: [],
      transcript,
      cards: new Map(),
      button,
      pinned: true,
      loading: false,
    };
    this.tabs.set(sessionId, tab);
    // A session now owns the view: sweep any standalone banner/notice left
    // over from before any tab existed — in the flex stack it would sit
    // beside the transcript instead of being replaced by it.
    for (const el of Array.from(this.viewStack.children)) {
      if (el instanceof HTMLElement && el.dataset.standalone === "true") el.remove();
    }
    this.viewStack.append(transcript);
    // Keep strip order = host order: new tabs sit before the trailing "+".
    this.tabStrip.insertBefore(button, this.tabStrip.querySelector(".tab-new"));
    this.setTabLabel(tab);
    return tab;
  }

  /** Make the strip match the host's tab list exactly. */
  private reconcileTabs(tabs: readonly TabInfo[], activeSessionId: string | undefined): void {
    const open = new Set(tabs.map((t) => t.sessionId));
    for (const [id, tab] of this.tabs) {
      if (open.has(id)) continue;
      tab.transcript.remove();
      tab.button.remove();
      this.tabs.delete(id);
    }
    for (const info of tabs) {
      const tab = this.ensureTab(info.sessionId);
      tab.streaming = info.streaming;
      if (info.title) tab.title = info.title;
      this.setTabLabel(tab);
    }
    if (tabs.length === 0) {
      this.tabStrip.hidden = true;
      return;
    }
    this.tabStrip.hidden = false;
    if (!this.tabStrip.querySelector(".tab-new")) {
      const add = document.createElement("button");
      add.className = "tab-new";
      add.type = "button";
      add.title = "New session";
      add.setAttribute("aria-label", "New session");
      add.textContent = "+";
      add.addEventListener("click", () => this.host.post({ type: "newSession" }));
      this.tabStrip.append(add);
    }
    this.showTab(activeSessionId);
  }

  /** Switch the visible tab; keeps a per-tab draft. A no-op when the tab is
   *  already visible — otherwise every state message would wipe the composer. */
  private showTab(sessionId: string | undefined): void {
    const tab = sessionId ? this.tabs.get(sessionId) : undefined;
    if (this.activeId === sessionId && tab) {
      this.emptyState.hidden = tab.loading || tab.streaming || tab.transcript.children.length > 0;
      return;
    }
    if (this.activeId) {
      const prev = this.tabs.get(this.activeId);
      if (prev) prev.draft = this.composer.value();
    }
    // First tab ever shown: seed its draft with the persisted one, so a
    // webview reload does not wipe what the user had typed.
    const persisted = !this.activeId ? this.host.getState()?.draft : undefined;
    this.activeId = tab ? sessionId : undefined;

    for (const t of this.tabs.values()) t.transcript.hidden = t !== tab;
    const hasContent =
      tab !== undefined && (tab.loading || tab.streaming || tab.transcript.children.length > 0);
    this.emptyState.hidden = hasContent;
    this.tabStrip.querySelectorAll(".tab").forEach((el) => {
      el.classList.toggle("active", (el as HTMLButtonElement).dataset.sessionId === sessionId);
    });

    if (!tab) {
      this.headerTitle.textContent = "Untitled";
      this.headerTitle.title = "Untitled";
      return;
    }
    this.headerTitle.textContent = tab.title && tab.title !== "New session" ? tab.title : "Untitled";
    this.headerTitle.title = this.headerTitle.textContent;
    this.composer.restore(tab.draft ?? persisted ?? "");
    this.composer.setStreaming(tab.streaming);
    this.composer.setCommands(tab.commands);
    // A tab that streamed while hidden sits where it was pinned; catch up.
    if (tab.pinned) this.scrollToBottom(tab, true);
  }

  private setTabLabel(tab: TabState): void {
    const label = tab.button.querySelector(".tab-label");
    if (label) label.textContent = tab.title || "Untitled";
    tab.button.dataset.sessionId = tab.sessionId;
    tab.button.title = tab.title || "Untitled";
    const dot = tab.button.querySelector(".tab-dot") as HTMLElement | null;
    if (dot) dot.hidden = !tab.streaming;
  }

  // -- turn rendering ------------------------------------------------------

  private clearTranscript(tab: TabState, loadingText?: string): void {
    tab.transcript.replaceChildren();
    if (loadingText) {
      const el = document.createElement("div");
      el.className = "loading";
      el.textContent = loadingText;
      tab.transcript.append(el);
    }
    this.showTab(this.activeId);
  }

  private renderTurns(tab: TabState, turns: readonly TurnSnapshot[]): void {
    tab.transcript.replaceChildren();
    tab.cards.clear();
    tab.openTextPart = undefined;
    tab.openThoughtPart = undefined;
    tab.loading = false;
    for (const turn of turns) {
      if (turn.role === "user") {
        this.appendUserTurn(tab, turn.prompt);
        continue;
      }
      for (const part of turn.parts) {
        if (part.kind === "text") this.appendAgentTextBlock(tab, markdownBlock(part.text));
        else if (part.kind === "thought") this.appendThoughtBlock(tab, markdownBlock(part.text));
        else this.upsertTool(tab, part.tool);
      }
    }
  }

  private appendUserTurn(tab: TabState, prompt: string): void {
    tab.transcript.hidden = false;
    this.emptyState.hidden = true;
    const turn = document.createElement("div");
    turn.className = "turn user";
    const bubble = document.createElement("div");
    bubble.className = "bubble";
    bubble.textContent = prompt;
    turn.append(bubble);
    tab.transcript.append(turn);
    this.scrollToBottom(tab);
  }

  /** Current agent turn of a tab, creating one if needed. */
  private agentTurn(tab: TabState): HTMLElement {
    const last = tab.transcript.lastElementChild;
    if (last instanceof HTMLElement && last.classList.contains("turn") && last.classList.contains("agent")) {
      return last;
    }
    tab.transcript.hidden = false;
    this.emptyState.hidden = true;
    const turn = document.createElement("div");
    turn.className = "turn agent";
    tab.transcript.append(turn);
    return turn;
  }

  private appendStream(tab: TabState, text: string, kind: "text" | "thought"): void {
    const turn = this.agentTurn(tab);
    let part =
      kind === "text"
        ? tab.openTextPart
        : tab.openThoughtPart;
    if (!part) {
      part = this.newStreamPart(kind);
      turn.append(part);
      if (kind === "text") tab.openTextPart = part;
      else tab.openThoughtPart = part;
    }
    const target = part.querySelector(".stream-body");
    if (target) target.textContent += text;
    this.scrollToBottom(tab);
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

  private finalizeStreamedParts(tab: TabState): void {
    for (const part of tab.transcript.querySelectorAll(".part.streaming")) {
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
    tab.openTextPart = undefined;
    tab.openThoughtPart = undefined;
    this.scrollToBottom(tab);
  }

  private upsertTool(tab: TabState, tool: ToolSnapshot): void {
    const existing = tab.cards.get(tool.toolCallId);
    if (existing) {
      existing.update(tool);
      return;
    }
    const card = toolCard(tool, (path) => this.host.post({ type: "revealFile", path }));
    tab.cards.set(tool.toolCallId, card);
    this.agentTurn(tab).append(card.root);
    this.scrollToBottom(tab);
  }

  private appendAgentTextBlock(tab: TabState, node: HTMLElement): void {
    const part = document.createElement("div");
    part.className = "part text";
    part.append(node);
    this.agentTurn(tab).append(part);
  }

  private appendThoughtBlock(tab: TabState, node: HTMLElement): void {
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
    this.agentTurn(tab).append(part);
  }

  private notice(tab: TabState, text: string): void {
    const el = document.createElement("div");
    el.className = "notice";
    el.textContent = text;
    tab.transcript.hidden = false;
    if (tab.sessionId === this.activeId) this.emptyState.hidden = true;
    tab.transcript.append(el);
    this.scrollToBottom(tab);
  }

  /** A notice with no tab to live in (no session open yet). */
  private standaloneNotice(text: string): void {
    const el = document.createElement("div");
    el.className = "notice";
    el.dataset.standalone = "true";
    el.textContent = text;
    this.viewStack.append(el);
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
    this.appendStandaloneOrActive(el);
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

    this.appendStandaloneOrActive(el);
  }

  /** Errors and ping results land in the active tab's transcript, or in the
   *  view stack when no session is open at all. */
  private appendStandaloneOrActive(el: HTMLElement): void {
    const tab = this.activeId ? this.tabs.get(this.activeId) : undefined;
    if (tab) {
      tab.transcript.hidden = false;
      this.emptyState.hidden = true;
      tab.transcript.append(el);
      this.scrollToBottom(tab);
    } else {
      el.dataset.standalone = "true";
      this.viewStack.append(el);
    }
  }

  // -- scrolling -----------------------------------------------------------

  private updatePinned(): void {
    const tab = this.activeId ? this.tabs.get(this.activeId) : undefined;
    if (!tab) return;
    const el = tab.transcript;
    tab.pinned = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
  }

  private scrollToBottom(tab: TabState, force = false): void {
    if (tab.sessionId !== this.activeId) return;
    if (!force && !tab.pinned) return;
    tab.transcript.scrollTop = tab.transcript.scrollHeight;
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