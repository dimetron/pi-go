// Composer: the prompt input at the bottom of the chat view. Slash-command
// popup, @-attachment chips, Enter=send / Shift+Enter=newline, stop button
// while a prompt is in flight.

import type { CommandInfo } from "../shared/protocol";

export interface ComposerCallbacks {
  onSend(text: string, attachments: string[]): void;
  onCancel(): void;
  onDraft(text: string): void;
  onRequestFilePicker(): void;
}

export class Composer {
  readonly root: HTMLElement;
  private readonly textarea: HTMLTextAreaElement;
  private readonly chips: HTMLElement;
  private readonly popup: HTMLElement;
  private readonly sendButton: HTMLButtonElement;
  private readonly stopButton: HTMLButtonElement;
  private readonly attachments: string[] = [];
  private commands: CommandInfo[] = [];
  private streaming = false;
  private popupIndex = 0;
  private popupToken = "";
  private draftTimer: number | undefined;

  constructor(parent: HTMLElement, private readonly callbacks: ComposerCallbacks) {
    this.root = document.createElement("div");
    this.root.className = "composer";

    this.chips = document.createElement("div");
    this.chips.className = "composer-chips";

    const inputRow = document.createElement("div");
    inputRow.className = "composer-row";

    const attach = document.createElement("button");
    attach.className = "composer-attach";
    attach.title = "Attach file (@)";
    attach.textContent = "@";
    attach.addEventListener("click", () => this.callbacks.onRequestFilePicker());

    this.textarea = document.createElement("textarea");
    this.textarea.className = "composer-input";
    this.textarea.rows = 1;
    this.textarea.placeholder = "Ask pi-go…  (@ to attach, / for commands)";
    this.textarea.addEventListener("input", () => {
      this.autosize();
      this.scheduleDraft();
      this.updatePopup();
    });
    this.textarea.addEventListener("keydown", (e) => this.onKeyDown(e));

    const actions = document.createElement("div");
    actions.className = "composer-actions";
    this.sendButton = document.createElement("button");
    this.sendButton.className = "composer-send";
    this.sendButton.title = "Send (Enter)";
    this.sendButton.textContent = "➤";
    this.sendButton.addEventListener("click", () => this.send());
    this.stopButton = document.createElement("button");
    this.stopButton.className = "composer-stop";
    this.stopButton.title = "Stop";
    this.stopButton.textContent = "⏹";
    this.stopButton.addEventListener("click", () => this.callbacks.onCancel());
    actions.append(this.sendButton, this.stopButton);

    inputRow.append(attach, this.textarea, actions);

    this.popup = document.createElement("div");
    this.popup.className = "composer-popup";
    this.popup.hidden = true;

    this.root.append(this.popup, this.chips, inputRow);
    parent.append(this.root);

    // Click anywhere in the composer focuses the text area.
    this.root.addEventListener("mousedown", (e) => {
      if (e.target === this.root || e.target === this.chips) this.textarea.focus();
    });
    document.addEventListener("click", (e) => {
      if (!this.popup.contains(e.target as Node)) this.popup.hidden = true;
    });
    this.updateButtons();
  }

  setStreaming(streaming: boolean): void {
    this.streaming = streaming;
    this.updateButtons();
  }

  setCommands(commands: CommandInfo[]): void {
    this.commands = commands;
    if (!this.popup.hidden) this.updatePopup();
  }

  addAttachment(path: string): void {
    if (!this.attachments.includes(path)) this.attachments.push(path);
    this.renderChips();
  }

  restore(text: string): void {
    this.textarea.value = text;
    this.autosize();
  }

  focus(): void {
    this.textarea.focus();
  }

  private updateButtons(): void {
    this.sendButton.hidden = this.streaming;
    this.stopButton.hidden = !this.streaming;
  }

