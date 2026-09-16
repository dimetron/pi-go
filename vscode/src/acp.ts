import * as vscode from "vscode";
import { ChildProcess, spawn } from "node:child_process";
import { Readable, Writable } from "node:stream";
import * as acp from "@agentclientprotocol/sdk";

export const SESSION_SCHEME = "pi-go-session";
export const SESSION_TYPE = "pi-go";

const log = vscode.window.createOutputChannel("pi-go", { log: true });

// ---------------------------------------------------------------------------
// ACP client — drives `pi acp-server` over stdio.
// ---------------------------------------------------------------------------

export interface SessionEntry {
  sessionId: string;
  title?: string | null;
  updatedAt?: number;
  cwd: string;
}

/** Capabilities pi-go advertises in its initialize response. */
export interface AgentCaps {
  protocolVersion: acp.ProtocolVersion;
  supportsList: boolean;
  embeddedContext: boolean;
}

export type UpdateListener = (update: acp.SessionNotification) => void;

/** Drives `pi acp-server` over stdio and funnels session updates to listeners. */
export class PiGoAcpClient implements vscode.Disposable {
  private process?: ChildProcess;
  private connection?: acp.ClientConnection;
  private initialized = false;
  private readonly listeners = new Set<UpdateListener>();
  private readonly known = new Map<string, SessionEntry>();
  private caps?: AgentCaps;
  private readonly commands = new Map<string, acp.AvailableCommand[]>();
  private readonly spawnError = new vscode.EventEmitter<string>();
  private lastSpawnFailed = 0;

  /** Fires with the error message when the acp-server binary cannot be spawned. */
  readonly onSpawnError = this.spawnError.event;

  onSessionUpdate(listener: UpdateListener): vscode.Disposable {
    this.listeners.add(listener);
    return { dispose: () => this.listeners.delete(listener) };
  }

  get capabilities(): AgentCaps | undefined {
    return this.caps;
  }

  /** Slash commands the agent advertised for this session (available_commands_update). */
  availableCommands(sessionId: string): acp.AvailableCommand[] {
    return this.commands.get(sessionId) ?? [];
  }

  private async ensureConnected(): Promise<acp.ClientConnection> {
    if (this.connection && this.process && !this.process.killed) return this.connection;

    const config = vscode.workspace.getConfiguration("pi-go");
    const command = config.get<string>("command", "pi");
    const args = config.get<string[]>("args", ["acp-server"]);
    const cwd = workspaceCwd();

    // Small backoff so a crashlooping binary does not spin the ext host.
    if (this.lastSpawnFailed && Date.now() - this.lastSpawnFailed < 250) {
      throw new Error(`acp-server is not running (${command})`);
    }

    log.info(`spawning acp-server: ${command} ${args.join(" ")} (cwd ${cwd})`);
    this.process = spawn(command, args, { cwd, stdio: ["pipe", "pipe", "pipe"] });
    this.process.on("error", (err) => {
      this.lastSpawnFailed = Date.now();
      this.spawnError.fire(err.message);
      log.error(`failed to spawn ${command}: ${err.message}`);
    });
    this.process.on("exit", (code, signal) => {
      log.error(`acp-server exited (code ${code}, signal ${signal ?? "none"})`);
      this.connection = undefined;
      this.initialized = false;
    });
    this.process.stderr?.on("data", (data: Buffer) =>
      log.append(`[acp-server] ${data.toString().trimEnd()}`));

    const stream = acp.ndJsonStream(
      Writable.toWeb(this.process.stdin!) as WritableStream<Uint8Array>,
      Readable.toWeb(this.process.stdout!) as ReadableStream<Uint8Array>,
    );
    const client = acp.client({ name: "pi-go-vscode" })
      .onNotification(acp.methods.client.session.update, ({ params }) => {
        if (params.update?.sessionUpdate === "available_commands_update") {
          this.commands.set(params.sessionId, [...params.update.availableCommands]);
        }
        for (const listener of this.listeners) listener(params);
      });
    this.connection = client.connect(stream);
    log.debug("waiting for acp-server initialize response…");
    await this.initializeOnce();
    return this.connection;
  }

  /** Sends initialize once per server process and caches its capabilities. */
  private async initializeOnce(): Promise<void> {
    if (this.initialized && this.connection) return;
    if (!this.connection) throw new Error("acp-server connection is not open");
    const init = await this.connection.agent.request(acp.methods.agent.initialize, {
      protocolVersion: acp.PROTOCOL_VERSION,
      clientCapabilities: {},
      clientInfo: { name: "pi-go-vscode", version: "0.1.0" },
    });
    if (init.protocolVersion !== acp.PROTOCOL_VERSION) {
      log.error(`unsupported ACP protocol version ${String(init.protocolVersion)}`);
      throw new Error(`unsupported ACP protocol version ${String(init.protocolVersion)}`);
    }
    this.caps = {
      protocolVersion: init.protocolVersion,
      supportsList: init.agentCapabilities?.sessionCapabilities?.list !== undefined,
      embeddedContext: init.agentCapabilities?.promptCapabilities?.embeddedContext === true,
    };
    log.info(
      `acp-server initialized (protocol ${init.protocolVersion}, ` +
        `supportsList=${this.caps.supportsList}, embeddedContext=${this.caps.embeddedContext})`,
    );
    this.initialized = true;
  }

