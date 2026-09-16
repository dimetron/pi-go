// Bun test setup: mock the `vscode` module before any source import resolves.
// The real module only exists inside the extension host; unit tests import
// source modules that `import * as vscode from "vscode"`, so every test run
// preloads this file (--preload). Grow the stub surface as more modules come
// under test — a missing export should fail loudly here, not silently pass.
import { mock } from "bun:test";

class MockEmitter<T> {
  private listeners = new Set<(e: T) => void>();
  event = (l: (e: T) => void) => {
    this.listeners.add(l);
    return { dispose: () => this.listeners.delete(l) };
  };
  fire(e: T) {
    for (const l of this.listeners) l(e);
  }
  dispose() {
    this.listeners.clear();
  }
}

mock.module("vscode", () => ({
  EventEmitter: MockEmitter,
  Uri: {
    joinPath: (...parts: string[]) => ({ toString: () => parts.join("/") }),
    file: (p: string) => ({ toString: () => `file://${p}`, fsPath: p }),
  },
  workspace: {
    workspaceFolders: [] as unknown[],
    getConfiguration: () => ({ get: () => undefined, has: () => false }),
  },
  window: {
    createOutputChannel: () => ({
      appendLine: () => {},
      show: () => {},
      dispose: () => {},
    }),
  },
  commands: {
    registerCommand: () => ({ dispose: () => {} }),
    executeCommand: () => Promise.resolve(),
  },
  CancellationTokenSource: class {
    token = { isCancellationRequested: false, onCancellationRequested: () => ({ dispose: () => {} }) };
    cancel() {}
    dispose() {}
  },
  ChatResponseTurn: {},
  ChatContext: {},
  MarkdownString: class {
    constructor(public value = "") {}
    appendMarkdown(s: string) { this.value += s; return this; }
  },
  TreeItem: class {
    constructor(public label?: string) {}
  },
  ThemeIcon: class {
    constructor(public id: string) {}
  },
}));