// Webview entry: acquires the VS Code API, boots the controller, routes
// postMessage traffic in both directions. Loaded as dist/webview.js (iife).

import type { HostToWebview, WebviewToHost } from "../shared/protocol";
import { ChatController } from "./controller";

declare function acquireVsCodeApi(): {
  postMessage(message: unknown): void;
  getState<T>(): T | undefined;
  setState(state: unknown): void;
};

const vscodeApi = acquireVsCodeApi();

// Restore the composer draft + last session across reloads of the view.
interface PersistedState {
  draft?: string;
  attachments?: string[];
  sessionId?: string;
  welcomeDismissed?: boolean;
}
let persisted: PersistedState | undefined;
try {
  persisted = vscodeApi.getState<PersistedState>();
} catch {
  persisted = undefined;
}

const controller = new ChatController(document.body, {
  post: (message: WebviewToHost) => vscodeApi.postMessage(message),
  setState: (state) => {
    persisted = { ...persisted, ...state };
    try {
      vscodeApi.setState(persisted);
    } catch {
      // Storage can throw in previews; drafts are a convenience only.
    }
  },
  getState: () => persisted,
});

window.addEventListener("message", (event) => {
  const message = event.data as HostToWebview;
  if (!message || typeof message !== "object" || typeof (message as { type?: unknown }).type !== "string") {
    return;
  }
  controller.handle(message);
});

controller.ready();
controller.focus();