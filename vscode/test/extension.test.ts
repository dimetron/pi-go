import { beforeEach, describe, expect, it, vi } from "vitest";
import vscode, { spy as mkSpy, type ChannelSpy } from "vscode";
import { activate } from "../src/extension";
import type { SessionEntry } from "../src/acp";

// activate() wires the real chatPanel/sessionsTree/acp modules together; mock
// all three and drive the real wiring logic in extension.ts itself.
const sessionsTreeMock = vi.hoisted(() => ({ registerSessionsTree: vi.fn() }));
const chatPanelState = vi.hoisted(() => ({ instances: [] as Record<string, unknown>[] }));
const acpState = vi.hoisted(() => ({
  instances: [] as {
    listeners: Set<(u: unknown) => void>;
    spawnListeners: Set<(m: string) => void>;
    capabilities: { embeddedContext: boolean; supportsList: boolean } | undefined;
    commands: Map<string, { name: string; description?: string }[]>;
    listResult: SessionEntry[];
    listError?: Error;
    loadError?: Error;
    newSessionError?: Error;
    promptError?: Error;
    newSessionCounter: number;
    promptCalls: unknown[][];
    loadCalls: unknown[];
  }[],
  counter: 0,
}));

vi.mock("../src/sessionsTree", () => ({ registerSessionsTree: sessionsTreeMock.registerSessionsTree }));

vi.mock("../src/chatPanel", () => {
  class FakePanel {
    openSession = vi.fn();
    startNewSession = vi.fn();
    attachFiles = vi.fn();
    resolveWebviewView = vi.fn();
    dispose = vi.fn();
    constructor() {
      chatPanelState.instances.push(this);
    }
  }
  return { ChatPanelProvider: FakePanel };
});

vi.mock("../src/acp", () => {
  class FakeClient {
    listeners = new Set<(u: unknown) => void>();
    spawnListeners = new Set<(m: string) => void>();
    capabilities: { embeddedContext: boolean; supportsList: boolean } | undefined = {
      embeddedContext: true,
      supportsList: true,
    };
    commands = new Map<string, { name: string; description?: string }[]>();
    listResult: SessionEntry[] = [];
    listError: Error | undefined;
    loadError: Error | undefined;
    newSessionError: Error | undefined;
    promptError: Error | undefined;
    newSessionCounter = 0;
    promptCalls: unknown[][] = [];
    loadCalls: unknown[] = [];
    constructor() {
      acpState.instances.push(this);
    }
    onSessionUpdate(fn: (u: unknown) => void): { dispose(): void } {
      this.listeners.add(fn);
      return { dispose: () => this.listeners.delete(fn) };
    }
    onSpawnError(fn: (m: string) => void): { dispose(): void } {
      this.spawnListeners.add(fn);
      return { dispose: () => this.spawnListeners.delete(fn) };
    }
    fireUpdate(update: unknown, sessionId = "s1"): void {
      for (const l of this.listeners) l({ sessionId, update });
    }
    fireSpawnError(message: string): void {
      for (const l of this.spawnListeners) l(message);
    }
    async newSession(): Promise<SessionEntry> {
      if (this.newSessionError) throw this.newSessionError;
      return { sessionId: `n${++this.newSessionCounter}`, cwd: "/tmp/ws" };
    }
    async listSessions(): Promise<SessionEntry[]> {
      if (this.listError) throw this.listError;
      return this.listResult;
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
      return this.commands.get(sessionId) ?? [];
    }
    async reconnect(): Promise<void> {}
    dispose(): void {}
  }
  return {
    PiGoAcpClient: FakeClient,
    chunkText: (u: { update?: { sessionUpdate?: string; content?: { type?: string; text?: string } } }) =>
      u.update?.content?.type === "text" &&
      (u.update?.sessionUpdate === "agent_message_chunk" || u.update?.sessionUpdate === "user_message_chunk")
        ? u.update.content.text
        : undefined,
    SESSION_SCHEME: "pi-go-session",
    SESSION_TYPE: "pi-go",
  };
});

