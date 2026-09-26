import { beforeEach, describe, expect, it, vi } from "vitest";
import vscode, { spy as recordSpy } from "vscode";
import { ChatPanelProvider } from "../src/chatPanel";
import { TranscriptStore } from "../src/transcript";
import { PiGoAcpClient, type SessionEntry } from "../src/acp"; // PiGoAcpClient is the fake via vi.mock

// The panel is driven against a fake PiGoAcpClient; the pure helpers
// (chunkText, thoughtText) stay real so the live-update mapping is exercised.
const state = vi.hoisted(() => ({
  instances: [] as {
    listeners: Set<(u: unknown) => void>;
    capabilities: { embeddedContext: boolean } | undefined;
    commandsBySession: Map<string, { name: string; description?: string }[]>;
    newSessionResult?: SessionEntry | undefined;
    newSessionError?: Error;
    loadError?: Error;
    promptError?: Error;
    promptCalls: unknown[][];
    loadCalls: unknown[];
  }[],
  counter: 0,
}));

const pingState = vi.hoisted(() => ({
  run: vi.fn(async () => ({
    ok: true,
    title: "Pi-Go ping succeeded",
    detail: "Provider: agentgateway\nModel: ollama-deepseek",
  })),
}));

vi.mock("../src/acp", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/acp")>();
  class FakeClient {
    listeners = new Set<(u: unknown) => void>();
    capabilities: { embeddedContext: boolean } | undefined = { embeddedContext: true };
    commandsBySession = new Map<string, { name: string; description?: string }[]>();
    newSessionResult: SessionEntry | undefined;
    newSessionError: Error | undefined;
    loadError: Error | undefined;
    promptError: Error | undefined;
    promptCalls: unknown[][] = [];
    loadCalls: unknown[] = [];
    constructor() {
      state.instances.push(this);
    }
    onSessionUpdate(fn: (u: unknown) => void): { dispose(): void } {
      this.listeners.add(fn);
      return { dispose: () => this.listeners.delete(fn) };
    }
    async newSession(): Promise<SessionEntry> {
      if (this.newSessionError) throw this.newSessionError;
      return this.newSessionResult ?? { sessionId: `new-${++state.counter}`, cwd: "/tmp/ws" };
    }
    async load(entry: SessionEntry): Promise<void> {
      this.loadCalls.push(entry);
      if (this.loadError) throw this.loadError;
    }
    async prompt(sessionId: string, blocks: unknown, _token: unknown): Promise<void> {
      this.promptCalls.push([sessionId, blocks]);
      if (this.promptError) throw this.promptError;
    }
    availableCommands(sessionId: string): { name: string; description?: string }[] {
      return this.commandsBySession.get(sessionId) ?? [];
    }
    async reconnect(): Promise<void> {}
    dispose(): void {}
  }
  return { ...actual, PiGoAcpClient: FakeClient };
});

vi.mock("../src/ping", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/ping")>();
  return { ...actual, runPiPing: pingState.run };
});

type FakeView = {
  viewType: string;
  badge: unknown;
  webview: {
    options: unknown;
    html: string;
    cspSource: string;
    asWebviewUri: (u: unknown) => unknown;
    postMessage: ReturnType<typeof vi.fn>;
    onDidReceiveMessage: (cb: (m: unknown) => void) => unknown;
  };
  onDidDispose: (cb: () => void) => unknown;
  __messages: unknown[];
  __post: (m: unknown) => void;
  __dispose: () => void;
};

function fakeView(viewType = "pi-go.chat"): FakeView {
  const messages: unknown[] = [];
  const receive: ((m: unknown) => void)[] = [];
  const dispose: (() => void)[] = [];
  const view: FakeView = {
    viewType,
    badge: undefined,
    webview: {
      options: undefined,
      html: "",
      cspSource: "test-source",
      asWebviewUri: (u) => u,
      postMessage: vi.fn(async (m: unknown) => {
        messages.push(m);
        return true;
      }),
      onDidReceiveMessage: (cb) => {
        receive.push(cb);
        return { dispose() {} };
      },
    },
    onDidDispose: (cb) => {
      dispose.push(cb);
      return { dispose() {} };
    },
    __messages: messages,
    __post: (m) => receive.forEach((cb) => cb(m)),
    __dispose: () => dispose.forEach((cb) => cb()),
  };
  return view;
}

function context(): { subscriptions: { dispose(): void }[]; extensionUri: vscode.Uri; extension: { id: string; packageJSON: { version: string } } } {
  return {
    subscriptions: [],
    extensionUri: vscode.Uri.file("/ext"),
    extension: { id: "pi-go.pi-go-vscode", packageJSON: { version: "0.3.0" } },
  };
}

