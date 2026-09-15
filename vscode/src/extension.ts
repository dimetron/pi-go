import * as vscode from "vscode";
import {
  PiGoAcpClient,
  type SessionEntry,
  SESSION_SCHEME,
  SESSION_TYPE,
  updateText,
} from "./acp";
import {
  chatParts,
  type ChatNamespaceWithSessions,
  type ChatSessionContentProviderDto,
  type ChatSessionItemDto,
  type ChatSessionItemProviderDto,
  type ChatSessionDto,
  type ChatTurnConstructors,
} from "./types/chatApi";

const log = vscode.window.createOutputChannel("pi-go", { log: true });

function errString(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "object" && err !== null && "message" in err) {
    return String((err as { message: unknown }).message);
  }
  return String(err);
}

// ---------------------------------------------------------------------------
// Transcript store — one entry per ACP session id.
// ---------------------------------------------------------------------------

interface Turn {
  role: "user" | "agent";
  text: string;
}

class TranscriptStore {
  private readonly sessions = new Map<string, vscode.Uri>();
  private readonly byUri = new Map<string, string>();
  private readonly turns = new Map<string, Turn[]>();
  private seq = 0;

  register(acpId: string): vscode.Uri {
    const existing = this.sessions.get(acpId);
    if (existing) return existing;
    const uri = vscode.Uri.parse(`${SESSION_SCHEME}:local/${Date.now().toString(36)}-${this.seq++}`);
    this.sessions.set(acpId, uri);
    this.byUri.set(uri.toString(), acpId);
    this.turns.set(acpId, []);
    return uri;
  }

  /** Drop the in-memory transcript before a session/load replay, so repeated
   *  resolves of the same session do not stack duplicate turns. */
  reset(acpId: string): void {
    this.turns.set(acpId, []);
  }

  uriFor(acpId: string): vscode.Uri | undefined {
    return this.sessions.get(acpId);
  }

  acpIdFor(uri: vscode.Uri): string | undefined {
    return this.byUri.get(uri.toString());
  }

  turnList(acpId: string): Turn[] {
    let t = this.turns.get(acpId);
    if (!t) {
      t = [];
      this.turns.set(acpId, t);
    }
    return t;
  }

  append(acpId: string, turn: Turn): void {
    const list = this.turnList(acpId);
    const last = list[list.length - 1];
    if (last && last.role === "agent" && turn.role === "agent") {
      last.text += turn.text;
    } else {
      list.push(turn);
    }
  }

  title(acpId: string): string {
    const first = this.turnList(acpId).find((t) => t.role === "user");
    const line = first?.text.trim().split("\n")[0] ?? "";
    if (!line) return "pi-go session";
    return line.length > 60 ? `${line.slice(0, 60)}…` : line;
  }
}

// ---------------------------------------------------------------------------

export function activate(context: vscode.ExtensionContext): void {
  const client = new PiGoAcpClient();
  const store = new TranscriptStore();
  const refresh = new vscode.EventEmitter<void>();
  context.subscriptions.push(client, refresh);

  // Keep the transcript in sync from raw ACP updates (covers session/load replay).
  context.subscriptions.push(
    client.onSessionUpdate((update) => {
      const text = updateText(update);
      if (!text) return;
      const isUser = update.update?.sessionUpdate === "user_message_chunk";
      store.append(update.sessionId, { role: isUser ? "user" : "agent", text });
      refresh.fire();
    }),
  );

  const chat = vscode.chat as unknown as ChatNamespaceWithSessions;
  const sessionsAvailable =
    typeof chat.registerChatSessionItemProvider === "function" &&
    typeof chat.registerChatSessionContentProvider === "function";

  if (sessionsAvailable) {
    activateNative(context, client, store, refresh, chat);
  } else {
    log.error(
      "chat session APIs unavailable. Launch VS Code with " +
        "--enable-proposed-api pi-go.pi-go-vscode to get native agent sessions.",
    );
  }

  // Fallback: quick one-shot prompt through an output channel.
  context.subscriptions.push(
    vscode.commands.registerCommand("pi-go.start", async () => {
      const input = await vscode.window.showInputBox({
        prompt: "Ask pi-go",
        placeHolder: "Explain this code…",
      });
      if (!input) return;
      try {
        const entry = await client.newSession();
        const out = vscode.window.createOutputChannel("pi-go chat");
        out.show(true);
        context.subscriptions.push(out);
        context.subscriptions.push(
          client.onSessionUpdate((update) => {
            if (update.sessionId !== entry.sessionId) return;
            const text = updateText(update);
            if (text && update.update?.sessionUpdate === "agent_message_chunk") {
              out.append(text);
            }
          }),
        );
        out.appendLine(`You: ${input}`);
        out.append("pi-go: ");
        await client.prompt(entry.sessionId, input, () => {}, new vscode.CancellationTokenSource().token);
        out.appendLine("");
      } catch (err) {
        void vscode.window.showErrorMessage(`pi-go failed: ${errString(err)}`);
      }
    }),
  );
}