function client(): (typeof acpState.instances)[number] {
  return acpState.instances[acpState.instances.length - 1];
}

function context(): vscode.ExtensionContext {
  return {
    subscriptions: [],
    extension: { id: "pi-go.pi-go-vscode", packageJSON: { version: "0.3.0" } },
    extensionUri: vscode.Uri.file("/ext"),
  } as never;
}

function fakeToken(cancelled = false): vscode.CancellationToken {
  return { isCancellationRequested: cancelled, onCancellationRequested: () => ({ dispose() {} }) } as never;
}

function fakeStream() {
  const pushed: unknown[] = [];
  return {
    pushed,
    progress: vi.fn(),
    markdown: vi.fn((s: string) => pushed.push(s)),
    push: vi.fn((p: unknown) => pushed.push(p)),
  };
}

// extension.ts grabs its log channel at import time; __reset() clears the
// channel list, so re-register the captured channel before each test.
const logChannel: ChannelSpy = vscode.channelByName("pi-go")!;

beforeEach(() => {
  vscode.__reset();
  vscode.outputChannels.push(logChannel);
  vscode.__workspaceFolders.push({ uri: vscode.Uri.file("/tmp/ws"), name: "ws", index: 0 });
  // Fresh window spies so tests that replace them never leak into each other.
  (vscode.window as unknown as Record<string, unknown>).showInputBox = mkSpy(async () => undefined);
  (vscode.window as unknown as Record<string, unknown>).showErrorMessage = mkSpy(async () => undefined);
  acpState.instances.length = 0;
  acpState.counter = 0;
  chatPanelState.instances.length = 0;
  sessionsTreeMock.registerSessionsTree.mockClear();
});

const chatSpies = () => vscode.chat as unknown as Record<string, { calls: unknown[][] }>;

describe("activate", () => {
  it("registers providers, commands, the panel, and the tree", () => {
    const ctx = context();
    activate(ctx);
    const log = vscode.channelByName("pi-go")!;

    expect(log.lines.some((l) => l.includes("activating pi-go extension (version 0.3.0)"))).toBe(true);
    expect(log.lines.some((l) => l.includes("pi-go extension activated"))).toBe(true);
    expect(log.lines.some((l) => l.includes("chat panel and sessions tree registered"))).toBe(true);

    expect(chatSpies().registerChatSessionItemProvider.calls).toHaveLength(1);
    expect(chatSpies().registerChatSessionItemProvider.calls[0][0]).toBe("pi-go");
    expect(chatSpies().registerChatSessionContentProvider.calls[0][0]).toBe("pi-go-session");
    expect((vscode.lm.registerLanguageModelChatProvider as unknown as { calls: unknown[][] }).calls[0][0]).toBe(
      "pi-go",
    );

    const providerCalls = vscode.window.registerWebviewViewProvider as unknown as { calls: unknown[][] };
    expect(providerCalls.calls.map((c) => c[0])).toEqual(["pi-go.chat"]);
    expect(vscode.__commands.has("pi-go.attachFile")).toBe(true);
    expect(vscode.__commands.has("pi-go.start")).toBe(false);
    expect(vscode.__commands.has("pi-go.refreshSessions")).toBe(true);
    expect(chatPanelState.instances).toHaveLength(1);
    expect(sessionsTreeMock.registerSessionsTree).toHaveBeenCalledTimes(1);
    // Subscriptions include the client, the listeners, the panel, the providers…
    expect(ctx.subscriptions.length).toBeGreaterThan(5);
  });

  it("logs an error when the chat session APIs are missing", () => {
    delete (vscode.chat as unknown as Record<string, unknown>).registerChatSessionItemProvider;
    activate(context());
    const log = vscode.channelByName("pi-go")!;
    expect(log.lines.some((l) => l.includes("chat session APIs unavailable"))).toBe(true);
    // The native providers were never registered, but the panel still was.
    expect(chatSpies().registerChatSessionContentProvider.calls).toHaveLength(0);
    expect(chatPanelState.instances).toHaveLength(1);
  });
});

