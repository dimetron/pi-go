import * as vscode from "vscode";
import {
  PiGoAcpClient,
  chunkText,
  type SessionEntry,
  SESSION_SCHEME,
  SESSION_TYPE,
} from "./acp";
import {
  chatParts,
  type ChatNamespaceWithSessions,
  type ChatSessionContentProviderDto,
  type ChatSessionDto,
  type ChatSessionItemDto,
  type ChatSessionItemProviderDto,
  type ChatTurnConstructors,
} from "./types/chatApi";
import { TranscriptStore, recordUpdate, toolStateOf, type ToolCallState } from "./transcript";
import {
  availableCommandsMarkdown,
  historyFromTurns,
  noticePartFor,
  thoughtPartFor,
  toolPartFor,
} from "./chatParts";
import { referencesToBlocks, skippedMentionsMarkdown } from "./mentions";
import { ChatPanelProvider } from "./chatPanel";
import { registerSessionsTree } from "./sessionsTree";

const log = vscode.window.createOutputChannel("pi-go", { log: true });

function errString(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "object" && err !== null && "message" in err) {
    return String((err as { message: unknown }).message);
  }
  return String(err);
}

// ---------------------------------------------------------------------------

export function activate(context: vscode.ExtensionContext): void {
  const client = new PiGoAcpClient();
  const store = new TranscriptStore();
  const refresh = new vscode.EventEmitter<void>();
  const active = new Set<string>(); // ACP ids with an in-flight prompt
  context.subscriptions.push(client, refresh);

  // Single writer to the transcript: every live prompt and session/load replay
  // funnels through here.
  context.subscriptions.push(
    client.onSessionUpdate((update) => {
      if (recordUpdate(store, update)) refresh.fire();
    }),
  );

  // Surface spawn failures (bad pi-go.command, missing binary).
  context.subscriptions.push(
    client.onSpawnError((message) => {
      void vscode.window
        .showErrorMessage(`pi-go failed to start: ${message}`, "Open Settings")
        .then((pick) => {
          if (pick === "Open Settings") {
            void vscode.commands.executeCommand(
              "workbench.action.openSettings",
              "pi-go.command",
            );
          }
        });
    }),
  );

  const chat = vscode.chat as unknown as ChatNamespaceWithSessions;
  const sessionsAvailable =
    typeof chat.registerChatSessionItemProvider === "function" &&
    typeof chat.registerChatSessionContentProvider === "function";

  try {
    if (sessionsAvailable) {
      activateNative(context, client, store, refresh, active, chat);
    } else {
      log.error(
        "chat session APIs unavailable. Launch VS Code with " +
          "--enable-proposed-api pi-go.pi-go-vscode to get native agent sessions.",
      );
    }
  } catch (err) {
    // A gated proposed API throws mid-activation; every command below must
    // still register or the UI dead-ends with "command not found". The
    // dedicated chat view works without native sessions.
    log.error(`native agent sessions setup failed: ${errString(err)}`);
  }

  // Public API in both modes: view-title refresh button.
  context.subscriptions.push(
    vscode.commands.registerCommand("pi-go.refreshSessions", () => refresh.fire()),
  );

  // Smoke hook for scripts/smoke.sh: prove activation and the ACP roundtrip
  // by writing progress lines to PI_GO_SMOKE_FILE (node fs in the ext host;
  // output channels flush lazily and are useless as a sync signal).
  if (process.env.PI_GO_SMOKE === "1" && process.env.PI_GO_SMOKE_FILE) {
    void selfCheck(client, process.env.PI_GO_SMOKE_FILE);
  }

  // Dedicated chat tab: one ChatPanelProvider instance serves both the
  // activity-bar container and the secondary-sidebar container (Claude's
  // pattern). Works without the proposed-API flag.
  const panel = new ChatPanelProvider(context, client, store, refresh, active);
  context.subscriptions.push(
    panel,
    vscode.window.registerWebviewViewProvider("pi-go.chat", panel, {
      webviewOptions: { retainContextWhenHidden: true },
    }),
    vscode.window.registerWebviewViewProvider("pi-go.chatSecondary", panel, {
      webviewOptions: { retainContextWhenHidden: true },
    }),
    vscode.commands.registerCommand("pi-go.attachFile", () => panel.attachFiles()),
  );

  // Sessions tree with the running-prompt badge (also drives pi-go.openSession
  // / pi-go.newSession).
  registerSessionsTree(context, client, store, refresh, active, panel);

  // Fallback: quick one-shot prompt through an output channel.
  let quickChat: vscode.OutputChannel | undefined;
  context.subscriptions.push(
    vscode.commands.registerCommand("pi-go.start", async () => {
      const input = await vscode.window.showInputBox({
        prompt: "Ask pi-go",
        placeHolder: "Explain this code…",
      });
      if (!input) return;
      try {
        const entry = await client.newSession();
        quickChat ??= vscode.window.createOutputChannel("pi-go chat");
        quickChat.show(true);
        context.subscriptions.push(
          client.onSessionUpdate((update) => {
            if (update.sessionId !== entry.sessionId) return;
            if (update.update?.sessionUpdate !== "agent_message_chunk") return;
            const text = chunkText(update);
            if (text) quickChat?.append(text);
          }),
        );
        quickChat.appendLine(`You: ${input}`);
        quickChat.append("pi-go: ");
        await client.prompt(
          entry.sessionId,
          [{ type: "text", text: input }],
          new vscode.CancellationTokenSource().token,
        );
        quickChat.appendLine("");
      } catch (err) {
        void vscode.window.showErrorMessage(`pi-go failed: ${errString(err)}`);
      }
    }),
  );
}