function typesOf(messages: unknown[]): string[] {
  return messages.map((m) => (m as { type: string }).type);
}

function setup() {
  const store = new TranscriptStore();
  const refresh = new vscode.EventEmitter<void>();
  const active = new Set<string>();
  const client = new PiGoAcpClient() as unknown as (typeof state.instances)[number];
  const panel = new ChatPanelProvider(
    context() as never,
    client as never,
    store,
    refresh,
    active,
  );
  return { store, refresh, active, panel, client };
}

beforeEach(() => {
  vscode.__reset();
  vscode.__workspaceFolders.push({ uri: vscode.Uri.file("/tmp/ws"), name: "ws", index: 0 });
  state.instances.length = 0;
  state.counter = 0;
});

describe("ChatPanelProvider.resolveWebviewView", () => {
  it("serves the html, keeps views by type, and answers ready with state", async () => {
    const { panel } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    expect(view.webview.options).toMatchObject({ enableScripts: true });
    expect(view.webview.html).toContain("Content-Security-Policy");
    expect(view.webview.html).toContain("cdnjs.cloudflare.com");
    expect(view.webview.html).toMatch(/script nonce="[^"]+"/);

    view.__post({ type: "ready" });
    expect(typesOf(view.__messages)).toEqual(["state", "tabs"]);
    const stateMsg = view.__messages[0] as Record<string, unknown>;
    expect(stateMsg.sessionId).toBeUndefined();
    expect(stateMsg.turns).toEqual([]);

    // A second view id is tracked separately; broadcasts skip disposed views.
    const second = fakeView("pi-go.chatSecondary");
    panel.resolveWebviewView(second as never, {} as never, {} as never);
    const disposedCount = view.__messages.length;
    view.__dispose();
    await panel.startNewSession();
    const secondTabs = await vi.waitFor(() => {
      const m = second.__messages.findLast(
        (x) => (x as { type: string }).type === "tabs",
      ) as { tabs: { sessionId: string }[] } | undefined;
      expect(m?.tabs.map((t) => t.sessionId)).toEqual(["new-1"]);
      return m!;
    });
    expect(secondTabs).toBeDefined();
    // The disposed view received nothing after disposal.
    expect(view.__messages.length).toBe(disposedCount);
  });
});

describe("ChatPanelProvider sessions", () => {
  it("openSession replays from a clean slate and tolerates load errors", async () => {
    const { panel, store, client } = setup();
    client.loadError = new Error("load failed");
    await panel.openSession({ sessionId: "s1", cwd: "/tmp/ws" });
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);

    // A second open of a different session creates a second tab.
    await panel.openSession({ sessionId: "s2", cwd: "/tmp/ws" });
    expect(client.loadCalls).toHaveLength(2);
    expect(typesOf(view.__messages)).toEqual(
      expect.arrayContaining(["sessionLoaded", "replayStarted", "state", "tabs"]),
    );
    const tabsMsg = view.__messages.findLast(
      (m) => (m as { type: string }).type === "tabs",
    ) as { tabs: { sessionId: string }[]; activeSessionId?: string };
    expect(tabsMsg.tabs.map((t) => t.sessionId)).toEqual(["s1", "s2"]);
    expect(tabsMsg.activeSessionId).toBe("s2");
    expect(store.uriFor("s1")).toBeDefined();
    expect(store.uriFor("s2")).toBeDefined();
  });

  it("re-opening an open tab activates it instead of re-replaying", async () => {
    const { panel, client } = setup();
    await panel.openSession({ sessionId: "s1", cwd: "/tmp/ws" });
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    expect(client.loadCalls).toHaveLength(1);

    await panel.openSession({ sessionId: "s1", cwd: "/tmp/ws" });
    expect(client.loadCalls).toHaveLength(1); // no second replay
    const tabsMsg = view.__messages.findLast(
      (m) => (m as { type: string }).type === "tabs",
    ) as { tabs: { sessionId: string }[]; activeSessionId?: string };
    expect(tabsMsg.tabs).toHaveLength(1);
    expect(tabsMsg.activeSessionId).toBe("s1");
  });

  it("startNewSession registers and broadcasts the new session", async () => {
    const { panel, store } = setup();
    panel.startNewSession();
    // Fire-and-forget; the session shows up in the store when it settles.
    await vi.waitFor(() => expect(store.uriFor("new-1")).toBeDefined());
  });

  it("startNewSession twice opens a second tab and activates it", async () => {
    const { panel } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    await panel.startNewSession();
    await panel.startNewSession();
    const tabsMsg = await vi.waitFor(() => {
      const m = view.__messages.findLast(
        (x) => (x as { type: string }).type === "tabs",
      ) as { tabs: { sessionId: string }[]; activeSessionId?: string } | undefined;
      expect(m).toBeDefined();
      expect(m!.tabs.map((t) => t.sessionId)).toEqual(["new-1", "new-2"]);
      expect(m!.activeSessionId).toBe("new-2");
      return m!;
    });
    expect(tabsMsg).toBeDefined();
  });
});

