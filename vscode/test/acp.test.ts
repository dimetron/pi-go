import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { EventEmitter } from "node:events";
import { PassThrough } from "node:stream";
import * as acp from "@agentclientprotocol/sdk";
import vscode from "vscode";
import { PiGoAcpClient, chunkText, isToolUpdate, thoughtText, toolPayload, workspaceCwd } from "../src/acp";

// The client talks to a real @agentclientprotocol/sdk connection whose
// transport is a fake child process: stdin is a PassThrough the test taps to
// serve JSON-RPC responses, stdout is a PassThrough the test writes requests
// and notifications into.

class FakeChild extends EventEmitter {
  stdin = new PassThrough();
  stdout = new PassThrough();
  stderr = new PassThrough();
  killed = false;
  exitCode: number | null = null;
  kill(): boolean {
    this.killed = true;
    this.exitCode = 0;
    return true;
  }
}

const spawnMock = vi.hoisted(() => vi.fn());
vi.mock("node:child_process", () => ({ spawn: spawnMock }));

// acp.ts creates its output channel once at import time; __reset() clears the
// spy registry, so re-register that channel before each test.
const logChannel = vscode.channelByName("pi-go")!;

const CAPS_OK = {
  protocolVersion: acp.PROTOCOL_VERSION,
  agentCapabilities: {
    sessionCapabilities: { list: {} },
    promptCapabilities: { embeddedContext: true },
  },
};

type Handlers = Record<string, (params: unknown, id: unknown) => unknown>;

/** The fake server answers with a JSON-RPC error when a handler returns this. */
class RpcError extends Error {
  constructor(message: string) {
    super(message);
  }
}