/** Smoke self-check: start the acp-server, create a session, prompt it, write markers. */
async function selfCheck(client: PiGoAcpClient, file: string): Promise<void> {
  const { appendFileSync } = require("node:fs") as typeof import("node:fs");
  const step = (msg: string) => {
    try {
      appendFileSync(file, `[smoke] ${msg}\n`);
    } catch {
      /* best effort */
    }
    log.info(`[smoke] ${msg}`);
  };
  try {
    step("extension activated");
    const entry = await client.newSession();
    step(`acp-server up, session ${entry.sessionId}`);
    const chunks: string[] = [];
    const sub = client.onSessionUpdate((update) => {
      if (update.sessionId !== entry.sessionId) return;
      if (update.update?.sessionUpdate !== "agent_message_chunk") return;
      const text = chunkText(update);
      if (text) chunks.push(text);
    });
    try {
      await client.prompt(entry.sessionId, [{ type: "text", text: "smoke" }], {
        isCancellationRequested: false,
        onCancellationRequested: () => ({ dispose: () => {} }),
      } as unknown as vscode.CancellationToken);
      step(`prompt roundtrip ok, ${chunks.length} chunk(s): ${chunks.join("").trim().slice(0, 80)}`);
    } finally {
      sub.dispose();
    }
    step("SMOKE-OK");
  } catch (err) {
    step(`SMOKE-FAIL ${errString(err)}`);
  }
}

