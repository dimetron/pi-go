// Mermaid diagram rendering for the chat webview. Fenced ```mermaid code
// blocks are converted to diagrams after the markdown finalize step. The
// mermaid library is lazy-loaded from cdnjs on the first diagram (keeps the
// webview bundle small and the CSP tight); if it fails to load (offline), the
// source code block stays visible as a fallback.

import { sanitizeSvg } from "./markdown";

// Pinned to the exact cdnjs build; mermaid 11.x dist/mermaid.min.js sets the
// window.mermaid global.
const MERMAID_SRC = "https://cdnjs.cloudflare.com/ajax/libs/mermaid/11.6.0/mermaid.min.js";

interface MermaidApi {
  initialize(config: Record<string, unknown>): void;
  render(id: string, text: string): Promise<{ svg: string }>;
}

declare global {
  interface Window {
    mermaid?: MermaidApi;
  }
}

let loader: Promise<MermaidApi> | undefined;

function loadMermaid(): Promise<MermaidApi> {
  if (window.mermaid) return Promise.resolve(window.mermaid);
  loader ??= new Promise<MermaidApi>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = MERMAID_SRC;
    script.onload = () => {
      if (window.mermaid) resolve(window.mermaid);
      else reject(new Error("mermaid loaded but global is missing"));
    };
    script.onerror = () => {
      loader = undefined;
      reject(new Error("mermaid failed to load"));
    };
    document.head.append(script);
  });
  return loader;
}

let seq = 0;
let configured = false;

/**
 * Replace every <pre><code class="language-mermaid"> under root with the
 * rendered SVG diagram. Blocks that fail to render (bad syntax, offline) are
 * left as code so nothing is lost.
 */
export async function renderMermaidBlocks(root: HTMLElement): Promise<void> {
  const sources = root.querySelectorAll("pre code.language-mermaid");
  if (sources.length === 0) return;
  let mermaid: MermaidApi;
  try {
    mermaid = await loadMermaid();
  } catch {
    return; // offline or CSP-blocked: keep the code blocks
  }
  if (!configured) {
    const dark = window.matchMedia?.("(prefers-color-scheme: dark)").matches ?? true;
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: "strict",
      theme: dark ? "dark" : "neutral",
    });
    configured = true;
  }
  for (const code of Array.from(sources)) {
    if (code.getAttribute("data-mermaid-done")) continue;
    code.setAttribute("data-mermaid-done", "1");
    const text = code.textContent ?? "";
    const holder = document.createElement("div");
    holder.className = "mermaid";
    try {
      const { svg } = await mermaid.render(`pi-go-mermaid-${seq++}`, text);
      // Mermaid's SVG output includes foreignObject HTML labels — sanitize it
      // with the SVG profile rather than trusting it outright.
      holder.innerHTML = sanitizeSvg(svg);
      const pre = code.closest("pre");
      if (pre) pre.replaceWith(holder);
    } catch {
      // Invalid diagram syntax: leave the original code block in place.
    }
  }
}