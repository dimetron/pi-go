import { beforeEach, describe, expect, it, vi } from "vitest";
import vscode from "vscode";
import { PiGoSessionsProvider, registerSessionsTree, SessionNode } from "../src/sessionsTree";
import { TranscriptStore } from "../src/transcript";
import type { SessionEntry } from "../src/acp";

// Fake ACP client surface used by the provider (enough of PiGoAcpClient).
function fakeClient(list: () => Promise<SessionEntry[]>) {
  return { listSessions: vi.fn(list) } as unknown as {
    listSessions: () => Promise<SessionEntry[]>;
  } & Record<string, unknown>;
}

function entry(partial: Partial<SessionEntry>): SessionEntry {
  return { sessionId: partial.sessionId ?? "s", cwd: partial.cwd ?? "/tmp/ws", ...partial };
}

beforeEach(() => {
  vscode.__reset();
  vscode.__workspaceFolders.push({ uri: vscode.Uri.file("/tmp/ws"), name: "ws", index: 0 });
});

describe("PiGoSessionsProvider.getChildren", () => {
  it("sorts newest first with undated entries last and builds labels", async () => {
    const store = new TranscriptStore();
    const refresh = new vscode.EventEmitter<void>();
    const active = new Set<string>();
    const provider = new PiGoSessionsProvider(
      fakeClient(async () => [
        entry({ sessionId: "old", cwd: "/home/user/proj", updatedAt: Date.now() - 3 * 60_000 }),
        entry({ sessionId: "new", cwd: "/home/user/proj", updatedAt: Date.now() - 30_000 }),
        entry({ sessionId: "undated", cwd: "/home/user/proj" }),
      ]),
      store,
      refresh,
      active,
    );
    const children = await provider.getChildren();
    expect(children.map((c) => c.entry.sessionId)).toEqual(["new", "old", "undated"]);
    expect(children[0].description).toContain("proj");
    expect(children[0].description).toContain("just now");
    expect(children[1].description).toContain("3m ago");
    expect(children[2].description).toBe("proj"); // no timestamp → cwd only
    // Label falls back to the transcript title when the agent sent none.
    store.appendUserTurn("old", "first prompt");
    const again = await provider.getChildren();
    expect(again[1].label).toBe("first prompt");
    provider.dispose();
  });

  it("shows a single-node unavailable message when listSessions fails", async () => {
    const provider = new PiGoSessionsProvider(
      fakeClient(async () => {
        throw new Error("spawn failed");
      }),
      new TranscriptStore(),
      new vscode.EventEmitter<void>(),
      new Set<string>(),
    );
    const children = await provider.getChildren();
    expect(children).toHaveLength(1);
    expect(children[0].label).toBe("pi-go unavailable");
    expect(children[0].description).toBe("check pi-go.command");
    provider.dispose();
  });
});

describe("PiGoSessionsProvider.getTreeItem", () => {
  it("picks the icon by active/replied state", () => {
    const store = new TranscriptStore();
    const refresh = new vscode.EventEmitter<void>();
    const active = new Set<string>(["running"]);
    const provider = new PiGoSessionsProvider(fakeClient(async () => []), store, refresh, active);

    store.appendUserTurn("replied", "q");
    store.appendMessageChunk("replied", "agent", "a", "m1");

    const running = provider.getTreeItem(new SessionNode("r", entry({ sessionId: "running" })));
    expect((running.iconPath as { id: string }).id).toBe("sync~spin");
    expect(running.contextValue).toBe("piGoSessionActive");

    const replied = provider.getTreeItem(new SessionNode("p", entry({ sessionId: "replied" })));
    expect((replied.iconPath as { id: string }).id).toBe("circle-filled");
    expect(replied.contextValue).toBe("piGoSession");

    const idle = provider.getTreeItem(new SessionNode("i", entry({ sessionId: "idle" })));
    expect((idle.iconPath as { id: string }).id).toBe("circle-outline");

    expect(replied.command?.command).toBe("pi-go.openSession");
    expect(replied.tooltip).toBeInstanceOf(vscode.MarkdownString);
    provider.dispose();
  });

  it("fires didChange on refreshTree", () => {
    const provider = new PiGoSessionsProvider(
      fakeClient(async () => []),
      new TranscriptStore(),
      new vscode.EventEmitter<void>(),
      new Set<string>(),
    );
    const fired: unknown[] = [];
    provider.onDidChangeTreeData(() => fired.push(1));
    provider.refreshTree();
    expect(fired).toHaveLength(1);
    provider.dispose();
  });
});

describe("relativeTime", () => {
  it("formats via descriptions", async () => {
    const provider = new PiGoSessionsProvider(
      fakeClient(async () => [
        entry({ sessionId: "a", cwd: "/w", updatedAt: Date.now() - 10_000 }),
        entry({ sessionId: "b", cwd: "/w", updatedAt: Date.now() - 5 * 60_000 }),
        entry({ sessionId: "c", cwd: "/w", updatedAt: Date.now() - 3 * 3600_000 }),
        entry({ sessionId: "d", cwd: "/w", updatedAt: Date.now() - 2 * 24 * 3600_000 }),
        entry({ sessionId: "e", cwd: "/w", updatedAt: Date.now() + 60_000 }), // future → ""
      ]),
      new TranscriptStore(),
      new vscode.EventEmitter<void>(),
      new Set<string>(),
    );
    const children = await provider.getChildren();
    // The future-dated entry sorts first; its delta is negative → no timestamp.
    expect(children.map((c) => c.description)).toEqual([
      "w",
      "w · just now",
      "w · 5m ago",
      "w · 3h ago",
      "w · 2d ago",
    ]);
    provider.dispose();
  });
});

describe("registerSessionsTree", () => {
  it("wires the tree view, badge, and commands", () => {
    const refresh = new vscode.EventEmitter<void>();
    const active = new Set<string>(["s1"]);
    const chatPanel = {
      openSession: vi.fn(async () => undefined),
      startNewSession: vi.fn(),
    };
    const { treeView, provider } = registerSessionsTree(
      { subscriptions: [] } as never,
      fakeClient(async () => []) as never,
      new TranscriptStore(),
      refresh,
      active,
      chatPanel,
    );
    expect(
      (vscode.window.createTreeView as unknown as { calls: unknown[][] }).calls[0],
    ).toEqual(["pi-go.sessions", { treeDataProvider: provider }]);

    // Badge follows the active count.
    refresh.fire();
    expect(treeView.badge).toEqual({ value: 1, tooltip: "1 prompt running" });
    active.delete("s1");
    refresh.fire();
    expect(treeView.badge).toBeUndefined();
    active.add("s1");
    active.add("s2");
    refresh.fire();
    expect(treeView.badge).toEqual({ value: 2, tooltip: "2 prompts running" });

    // Commands are registered and wired.
    const node = new SessionNode("n", entry({ sessionId: "s1" }));
    vscode.__commands.get("pi-go.openSession")?.(node);
    expect(chatPanel.openSession).toHaveBeenCalledWith(node.entry);
    vscode.__commands.get("pi-go.openSession")?.(undefined); // guard: no node
    vscode.__commands.get("pi-go.openSession")?.({ entry: { sessionId: "" } }); // guard: empty id
    expect(chatPanel.openSession).toHaveBeenCalledTimes(1);
    vscode.__commands.get("pi-go.newSession")?.();
    expect(chatPanel.startNewSession).toHaveBeenCalled();

    provider.dispose();
  });
});