  private onKeyDown(e: KeyboardEvent): void {
    if (!this.popup.hidden && (e.key === "ArrowUp" || e.key === "ArrowDown" || e.key === "Tab")) {
      e.preventDefault();
      const items = this.popup.querySelectorAll(".popup-item");
      const max = items.length - 1;
      this.popupIndex = e.key === "ArrowUp"
        ? Math.max(0, this.popupIndex - 1)
        : Math.min(max, this.popupIndex + 1);
      items.forEach((el, i) => el.classList.toggle("selected", i === this.popupIndex));
      if (e.key === "Tab") this.pick(this.popupIndex);
      return;
    }
    if (!this.popup.hidden && e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      this.pick(this.popupIndex);
      return;
    }
    if (e.key === "Escape") {
      this.popup.hidden = true;
      return;
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      this.send();
    }
  }

  private pick(index: number): void {
    const item = this.popup.querySelectorAll(".popup-item")[index] as HTMLElement | undefined;
    if (!item) return;
    const name = item.dataset.name ?? "";
    // Replace the partial "/token" before the caret with the full command.
    const caret = this.textarea.selectionStart ?? this.textarea.value.length;
    const before = this.textarea.value.slice(0, caret);
    const tokenStart = before.lastIndexOf(this.popupToken);
    if (tokenStart >= 0) {
      const after = this.textarea.value.slice(caret);
      this.textarea.value = `${this.textarea.value.slice(0, tokenStart)}/${name} ${after}`;
      const pos = tokenStart + name.length + 2;
      this.textarea.setSelectionRange(pos, pos);
    }
    this.popup.hidden = true;
    this.autosize();
    this.textarea.focus();
  }

  private updatePopup(): void {
    // A "/word" fragment running back to whitespace at the caret opens the list.
    const caret = this.textarea.selectionStart ?? 0;
    const before = this.textarea.value.slice(0, caret);
    const match = /(?:^|\s)(\/[A-Za-z0-9_-]*)$/.exec(before);
    if (!match) {
      this.popup.hidden = true;
      return;
    }
    const token = match[1].toLowerCase();
    this.popupToken = match[1];
    const items = this.commands.filter((c) => `/${c.name}`.toLowerCase().startsWith(token));
    if (items.length === 0) {
      this.popup.hidden = true;
      return;
    }
    this.popupIndex = Math.min(this.popupIndex, items.length - 1);
    this.popup.replaceChildren(
      ...items.map((c, i) => {
        const el = document.createElement("div");
        el.className = `popup-item${i === this.popupIndex ? " selected" : ""}`;
        el.dataset.name = c.name;
        const label = document.createElement("span");
        label.className = "popup-name";
        label.textContent = `/${c.name}`;
        const detail = document.createElement("span");
        detail.className = "popup-detail";
        detail.textContent = c.description ?? "";
        el.append(label, detail);
        el.addEventListener("mousedown", (e) => {
          e.preventDefault();
          this.pick(i);
        });
        return el;
      }),
    );
    this.popup.hidden = false;
  }

  private send(): void {
    const text = this.textarea.value.trim();
    if (!text || this.streaming) return;
    this.textarea.value = "";
    this.autosize();
    const files = [...this.attachments];
    this.attachments.length = 0;
    this.renderChips();
    this.popup.hidden = true;
    this.callbacks.onSend(text, files);
  }

  private scheduleDraft(): void {
    window.clearTimeout(this.draftTimer);
    this.draftTimer = window.setTimeout(() => this.callbacks.onDraft(this.textarea.value), 250);
  }

  private renderChips(): void {
    this.chips.replaceChildren(
      ...this.attachments.map((path) => {
        const chip = document.createElement("span");
        chip.className = "chip";
        const label = document.createElement("span");
        label.textContent = path.split("/").pop() ?? path;
        label.title = path;
        const remove = document.createElement("button");
        remove.className = "chip-remove";
        remove.textContent = "×";
        remove.title = "Remove";
        remove.addEventListener("click", () => {
          const idx = this.attachments.indexOf(path);
          if (idx >= 0) this.attachments.splice(idx, 1);
          this.renderChips();
        });
        chip.append(label, remove);
        return chip;
      }),
    );
    this.chips.hidden = this.attachments.length === 0;
  }

  private autosize(): void {
    this.textarea.style.height = "auto";
    this.textarea.style.height = `${Math.min(this.textarea.scrollHeight, 200)}px`;
  }
}