describe("spawn failure surfacing", () => {
  it("surfaces spawn failures through showErrorMessage and opens settings", async () => {
    activate(context());
    (vscode.window as unknown as { showErrorMessage: unknown }).showErrorMessage = mkSpy(
      async () => "Open Settings",
    );
    client().fireSpawnError("spawn pi ENOENT");
    await vi.waitFor(() => {
      const err = vscode.window.showErrorMessage as unknown as { calls: unknown[][] };
      expect(err.calls[0]?.[0]).toBe("pi-go failed to start: spawn pi ENOENT");
      expect(err.calls[0]?.[1]).toBe("Open Settings");
    });
    await vi.waitFor(() => {
      const exec = vscode.commands.executeCommand as unknown as { calls: unknown[][] };
      expect(exec.calls[0]).toEqual(["workbench.action.openSettings", "pi-go.command"]);
    });

    // A dismissed dialog does not open settings.
    (vscode.window as unknown as { showErrorMessage: unknown }).showErrorMessage = mkSpy(
      async () => undefined,
    );
    client().fireSpawnError("again");
    await vi.waitFor(() => {
      const err = vscode.window.showErrorMessage as unknown as { calls: unknown[][] };
      expect(err.calls).toHaveLength(1);
    });
    const exec = vscode.commands.executeCommand as unknown as { calls: unknown[][] };
    expect(exec.calls).toHaveLength(1);
  });
});