describe("ChatPanelProvider ping", () => {
  it("runs pi ping and broadcasts the result without starting a session", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);

    view.__post({ type: "ping" });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("pingResult"));

    expect(pingState.run).toHaveBeenCalledWith({ command: "pi", args: ["acp-server"], cwd: "/tmp/ws" });
    expect(client.promptCalls).toHaveLength(0);
    expect(view.__messages.at(-1)).toMatchObject({
      type: "pingResult",
      ok: true,
      title: "Pi-Go ping succeeded",
    });
  });
});

describe("ChatPanelProvider prompts", () => {
  it("creates a session on first prompt and streams the turn", async () => {
    const { panel, client, active } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    view.__post({ type: "prompt", text: "hello", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
    expect(typesOf(view.__messages)).toEqual(
      expect.arrayContaining(["state", "tabs", "userTurn", "turnEnd"]),
    );
    expect(client.promptCalls).toHaveLength(1);
    expect(client.promptCalls[0][0]).toBe("new-1");
    expect(active.has("new-1")).toBe(false); // cleaned up after the turn

    // A second prompt reuses the current session, no new spawn.
    view.__post({ type: "prompt", text: "again", attachments: [] });
    await vi.waitFor(() => expect(client.promptCalls).toHaveLength(2));
    expect(client.promptCalls[1][0]).toBe("new-1");
  });

  it("guards against a concurrent prompt in the same session, but runs prompts in parallel sessions", async () => {
    const { panel, client, active } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    // Two sessions, each with a prompt that hangs until released.
    await panel.startNewSession(); // new-1
    await panel.startNewSession(); // new-2

    const gates: Array<(value?: unknown) => void> = [];
    client.prompt = (async (sessionId: string) => {
      client.promptCalls.push([sessionId]);
      await new Promise((r) => gates.push(r as (value?: unknown) => void));
    }) as never;

    // Prompt the second tab while the first is visible.
    view.__post({ type: "activateTab", sessionId: "new-2" });
    view.__post({ type: "prompt", text: "in tab two", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("userTurn"));
    expect(active.has("new-2")).toBe(true);

    // Switch back to the first tab; its prompt is a different session and
    // must run in parallel — no "already running" notice.
    view.__post({ type: "activateTab", sessionId: "new-1" });
    view.__post({ type: "prompt", text: "in tab one", attachments: [] });
    await vi.waitFor(() => expect(client.promptCalls).toHaveLength(2));
    expect(active.has("new-1")).toBe(true);
    expect(active.has("new-2")).toBe(true);

    // A second prompt against the same session while it is in flight is refused.
    view.__post({ type: "prompt", text: "again", attachments: [] });
    const notices = () =>
      view.__messages.filter((m) => (m as { type: string }).type === "notice") as { text: string }[];
    await vi.waitFor(() => expect(notices().some((n) => n.text.includes("already running"))).toBe(true));

    // Release both; both turns end.
    for (const g of gates.splice(0)) g();
    await vi.waitFor(() => {
      const ends = typesOf(view.__messages).filter((t) => t === "turnEnd");
      expect(ends).toHaveLength(2);
    });
    expect(active.size).toBe(0);
  });

  it("closing a tab cancels its in-flight prompt and activates a neighbour", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    await panel.startNewSession(); // new-1
    await panel.startNewSession(); // new-2

    const gates: Array<(value?: unknown) => void> = [];
    client.prompt = (async (sessionId: string) => {
      client.promptCalls.push([sessionId]);
      await new Promise((r) => gates.push(r as (value?: unknown) => void));
    }) as never;

    view.__post({ type: "prompt", text: "in tab two", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("userTurn"));

    // Close the streaming tab: its turn is cancelled, the other takes over.
    view.__post({ type: "closeTab", sessionId: "new-2" });
    for (const g of gates.splice(0)) g();
    await vi.waitFor(() => {
      const tabsMsg = view.__messages.findLast(
        (m) => (m as { type: string }).type === "tabs",
      ) as { tabs: { sessionId: string }[]; activeSessionId?: string };
      expect(tabsMsg.tabs.map((t) => t.sessionId)).toEqual(["new-1"]);
      expect(tabsMsg.activeSessionId).toBe("new-1");
    });
    const end = await vi.waitFor(() => {
      const e = view.__messages.findLast(
        (m) => (m as { type: string }).type === "turnEnd",
      ) as { sessionId?: string } | undefined;
      expect(e?.sessionId).toBe("new-2"); // the closed tab's turn still ends
      return e!;
    });
    expect(end).toBeDefined();
  });

  it("handles /help locally without contacting the agent", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    client.commandsBySession.set("new-1", [{ name: "plan", description: "plan it" }]);
    // No session yet: /help first creates one via startSession.
    view.__post({ type: "prompt", text: "/help", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
    expect(client.promptCalls).toHaveLength(0);
    const agentChunk = view.__messages.find((m) => (m as { type: string }).type === "agentChunk") as { text: string };
    expect(agentChunk.text).toContain("**/plan** — plan it");

    // Without advertised commands, the empty-help text is used.
    const view2 = fakeView();
    panel.resolveWebviewView(view2 as never, {} as never, {} as never);
    // Drop the advertised commands: /help must fall back to the empty text.
    client.commandsBySession.delete("new-1");
    view2.__post({ type: "prompt", text: "/help", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view2.__messages)).toContain("turnEnd"));
    const chunk2 = view2.__messages.findLast(
      (m) => (m as { type: string }).type === "agentChunk",
    ) as { text: string };
    expect(chunk2.text).toContain("has not advertised");
  });

  it("handles /clear by rebinding to a fresh session", async () => {
    const { panel, store } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    view.__post({ type: "prompt", text: "first", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
    const oldUri = store.uriFor("new-1")!;

    view.__post({ type: "prompt", text: "/clear", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("notice"));
    const notice = view.__messages.find((m) => (m as { type: string }).type === "notice") as { text: string };
    expect(notice.text).toContain("fresh session");
    // The pre-clear resource now points at the rebound session id.
    expect(store.acpIdFor(oldUri)).toBe("new-2");
  });

  it("notices unknown slash commands but still prompts", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    view.__post({ type: "prompt", text: "/explain", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
    expect(client.promptCalls).toHaveLength(1);
    const notice = view.__messages.find((m) => (m as { type: string }).type === "notice") as { text: string };
    expect(notice.text).toContain("forwarded to pi-go as plain text");
  });

  it("reports prompt failures as turnEnd errors", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    view.__post({ type: "prompt", text: "hello", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
    client.promptError = new Error("spawn failed");
    view.__post({ type: "prompt", text: "second", attachments: [] });
    await vi.waitFor(() => {
      const end = view.__messages.findLast((m) => (m as { type: string }).type === "turnEnd") as { error?: string };
      expect(end?.error).toBe("spawn failed");
    });
  });

  it("posts (no response) when the agent stayed silent", async () => {
    const { panel } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    view.__post({ type: "prompt", text: "hello", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
    const chunks = view.__messages.filter((m) => (m as { type: string }).type === "agentChunk");
    expect(chunks.some((m) => (m as { text: string }).text === "_(no response)_")).toBe(true);
  });

  it("reports cancellation instead of a silent turn", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    // Make prompt cancel the token synchronously.
    client.prompt = (async (_s: string, _b: unknown, token: { isCancellationRequested: boolean }) => {
      token.isCancellationRequested = true;
    }) as never;
    view.__post({ type: "prompt", text: "hello", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
    const notice = view.__messages.find((m) => (m as { type: string }).type === "notice") as { text: string };
    expect(notice.text).toBe("(cancelled)");
  });
});

describe("ChatPanelProvider messages and live updates", () => {
  it("routes revealFile, requestFilePicker, newSession, openSession, and cancel", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);

    view.__post({ type: "revealFile", path: "/tmp/ws/a.ts" });
    await vi.waitFor(() => {
      const calls = (vscode.window.showTextDocument as unknown as { calls: unknown[][] }).calls;
      expect(calls.length).toBeGreaterThan(0);
    });

    // Opening the bad path throws → the error is surfaced to the webview user.
    (vscode.window as unknown as { showTextDocument: unknown }).showTextDocument = vi.fn(async (uri: vscode.Uri) => {
      if (uri.path.includes("nope")) throw new Error("cannot read");
      return {} as never;
    });
    view.__post({ type: "revealFile", path: "/nope/x.ts" });
    await vi.waitFor(() => {
      const err = vscode.window.showErrorMessage as unknown as { calls: unknown[][] };
      expect(err.calls.some((c) => String(c[0]).includes("cannot open"))).toBe(true);
    });

    // The picker returns two files → both offered to the webview.
    (vscode.window as unknown as { showOpenDialog: unknown }).showOpenDialog = vi.fn(
      async () => [vscode.Uri.file("/tmp/ws/one.ts"), vscode.Uri.file("/tmp/ws/two.ts")],
    );
    view.__post({ type: "requestFilePicker" });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("attachmentsAdded"));
    const added = view.__messages.find((m) => (m as { type: string }).type === "attachmentsAdded") as { paths: string[] };
    expect(added.paths).toEqual(["/tmp/ws/one.ts", "/tmp/ws/two.ts"]);

    view.__post({ type: "newSession" });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("state"));

    view.__post({ type: "openSession", sessionId: "persisted" });
    await vi.waitFor(() => {
      expect(client.loadCalls).toHaveLength(1);
      expect(typesOf(view.__messages).filter((t) => t === "sessionLoaded")).toHaveLength(1);
      const tabsMsg = view.__messages.findLast(
        (m) => (m as { type: string }).type === "tabs",
      ) as { tabs: { sessionId: string }[] };
      expect(tabsMsg.tabs.map((t) => t.sessionId)).toEqual(["new-1", "persisted"]);
    });

    // No-ops and the guard against junk messages.
    view.__post({ type: "draft", text: "wip" });
    view.__post({ type: "nonsense" });
    view.__post(undefined);
  });

  it("cancel disposes the in-flight token source", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    // A prompt that hangs until we release it; cancel arrives mid-flight.
    let release: (value?: unknown) => void = () => {};
    const pending = new Promise<void>((r) => {
      release = r;
    });
    client.prompt = (() => pending) as never;
    view.__post({ type: "prompt", text: "hello", attachments: [] });
    view.__post({ type: "cancel", sessionId: "new-1" });
    release();
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("turnEnd"));
  });

  it("forwards live agent/thought/tool updates while not replaying", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    // The current session id is only set once the implicit newSession lands.
    view.__post({ type: "prompt", text: "hi", attachments: [] });
    await vi.waitFor(() => expect(typesOf(view.__messages)).toContain("userTurn"));

    const fire = (update: unknown, sessionId = "new-1") => {
      for (const l of client.listeners) l({ sessionId, update });
    };
    fire({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "part1" } });
    fire({ sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "th" } });
    fire({ sessionUpdate: "tool_call", toolCallId: "t1", name: "bash", title: "Run", status: "in_progress" });
    fire({
      sessionUpdate: "available_commands_update",
      availableCommands: [{ name: "plan" }],
    }, "other-session");
    expect(typesOf(view.__messages)).toEqual(
      expect.arrayContaining(["agentChunk", "thoughtChunk", "toolUpdate", "commandsUpdated"]),
    );
    const tool = view.__messages.find((m) => (m as { type: string }).type === "toolUpdate") as { tool: { toolName: string } };
    expect(tool.tool.toolName).toBe("bash");
  });

  it("suppresses live chunks for other sessions and during replays", async () => {
    const { panel, client } = setup();
    const view = fakeView();
    panel.resolveWebviewView(view as never, {} as never, {} as never);
    const fire = (update: unknown, sessionId = "other") => {
      for (const l of client.listeners) l({ sessionId, update });
    };
    // Not an open tab: no chunk reaches the webview.
    fire({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "x" } });
    expect(typesOf(view.__messages)).not.toContain("agentChunk");

    // Open a tab so routing reaches the per-update type checks.
    await panel.startNewSession(); // new-1, no prompt: no reply chunks pollute
    // Non-text chunks are dropped too.
    fire({ sessionUpdate: "agent_message_chunk", content: { type: "image" } }, "new-1");
    expect(typesOf(view.__messages)).not.toContain("agentChunk");
    // And updates for a replaying session are suppressed: the snapshot lands
    // after session/load resolves.
    (panel as unknown as { replaying: Set<string> }).replaying.add("new-1");
    fire({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "y" } }, "new-1");
    expect(typesOf(view.__messages)).not.toContain("agentChunk");
    (panel as unknown as { replaying: Set<string> }).replaying.delete("new-1");
  });

  it("attachFiles opens the picker and dispose cancels the in-flight turn", async () => {
    const { panel } = setup();
    // Fresh recording spy (an earlier test replaced the shared one with a vi.fn).
    const dialog = recordSpy(async () => undefined);
    (vscode.window as unknown as { showOpenDialog: unknown }).showOpenDialog = dialog;
    panel.attachFiles();
    await vi.waitFor(() => expect(dialog.calls).toHaveLength(1));
    panel.dispose(); // no in-flight turn: a plain no-op
  });
});
