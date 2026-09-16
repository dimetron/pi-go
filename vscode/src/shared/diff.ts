// LCS line diff for tool-call diff bodies. Shared with the webview bundle
// (dependency-free, browser-safe). The whole-file "-every-line / +every-line"
// rendering used by the native path is the fallback for oversized inputs.

export type DiffLineType = "ctx" | "add" | "del";

export interface DiffLine {
  type: DiffLineType;
  text: string;
}

/** Cap per side: beyond this the DP table would blow past a few million cells
 *  and the fallback (every old line removed, every new line added) is used. */
const MAX_LINES = 4000;

/**
 * Split two texts into context / added / removed lines using a longest-common-
 * subsequence diff. Equal adjacent lines are kept in order; only genuinely
 * changed lines are marked.
 */
export function lineDiff(oldText: string, newText: string): DiffLine[] {
  const a = oldText.split("\n");
  const b = newText.split("\n");
  const n = a.length;
  const m = b.length;

  if (n === 0 && m === 0) return [];
  if (n === 0) return b.map((text) => ({ type: "add" as const, text }));
  if (m === 0) return a.map((text) => ({ type: "del" as const, text }));

  // Guard: fall back to the naive whole-file diff for pathological inputs.
  if (n > MAX_LINES || m > MAX_LINES || n * m > MAX_LINES * MAX_LINES) {
    return [
      ...a.map((text) => ({ type: "del" as const, text })),
      ...b.map((text) => ({ type: "add" as const, text })),
    ];
  }

  // LCS lengths table (rows: a, cols: b), built bottom-up in a flat array.
  const table = new Uint32Array((n + 1) * (m + 1));
  const at = (i: number, j: number): number => table[i * (m + 1) + j];
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      table[i * (m + 1) + j] =
        a[i] === b[j] ? at(i + 1, j + 1) + 1 : Math.max(at(i + 1, j), at(i, j + 1));
    }
  }

  // Walk the table from the top-left corner, emitting lines in order.
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ type: "ctx", text: a[i] });
      i++;
      j++;
    } else if (at(i + 1, j) >= at(i, j + 1)) {
      out.push({ type: "del", text: a[i] });
      i++;
    } else {
      out.push({ type: "add", text: b[j] });
      j++;
    }
  }
  while (i < n) out.push({ type: "del", text: a[i++] });
  while (j < m) out.push({ type: "add", text: b[j++] });
  return out;
}