describe("native sessions path", () => {
  function activateWithCtors(): vscode.ExtensionContext {
    const ctx = context();
    activate(ctx);
    return ctx;
  }

  function itemProvider(): { provideChatSessionItems: (t: unknown) => Promise<unknown[]> } {
    return chatSpies().registerChatSessionItemProvider.calls[0][1];
  }

  function contentProvider(): {
    provideChatSessionContent: (r: unknown, t: unknown) => Promise<Record<string, unknown>>;
  } {
    return chatSpies().registerChatSessionContentProvider.calls[0][1];
  }

  it("lists sessions as items with labels and status", async () => {
    activateWithCtors();
    client().listResult = [
      { sessionId: "s1", cwd: "/tmp/ws", title: "Agent title", updatedAt: 1_000 },
      { sessionId: "s2", cwd: "/tmp/ws" },
    ];
    const items = (await itemProvider().provideChatSessionItems(fakeToken())) as {
      label: string;
      status?: number;
      timing?: { created: number };
      resource: vscode.Uri;
    }[];
    expect(items).toHaveLength(2);
    expect(items[0].label).toBe("Agent title");
    expect(items[0].status).toBeUndefined(); // no reply yet, not active
    expect(items[0].timing).toEqual({ created: 1_000, lastRequestEnded: 1_000 });
    expect(items[1].label).toBe("pi-go session"); // falls back to the store title
    expect(items[1].resource.scheme).toBe("pi-go-session"); // store resources use the session scheme

    // After an agent reply is recorded, the status turns completed.
    client().fireUpdate(
      { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hi" } },
      "s2",
    );
    const again = (await itemProvider().provideChatSessionItems(fakeToken())) as { status?: number }[];
    expect(again[1].status).toBe(1);

    // Failures degrade to an empty list.
    client().listError = new Error("spawn failed");
    expect(await itemProvider().provideChatSessionItems(fakeToken())).toEqual([]);
    const log = vscode.channelByName("pi-go")!;
    expect(log.lines.some((l) => l.includes("provideChatSessionItems failed: spawn failed"))).toBe(true);
  });

  it("replays sessions through session/load and tolerates load errors", async () => {
    activateWithCtors();
    client().listResult = [{ sessionId: "s1", cwd: "/tmp/ws", title: "T" }];
    const items = (await itemProvider().provideChatSessionItems(fakeToken())) as { resource: vscode.Uri }[];
    const resource = items[0].resource;

    client().loadError = new Error("load failed");
    const session = await contentProvider().provideChatSessionContent(resource, fakeToken());
    expect(client().loadCalls).toHaveLength(1);
    expect(client().loadCalls[0]).toEqual({ sessionId: "s1", cwd: "/tmp/ws" });
    const log = vscode.channelByName("pi-go")!;
    expect(log.lines.some((l) => l.includes("session/load for s1: load failed"))).toBe(true);
    // Nothing replayed: empty history, default title.
    expect(session.title).toBe("pi-go session");
    expect(session.history).toEqual([]);
    expect(session.requestHandler).toBeTypeOf("function");

    // Unknown resources throw.
    await expect(
      contentProvider().provideChatSessionContent(vscode.Uri.parse("pi-go-session:local/unknown"), fakeToken()),
    ).rejects.toThrow(/unknown session resource/);
  });

  it("requestHandler: /clear rebinds to a fresh session", async () => {
    activateWithCtors();
    client().listResult = [{ sessionId: "s1", cwd: "/tmp/ws" }];
    const items = (await itemProvider().provideChatSessionItems(fakeToken())) as { resource: vscode.Uri }[];
    const session = await contentProvider().provideChatSessionContent(items[0].resource, fakeToken());
    const stream = fakeStream();
    const result = (await (
      session.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )(
      { prompt: "/clear", references: [] },
      {},
      stream,
      fakeToken(),
    )) as { metadata?: Record<string, unknown> };
    expect(result.metadata?.cleared).toBe(true);
    expect(result.metadata?.newSessionId).toBe("n1");
    expect(stream.pushed.join("")).toContain("fresh session");

    // A failed newSession reports the error instead.
    const session2 = await contentProvider().provideChatSessionContent(items[0].resource, fakeToken());
    client().newSessionError = new Error("spawn failed");
    const stream2 = fakeStream();
    const result2 = (await (
      session2.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )({ prompt: "/clear", references: [] }, {}, stream2, fakeToken())) as {
      errorDetails?: { message: string };
    };
    expect(result2.errorDetails?.message).toBe("spawn failed");
    expect((stream2.pushed as string[]).some((s) => s.includes("pi-go error:"))).toBe(true);
  });

  it("requestHandler: /help renders advertised commands", async () => {
    activateWithCtors();
    client().listResult = [{ sessionId: "s1", cwd: "/tmp/ws" }];
    const items = (await itemProvider().provideChatSessionItems(fakeToken())) as { resource: vscode.Uri }[];
    const session = await contentProvider().provideChatSessionContent(items[0].resource, fakeToken());
    client().commands.set("s1", [{ name: "plan", description: "Plan it" }]);
    const stream = fakeStream();
    const result = (await (
      session.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )({ prompt: "/help", references: [] }, {}, stream, fakeToken())) as {
      metadata?: Record<string, unknown>;
    };
    expect(result.metadata?.help).toBe(true);
    expect(stream.pushed.join("")).toContain("**/plan** — Plan it");
    expect(stream.pushed.join("")).toContain("forwarded to the model as text");
  });

  it("requestHandler: normal prompts stream updates and mention blocks", async () => {
    activateWithCtors();
    client().listResult = [{ sessionId: "s1", cwd: "/tmp/ws" }];
    const items = (await itemProvider().provideChatSessionItems(fakeToken())) as { resource: vscode.Uri }[];
    const session = await contentProvider().provideChatSessionContent(items[0].resource, fakeToken());
    const stream = fakeStream();

    // A reference the agent can embed + agent updates fired mid-prompt.
    vscode.__fs.files.set("/tmp/ws/notes.md", { size: 3, bytes: new TextEncoder().encode("abc") });
    let sentBlocks: unknown;
    (client() as unknown as { prompt: unknown }).prompt = (async (
      _sessionId: string,
      blocks: unknown,
      _token: unknown,
    ) => {
      sentBlocks = blocks;
      client().fireUpdate({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "reply!" } });
      client().fireUpdate({
        sessionUpdate: "tool_call",
        toolCallId: "t1",
        name: "bash run",
        title: "Run bash",
        status: "in_progress",
      });
    }) as never;

    const result = (await (
      session.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )(
      { prompt: "explain", references: [{ value: vscode.Uri.file("/tmp/ws/notes.md") }] },
      {},
      stream,
      fakeToken(),
    )) as { metadata?: Record<string, unknown> };

    expect(result.metadata?.agent).toBe("pi-go");
    expect((stream.pushed as string[]).some((s) => s.includes("reply!"))).toBe(true);
    const pushedNames = stream.push.mock.calls.map(
      (c) => (c[0] as { constructor: { name: string } }).constructor.name,
    );
    expect(pushedNames).toContain("ChatToolInvocationPart");
    const part = stream.push.mock.calls[0][0] as { toolName: string; enablePartialUpdate?: boolean };
    expect(part.toolName).toBe("bash run"); // from the tool_call's name field
    expect(part.enablePartialUpdate).toBe(true);
    // The mention was embedded: the prompt blocks include the resource.
    const blocks = sentBlocks as { type: string }[];
    expect(blocks[0]).toEqual({ type: "text", text: "explain" });
    expect(blocks.some((b) => b.type === "resource")).toBe(true);

    // A silent agent gets a "(no response)" note: re-resolving the session
    // resets the transcript, so the agent turn from the first prompt is gone.
    await contentProvider().provideChatSessionContent(items[0].resource, fakeToken());
    (client() as unknown as { prompt: unknown }).prompt = (async () => {}) as never;
    const stream2 = fakeStream();
    await (session.requestHandler as (...a: unknown[]) => Promise<unknown>)(
      { prompt: "again", references: [] },
      {},
      stream2,
      fakeToken(),
    );
    expect((stream2.pushed as string[]).some((s) => s.includes("no response"))).toBe(true);
  });

  it("requestHandler: errors and cancellation are reported", async () => {
    activateWithCtors();
    client().listResult = [{ sessionId: "s1", cwd: "/tmp/ws" }];
    const items = (await itemProvider().provideChatSessionItems(fakeToken())) as { resource: vscode.Uri }[];
    const session = await contentProvider().provideChatSessionContent(items[0].resource, fakeToken());

    client().promptError = new Error("boom");
    const stream = fakeStream();
    const result = (await (
      session.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )({ prompt: "hello", references: [] }, {}, stream, fakeToken())) as {
      errorDetails?: { message: string };
    };
    expect(result.errorDetails?.message).toBe("boom");
    expect((stream.pushed as string[]).some((s) => s.includes("pi-go error:"))).toBe(true);
    const log = vscode.channelByName("pi-go")!;
    expect(log.lines.some((l) => l.includes("prompt failed: boom"))).toBe(true);

    // Non-Error throwables are stringified through errString's fallbacks.
    client().promptError = { message: "plain object" } as never;
    const streamObj = fakeStream();
    const resultObj = (await (
      session.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )({ prompt: "hello", references: [] }, {}, streamObj, fakeToken())) as {
      errorDetails?: { message: string };
    };
    expect(resultObj.errorDetails?.message).toBe("plain object");

    client().promptError = "raw string" as never;
    const streamRaw = fakeStream();
    const resultRaw = (await (
      session.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )({ prompt: "hello", references: [] }, {}, streamRaw, fakeToken())) as {
      errorDetails?: { message: string };
    };
    expect(resultRaw.errorDetails?.message).toBe("raw string");

    // Cancelled mid-flight → "(cancelled)", no error details.
    client().promptError = new Error("cancelled away");
    const stream2 = fakeStream();
    const result2 = (await (
      session.requestHandler as (...a: unknown[]) => Promise<Record<string, unknown>>
    )({ prompt: "hello", references: [] }, {}, stream2, fakeToken(true))) as {
      metadata?: Record<string, unknown>;
    };
    expect(result2.metadata?.cancelled).toBe(true);
    expect((stream2.pushed as string[]).some((s) => s.includes("(cancelled)"))).toBe(true);

    // Unknown slash commands get the forwarding notice part.
    const stream3 = fakeStream();
    await (session.requestHandler as (...a: unknown[]) => Promise<unknown>)(
      { prompt: "/explain", references: [] },
      {},
      stream3,
      fakeToken(),
    );
    const pushedNames = stream3.push.mock.calls.map(
      (c) => (c[0] as { constructor: { name: string } }).constructor.name,
    );
    expect(pushedNames).toContain("ChatResponseInfoPart");
  });

  it("completion items come from the last active session's commands", async () => {
    // Capture the participant object the mock hands back so we can read the
    // variable provider extension.ts assigns onto it.
    const participant: { id?: string; participantVariableProvider?: {
      provider: { provideCompletionItems: (q: string, t: unknown) => unknown[] };
      triggerCharacters: string[];
    } } = {};
    (vscode.chat as unknown as Record<string, unknown>).createChatParticipant = mkSpy(() => participant);

    activateWithCtors();
    expect(chatSpies().createChatParticipant.calls[0][0]).toBe("pi-go.pi-go-vscode.agent");

    client().listResult = [{ sessionId: "s1", cwd: "/tmp/ws" }];
    const items = (await itemProvider().provideChatSessionItems(fakeToken())) as { resource: vscode.Uri }[];
    // No session opened yet → no completions.
    expect(
      participant.participantVariableProvider!.provider.provideCompletionItems("/", fakeToken()),
    ).toEqual([]);

    await contentProvider().provideChatSessionContent(items[0].resource, fakeToken());
    client().commands.set("s1", [{ name: "plan", description: "Plan it" }, { name: "no-desc" }]);
    const completions = participant.participantVariableProvider!.provider.provideCompletionItems(
      "/",
      fakeToken(),
    ) as { insertText: string; detail: string }[];
    expect(completions).toHaveLength(2);
    expect(completions[0].insertText).toBe("/plan");
    expect(completions[0].detail).toBe("Plan it");
    expect(completions[1].detail).toBe("");
    expect(participant.participantVariableProvider!.triggerCharacters).toEqual(["/"]);
  });

  it("the language model stub answers metadata and refuses generation", async () => {
    activateWithCtors();
    const [vendor, provider] = (
      vscode.lm.registerLanguageModelChatProvider as unknown as { calls: unknown[][] }
    ).calls[0] as [string, Record<string, (...args: unknown[]) => Promise<unknown>>];
    expect(vendor).toBe("pi-go");
    const models = (await provider.provideLanguageModelChatInformation()) as {
      id: string;
      name: string;
      targetChatSessionType: string;
    }[];
    expect(models[0].id).toBe("agent");
    expect(models[0].name).toBe("pi-go agent");
    expect(models[0].targetChatSessionType).toBe("pi-go");
    await expect(provider.provideLanguageModelChatResponse()).rejects.toThrow(
      /answers prompts inside agent sessions/,
    );
    await expect(provider.provideTokenCount()).resolves.toBe(0);
  });

});

