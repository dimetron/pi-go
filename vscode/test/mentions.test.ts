import { describe, expect, it } from "vitest";
import vscode from "vscode";
import { pathsToBlocks, referencesToBlocks, skippedMentionsMarkdown } from "../src/mentions";

function addFile(path: string, size: number, text = "hello"): void {
  vscode.__fs.files.set(path, { size, bytes: new TextEncoder().encode(text) });
}

const nullToken = { isCancellationRequested: false } as never;

describe("pathsToBlocks", () => {
  it("embeds text files as resource blocks", async () => {
    addFile("/tmp/ws/a.go", 5, "package main");
    const res = await pathsToBlocks(["/tmp/ws/a.go"], nullToken);
    expect(res.blocks).toEqual([
      {
        type: "resource",
        resource: { uri: "file:/tmp/ws/a.go", mimeType: "text/plain", text: "package main" },
      },
    ]);
    expect(res.skipped).toEqual([]);
    expect(res.truncated).toEqual([]);
  });

  it("skips binary/unknown extensions", async () => {
    addFile("/tmp/ws/img.png", 10);
    addFile("/tmp/ws/Makefile", 10); // no extension → unknown ext
    const res = await pathsToBlocks(["/tmp/ws/img.png", "/tmp/ws/Makefile"], nullToken);
    expect(res.blocks).toEqual([]);
    expect(res.skipped).toEqual(["tmp/ws/img.png", "tmp/ws/Makefile"]);
  });

  it("skips files over the per-file byte cap", async () => {
    addFile("/tmp/ws/big.ts", 256 * 1024 + 1);
    const res = await pathsToBlocks(["/tmp/ws/big.ts"], nullToken);
    expect(res.blocks).toEqual([]);
    expect(res.skipped).toEqual(["tmp/ws/big.ts"]);
  });

  it("skips files that would blow the total cap", async () => {
    // Four 256 KiB files exactly fill the 1 MiB total; the fifth trips it.
    for (const name of ["a.ts", "b.ts", "c.ts", "d.ts", "e.ts"]) {
      addFile(`/tmp/ws/${name}`, 256 * 1024);
    }
    const res = await pathsToBlocks(
      ["a.ts", "b.ts", "c.ts", "d.ts", "e.ts"].map((n) => `/tmp/ws/${n}`),
      nullToken,
    );
    expect(res.blocks).toHaveLength(4);
    expect(res.skipped).toEqual(["tmp/ws/e.ts"]);
  });

  it("caps the number of files at MAX_FILES", async () => {
    for (let i = 0; i < 12; i++) addFile(`/tmp/ws/f${i}.md`, 10);
    const res = await pathsToBlocks(
      Array.from({ length: 12 }, (_, i) => `/tmp/ws/f${i}.md`),
      nullToken,
    );
    expect(res.blocks).toHaveLength(10);
    expect(res.skipped).toEqual(["tmp/ws/f10.md", "tmp/ws/f11.md"]);
  });

  it("skips files the fs cannot stat or read", async () => {
    // stat fails (not in the map)
    const res = await pathsToBlocks(["/tmp/ws/missing.md"], nullToken);
    expect(res.skipped).toEqual(["tmp/ws/missing.md"]);
  });

  it("stops early when cancelled", async () => {
    addFile("/tmp/ws/a.ts", 5);
    addFile("/tmp/ws/b.ts", 5);
    const res = await pathsToBlocks(["/tmp/ws/a.ts", "/tmp/ws/b.ts"], {
      isCancellationRequested: true,
    } as never);
    expect(res.blocks).toEqual([]);
    expect(res.skipped).toEqual([]);
  });
});

describe("referencesToBlocks", () => {
  it("keeps only file-scheme Uri references", async () => {
    addFile("/tmp/ws/a.ts", 5);
    const fileUri = vscode.Uri.file("/tmp/ws/a.ts");
    const res = await referencesToBlocks(
      [
        { value: fileUri },
        { value: vscode.Uri.parse("untitled:scratch") }, // non-file scheme
        { value: "just a string" }, // not a Uri
      ] as never,
      nullToken,
    );
    expect(res.blocks).toHaveLength(1);
    expect(res.skipped).toEqual([]);
  });

  it("tolerates undefined references", async () => {
    const res = await referencesToBlocks(undefined, nullToken);
    expect(res.blocks).toEqual([]);
  });
});

describe("skippedMentionsMarkdown", () => {
  it("is undefined for an empty list", () => {
    expect(skippedMentionsMarkdown([])).toBeUndefined();
  });

  it("lists skipped names", () => {
    expect(skippedMentionsMarkdown(["a.bin", "b.png"])).toBe(
      "Skipped large or non-text attachment(s): `a.bin`, `b.png` (pi-go accepts text files only).",
    );
  });
});