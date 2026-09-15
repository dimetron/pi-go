import * as vscode from "vscode";
import type * as acp from "@agentclientprotocol/sdk";

// Caps for @-mention attachment: keep prompts bounded and never send binaries
// (pi-go advertises embeddedContext for text only, not images).
const MAX_FILE_BYTES = 256 * 1024;
const MAX_FILES = 10;
const MAX_TOTAL_BYTES = 1024 * 1024;

// Text extensions pi-go can sensibly embed; anything else is skipped.
const TEXT_EXTENSIONS = new Set([
  "bash", "c", "cc", "conf", "cpp", "cs", "css", "csv", "cxx", "diff", "env", "example",
  "go", "gradle", "h", "hh", "hpp", "htm", "html", "ini", "java", "js", "json", "jsonc",
  "jsx", "kt", "lock", "log", "md", "mdx", "mjs", "patch", "php", "pl", "properties",
  "proto", "py", "rb", "rs", "scss", "sh", "sql", "svg", "swift", "text", "toml", "ts",
  "tsx", "txt", "xml", "yaml", "yml", "zig",
]);

/** Result of converting chat @-mentions into ACP content blocks. */
export interface MentionsResult {
  blocks: acp.ContentBlock[];
  skipped: string[];
  truncated: string[];
}

/**
 * Convert ChatRequest references (from @file mentions in the session editor)
 * into ACP embedded resource blocks. Non-file references, oversized files and
 * unknown binary extensions are skipped and reported by name.
 */
export async function referencesToBlocks(
  references: readonly vscode.ChatPromptReference[] | undefined,
  token: vscode.CancellationToken,
): Promise<MentionsResult> {
  const result: MentionsResult = { blocks: [], skipped: [], truncated: [] };
  if (!references || references.length === 0) return result;

  let total = 0;
  for (const ref of references) {
    if (token.isCancellationRequested) break;
    if (!(ref.value instanceof vscode.Uri)) continue;
    const uri = ref.value;
    if (uri.scheme !== "file") {
      result.skipped.push(vscode.workspace.asRelativePath(uri, false));
      continue;
    }
    if (result.blocks.length >= MAX_FILES) {
      result.skipped.push(vscode.workspace.asRelativePath(uri, false));
      continue;
    }
    const ext = uri.path.split(".").pop()?.toLowerCase() ?? "";
    if (!TEXT_EXTENSIONS.has(ext)) {
      result.skipped.push(vscode.workspace.asRelativePath(uri, false));
      continue;
    }
    try {
      const stat = await vscode.workspace.fs.stat(uri);
      if (stat.size > MAX_FILE_BYTES) {
        result.skipped.push(vscode.workspace.asRelativePath(uri, false));
        continue;
      }
      if (total + stat.size > MAX_TOTAL_BYTES) {
        result.skipped.push(vscode.workspace.asRelativePath(uri, false));
        continue;
      }
      const bytes = await vscode.workspace.fs.readFile(uri);
      const text = Buffer.from(bytes).toString("utf8");
      total += stat.size;
      result.blocks.push({
        type: "resource",
        resource: { uri: uri.toString(), mimeType: "text/plain", text },
      });
    } catch (err) {
      result.skipped.push(vscode.workspace.asRelativePath(uri, false));
      void err;
    }
  }
  return result;
}

/** Warning banner text for the files referencesToBlocks left out. */
export function skippedMentionsMarkdown(skipped: string[]): string | undefined {
  if (skipped.length === 0) return undefined;
  const names = skipped.map((n) => `\`${n}\``).join(", ");
  return `Skipped large or non-text attachment(s): ${names} (pi-go accepts text files only).`;
}