describe("wiring and live-update paths", () => {
  function activateWithCtors(): void {
    activate(context());
  }

  function itemProvider(): { provideChatSessionItems: (t: unknown) => Promise<unknown[]>; onDidChangeChatSessionItems: (cb: () => void) => unknown } {
    return chatSpies().registerChatSessionItemProvider.calls[0][1];
  }

  function contentProvider(): {
    provideChatSessionContent: (r: unknown, t: unknown) => Promise<Record<string, unknown>>;
  } {
    return chatSpies().registerChatSessionContentProvider.calls[0][1];
  }

  function openSession(): Promise<Record<string, unknown>> {
    client().listResult = [{ sessionId: "s1", cwd: "/tmp/ws" }];
    return (async () => {
      const items = (await itemProvider().provideChatSessionItems(fakeToken())) as { resource: vscode.Uri }[];
      return contentProvider().provideChatSessionContent(items[0].resource, fakeToken());
    })();
  }

  it("pi-go.attachFile delegates to the panel", () => {
    activateWithCtors();
    vscode.__commands.get("pi-go.attachFile")?.();
    expect(chatPanelState.instances[0].attachFiles).toHaveBeenCalled();
  });

  it("pi-go.refreshSessions and workspace-folder changes refresh the session list", async () => {
    activateWithCtors();
    const events: unknown[] = [];
    itemProvider().onDidChangeChatSessionItems(() => events.push(1));

    vscode.__commands.get("pi-go.refreshSessions")?.();
    expect(events).toHaveLength(1);

    // A workspace switch reconnects the client (new cwd) and refreshes.
    const reconnect = vi.fn(async () => undefined);
    (client() as unknown as { reconnect: unknown }).reconnect = reconnect;
    // calls accumulate across activates in this file; this test's is the last.
    const subs = (vscode.workspace.onDidChangeWorkspaceFolders as unknown as { calls: unknown[][] }).calls;
    const cb = subs[subs.length - 1][0] as () => void;
    cb();
    expect(reconnect).toHaveBeenCalled();
    expect(events).toHaveLength(2);
  });

  it("forwards live editor updates: thoughts, tool updates, junk, other sessions", async () => {
    activateWithCtors();
    const session = await openSession();
    const stream = fakeStream();
    (client() as unknown as { prompt: unknown }).prompt = (async () => {
      const fire = (update: unknown, sessionId = "s1") => client().fireUpdate(update, sessionId);
      fire({ sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "hmm" }, messageId: "m1" });
      fire({ sessionUpdate: "tool_call_update", toolCallId: "t9", name: "bash", title: "Run", status: "completed" });
      fire({ sessionUpdate: "agent_message_chunk", content: { type: "image" } }); // non-text content
      fire({ sessionUpdate: "mystery_event" }); // default switch arm
      fire({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "zz" } }, "other"); // wrong session
      fire(undefined as never); // missing update payload
    }) as never;

    await (session.requestHandler as (...a: unknown[]) => Promise<unknown>)(
      { prompt: "watch", references: [] },
      {},
      stream,
      fakeToken(),
    );

    const pushedNames = stream.push.mock.calls.map(
      (c) => (c[0] as { constructor: { name: string } }).constructor.name,
    );
    expect(pushedNames).toContain("ChatResponseThinkingProgressPart");
    expect(pushedNames).toContain("ChatToolInvocationPart");
    const toolPart = stream.push.mock.calls[1][0] as { toolCallId: string; isComplete?: boolean };
    expect(toolPart.toolCallId).toBe("t9");
    expect(toolPart.isComplete).toBe(true);
    // No markdown leaked for the non-text chunk / other-session update.
    expect(stream.pushed as string[]).not.toContain("zz");
  });

  it("surfaces skipped mentions as a warning part", async () => {
    activateWithCtors();
    const session = await openSession();
    const stream = fakeStream();
    await (session.requestHandler as (...a: unknown[]) => Promise<unknown>)(
      // The file is not in the fake fs → skipped → warning notice.
      { prompt: "hello", references: [{ value: vscode.Uri.file("/tmp/ws/missing.bin") }] },
      {},
      stream,
      fakeToken(),
    );
    const pushedNames = stream.push.mock.calls.map(
      (c) => (c[0] as { constructor: { name: string } }).constructor.name,
    );
    expect(pushedNames).toContain("ChatResponseWarningPart");
    // The skipped file was also not embedded.
  });

  it("notes cancellation right after a silent prompt finishes", async () => {
    activateWithCtors();
    const session = await openSession();
    const token = { isCancellationRequested: false, onCancellationRequested: () => ({ dispose() {} }) };
    (client() as unknown as { prompt: unknown }).prompt = (async (
      _s: string,
      _b: unknown,
      tok: { isCancellationRequested: boolean },
    ) => {
      tok.isCancellationRequested = true;
    }) as never;
    const stream = fakeStream();
    await (session.requestHandler as (...a: unknown[]) => Promise<unknown>)(
      { prompt: "hi", references: [] },
      {},
      stream,
      token as never,
    );
    expect((stream.pushed as string[]).some((s) => s.includes("(cancelled)"))).toBe(true);
  });
});
