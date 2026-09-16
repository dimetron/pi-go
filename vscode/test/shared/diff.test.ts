import { describe, expect, it } from "vitest";
import { lineDiff, type DiffLine } from "../../src/shared/diff";

const types = (lines: DiffLine[]) => lines.map((l) => `${l.type[0]}:${l.text}`);

describe("lineDiff", () => {
  it("treats two empty texts as one shared empty line", () => {
    // "".split("\n") is [""], so neither side is truly empty.
    expect(types(lineDiff("", ""))).toEqual(["c:"]);
  });

  it("removes the empty line and adds everything for an empty old text", () => {
    expect(types(lineDiff("", "a\nb"))).toEqual(["d:", "a:a", "a:b"]);
  });

  it("deletes everything and adds the empty line for an empty new text", () => {
    expect(types(lineDiff("a\nb", ""))).toEqual(["d:a", "d:b", "a:"]);
  });

  it("keeps identical text as context", () => {
    expect(types(lineDiff("a\nb\n", "a\nb\n"))).toEqual(["c:a", "c:b", "c:"]);
  });

  it("marks only genuinely changed lines", () => {
    const out = lineDiff("const a = 1;\nconst b = 2;\nconst c = 3;", "const a = 1;\nconst b = 22;\nconst c = 3;");
    expect(types(out)).toEqual(["c:const a = 1;", "d:const b = 2;", "a:const b = 22;", "c:const c = 3;"]);
  });

  it("handles insertions and deletions", () => {
    const out = lineDiff("a\nb\nc", "a\nc");
    expect(types(out)).toEqual(["c:a", "d:b", "c:c"]);
    const out2 = lineDiff("a\nc", "a\nb\nc");
    expect(types(out2)).toEqual(["c:a", "a:b", "c:c"]);
  });

  it("falls back to naive del/add for oversized inputs", () => {
    const big = Array.from({ length: 4001 }, (_, i) => `line${i}`).join("\n");
    const out = lineDiff(big, big);
    // Over the cap: every line is reported as removed then added, none as ctx.
    expect(out.some((l) => l.type === "ctx")).toBe(false);
    expect(out.filter((l) => l.type === "del")).toHaveLength(4001);
    expect(out.filter((l) => l.type === "add")).toHaveLength(4001);
  });
});