function activateNative(
  context: vscode.ExtensionContext,
  client: PiGoAcpClient,
  store: TranscriptStore,
  refresh: vscode.EventEmitter<void>,
  chat: ChatNamespaceWithSessions,
): void {
  const ctors = chatParts() as ChatTurnConstructors | undefined;
  if (!ctors || !ctors.ChatRequestTurn || !ctors.ChatResponseTurn || !ctors.ChatResponseMarkdownPart) {
    log.error("chat turn constructors not found; history will render empty");
  }

  // -------------------------------------------------------------------------
  // Item provider — sessions listed in the Agent Sessions view.
  // -------------------------------------------------------------------------
  const itemProvider: ChatSessionItemProviderDto = {
    onDidChangeChatSessionItems: refresh.event,
    provideChatSessionItems: async (_token) => {
      try {
        const entries = await client.listSessions();
        return entries.map((entry: SessionEntry): ChatSessionItemDto => {
          const uri = store.register(entry.sessionId);
          const acpId = entry.sessionId;
          const hasReply = [...store.turnList(acpId)].reverse().some((t) => t.role === "agent");
          return {
            resource: uri,
            label: entry.title ?? store.title(acpId),
            status: hasReply ? 1 : undefined,
            timing:
              entry.updatedAt !== undefined
                ? { created: entry.updatedAt, lastRequestEnded: entry.updatedAt }
                : undefined,
          };
        });
      } catch (err) {
        log.error(`provideChatSessionItems failed: ${errString(err)}`);
        return [];
      }
    },
  };

  // -------------------------------------------------------------------------
  // Content provider — transcript rendering + prompt routing.
  // -------------------------------------------------------------------------
  const participant = vscode.chat.createChatParticipant(`${context.extension.id}.agent`, () => {
    // Required argument of registerChatSessionContentProvider; prompts for
    // content-provider sessions are routed to the session requestHandler.
    // The participant's display name comes from package.json chatParticipants.
  });

  const contentProvider: ChatSessionContentProviderDto = {
    provideChatSessionContent: async (resource, _token) => {
      const acpId = store.acpIdFor(resource);
      if (!acpId) throw new Error(`pi-go: unknown session resource ${resource.toString()}`);

      // Replay the persisted transcript over ACP so stored turns land in the
      // transcript store through the session-update listener. Reset first: the
      // replay appends from a clean slate each time the session is resolved.
      const entry: SessionEntry = { sessionId: acpId, cwd: workspaceCwd() };
      store.reset(acpId);
      try {
        await client.load(entry);
      } catch (err) {
        log.error(`session/load for ${acpId}: ${errString(err)}`);
      }

      return {
        title: store.title(acpId),
        history: toHistory(store.turnList(acpId), ctors),
        activeResponseCallback: undefined,
        requestHandler: createRequestHandler(client, acpId),
        options: undefined,
      };
    },
  };

  context.subscriptions.push(
    chat.registerChatSessionItemProvider(SESSION_TYPE, itemProvider),
    // The content provider is resolved by the session resource's URI scheme,
    // not by the chat session type — item-provider URIs use pi-go-session.
    chat.registerChatSessionContentProvider(SESSION_SCHEME, contentProvider, participant),
  );

  // -------------------------------------------------------------------------
  // Language model stub — lets the sessions editor resolve request.model
  // without a Copilot sign-in. Generation never goes through it: prompts are
  // answered by the pi-go agent inside the session requestHandler.
  // -------------------------------------------------------------------------
  const modelInfo: vscode.LanguageModelChatInformation = {
    id: "agent",
    name: "pi-go agent",
    family: "pi-go",
    version: "1.0.0",
    maxInputTokens: 1_000_000,
    maxOutputTokens: 1_000_000,
    capabilities: { toolCalling: true },
  };
  context.subscriptions.push(
    vscode.lm.registerLanguageModelChatProvider("pi-go", {
      provideLanguageModelChatInformation: async () => [modelInfo],
      provideLanguageModelChatResponse: async () => {
        throw new Error(
          "pi-go answers prompts inside agent sessions, not through the language model API.",
        );
      },
      provideTokenCount: async () => 0,
    }),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("pi-go.refreshSessions", () => refresh.fire()),
  );
}

// ---------------------------------------------------------------------------
// requestHandler — bridge a VS Code chat prompt to ACP session/prompt.
// ---------------------------------------------------------------------------

function createRequestHandler(client: PiGoAcpClient, acpId: string): vscode.ChatRequestHandler {
  return async (request, _context, stream, token) => {
    stream.progress("Talking to pi-go…");
    let wrote = false;
    try {
      await client.prompt(
        acpId,
        request.prompt,
        (chunk) => {
          if (!chunk) return;
          wrote = true;
          stream.markdown(chunk);
        },
        token,
      );
      if (!wrote) stream.markdown("_(no response)_");
      return { metadata: { agent: "pi-go" } };
    } catch (err) {
      if (token.isCancellationRequested) {
        stream.markdown("\n\n_(cancelled)_");
        return { metadata: { cancelled: true } };
      }
      const msg = errString(err);
      log.error(`prompt failed: ${msg}`);
      stream.markdown(`\n\n**pi-go error:** ${msg}`);
      return { errorDetails: { message: msg } };
    }
  };
}

/** Build real ChatRequestTurn/ChatResponseTurn instances for session history. */
function toHistory(
  turns: Array<{ role: "user" | "agent"; text: string }>,
  ctors: ChatTurnConstructors | undefined,
): unknown[] {
  if (!ctors) return [];
  const out: unknown[] = [];
  for (const turn of turns) {
    if (turn.role === "user") {
      out.push(new ctors.ChatRequestTurn(turn.text, undefined, [], "pi-go", []));
    } else {
      const part = new ctors.ChatResponseMarkdownPart(turn.text);
      out.push(new ctors.ChatResponseTurn([part], {}, "pi-go"));
    }
  }
  return out;
}

function workspaceCwd(): string {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? process.cwd();
}

export function deactivate(): void {
  // Subscriptions already dispose the client (killing the acp-server) and the
  // output channels; nothing extra to release.
}