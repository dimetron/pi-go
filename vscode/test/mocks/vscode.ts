// Shared test double for the `vscode` module. Wired in via the "^vscode$" alias
// in vitest.config.mts — never imported by runtime code. One instance per test
// file (vitest isolates module registries), so tests mutate state freely via
// the __* helpers.

// -- utils -------------------------------------------------------------------

type Fn = (...args: unknown[]) => unknown;

/** Minimal spy: records calls and returns a preset value. */
export function spy<T>(impl?: (...args: unknown[]) => T): ((...args: unknown[]) => T) & { calls: unknown[][] } {
  const fn = ((...args: unknown[]) => fn.calls.push(args) && impl?.(...args)) as ReturnType<typeof spy>;
  fn.calls = [];
  return fn;
}

const disposable = () => ({ disposed: false, dispose() { this.disposed = true; } });

// -- Uri ---------------------------------------------------------------------

export class Uri {
  scheme: string;
  path: string;
  constructor(scheme: string, path: string) {
    this.scheme = scheme;
    this.path = path;
  }
  get fsPath(): string {
    return this.path;
  }
  toString(): string {
    return `${this.scheme}:${this.path}`;
  }
  static file(p: string): Uri {
    return new Uri("file", p.startsWith("/") ? p : `/${p}`);
  }
  static parse(s: string): Uri {
    const idx = s.indexOf(":");
    if (idx < 0) return new Uri("file", s);
    return new Uri(s.slice(0, idx), s.slice(idx + 1));
  }
  static joinPath(base: Uri, ...parts: string[]): Uri {
    return new Uri(base.scheme, [base.path, ...parts].join("/"));
  }
  with(change: { scheme?: string; path?: string }): Uri {
    return new Uri(change.scheme ?? this.scheme, change.path ?? this.path);
  }
}

// -- events -------------------------------------------------------------------

class TinyEmitter<T> {
  private listeners = new Set<(e: T) => unknown>();
  event = ((listener: (e: T) => unknown) => {
    this.listeners.add(listener);
    return disposable();
  }) as any;
  fire(e: T): void {
    for (const l of [...this.listeners]) l(e);
  }
  dispose(): void {
    this.listeners.clear();
  }
}

export { TinyEmitter as EventEmitter };

// -- tokens --------------------------------------------------------------------

export class CancellationTokenSource {
  private emitter = new TinyEmitter<unknown>();
  readonly token = {
    isCancellationRequested: false,
    onCancellationRequested: this.emitter.event,
  };
  cancel(): void {
    this.token.isCancellationRequested = true;
    this.emitter.fire(undefined);
  }
  dispose(): void {
    this.emitter.dispose();
  }
}

// -- output channels -------------------------------------------------------------

export interface ChannelSpy {
  name: string;
  lines: string[];
  info: (msg: string) => void;
  debug: (msg: string) => void;
  error: (msg: string | Error) => void;
  append: (msg: string) => void;
  appendLine: (msg: string) => void;
  show: Fn;
  dispose: Fn;
}

export const outputChannels: ChannelSpy[] = [];

export function channelByName(name: string): ChannelSpy | undefined {
  return outputChannels.find((c) => c.name === name);
}

function createOutputChannel(name: string): ChannelSpy {
  const lines: string[] = [];
  const ch: ChannelSpy = {
    name,
    lines,
    info: (msg) => lines.push(`[info] ${msg}`),
    debug: (msg) => lines.push(`[debug] ${msg}`),
    error: (msg) => lines.push(`[error] ${typeof msg === "string" ? msg : msg.message}`),
    append: (msg) => lines.push(msg),
    appendLine: (msg) => lines.push(msg),
    show: spy(),
    dispose: spy(),
  };
  outputChannels.push(ch);
  return ch;
}

// -- configuration -----------------------------------------------------------------

export const __config = new Map<string, unknown>();

export const __workspaceFolders: { uri: Uri; name: string; index: number }[] = [];

// -- webview / tree fakes state ------------------------------------------------------

export const __commands = new Map<string, (...args: unknown[]) => unknown>();

export const __fs = {
  /** fsPath → { size, bytes } */
  files: new Map<string, { size: number; bytes?: Uint8Array }>(),
};

// -- window / workspace / commands / chat / lm ----------------------------------------

export const window = {
  createOutputChannel,
  createTreeView: spy(() => ({
    badge: undefined as unknown,
    reveal: spy(),
    dispose: spy(),
  })),
  registerWebviewViewProvider: spy(() => disposable()),
  registerTreeDataProvider: spy(() => disposable()),
  showErrorMessage: spy(async () => undefined),
  showInformationMessage: spy(async () => undefined),
  showInputBox: spy(async () => undefined),
  showOpenDialog: spy(async () => undefined),
  showTextDocument: spy(async () => undefined),
};

export const commands = {
  registerCommand: (id: string, fn: (...args: unknown[]) => unknown) => {
    __commands.set(id, fn);
    return disposable();
  },
  executeCommand: spy(async () => undefined),
  registerTextEditorCommand: spy(() => disposable()),
};

