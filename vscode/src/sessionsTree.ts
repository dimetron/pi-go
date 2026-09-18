import * as vscode from "vscode";
import { PiGoAcpClient, type SessionEntry } from "./acp";
import type { TranscriptStore } from "./transcript";

/**
 * Sessions tree for the pi-go activity-bar container: persisted sessions from
 * session/list, with the active-prompt count as the view badge.
 */
export class PiGoSessionsProvider implements vscode.TreeDataProvider<SessionNode> {
  private readonly didChange = new vscode.EventEmitter<SessionNode | undefined | void>();
  readonly onDidChangeTreeData = this.didChange.event;
  private readonly refreshSub: vscode.Disposable;

  constructor(
    private readonly client: PiGoAcpClient,
    private readonly store: TranscriptStore,
    private readonly refresh: vscode.EventEmitter<void>,
    private readonly active: Set<string>,
  ) {
    // One clock for everything: transcript writes, the native sessions path,
    // and this tree all refresh off the shared emitter.
    this.refreshSub = refresh.event(() => this.didChange.fire());
  }

  dispose(): void {
    this.refreshSub.dispose();
    this.didChange.dispose();
  }

  refreshTree(): void {
    this.didChange.fire();
  }

  getTreeItem(node: SessionNode): vscode.TreeItem {
    const item = new vscode.TreeItem(node.label);
    item.description = node.description;
    item.tooltip = new vscode.MarkdownString(
      `**${node.label}**\n\n${node.entry.sessionId}\n\n${node.description ?? ""}`,
    );
    item.contextValue = this.active.has(node.entry.sessionId) ? "piGoSessionActive" : "piGoSession";
    item.command = {
      command: "pi-go.openSession",
      title: "Open in chat",
      arguments: [node],
    };
    const hasReply = this.store.snapshot(node.entry.sessionId).some((t) => t.role === "agent");
    if (this.active.has(node.entry.sessionId)) {
      item.iconPath = new vscode.ThemeIcon("sync~spin", new vscode.ThemeColor("charts.green"));
    } else if (hasReply) {
      item.iconPath = new vscode.ThemeIcon("circle-filled");
    } else {
      item.iconPath = new vscode.ThemeIcon("circle-outline");
    }
    return item;
  }

  async getChildren(): Promise<SessionNode[]> {
    let entries: SessionEntry[];
    try {
      entries = await this.client.listSessions();
    } catch {
      const node = new SessionNode("pi-go unavailable", { sessionId: "", cwd: "" }, "check pi-go.command");
      return [node];
    }
    // Newest first; undated entries last.
    entries.sort((a, b) => (b.updatedAt ?? 0) - (a.updatedAt ?? 0));
    return entries.map((entry) => {
      const label = entry.title ?? this.store.title(entry.sessionId);
      const cwd = entry.cwd.split("/").filter(Boolean).pop() ?? entry.cwd;
      const when = relativeTime(entry.updatedAt);
      const description = when ? `${cwd} · ${when}` : cwd;
      return new SessionNode(label, entry, description);
    });
  }
}

export class SessionNode {
  constructor(
    readonly label: string,
    readonly entry: SessionEntry,
    readonly description?: string,
  ) {}
}

function relativeTime(updatedAt: number | undefined): string {
  if (!updatedAt) return "";
  const delta = Date.now() - updatedAt;
  if (delta < 0) return "";
  const minutes = Math.floor(delta / 60_000);
  if (minutes < 1) return "just now";
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

/** Wire the sessions tree, its badge, and the open/new commands. */
export function registerSessionsTree(
  context: vscode.ExtensionContext,
  client: PiGoAcpClient,
  store: TranscriptStore,
  refresh: vscode.EventEmitter<void>,
  active: Set<string>,
  chatPanel: { openSession(entry: SessionEntry): Promise<void>; startNewSession(): void },
): { treeView: vscode.TreeView<SessionNode>; provider: PiGoSessionsProvider } {
  const provider = new PiGoSessionsProvider(client, store, refresh, active);
  const treeView = vscode.window.createTreeView("pi-go.sessions", { treeDataProvider: provider });

  // Badge = number of prompts currently running (Claude/Codex pattern: badge
  // 0 is simply not set).
  context.subscriptions.push(
    refresh.event(() => {
      const value = active.size;
      treeView.badge =
        value > 0
          ? { value, tooltip: value === 1 ? "1 prompt running" : `${value} prompts running` }
          : undefined;
    }),
  );

  context.subscriptions.push(
    vscode.commands.registerCommand("pi-go.openSession", (node: SessionNode) => {
      if (!node?.entry?.sessionId) return;
      void vscode.commands.executeCommand("pi-go.chat.focus");
      void chatPanel.openSession(node.entry);
    }),
    vscode.commands.registerCommand("pi-go.newSession", () => {
      void vscode.commands.executeCommand("pi-go.chat.focus");
      chatPanel.startNewSession();
    }),
  );

  context.subscriptions.push(treeView, provider);
  return { treeView, provider };
}