  /** Runs one agent request, respawning the server once on transport failure. */
  private async request<R>(method: acp.AgentRequestMethod, params: unknown): Promise<R> {
    const conn = await this.ensureConnected();
    try {
      return await (conn.agent.request as (m: acp.AgentRequestMethod, p: unknown) => Promise<R>)(method, params);
    } catch (err) {
      const dead = !this.process || this.process.exitCode !== null || this.process.killed;
      if (!dead) throw err;
      // The server died under us: respawn once and retry the request.
      log.info("acp-server died mid-request, respawning");
      this.disposeConnection();
      await new Promise((r) => setTimeout(r, 250));
      const retry = await this.ensureConnected();
      return await (retry.agent.request as (m: acp.AgentRequestMethod, p: unknown) => Promise<R>)(method, params);
    }
  }

  async newSession(): Promise<SessionEntry> {
    const res = await this.request<acp.NewSessionResponse>(acp.methods.agent.session.new, {
      cwd: workspaceCwd(),
      mcpServers: [],
    });
    const entry: SessionEntry = { sessionId: res.sessionId, cwd: workspaceCwd() };
    this.known.set(entry.sessionId, entry);
    return entry;
  }

  /** Returns persisted sessions when the agent supports session/list. */
  async listSessions(): Promise<SessionEntry[]> {
    await this.ensureConnected();
    if (!this.caps?.supportsList) return [];

    const res = await this.request<acp.ListSessionsResponse>(acp.methods.agent.session.list, {
      cwd: workspaceCwd(),
    });
    const out: SessionEntry[] = [];
    for (const s of res.sessions) {
      const existing = this.known.get(s.sessionId);
      const entry: SessionEntry = {
        sessionId: s.sessionId,
        title: s.title ?? existing?.title,
        updatedAt: s.updatedAt ? Date.parse(s.updatedAt) : existing?.updatedAt,
        cwd: s.cwd ?? existing?.cwd ?? workspaceCwd(),
      };
      this.known.set(s.sessionId, entry);
      out.push(entry);
    }
    return out;
  }

  /** Replays a persisted transcript through session/update events. */
  async load(entry: SessionEntry): Promise<void> {
    await this.request<acp.LoadSessionResponse>(acp.methods.agent.session.load, {
      sessionId: entry.sessionId,
      cwd: entry.cwd || workspaceCwd(),
      mcpServers: [],
    });
  }

  /** Run one prompt turn with the given content blocks; stream the result by
   *  listening to onSessionUpdate — every listener sees the live updates. */
  async prompt(
    sessionId: string,
    blocks: acp.ContentBlock[],
    token: vscode.CancellationToken,
  ): Promise<void> {
    const cancel = token.onCancellationRequested(() => {
      void this.cancel(sessionId);
    });
    try {
      await this.request<acp.PromptResponse>(acp.methods.agent.session.prompt, {
        sessionId,
        prompt: blocks,
      });
    } finally {
      cancel.dispose();
    }
  }

  async cancel(sessionId: string): Promise<void> {
    try {
      const conn = await this.ensureConnected();
      void conn.agent.notify(acp.methods.agent.session.cancel, { sessionId });
    } catch {
      // The server may already be gone; cancellation is best-effort.
    }
  }

  /** Kill the server and drop all connection state; the next request respawns. */
  async reconnect(): Promise<void> {
    this.disposeConnection();
  }

  private disposeConnection(): void {
    this.connection?.close();
    this.process?.kill();
    this.connection = undefined;
    this.process = undefined;
    this.initialized = false;
    this.caps = undefined;
    this.commands.clear();
  }

  dispose(): void {
    this.disposeConnection();
    this.spawnError.dispose();
  }
}

export function workspaceCwd(): string {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? process.cwd();
}

/** Text of a session-update message chunk, when the block carries text. */
export function chunkText(update: acp.SessionNotification): string | undefined {
  const u = update.update;
  if (!u || (u.sessionUpdate !== "agent_message_chunk" && u.sessionUpdate !== "user_message_chunk")) {
    return undefined;
  }
  const block = u.content;
  if (!block || block.type !== "text") return undefined;
  return block.text;
}

/** Text of a thinking chunk, when present. */
export function thoughtText(update: acp.SessionNotification): string | undefined {
  const u = update.update;
  if (!u || u.sessionUpdate !== "agent_thought_chunk") return undefined;
  const block = u.content;
  if (!block || block.type !== "text") return undefined;
  return block.text;
}

/** True when the update is a tool_call or tool_call_update notification. */
export function isToolUpdate(update: acp.SessionNotification): boolean {
  const u = update.update;
  return u?.sessionUpdate === "tool_call" || u?.sessionUpdate === "tool_call_update";
}

/** The tool-call payload of a tool_call/tool_call_update notification. */
export function toolPayload(update: acp.SessionNotification): acp.ToolCallUpdate | undefined {
  if (!isToolUpdate(update)) return undefined;
  return update.update as unknown as acp.ToolCallUpdate;
}