/** Serve JSON-RPC requests written by the client through the fake stdin. */
function serve(child: FakeChild, handlers: Handlers): void {
  let buf = "";
  child.stdin.on("data", (chunk: Buffer) => {
    buf += chunk.toString();
    let idx: number;
    while ((idx = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, idx).trim();
      buf = buf.slice(idx + 1);
      if (!line) continue;
      const msg = JSON.parse(line) as { id?: unknown; method: string; params: unknown };
      const respond = handlers[msg.method];
      if (!respond || msg.id === undefined) continue;
      try {
        const result = respond(msg.params, msg.id);
        child.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id: msg.id, result })}\n`);
      } catch (err) {
        const message = err instanceof RpcError ? err.message : String(err);
        child.stdout.write(
          `${JSON.stringify({ jsonrpc: "2.0", id: msg.id, error: { code: -32000, message } })}\n`,
        );
      }
    }
  });
}

function spawnServing(handlers: Handlers): FakeChild {
  const child = new FakeChild();
  spawnMock.mockImplementation(() => child);
  serve(child, handlers);
  return child;
}

function setWorkspaceRoot(): void {
  vscode.__workspaceFolders.push({ uri: vscode.Uri.file("/tmp/ws"), name: "ws", index: 0 });
}

beforeEach(() => {
  vscode.__reset();
  vscode.outputChannels.push(logChannel);
  setWorkspaceRoot();
  spawnMock.mockReset();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("PiGoAcpClient.ensureConnected", () => {
  it("initializes and maps capabilities (supportsList, embeddedContext)", async () => {
    spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    try {
      await client.listSessions(); // triggers connect + initialize
      expect(client.capabilities).toEqual({
        protocolVersion: acp.PROTOCOL_VERSION,
        supportsList: true,
        embeddedContext: true,
      });
      expect(spawnMock).toHaveBeenCalledWith(
        "pi",
        ["acp-server"],
        expect.objectContaining({ cwd: "/tmp/ws" }),
      );
      expect(client.capabilities?.supportsList).toBe(true);
      expect(client.capabilities?.embeddedContext).toBe(true);
    } finally {
      await client.dispose();
    }
  });

  it("supportsList is false without sessionCapabilities.list", async () => {
    spawnServing({
      initialize: () => ({
        protocolVersion: acp.PROTOCOL_VERSION,
        agentCapabilities: { promptCapabilities: {} },
      }),
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    try {
      await client.listSessions();
      expect(client.capabilities?.supportsList).toBe(false);
      expect(client.capabilities?.embeddedContext).toBe(false);
    } finally {
      await client.dispose();
    }
  });

  it("rejects on protocol-version mismatch instead of hanging", async () => {
    spawnServing({ initialize: () => ({ protocolVersion: acp.PROTOCOL_VERSION + 1 }) });
    const client = new PiGoAcpClient();
    try {
      await expect(
        Promise.race([
          client.listSessions(),
          new Promise((_, rej) => setTimeout(() => rej(new Error("test timeout")), 5_000)),
        ]),
      ).rejects.toThrow(/unsupported ACP protocol version/);
    } finally {
      await client.dispose();
    }
  });

  // The spawn-failure race fix: an async child 'error' event (ENOENT) must
  // reject ensureConnected rather than leave the caller awaiting an initialize
  // response that never arrives.
  it("rejects with the spawn error (ENOENT) — the race fix", async () => {
    spawnMock.mockImplementation(() => {
      const child = new FakeChild();
      setTimeout(() => {
        const err = Object.assign(new Error("spawn pi ENOENT"), { code: "ENOENT" });
        child.emit("error", err);
      }, 10);
      return child;
    });
    const client = new PiGoAcpClient();
    try {
      await expect(
        Promise.race([
          client.listSessions(),
          new Promise((_, rej) => setTimeout(() => rej(new Error("hung: spawn failure was not surfaced")), 3_000)),
        ]),
      ).rejects.toThrow(/ENOENT/);
      // State was dropped: an immediate retry hits the respawn backoff, not a
      // reuse of the dead half-open connection.
      await expect(client.listSessions()).rejects.toThrow(/not running/);
    } finally {
      await client.dispose();
    }
  });

  it("prompt() also rejects on spawn failure", async () => {
    spawnMock.mockImplementation(() => {
      const child = new FakeChild();
      setTimeout(() => child.emit("error", Object.assign(new Error("spawn EACCES"), { code: "EACCES" })), 5);
      return child;
    });
    const client = new PiGoAcpClient();
    try {
      await expect(
        client.prompt("s1", [{ type: "text", text: "hi" }], {
          isCancellationRequested: false,
          onCancellationRequested: () => ({ dispose() {} }),
        } as never),
      ).rejects.toThrow(/EACCES/);
    } finally {
      await client.dispose();
    }
  });

  it("fires onSpawnError with the message", async () => {
    spawnMock.mockImplementation(() => {
      const child = new FakeChild();
      setTimeout(() => child.emit("error", Object.assign(new Error("boom"), { code: "ENOENT" })), 5);
      return child;
    });
    const client = new PiGoAcpClient();
    const errors: string[] = [];
    const sub = client.onSpawnError((m) => errors.push(m));
    try {
      await expect(client.listSessions()).rejects.toThrow(/boom/);
      await vi.waitFor(() => expect(errors).toEqual(["boom"]));
    } finally {
      sub.dispose();
      await client.dispose();
    }
  });

  it("respawns and retries once when the server dies mid-request", async () => {
    const first = new FakeChild();
    spawnMock.mockImplementation(() => first);
    serve(first, {
      initialize: () => CAPS_OK,
      "session/list": () => {
        // Simulate the server dying while handling the request: the request
        // errors AND the process is gone, so the client must respawn.
        first.exitCode = 1;
        first.killed = true;
        throw new RpcError("server died");
      },
    });

    const second = new FakeChild();
    serve(second, {
      initialize: () => CAPS_OK,
      "session/list": () => ({
        sessions: [{ sessionId: "fresh", cwd: "/tmp/ws", title: "t" }],
      }),
    });
    // The retry's ensureConnected respawn gets the healthy server.
    let calls = 0;
    spawnMock.mockImplementation(() => (calls++ === 0 ? first : second));

    const client = new PiGoAcpClient();
    try {
      // The retry path sleeps 250ms before respawning; total must stay well
      // under the default 5s test timeout.
      const entries = await client.listSessions();
      expect(entries.map((e) => e.sessionId)).toEqual(["fresh"]);
      expect(spawnMock).toHaveBeenCalledTimes(2);
    } finally {
      await client.dispose();
    }
  });

  it("retries also cover prompt after a server restart", async () => {
    const first = new FakeChild();
    serve(first, {
      initialize: () => CAPS_OK,
      "session/prompt": () => {
        first.exitCode = 1;
        throw new RpcError("server died");
      },
    });
    const second = new FakeChild();
    serve(second, {
      initialize: () => CAPS_OK,
      "session/prompt": () => ({ stopReason: "end_turn" }),
    });
    let calls = 0;
    spawnMock.mockImplementation(() => (calls++ === 0 ? first : second));

    const client = new PiGoAcpClient();
    try {
      await client.prompt("s1", [{ type: "text", text: "hi" }], {
        isCancellationRequested: false,
        onCancellationRequested: () => ({ dispose() {} }),
      } as never);
      expect(spawnMock).toHaveBeenCalledTimes(2);
    } finally {
      await client.dispose();
    }
  });
});

describe("PiGoAcpClient.listSessions", () => {
  it("returns [] when the agent does not support list", async () => {
    spawnServing({
      initialize: () => ({ protocolVersion: acp.PROTOCOL_VERSION, agentCapabilities: {} }),
    });
    const client = new PiGoAcpClient();
    try {
      expect(await client.listSessions()).toEqual([]);
    } finally {
      await client.dispose();
    }
  });

  it("returns entries, parses updatedAt, and merges known entries", async () => {
    spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({
        sessions: [
          { sessionId: "a", cwd: "/tmp/ws", title: "Agent title", updatedAt: "2026-01-01T00:00:00.000Z" },
          { sessionId: "b", cwd: undefined, title: null, updatedAt: null },
        ],
      }),
      "session/new": () => ({ sessionId: "b" }),
    });
    const client = new PiGoAcpClient();
    try {
      // Make "b" known with a title the list response must preserve.
      await client.newSession();
      const list = await client.listSessions();
      expect(list).toHaveLength(2);
      const a = list.find((e) => e.sessionId === "a")!;
      expect(a.title).toBe("Agent title");
      expect(a.updatedAt).toBe(Date.parse("2026-01-01T00:00:00.000Z"));
      const b = list.find((e) => e.sessionId === "b")!;
      expect(b.title).toBeUndefined(); // null title falls back to known (no title either)
      expect(b.cwd).toBe("/tmp/ws"); // falls back to workspaceCwd
      expect(b.updatedAt).toBeUndefined();
    } finally {
      await client.dispose();
    }
  });

  it("newSession stores the created entry", async () => {
    spawnServing({
      initialize: () => CAPS_OK,
      "session/new": () => ({ sessionId: "new-1" }),
    });
    const client = new PiGoAcpClient();
    try {
      const entry = await client.newSession();
      expect(entry).toEqual({ sessionId: "new-1", cwd: "/tmp/ws" });
    } finally {
      await client.dispose();
    }
  });
});

describe("PiGoAcpClient notifications and lifecycle", () => {
  it("captures available_commands_update notifications", async () => {
    const child = spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    try {
      await client.listSessions();
      child.stdout.write(
        `${JSON.stringify({
          jsonrpc: "2.0",
          method: "session/update",
          params: {
            sessionId: "s1",
            update: {
              sessionUpdate: "available_commands_update",
              availableCommands: [{ name: "plan", description: "Make a plan" }],
            },
          },
        })}\n`,
      );
      await vi.waitFor(() => {
        expect(client.availableCommands("s1")).toEqual([
          { name: "plan", description: "Make a plan" },
        ]);
      });
      expect(client.availableCommands("unknown")).toEqual([]);
    } finally {
      await client.dispose();
    }
  });

  it("fans session updates out to registered listeners", async () => {
    const child = spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    const seen: unknown[] = [];
    const sub = client.onSessionUpdate((u) => seen.push(u));
    try {
      await client.listSessions();
      child.stdout.write(
        `${JSON.stringify({
          jsonrpc: "2.0",
          method: "session/update",
          params: {
            sessionId: "s1",
            update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "hi" } },
          },
        })}\n`,
      );
      await vi.waitFor(() => expect(seen).toHaveLength(1));
      sub.dispose();
      child.stdout.write(
        `${JSON.stringify({
          jsonrpc: "2.0",
          method: "session/update",
          params: { sessionId: "s1", update: { sessionUpdate: "agent_message_chunk", content: { type: "text", text: "x" } } },
        })}\n`,
      );
      await new Promise((r) => setTimeout(r, 20));
      expect(seen).toHaveLength(1);
    } finally {
      await client.dispose();
    }
  });

  it("load() sends session/load", async () => {
    spawnServing({
      initialize: () => CAPS_OK,
      "session/load": (params) => {
        expect(params).toMatchObject({ sessionId: "s1", cwd: "/tmp/ws" });
        return {};
      },
    });
    const client = new PiGoAcpClient();
    try {
      await client.load({ sessionId: "s1", cwd: "/tmp/ws" });
    } finally {
      await client.dispose();
    }
  });

  it("cancel() notifies the server; a broken connection is swallowed", async () => {
    const child = spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const notifications: unknown[] = [];
    child.stdin.on("data", (chunk: Buffer) => {
      const line = chunk.toString().trim();
      if (!line) return;
      const msg = JSON.parse(line) as { method?: string; params?: unknown };
      if (msg.method === "session/cancel") notifications.push(msg.params);
    });
    const client = new PiGoAcpClient();
    try {
      await client.listSessions();
      await client.cancel("s1");
      await vi.waitFor(() => expect(notifications).toEqual([{ sessionId: "s1" }]));
    } finally {
      await client.dispose();
    }

    // Server gone: ensureConnected fails, cancel must still resolve.
    spawnMock.mockImplementation(() => {
      const c = new FakeChild();
      setTimeout(() => c.emit("error", Object.assign(new Error("gone"), { code: "ENOENT" })), 5);
      return c;
    });
    const client2 = new PiGoAcpClient();
    await expect(client2.cancel("s1")).resolves.toBeUndefined();
    await client2.dispose();
  });

  it("dispose() closes the connection and kills the process", async () => {
    const child = spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    await client.listSessions();
    expect(child.killed).toBe(false);
    await client.dispose();
    expect(child.killed).toBe(true);
    // A second dispose is a no-op (spawnError emitter already disposed).
    client.dispose();
  });

  it("reconnect() drops the server so the next call respawns", async () => {
    const first = spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    await client.listSessions();
    await client.reconnect();
    expect(first.killed).toBe(true);
    const second = new FakeChild();
    spawnMock.mockImplementation(() => second);
    serve(second, { initialize: () => CAPS_OK, "session/list": () => ({ sessions: [] }) });
    await client.listSessions();
    expect(spawnMock).toHaveBeenCalledTimes(2);
    await client.dispose();
  });

  it("stderr is appended to the log channel", async () => {
    const child = spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    try {
      await client.listSessions();
      child.stderr.emit("data", Buffer.from("server says hi\n"));
      await vi.waitFor(() => {
        const ch = vscode.channelByName("pi-go");
        expect(ch?.lines.some((l) => l.includes("[acp-server] server says hi"))).toBe(true);
      });
    } finally {
      await client.dispose();
    }
  });

  it("uses the configured command and args", async () => {
    vscode.__config.set("pi-go.command", "my-pi");
    vscode.__config.set("pi-go.args", ["serve", "--acp"]);
    spawnServing({
      initialize: () => CAPS_OK,
      "session/list": () => ({ sessions: [] }),
    });
    const client = new PiGoAcpClient();
    try {
      await client.listSessions();
      expect(spawnMock).toHaveBeenCalledWith("my-pi", ["serve", "--acp"], expect.anything());
    } finally {
      await client.dispose();
    }
  });
});

describe("acp pure helpers", () => {
  const msg = (update: unknown) => ({ sessionId: "s1", update } as never);

  it("chunkText extracts agent/user text chunks only", () => {
    expect(chunkText(msg({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "a" } }))).toBe("a");
    expect(chunkText(msg({ sessionUpdate: "user_message_chunk", content: { type: "text", text: "u" } }))).toBe("u");
    expect(chunkText(msg({ sessionUpdate: "agent_message_chunk", content: { type: "image" } }))).toBeUndefined();
    expect(chunkText(msg({ sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "t" } }))).toBeUndefined();
    expect(chunkText(msg(undefined))).toBeUndefined();
    expect(chunkText(msg({ sessionUpdate: "agent_message_chunk", content: undefined }))).toBeUndefined();
  });

  it("thoughtText extracts thought chunks only", () => {
    expect(thoughtText(msg({ sessionUpdate: "agent_thought_chunk", content: { type: "text", text: "t" } }))).toBe("t");
    expect(thoughtText(msg({ sessionUpdate: "agent_message_chunk", content: { type: "text", text: "m" } }))).toBeUndefined();
    expect(thoughtText(msg(undefined))).toBeUndefined();
    expect(thoughtText(msg({ sessionUpdate: "agent_thought_chunk", content: { type: "x" } }))).toBeUndefined();
  });

  it("isToolUpdate / toolPayload pick tool notifications", () => {
    const tool = { sessionUpdate: "tool_call", toolCallId: "1", title: "T" };
    expect(isToolUpdate(msg(tool))).toBe(true);
    expect(isToolUpdate(msg({ sessionUpdate: "tool_call_update", toolCallId: "1" }))).toBe(true);
    expect(isToolUpdate(msg({ sessionUpdate: "agent_message_chunk" }))).toBe(false);
    expect(toolPayload(msg(tool))).toBe(tool);
    expect(toolPayload(msg({ sessionUpdate: "agent_message_chunk" }))).toBeUndefined();
  });

  it("workspaceCwd falls back to process.cwd() without folders", () => {
    expect(workspaceCwd()).toBe("/tmp/ws");
    vscode.__workspaceFolders.length = 0;
    expect(workspaceCwd()).toBe(process.cwd());
  });
});