function activateNative(
  context: vscode.ExtensionContext,
  client: PiGoAcpClient,
  store: TranscriptStore,
  refresh: vscode.EventEmitter<void>,
  active: Set<string>,
  chat: ChatNamespaceWithSessions,
): void {
  const ctors = chatParts() as ChatTurnConstructors;
  if (!ctors.ChatRequestTurn || !ctors.ChatResponseTurn2 || !ctors.ChatResponseMarkdownPart) {
    log.error("chat turn constructors not found; history will render empty");
  }

  // A workspace switch changes the cwd every session is rooted in: drop the
  // server (the next request respawns in the new cwd) and refresh the list.
  context.subscriptions.push(
    vscode.workspace.onDidChangeWorkspaceFolders(() => {
      void client.reconnect();
      refresh.fire();
    }),
  );

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
          const hasReply = store.snapshot(acpId).some((t) => t.role === "agent");
          return {
            resource: uri,
            label: entry.title ?? store.title(acpId),
            status: active.has(acpId) ? 2 : hasReply ? 1 : undefined,
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

  // Slash-command autocomplete for the pi-go agent's advertised commands.
  // Whether the sessions editor consults this depends on the surface; the
  // requestHandler also intercepts /help, so this is a progressive
  // enhancement only. participantVariableProvider sits behind the
  // chatParticipantAdditions gate — setting it without --enable-proposed-api
  // throws, so it is set through a guarded helper.
  let lastActive: string | undefined;
  const variable = participant as vscode.ChatParticipant & {
    participantVariableProvider?: {
      provider: {
        provideCompletionItems(
          query: string,
          token: vscode.CancellationToken,
        ): vscode.ProviderResult<unknown[]>;
      };
      triggerCharacters: string[];
    };
  };
  if (!guardedProposed(() => {
    variable.participantVariableProvider = {
      provider: {
        provideCompletionItems: (query, _token) => {
          if (!lastActive) return [];
          const Ctor = ctorOf<"ChatCompletionItem">(ctors, "ChatCompletionItem");
          if (!Ctor) return [];
          return client.availableCommands(lastActive).map((c) => {
            const item = new Ctor(c.name, `/${c.name}`, []) as { insertText?: string; detail?: string };
            item.insertText = `/${c.name}`;
            item.detail = c.description ?? "";
            return item;
          });
        },
      },
      triggerCharacters: ["/"],
    };
  }, "participantVariableProvider")) {
    log.error(
      "slash-command autocomplete skipped (chatParticipantAdditions gated). " +
        "Launch with --enable-proposed-api pi-go.pi-go-vscode to enable.",
    );
  }

  const contentProvider: ChatSessionContentProviderDto = {
    provideChatSessionContent: async (resource, _token) => {
      const acpId = store.acpIdFor(resource);
      if (!acpId) throw new Error(`pi-go: unknown session resource ${resource.toString()}`);
      lastActive = acpId;

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
        history: historyFromTurns(store.snapshot(acpId), ctors),
        activeResponseCallback: undefined,
        requestHandler: createRequestHandler(client, store, refresh, active, acpId, ctors),
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
  // targetChatSessionType scopes the model to pi-go agent sessions: without it
  // the stub shows up in the regular Chat panel's model picker, where a picked
  // prompt routes to provideLanguageModelChatResponse below and errors. The
  // field is copied by the 1.137 ext host but is missing from the stable d.ts,
  // so the literal is built loosely and cast. Deliberately no
  // isUserSelectable: false — that flag also hides the model from typed
  // sessions, where it must stay resolvable.
  const modelInfo = {
    id: "agent",
    name: "pi-go agent",
    family: "pi-go",
    version: "1.0.0",
    maxInputTokens: 1_000_000,
    maxOutputTokens: 1_000_000,
    capabilities: { toolCalling: true },
    targetChatSessionType: SESSION_TYPE,
  } as vscode.LanguageModelChatInformation;
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
}

// ---------------------------------------------------------------------------
// requestHandler — bridge a VS Code chat prompt to ACP session/prompt.
// ---------------------------------------------------------------------------

function createRequestHandler(
  client: PiGoAcpClient,
  store: TranscriptStore,
  refresh: vscode.EventEmitter<void>,
  active: Set<string>,
  acpId: string,
  ctors: ChatTurnConstructors,
): vscode.ChatRequestHandler {
  return async (request, _context, stream, token) => {
    stream.progress("Talking to pi-go…");
    const prompt = request.prompt.trim();

    // pi-go's ACP path does not dispatch slash commands (its TUI does), so
    // the universal ones are handled locally; everything else is forwarded.
    if (prompt === "/clear") {
      try {
        const entry = await client.newSession();
        const uri = store.uriFor(acpId);
        if (uri) store.rebind(uri, entry.sessionId);
        store.appendUserTurn(entry.sessionId, "/clear");
        stream.markdown(
          "_Started a fresh session._ The previous transcript is still on disk — " +
            "reopen it from the sessions list.",
        );
        refresh.fire();
        return { metadata: { cleared: true, newSessionId: entry.sessionId } };
      } catch (err) {
        stream.markdown(`**pi-go error:** ${errString(err)}`);
        return { errorDetails: { message: errString(err) } };
      }
    }
    if (prompt === "/help") {
      const commands = client.availableCommands(acpId);
      stream.markdown(availableCommandsMarkdown(commands));
      stream.markdown(
        "\n\n_Note: slash commands are dispatched only in pi-go's TUI today; in VS Code " +
          "sessions they are forwarded to the model as text (with `/clear` and `/help` " +
          "handled by the extension)._",
      );
      return { metadata: { help: true } };
    }

    try {
      const wasSlash = prompt.startsWith("/");
      if (wasSlash && prompt !== "/clear" && prompt !== "/help") {
        const part = noticePartFor(
          "Slash commands are forwarded to pi-go as plain text in VS Code sessions.",
          ctors,
        );
        if (part) pushPart(stream, part);
        else stream.markdown("\n\n_Slash commands are forwarded to pi-go as plain text in VS Code sessions._");
      }
      await runPrompt(client, store, refresh, acpId, request, stream, token, ctors);
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
    } finally {
      refresh.fire();
    }
  };
}

async function runPrompt(
  client: PiGoAcpClient,
  store: TranscriptStore,
  refresh: vscode.EventEmitter<void>,
  acpId: string,
  request: vscode.ChatRequest,
  stream: vscode.ChatResponseStream,
  token: vscode.CancellationToken,
  ctors: ChatTurnConstructors,
): Promise<void> {
  // Editor-side listener: renders live updates into the chat editor. The
  // transcript store is fed by the global session-update listener instead.
  const editorListener = client.onSessionUpdate((update) => {
    if (update.sessionId !== acpId || token.isCancellationRequested) return;
    const u = update.update;
    if (!u) return;
    switch (u.sessionUpdate) {
      case "agent_message_chunk": {
        const block = u.content;
        if (block?.type === "text" && block.text) stream.markdown(block.text);
        break;
      }
      case "agent_thought_chunk": {
        const block = u.content;
        if (block?.type === "text" && block.text) {
          const part = thoughtPartFor(block.text, u.messageId ?? undefined, ctors);
          if (part) pushPart(stream, part);
          else stream.markdown(`\n\n> _thinking…_ ${block.text}\n`);
        }
        break;
      }
      case "tool_call":
      case "tool_call_update": {
        const state = toolStateOf(u);
        const part = toolPartFor(state, ctors, { live: true });
        if (part) pushPart(stream, part);
        break;
      }
      default:
        break;
    }
  });

  // Assemble the prompt: the text plus @-mention files as embedded resources
  // (pi-go advertises embeddedContext for text content).
  const mentions = await referencesToBlocks(request.references, token);
  const blocks: Array<{ type: "text"; text: string } | (typeof mentions.blocks)[number]> = [
    { type: "text", text: request.prompt },
  ];
  if (client.capabilities?.embeddedContext && mentions.blocks.length) {
    blocks.push(...mentions.blocks);
  }
  const skipNotice = skippedMentionsMarkdown(mentions.skipped);
  if (skipNotice) {
    const part = noticePartFor(skipNotice, ctors, true);
    if (part) pushPart(stream, part);
    else stream.markdown(`\n\n_${skipNotice}_`);
  }

  store.appendUserTurn(acpId, request.prompt);
  try {
    await client.prompt(acpId, blocks, token);
    if (token.isCancellationRequested) stream.markdown("\n\n_(cancelled)_");
    else if (!store.snapshot(acpId).some((t) => t.role === "agent")) stream.markdown("_(no response)_");
    refresh.fire();
  } finally {
    editorListener.dispose();
  }
}

function ctorOf<K extends keyof ChatTurnConstructors>(
  ctors: ChatTurnConstructors,
  name: K,
): ChatTurnConstructors[K] | undefined {
  const value = (ctors as unknown as Record<string, unknown>)[name as string];
  return typeof value === "function" ? (value as ChatTurnConstructors[K]) : undefined;
}

/**
 * Push a proposed-API part into the live stream. The public d.ts narrows
 * push() to plain ChatResponsePart; the extended overload (chatParticipantAdditions)
 * accepts these parts at runtime, so the cast only crosses the d.ts gap.
 */
function pushPart(stream: vscode.ChatResponseStream, part: unknown): void {
  stream.push(part as vscode.ChatResponsePart);
}

/**
 * Run a proposed-API touchpoint under the ext-host gate. Without
 * --enable-proposed-api the ext host throws on the property setter itself;
 * catching here keeps activation alive. Returns false when the call was
 * blocked so callers can log a degradation notice.
 */
function guardedProposed(fn: () => void, what: string): boolean {
  try {
    fn();
    return true;
  } catch (err) {
    log.error(`proposed API rejected (${what}): ${errString(err)}`);
    return false;
  }
}

function workspaceCwd(): string {
  return vscode.workspace.workspaceFolders?.[0]?.uri.fsPath ?? process.cwd();
}

export function deactivate(): void {
  // Subscriptions already dispose the client (killing the acp-server) and the
  // output channels; nothing extra to release.
}