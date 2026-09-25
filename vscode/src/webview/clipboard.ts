// Copy-to-clipboard button shared by tool cards and markdown code blocks.
// Falls back to a hidden textarea + execCommand when the async Clipboard API
// is unavailable in the webview context (no secure-context flag, denied
// permission) so the control still works instead of silently doing nothing.

import { icon } from "./icons";

export function copyButton(label: string, getText: () => string): HTMLButtonElement {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "copy-button";
  button.title = label;
  button.setAttribute("aria-label", label);
  button.append(icon("copy"));
  button.addEventListener("click", () => {
    void copyText(getText()).then((ok) => flashCopyState(button, ok));
  });
  return button;
}

async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return legacyCopy(text);
  }
}

function legacyCopy(text: string): boolean {
  const area = document.createElement("textarea");
  area.value = text;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.append(area);
  area.select();
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  area.remove();
  return ok;
}

function flashCopyState(button: HTMLButtonElement, ok: boolean): void {
  button.classList.toggle("copied", ok);
  button.classList.toggle("copy-failed", !ok);
  button.replaceChildren(icon(ok ? "check" : "close"));
  window.setTimeout(() => {
    button.classList.remove("copied", "copy-failed");
    button.replaceChildren(icon("copy"));
  }, 1200);
}
