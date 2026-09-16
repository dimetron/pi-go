import { describe, expect, it } from "bun:test";
import { lineDiff } from "../../src/shared/diff";

describe("lineDiff", () => {
  it("returns nothing for identical inputs", () => {
    expect(lineDiff("a\nb\nc", "a\nb\nc")).toEqual([
      { type: "ctx", text: "a" },
      { type: "ctx", text: "b" },
      { type: "ctx", text: "c" },
    ]);
  });

  it("treats an empty old string as one empty line to delete", () => {
    // "".split("\n") is [""], not [] — the empty side always participates as
    // an empty line. This pins that behavior.
    expect(lineDiff("", "x\ny")).toEqual([
      { type: "del", text: "" },
      { type: "add", text: "x" },
      { type: "add", text: "y" },
    ]);
  });

  it("treats an empty new string as one empty line to add", () => {
    expect(lineDiff("x\ny", "")).toEqual([
      { type: "del", text: "x" },
      { type: "del", text: "y" },
      { type: "add", text: "" },
    ]);
  });

  it("marks a changed middle line as del + add, keeping context", () => {
    expect(lineDiff("a\nb\nc", "a\nB\nc")).toEqual([
      { type: "ctx", text: "a" },
      { type: "del", text: "b" },
      { type: "add", text: "B" },
      { type: "ctx", text: "c" },
    ]);
  });

  it("handles a trailing newline as an extra empty line", () => {
    expect(lineDiff("a", "a\n")).toEqual([
      { type: "ctx", text: "a" },
      { type: "add", text: "" },
    ]);
  });
});