export const workspace = {
  workspaceFolders: __workspaceFolders,
  getConfiguration: (section?: string) => ({
    get(key: string, defaultValue?: unknown): unknown {
      const full = section ? `${section}.${key}` : key;
      return __config.has(full) ? __config.get(full) : defaultValue;
    },
  }),
  asRelativePath: (uri: Uri) => uri.fsPath.replace(/^\//, ""),
  onDidChangeWorkspaceFolders: spy(() => disposable()),
  fs: {
    stat: async (uri: Uri) => {
      const f = __fs.files.get(uri.fsPath);
      if (!f) throw new Error(`file not found: ${uri.fsPath}`);
      return { size: f.size, type: 0 } as unknown as { size: number; type: number };
    },
    readFile: async (uri: Uri) => {
      const f = __fs.files.get(uri.fsPath);
      if (!f) throw new Error(`file not found: ${uri.fsPath}`);
      return f.bytes ?? new TextEncoder().encode("content");
    },
  },
};

export const chat = {
  registerChatSessionItemProvider: spy(() => disposable()),
  registerChatSessionContentProvider: spy(() => disposable()),
  createChatParticipant: spy((id: string) => ({
    id,
    participantVariableProvider: undefined as unknown,
  })),
};

export const lm = {
  registerLanguageModelChatProvider: spy(() => disposable()),
};

// -- misc types ------------------------------------------------------------------------

export class MarkdownString {
  constructor(public value = "") {}
}

export class ThemeIcon {
  constructor(public id: string, public color?: ThemeColor) {}
}

export class ThemeColor {
  constructor(public id: string) {}
}

export class TreeItem {
  label: string;
  description?: string;
  tooltip?: unknown;
  contextValue?: string;
  command?: unknown;
  iconPath?: unknown;
  constructor(label: string, _collapsible?: unknown) {
    this.label = label;
  }
}

export enum ExtensionMode {
  Production = 1,
  Development = 2,
  Test = 3,
}

export const Disposable = { from: () => disposable() };

// -- chat turn constructors (proposed-API surface) ------------------------------
// The extension resolves these dynamically off the vscode namespace at runtime
// (see src/types/chatApi.ts chatParts()); stubs record their constructor args.
// Each is a named export so `import * as vscode` namespaces expose them the
// way the real ext host does.

export class ChatRequestTurn {
  constructor(...args: unknown[]) {
    Object.assign(this, { args });
  }
}
export class ChatResponseTurn {
  constructor(...args: unknown[]) {
    Object.assign(this, { args });
  }
}
export class ChatResponseTurn2 {
  constructor(...args: unknown[]) {
    Object.assign(this, { args });
  }
}
export class ChatResponseMarkdownPart {
  constructor(public value: unknown) {}
}
export class ChatToolInvocationPart {
  invocationMessage?: string;
  pastTenseMessage?: string;
  isComplete?: boolean;
  isError?: boolean | undefined;
  enablePartialUpdate?: boolean;
  toolSpecificData?: unknown;
  constructor(public toolName: string, public toolCallId: string, public errorMessage?: string) {}
}
export class McpToolInvocationContentData {
  constructor(public data: Uint8Array, public mimeType: string) {}
}
export class ChatResponseThinkingProgressPart {
  constructor(public value: unknown, public id?: string, public metadata?: Record<string, unknown>) {}
}
export class ChatResponseWarningPart {
  constructor(public value: unknown) {}
}
export class ChatResponseInfoPart {
  constructor(public value: unknown) {}
}
export class ChatCompletionItem {
  insertText?: string;
  detail?: string;
  constructor(public id: string, public label: string, public values: unknown[]) {}
}

export const env = { appName: "Visual Studio Code" };

export const extensions = { getExtension: spy(() => undefined) };

export const OverviewRulerLane = { Left: 1 };

export const StatusBarAlignment = { Left: 1, Right: 2 };

/** Wipe all mutable test state (config, fs, commands, folders, channels). */
export function __reset(): void {
  __config.clear();
  __fs.files.clear();
  __commands.clear();
  __workspaceFolders.length = 0;
  outputChannels.length = 0;
  for (const key of Object.keys(chat)) {
    if (typeof (chat as Record<string, unknown>)[key] !== "function") {
      delete (chat as Record<string, unknown>)[key];
    }
  }
  chat.registerChatSessionItemProvider = spy(() => disposable());
  chat.registerChatSessionContentProvider = spy(() => disposable());
  chat.createChatParticipant = spy((id: string) => ({ id, participantVariableProvider: undefined }));
}

export default {
  Uri,
  EventEmitter: TinyEmitter,
  CancellationTokenSource,
  MarkdownString,
  ThemeIcon,
  ThemeColor,
  TreeItem,
  ExtensionMode,
  Disposable,
  ChatRequestTurn,
  ChatResponseTurn,
  ChatResponseTurn2,
  ChatResponseMarkdownPart,
  ChatToolInvocationPart,
  McpToolInvocationContentData,
  ChatResponseThinkingProgressPart,
  ChatResponseWarningPart,
  ChatResponseInfoPart,
  ChatCompletionItem,
  window,
  commands,
  workspace,
  chat,
  lm,
  env,
  extensions,
  OverviewRulerLane,
  StatusBarAlignment,
  __reset,
  __config,
  __fs,
  __commands,
  __workspaceFolders,
  outputChannels,
  channelByName,
};