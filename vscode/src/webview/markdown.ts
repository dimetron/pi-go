// Markdown → sanitized HTML for the chat webview. Invoked only at stream
// finalize and for static history — live chunks append via textContent so the
// sanitizer never runs per-chunk.

import { marked } from "marked";
import DOMPurify from "dompurify";

// Tight allowlist: prose + code + tables only. Links keep href but get
// rel/target stripped (the webview cannot navigate anyway; the host opens
// files on request via revealFile messages).
const ALLOWED_TAGS = [
  "a", "p", "br", "hr", "blockquote", "pre", "code",
  "ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6",
  "strong", "em", "del", "s", "sup", "sub",
  "table", "thead", "tbody", "tr", "th", "td",
  "img", "span", "details", "summary",
];

const ALLOWED_ATTR = ["href", "src", "alt", "title", "class"];

marked.setOptions({ gfm: true, breaks: true });

/** Render markdown text to a sanitized HTML string. */
export function renderMarkdown(text: string): string {
  const raw = marked.parse(text, { async: false });
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS,
    ALLOWED_ATTR,
    FORBID_ATTR: ["style", "target"],
    FORBID_TAGS: ["style", "form", "input", "button", "iframe", "script"],
    ALLOW_DATA_ATTR: false,
  });
}

// A separate profile for diagram output (mermaid SVGs carry foreignObject
// HTML labels and SVG-specific attributes the HTML allowlist would strip).
const svgSanitizer = DOMPurify();

/** Sanitize a rendered diagram's SVG markup. Mermaid styles its shapes with a
 *  <style> element inside the SVG, so that stays (scoped to the diagram). */
export function sanitizeSvg(svg: string): string {
  return svgSanitizer.sanitize(svg, {
    USE_PROFILES: { svg: true, svgFilters: true },
    ADD_TAGS: ["foreignObject", "style"],
    ADD_ATTR: ["style"],
  });
}